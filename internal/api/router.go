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

	api := r.Group("/api")
	api.Use(corsMiddleware(corsAll, corsOrigins), authMiddleware(token))
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
