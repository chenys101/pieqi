package api

import (
	"net/http"
	"time"

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

	// Feedback（p0-design.md §5）：Checkpoint/Rewind 与 Preview；
	// wired by SetFeedback; nil-safe（未接线时端点返回降级响应）。
	feedback *core.FeedbackStore
	preview  *core.PreviewManager

	// P1 Checks 重跑 runner（p1-design.md §5）；nil-safe（未接线时仅事件流派生）。
	checks *core.CheckRunner

	// Feedback P2（p2-design.md）：视觉采集 + 推送注册表；
	// wired by SetVisualCapture/SetPushRegistry; nil-safe（未接线时端点降级响应）。
	visual *core.VisualCaptureManager
	push   *core.PushRegistry

	// Lark Device Flow (扫码一键创建飞书应用)。wired by SetLarkReg;
	// nil-safe for tests that don't exercise larkreg routes.
	larkRegRunner    larkRegRunner
	larkRegState     *larkRegState
	larkRegCredPath  string
	larkConfigApplier larkConfigApplier // wired by SetLarkConfigApplier; nil-safe

	// 机器人绑定记录（复数，D1 定案）。wired by SetBotStore; nil-safe
	// （未接线时 /api/bots 返回空列表 / 503）。
	bots *core.BotStore

	// botChanged 在机器人记录增删后回调，让渠道控制器重建运行中的实例
	// （否则删掉一台机器人，它的适配器还活着、还在收发消息）。
	// wired by SetBotChangeHook; nil-safe。
	botChanged func()

	// 全局偏好（审批自动放行 / 免打扰 / 事件保留上限）。
	// wired by SetSettingsStore; nil-safe（未接线时 GET 回出厂默认、PATCH 503）。
	settings *core.SettingsStore

	// 诊断日志目录 + 进程启动时刻（导出诊断包时随附元信息）。
	// wired by SetLogDir; 空 = 未接线（导出端点返回 503）。
	logDir    string
	startedAt time.Time
}

// NewServer 创建 API 服务。
func NewServer(cfg *config.Config, store *core.TaskStore, runner *core.TaskRunner, hooks *core.HookService, bus *core.EventBus, skills *core.SkillScanner, commands *core.CommandScanner) *Server {
	return &Server{
		cfg:       cfg,
		store:     store,
		runner:    runner,
		hooks:     hooks,
		bus:       bus,
		skills:    skills,
		commands:  commands,
		startedAt: time.Now(),
	}
}

// SetAuth wires the auth service and tunnel manager. Called by main.go
// after NewServer. nil-safe: legacy tests that don't call SetAuth leave
// these nil, and the handlers/middlewares nil-check before use.
func (s *Server) SetAuth(svc *auth.Service, tunnel *auth.TunnelManager) {
	s.auth = svc
	s.tunnel = tunnel
}

// SetFeedback wires the feedback store and preview manager (Feedback P0).
// Called by main.go after NewServer; nil-safe for tests that don't exercise
// feedback/preview routes.
func (s *Server) SetFeedback(fs *core.FeedbackStore, pm *core.PreviewManager) {
	s.feedback = fs
	s.preview = pm
}

// SetCheckRunner wires the check rerun runner (Feedback P1). nil-safe.
func (s *Server) SetCheckRunner(cr *core.CheckRunner) {
	s.checks = cr
}

// SetVisualCapture wires the visual capture manager (Feedback P2). nil-safe.
func (s *Server) SetVisualCapture(vm *core.VisualCaptureManager) {
	s.visual = vm
}

// SetPushRegistry wires the evidence push registry (Feedback P2). nil-safe.
func (s *Server) SetPushRegistry(pr *core.PushRegistry) {
	s.push = pr
}

// SetBotStore 注入机器人绑定存储（main.go 调用）。nil-safe：未接线时
// GET /api/bots 返回空列表，写接口返回 503。
func (s *Server) SetBotStore(bs *core.BotStore) {
	s.bots = bs
}

