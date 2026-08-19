package api

import (
	"net/http"

	"pieqi/internal/auth"
	"pieqi/internal/config"
	"pieqi/internal/core"

	"github.com/gin-gonic/gin"
)

// Server 监控/干预 HTTP API。挂在主进程 gin router 上。
type Server struct {
	cfg      *config.Config
	store    *core.TaskStore
	runner   *core.TaskRunner
	hooks    *core.HookService
	bus      *core.EventBus
	skills   *core.SkillScanner
	commands *core.CommandScanner
	auth     *auth.Service       // wired by SetAuth; nil-safe for legacy tests
	tunnel   *auth.TunnelManager // wired by SetAuth; nil-safe for legacy tests

	// Lark Device Flow (扫码一键创建飞书应用)。wired by SetLarkReg;
	// nil-safe for tests that don't exercise larkreg routes.
	larkRegRunner   larkRegRunner
	larkRegState    *larkRegState
	larkRegCredPath string
}

// NewServer 创建 API 服务。
func NewServer(cfg *config.Config, store *core.TaskStore, runner *core.TaskRunner, hooks *core.HookService, bus *core.EventBus, skills *core.SkillScanner, commands *core.CommandScanner) *Server {
	return &Server{
		cfg:      cfg,
		store:    store,
		runner:   runner,
		hooks:    hooks,
		bus:      bus,
		skills:   skills,
		commands: commands,
	}
}

// SetAuth wires the auth service and tunnel manager. Called by main.go
// after NewServer. nil-safe: legacy tests that don't call SetAuth leave
// these nil, and the handlers/middlewares nil-check before use.
func (s *Server) SetAuth(svc *auth.Service, tunnel *auth.TunnelManager) {
	s.auth = svc
	s.tunnel = tunnel
}

// Register 在 gin router 上挂 /api/* 与 /internal/* 路由。
func (s *Server) Register(r gin.IRouter) {
	token := ""
	corsAll := true
	var corsOrigins []string
	if s.cfg != nil {
		token = s.cfg.API.Token
		corsOrigins = s.cfg.API.CORSOrigins
		if len(corsOrigins) > 0 {
			corsAll = false
		}
	}

	// 业务 API：wired 了 auth 走 ExternalAuthMiddleware（外网身份+token；内网放行），
	// 否则回退 legacy Bearer token（旧测试与本地 dev 兼容）。
	api := r.Group("/api")
	api.Use(corsMiddleware(corsAll, corsOrigins))
	if s.auth != nil {
		api.Use(s.auth.ExternalAuthMiddleware())
	} else {
		api.Use(authMiddleware(token))
	}
	{
		api.GET("/tasks", s.listTasks)
		api.GET("/tasks/:id", s.getTask)
		api.POST("/tasks", s.createTask)
		api.POST("/tasks/:id/intervene", s.intervene)
		api.POST("/tasks/:id/cancel", s.cancelTask)
		api.DELETE("/tasks/:id", s.deleteTask)
		api.GET("/skills", s.listSkills)   // Phase 6 实现，先占位
		api.GET("/commands", s.listCommands)
		api.GET("/ws", s.handleWS)         // Phase 5 实现
	}

	// Auth (binding) 路由：bind/unbind 仅内网（BindOpGateMiddleware）；
	// status 公开（前端 boot 轮询，无 gate）。
	if s.auth != nil {
		authGrp := r.Group("/api/auth", corsMiddleware(corsAll, corsOrigins), s.auth.BindOpGateMiddleware())
		authGrp.POST("/bind", s.bind)
		authGrp.DELETE("/bind", s.unbind)
		r.GET("/api/auth/status", corsMiddleware(corsAll, corsOrigins), s.authStatus)
	}

	// Tunnel 路由：
	//   - /start: bootstrap path, gated ONLY by TunnelOpGate (Lark-mobile + external).
	//     No pre-existing token is required because /start mints the very first
	//     token. Requiring ExternalAuth here would deadlock (PRD §4.4 lists tunnel
	//     start as the token-issuance trigger).
	//   - /stop, /reset: operate on a running tunnel, so they require a valid
	//     token + identity (ExternalAuth) IN ADDITION to the Lark-mobile gate.
	//   - /status, /qrcode: public read-only (handler masks the token).
	if s.auth != nil && s.tunnel != nil {
		tunnelStart := r.Group("/api/tunnel", corsMiddleware(corsAll, corsOrigins),
			s.auth.TunnelOpGateMiddleware())
		tunnelStart.POST("/start", s.tunnelStart)

		tunnelMutate := r.Group("/api/tunnel", corsMiddleware(corsAll, corsOrigins),
			s.auth.ExternalAuthMiddleware(), s.auth.TunnelOpGateMiddleware())
		tunnelMutate.POST("/stop", s.tunnelStop)
		tunnelMutate.POST("/reset", s.tunnelReset)

		r.GET("/api/tunnel/status", corsMiddleware(corsAll, corsOrigins), s.tunnelStatus)
		r.GET("/api/tunnel/qrcode", corsMiddleware(corsAll, corsOrigins), s.tunnelQRCode)
	}

	// Lark Device Flow 路由:扫码一键创建飞书应用。
	// 仅内网(同 bind/unbind 的 BindOpGateMiddleware)—— 防止从公网触发
	// 应用创建流程。不能套 ExternalAuthMiddleware(接入前还没有凭据)。
	if s.auth != nil && s.larkRegRunner != nil {
		larkRegGrp := r.Group("/api/larkreg", corsMiddleware(corsAll, corsOrigins),
			s.auth.BindOpGateMiddleware())
		larkRegGrp.POST("/start", s.larkRegStart)
		larkRegGrp.GET("/poll", s.larkRegPoll)
	}

	// hook 子进程回连（仅本地，不走 auth）
	r.POST("/internal/hook", s.hookCallback)
}

// listSkills 扫描 Claude skills 目录，返回 skill 胶囊列表（REQ-04/05）。
func (s *Server) listSkills(c *gin.Context) {
	if s.skills == nil {
		c.JSON(http.StatusOK, gin.H{"skills": []interface{}{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"skills": s.skills.Scan()})
}

// listCommands 扫描 Claude commands 目录，返回用户自定义命令列表。
func (s *Server) listCommands(c *gin.Context) {
	if s.commands == nil {
		c.JSON(http.StatusOK, gin.H{"commands": []interface{}{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"commands": s.commands.Scan()})
}