// SetBotChangeHook 注入「机器人记录已变」回调（main.go 传渠道控制器的 Rebuild）。
// nil-safe：未接线时记录照常落盘，只是运行中的实例不会即时跟随。
func (s *Server) SetBotChangeHook(f func()) {
	s.botChanged = f
}

// SetSettingsStore 注入全局偏好存储（main.go 调用）。nil-safe：
// 未接线时 GET /api/settings 返回出厂默认，PATCH 返回 503。
func (s *Server) SetSettingsStore(ss *core.SettingsStore) {
	s.settings = ss
}

// SetLogDir 注入日志目录（main.go 调用），供诊断导出端点打包日志。
// nil-safe：未接线时 /api/diagnostics/export 返回 503。
func (s *Server) SetLogDir(dir string) {
	s.logDir = dir
}

// apiAuthMiddleware 返回业务 API 的鉴权中间件，与 /api 主组保持同一套语义：
// wired 了 auth 走 ExternalAuthMiddleware（外网身份+token；内网放行），
// 否则回退 legacy Bearer token（旧测试 / 本地 dev）。
func (s *Server) apiAuthMiddleware(token string) gin.HandlerFunc {
	if s.auth != nil {
		return s.auth.ExternalAuthMiddleware()
	}
	return authMiddleware(token)
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
		// Feedback P0（p0-design.md §5）：总览 / Diff / Rewind / Preview
		api.GET("/tasks/:id/feedback", s.getFeedback)
		api.GET("/tasks/:id/feedback/diff", s.getFeedbackDiff)
		api.POST("/tasks/:id/rewind", s.postRewind)
		// Feedback P1（p1-design.md §11）：前瞻 Diff / Checks / Outcome / Evidence / Continue
		api.GET("/tasks/:id/approvals/:decisionId/diff", s.getApprovalDiff)
		api.GET("/tasks/:id/checks", s.listChecks)
		api.POST("/tasks/:id/checks/:checkId/rerun", s.rerunCheck)
		api.GET("/tasks/:id/outcome", s.getOutcome)
		api.GET("/tasks/:id/evidence", s.getEvidence)
		api.POST("/tasks/:id/continue", s.postContinue)
		// Feedback P2（p2-design.md §9）：Evidence Push（截图/console/network 子路径
		// 挂 preview wildcard，见 preview.go 分发）
		api.POST("/tasks/:id/push", s.postPush)
		// 文件预览（markdown/pdf 原始内容，file_preview.go）
		api.GET("/tasks/:id/file", s.getTaskFile)
		// preview 控制端点与代理共用一条 wildcard 路由（见 previewRoute 分发说明）
		api.Any("/tasks/:id/preview/*path", s.previewRoute)
		api.GET("/skills", s.listSkills)   // Phase 6 实现，先占位
		api.GET("/commands", s.listCommands)
		api.GET("/ws", s.handleWS)         // Phase 5 实现
	}

	// Preview 代理挂载点：刻意独立于 /api（见 core.PreviewMountPrefix 说明），
	// 使请求不进入被预览项目自身的 /api 代理规则，避免回环 431。
	// 中间件与 /api 组完全一致（CORS + 外网鉴权），保证安全能力不回退。
	previewGrp := r.Group(core.PreviewMountPrefix,
		corsMiddleware(corsAll, corsOrigins), s.apiAuthMiddleware(token))
	previewGrp.Any("/:id/*path", s.previewRoute)

	// Auth (binding) 路由：bind/unbind 仅内网（BindOpGateMiddleware）；
	// status 公开（前端 boot 轮询，无 gate）。
	if s.auth != nil {
		authGrp := r.Group("/api/auth", corsMiddleware(corsAll, corsOrigins), s.auth.BindOpGateMiddleware())
		authGrp.POST("/bind", s.bind)
		authGrp.DELETE("/bind", s.unbind)
		r.GET("/api/auth/status", corsMiddleware(corsAll, corsOrigins), s.authStatus)
	}

	// Tunnel 路由（D3-c 定案：收口匿名 bootstrap，见 IMPLEMENTATION-PLAN §4.2.1）。
	//
	// 所有**变更类**端点（/start /stop /reset /renew）统一走 tunnelMutate：
	// UA 门（仅飞书移动端 + 外网）**加** ExternalAuth（有效 token）。
	//
	// 为什么 /start 也要求 token —— 它曾经只挂 UA 门，理由是"首启还没 token，
	// 要求 token 会死锁"。那个担心的前提是错的：**IM 命令就是 bootstrap**，
	// 它直接调 TunnelManager.Start()，根本不经过 HTTP（见 core.Bridge.handleTunnelCommand）。
	// 于是"只挂 UA 门"实际得到的是一个任何人声明自己是飞书移动端就能自取
	// **管理员代理凭据**的入口 —— 而 token 的可信度完全依赖"只经 IM 交付给管理员"
	// 这条有 adminBinding.Match 把关的通道。把签发挪到可伪造的 UA 声明上，等于绕开它。
	//
	// 功能无损：PWA 面板在**已有 token**（深链带进来）时仍可重启 / 停止 / 续期 / 轮换；
	// token 过期则回落 IM 命令（支持 启动 / 停止 / 续期，RenewToken 在"token 已过期
	// 但隧道仍活着"时会签发全新 token，已覆盖 /reset 的换码场景）。
	// /status /qrcode 保持公开只读（handler 已对 token 做掩码）。
	if s.auth != nil && s.tunnel != nil {
		tunnelMutate := r.Group("/api/tunnel", corsMiddleware(corsAll, corsOrigins),
			s.auth.ExternalAuthMiddleware(), s.auth.TunnelOpGateMiddleware())
		tunnelMutate.POST("/start", s.tunnelStart)
		tunnelMutate.POST("/stop", s.tunnelStop)
		tunnelMutate.POST("/reset", s.tunnelReset)
		tunnelMutate.POST("/renew", s.tunnelRenew)

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
		larkRegGrp.GET("/status", s.larkRegStatus)
		larkRegGrp.GET("/config", s.larkRegConfig)
		larkRegGrp.POST("/config", s.larkRegConfigUpdate)
	}

	// 机器人绑定记录（复数，D1 定案）。读写**鉴权分层**：
	//   - 读（GET）：与 /api 同套鉴权 —— 手机 / PWA 在外网也要能看列表；
	//     若沿用写接口的 BindOpGate（仅内网），外网一律 403。
	//   - 写（POST/PATCH/DELETE）：BindOpGate（仅内网）—— 绑定是高风险操作。
	// 返回体只有 model.Bot 元数据，不含 app_secret（它在 per-bot 凭据文件里）。
	if s.bots != nil {
		r.GET("/api/bots", corsMiddleware(corsAll, corsOrigins),
			s.apiAuthMiddleware(token), s.listBots)
		if s.auth != nil {
			botsWrite := r.Group("/api/bots", corsMiddleware(corsAll, corsOrigins),
				s.auth.BindOpGateMiddleware())
			botsWrite.POST("", s.createBot)
			botsWrite.PATCH("/:id", s.updateBot)
			botsWrite.DELETE("/:id", s.deleteBot)
		}
	}

	// hook 子进程回连（仅本地，不走 auth）
	r.POST("/internal/hook", s.hookCallback)

	// 全局偏好（审批自动放行 / 免打扰 / 事件保留上限）。
	// 读写同套鉴权（内网放行 / 外网需有效 token）—— 它是一组不含秘密的偏好值，
	// 而 token 本身就是管理员的代理凭据（见 patchSettings 的注释）。
	{
		api.GET("/settings", s.getSettings)
		api.PATCH("/settings", s.patchSettings)
	}

	// 诊断日志导出：打包最近 7 天日志（+ meta）为 zip。
	// 日志可能含项目路径 / 任务标题，故与 /api 主组同套鉴权。
	api.GET("/diagnostics/export", s.exportDiagnostics)
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
