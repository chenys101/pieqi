// Package main: cmd/pieqi — Pieqi 桥接服务入口。
//
// 双模式：
//   - 无参数：启动 HTTP 服务器（API + PWA + 渠道 webhook）
//   - pre-tool-use：Claude Code PreToolUse hook 子进程，回连主进程 /internal/hook 等待人类决策
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/agent/claude"
	"pieqi/internal/api"
	"pieqi/internal/auth"
	"pieqi/internal/channel/wechat"
	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/larkreg"
	"pieqi/internal/logging"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
	// 最早一步：剔除宿主（WorkBuddy 会话）经 NODE_OPTIONS 注入的 shim。
	// 必须早于任何子进程 spawn —— 环境一旦被继承出去就收不回来；
	// 且自重启（os.Environ() 派生新进程）靠这一步保证"污染不会跨代累积"。
	// 详见 internal/core/env_sanitize.go 的文件头注释。
	strippedNodeOpts := core.SanitizeInheritedNodeOptions()

	// pre-tool-use 子命令：Claude Code PreToolUse hook 回连主进程
	if len(os.Args) > 1 && os.Args[1] == "pre-tool-use" {
		runPreToolUse(os.Args[2:])
		return
	}

	// --- 加载配置 ---
	cfgPath := "config.yaml"
	if p := os.Getenv("PIEQI_CONFIG"); p != "" {
		cfgPath = p
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// 加载运行时渠道配置（扫码/手工落盘的凭据文件，若存在则覆盖 config 默认值）
	loadLarkChannelConfig(cfg)

	// --- 数据目录（默认 ~/.pieqi，PIEQI_HOME 可覆盖；运行时数据不入仓库） ---
	dataRoot := config.DefaultDataRoot()

	// --- 日志 ---
	// 同时写控制台与 <dataRoot>/logs/pieqi-YYYY-MM-DD.log。
	// 落盘不是为了"多一份备份"，而是设置页那个「导出诊断日志」按钮必须
	// **真的有文件可导** —— 控制台日志在终端滚动后就没了，而排查发生在事后。
	logDir := filepath.Join(dataRoot, "logs")
	logger, closeLog, err := logging.NewLogger(cfg.Server.Mode, logDir)
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}
	defer closeLog()

	// P5：pieqi.acp.* 旧字段迁移告警（仅显式配置时触发，旧语义仍生效）
	for _, d := range cfg.Deprecations {
		logger.Warn("config deprecated field", zap.String("hint", d))
	}

	// 启动时剔除了宿主注入的 shim 才告警：正常情况下没有（干净启动不打扰），
	// 一旦出现即说明是从 WorkBuddy 会话里拉起的，日志留痕便于解释
	// "为什么子进程里的删除行为与终端不一致"。
	if len(strippedNodeOpts) > 0 {
		logger.Warn("stripped host-injected NODE_OPTIONS shim",
			zap.Strings("removed", strippedNodeOpts),
			zap.String("reason", "会话级 fs 删除闸门 shim 不应继承进 pieqi 及其子进程"))
	}

	for _, dir := range []string{
		filepath.Join(dataRoot, "tasks"),
		filepath.Join(dataRoot, "worktrees"),
		filepath.Join(dataRoot, "checkpoints"), // Feedback P0：Turn 快照/baseline 落盘
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			logger.Fatal("mkdir data dir", zap.String("dir", dir), zap.Error(err))
		}
	}

	// --- 核心组件 ---
	//
	// 自重启交接：先解析上一轮留下的交接单，判定"重启到底成没成"。
	// **必须在 NewTaskStore 之前**——load() 要靠这份判定决定被打断的会话是
	// 标 failed（意外）还是保留 running 待自动接续（计划内）。execPath 在这里
	// 先取，因为它同时是判定依据（新二进制摘要）和 hook 回连路径。
	execPath, err := os.Executable()
	if err != nil {
		logger.Fatal("get executable path", zap.Error(err))
	}
	execPath, _ = filepath.Abs(execPath)

	restartOutcome := core.ResolveRestartHandoff(dataRoot, execPath)
	var interruptedIDs []string
	if restartOutcome.WasSelfRestart {
		logger.Info("self-restart handoff found",
			zap.Bool("succeeded", restartOutcome.Succeeded),
			zap.String("reason", restartOutcome.Reason))
	}
	if restartOutcome.Succeeded && restartOutcome.Journal != nil {
		interruptedIDs = restartOutcome.Journal.AffectedTaskIDs
		logger.Info("self-restart succeeded; tasks to auto-resume",
			zap.Int("count", len(interruptedIDs)))
	} else if restartOutcome.WasSelfRestart {
		// 判定失败：不接续（见 ResumeInterrupted 注释），但仍要让这些任务
		// 落到 failed 而不是永远 running —— 传空列表即走原有的"重启打断"分支。
		logger.Warn("self-restart did not succeed; interrupted tasks stay failed",
			zap.String("reason", restartOutcome.Reason))
	}

	store, err := core.NewTaskStore(filepath.Join(dataRoot, "tasks"), interruptedIDs...)
	if err != nil {
		logger.Fatal("init task store", zap.Error(err))
	}

	bus := core.NewEventBus()
	worktreeBase := cfg.Pieqi.WorktreeBase
	if worktreeBase == "" {
		worktreeBase = filepath.Join(dataRoot, "worktrees")
	}
	wm := core.NewWorktreeManager(logger, worktreeBase)
	hooks := core.NewHookService(cfg.Pieqi.HookTimeout)
	skills := core.NewSkillScanner(logger, cfg.Pieqi.SkillsDirs)
	commands := core.NewCommandScanner(logger, nil)

	// pieqi 可执行文件绝对路径已在上面取过（PreToolUse hook 子进程回连 + 自重启判定共用）。

	hookTimeoutSec := int(cfg.Pieqi.HookTimeout / time.Second)

	// 全局偏好（跨任务 / 跨项目 / 跨 agent）：审批自动放行 / 免打扰 / 事件保留上限。
	//
	// 它是这些偏好的**唯一**事实来源。config.yaml 的 pieqi.auto_approve_tools 是旧的
	// "裸 ToolKind 白名单"，默认值 {edit,delete,move} 里 delete 恰恰是风险模型中的
	// **L3（破坏性操作，不可自动放行）**。把一个与新模型直接冲突的旧值继承下来，
	// 只会得到"界面上说不可配置、实际却在放行"的静默矛盾 —— 所以不从它推导，
	// 从设计默认值（L0/L1 放行）开始，由用户在设置页按需调整。
	settingsStore, err := core.NewSettingsStore(filepath.Join(dataRoot, "settings.json"))
	if err != nil {
		logger.Fatal("init settings store", zap.Error(err))
	}

	runner := core.NewTaskRunner(
		logger, store, wm, bus, hooks,
		"", cfg.Pieqi.PermissionMode, cfg.Pieqi.CleanupWorktrees,
		execPath, cfg.Server.Port, cfg.Pieqi.HookTools, hookTimeoutSec,
		cfg.Pieqi.MaxConcurrentPerProject, cfg.Pieqi.BaseBranch,
	)
	// 免审名单由全局偏好推导（L0/L1 可配、L2/L3 永不放行）；
	// 设置页改动后经 OnChange 即刻推给 runner（只影响之后创建的会话）。
	runner.SetAutoApproveTools(settingsStore.Get().AutoApproveTools())
	runner.SetDNDChecker(func() bool { return settingsStore.Get().InDND(time.Now()) })
	// 事件保留上限（切片 5）：超出后从最旧的开始丢弃，避免长任务把内存吃掉。
	// 与免审名单同源（settings.json），改动同样即时生效。
	store.SetEventRetention(settingsStore.Get().EventRetention)
	settingsStore.OnChange(func(s core.Settings) {
		runner.SetAutoApproveTools(s.AutoApproveTools())
		store.SetEventRetention(s.EventRetention)
		logger.Info("settings changed",
			zap.Bool("auto_approve_l0", s.AutoApproveL0),
			zap.Bool("auto_approve_l1", s.AutoApproveL1),
			zap.Bool("dnd", s.DNDEnabled),
			zap.Int("event_retention", s.EventRetention))
	})

	// Feedback P0（p0-design.md）：Checkpoint 存储 + Preview 管理器。
	// runner 挂钩（baseline / Turn 快照捕获），API 侧经 SetFeedback 接线。
	feedbackStore := core.NewFeedbackStore(logger, filepath.Join(dataRoot, "checkpoints"))
	runner.SetFeedbackStore(feedbackStore)
	previewMgr := core.NewPreviewManager(logger)
	// Task 终态/删除时自动回收 preview 进程
	previewMgr.WatchBus(bus)

	// Feedback P1（p1-design.md）：Checks 重跑 runner（事件流复用派生无需状态）。
	checkRunner := core.NewCheckRunner(logger, filepath.Join(dataRoot, "checks"))

	// IM 机器人绑定记录（复数，D1/D2 定案）：~/.pieqi/bots/。
	// 元数据 index.json 可下发前端；per-bot 凭据文件含 secret，不进 API 响应。
	botStore, err := core.NewBotStore(cfg.Pieqi.BotsDir)
	if err != nil {
		logger.Fatal("init bot store", zap.Error(err))
	}

	// Feedback P2（p2-design.md）：视觉采集（截图/console/network）+ Evidence Push。
	// task 删除时回收截图与事件窗口；推送注册表订阅终态自动推 Outcome。
	visualMgr := core.NewVisualCaptureManager(logger, filepath.Join(dataRoot, "previews"))
	visualMgr.WatchBus(bus)
	pushRegistry := core.NewPushRegistry(logger, store)
	pushRegistry.WatchBus(bus)

	// ACP 路径（Phase 2）→ 已由多 Agent 默认驱动取代（#2）：
	//   agents.claude.transport=sdk-bridge（默认）→ 任务经 agent.Open("claude") 驱动
	//     （桥为主力，桥不可用自动回退 print）；
	//   多 Agent（本处新增）：进入该路径后，**每个任务**按 Task.Agent 选 agent——
	//     新任务页选择器写 Task.Agent，"claude"（默认）/ "qoder" 均可，由同一 AgentManager
	//     按名分发到对应 provider（见 agent.NewMultiAgentSessionManager）。
	//   以上均不命中时回退旧 use_acp AgentManager（transport=print + use_acp=true）。
	//   默认路径（use_acp=false + transport=print）保持 Phase 1 claude -p 不变。
	var acpMgr *agent.AgentManager

	// 可被任务选择的 agent 目录：新任务页选择器的数据源，同时也是 AgentManager 的选路依据。
	// AvailableAgents 恒含 claude（且排首位），故默认 agent = 列表首位 = Claude Code。
	availableAgents := agent.AvailableAgents(cfg.Agents)
	agentNames := make([]string, 0, len(availableAgents))
	for _, a := range availableAgents {
		agentNames = append(agentNames, a.Name)
	}
	defaultAgentName := agent.AgentClaude
	if len(agentNames) > 0 {
		defaultAgentName = agentNames[0]
	}

	// 是否进入 session 驱动路径：沿用改造前的判据（claude 走桥，或有任一 ACP 系 agent 已配置）。
	// 判据不变是刻意的——本次改动只让「进来之后选哪个 agent」变得可选，不改变
	// 「哪些配置会进入这条路径」，避免旧配置（use_acp + print）被无声改道。
	// 表本身也是 provider 的注册依据（见下方「多 Agent」接线），两处共用同一判据。
	acpProviders := agent.ACPProviderConfigFromAgents(cfg.Agents)
	if cfg.Agents.Claude.Transport == "sdk-bridge" || len(acpProviders.Agents) > 0 {
		mgr := agent.NewMultiAgentSessionManager(agentNames, defaultAgentName, agent.ManagerConfig{
			MaxConcurrent: cfg.Pieqi.MaxConcurrentPerProject,
			// 会话空闲回收阈值：复用旧 acp.idle_timeout（默认 15m），轮间保活上限
			IdleTimeout: cfg.Pieqi.ACP.IdleTimeout,
		}, logger)
		acpMgr = mgr
		runner.SetAgentManager(mgr, true, cfg.Pieqi.HookTimeout)
		logger.Info("agent session manager enabled",
			zap.Strings("agents", agentNames),
			zap.String("default_agent", defaultAgentName),
			zap.String("claude_transport", cfg.Agents.Claude.Transport))
		// 后台空闲回收：会话跨轮保活，超过 idle_timeout 无对话优雅关闭（避免孤儿进程累积）。
		mgr.StartReaper(cfg.Pieqi.ACP.IdleTimeout / 3)
	} else if cfg.Pieqi.ACP.UseACP {
		mgr := agent.NewAgentManager(agent.ManagerConfigFromPieqi(cfg.Pieqi), logger)
		acpMgr = mgr
		// 透明回退时记录真实 primaryErr（此前只记通用文案"ACP 适配器不可用"，失败原因黑盒）。
		// 回退本身不阻塞 Open（异步触发）；这里把触发回退的 primary 失败原因落到日志，便于定位。
		mgr.SetFallbackHook(func(taskID string, primaryErr error) {
			logger.Warn("acp adapter unavailable, fell back to claude -p",
				zap.String("task", taskID), zap.Error(primaryErr))
		})
		runner.SetAgentManager(mgr, cfg.Pieqi.ACP.UseACP, cfg.Pieqi.HookTimeout)
		logger.Info("acp agent manager enabled", zap.String("agent_type", cfg.Pieqi.ACP.AgentType))
		// 后台空闲回收：ACP 会话跨轮保活，超过 idle_timeout 无对话优雅关闭（避免孤儿进程累积）。
		// tick 取 idle_timeout 的 1/3，保证回收延迟上限；idle_timeout<=0 时 StartReaper 为 no-op。
		mgr.StartReaper(cfg.Pieqi.ACP.IdleTimeout / 3)
	}

	// --- Bridge（IM 渠道编排） ---
	bridge := core.NewBridge(logger)
	if cfg.Pieqi.Enabled {
		bridge.EnablePieqi(store, runner, bus)
	}
	runner.SetNotifier(bridge.NotifyOrigin)

	// --- 多 Agent（multi-agent.md §9 / 修订版 §9）：agents.* 配置接线 ---
	// claude：sdk-bridge 时注册 bridge provider（探活失败可自动 spawn 常驻桥），
	// 桥不可用由 openSession 回退 print；print 时直接注册 claude -p 回退。
	var claudeProc *claude.Proc
	{
		cc := claude.ConfigFromAgents(cfg.Agents)
		cc.Logger = logger
		if cc.Transport == "sdk-bridge" && cfg.Agents.Claude.Bridge.AutoStart {
			proc := claude.NewProc(claude.ProcConfig{
				BaseURL: cfg.Agents.Claude.Bridge.BaseURL,
				Token:   cfg.Agents.Claude.Bridge.Token,
				Dir:     cfg.Agents.Claude.Bridge.Dir,
				Logger:  logger,
			})
			if err := proc.EnsureRunning(context.Background()); err != nil {
				logger.Warn("claude sdk-bridge auto-start failed; sessions will fall back to print",
					zap.Error(err))
			} else {
				claudeProc = proc
				logger.Info("claude sdk-bridge ensured",
					zap.String("base_url", cfg.Agents.Claude.Bridge.BaseURL))
			}
		}
		claude.Configure(cc)
	}
	// ACP 系 agent（qoder / dsh）工厂：配置里 transport=acp 的才进表，业务 agent.Open("<名字>") 即用。
	// 名字与 AvailableAgents 的 Name 同一个串，两处共用 ACPProviderConfigFromAgents 的判据。
	if len(acpProviders.Agents) > 0 {
		acpProviders.Logger = logger
		agent.ConfigureACPProviders(acpProviders)
		enabled := make([]string, 0, len(acpProviders.Agents))
		for _, a := range availableAgents {
			if _, ok := acpProviders.Agents[a.Name]; ok {
				enabled = append(enabled, a.Name)
			}
		}
		logger.Info("acp agent providers configured", zap.Strings("agents", enabled))
	}

	// --- Gin ---
	gin.SetMode(cfg.Server.Mode)
	r := gin.Default()

	// 渠道：lark 走控制器（多机器人 + 配置热应用）；wechat 保持原样
	var larkController *larkChannelController
	if cfg.Channels.Lark.Enabled {
		larkController = newLarkChannelController(logger, bridge, r, botStore)
		if err := larkController.Init(cfg.Channels.Lark); err != nil {
			logger.Fatal("init lark", zap.Error(err))
		}
	}
	if cfg.Channels.WeChat.Enabled {
		wechatAdapter := wechat.New(logger, cfg.Channels.WeChat.BaseURL)
		if err := wechatAdapter.Init(r); err != nil {
			logger.Fatal("init wechat", zap.Error(err))
		}
		bridge.RegisterReceiver(wechatAdapter)
		go func() {
			if err := wechatAdapter.Start(context.Background()); err != nil {
				logger.Error("wechat start", zap.Error(err))
			}
		}()
		logger.Info("wechat channel enabled")
	}

	// P2 推送 provider 注册：已启用的 IM 渠道（lark/wechat）复用 Bridge sender；
	// webhook 通用通道经 PIEQI_PUSH_WEBHOOK_URL 环境变量按需开启。
	for _, name := range []string{"lark", "wechat"} {
		if sender, ok := bridge.Sender(name); ok {
			pushRegistry.Register(core.NewSenderProvider(name, sender))
			logger.Info("push provider registered", zap.String("channel", name))
		}
	}
	if webhookURL := os.Getenv("PIEQI_PUSH_WEBHOOK_URL"); webhookURL != "" {
		pushRegistry.Register(core.NewWebhookProvider(webhookURL))
		logger.Info("push provider registered", zap.String("channel", "webhook"))
	}

	// --- Auth (Feishu binding + Cloudflared tunnel) ---
	authBindings, err := auth.NewBindingStore(cfg.Auth.FeishuBindingFile)
	if err != nil {
		logger.Fatal("init binding store", zap.Error(err))
	}
	authTokens := auth.NewTokenStore()

	// Cloudflare Access（可选）：外网主鉴权。源站必须自行验签
	// Cf-Access-Jwt-Assertion，否则伪造同名头即可绕过（见 internal/auth/access.go）。
	// 配置不全时**不静默降级**：警告并保持未启用，让 token 兜底继续生效。
	var accessVerifier *auth.AccessVerifier
	if cfg.Auth.Access.Enabled {
		v, err := auth.NewAccessVerifier(auth.AccessParams{
			TeamDomain: cfg.Auth.Access.TeamDomain,
			Audience:   cfg.Auth.Access.Audience,
			JWKSURL:    cfg.Auth.Access.JWKSURL,
		})
		if err != nil {
			logger.Warn("cloudflare access enabled but misconfigured; 该通道不生效，"+
				"外网仍只认 tunnel token 兜底", zap.Error(err))
		} else {
			accessVerifier = v
			logger.Info("cloudflare access enabled",
				zap.String("team_domain", cfg.Auth.Access.TeamDomain),
				zap.Bool("token_fallback", cfg.Auth.Access.TokenFallbackEnabled()))
		}
	}
	tokenDisabled := cfg.Auth.Access.Enabled && !cfg.Auth.Access.TokenFallbackEnabled()
	if accessVerifier == nil && tokenDisabled {
		// 两道凭据通道都关掉 = 外网全拒。宁可响亮地警告，也不静默裸奔或静默锁死。
		logger.Warn("外网无任何可用凭据通道：cloudflare_access 未生效且 token_fallback=false，" +
			"所有外网请求将返回 401（内网/本机不受影响）")
	}

	authSvc := &auth.Service{
		Debug:         auth.NewDebugSwitch(cfg.Auth.DebugSkipAllAuth),
		Bindings:      authBindings,
		Tokens:        authTokens,
		Limiter:       auth.NewIPLimiter(cfg.Auth.RateLimit.MaxFailuresPerMin, cfg.Auth.RateLimit.BlacklistDuration),
		Audit:         auth.NewAuditLogger(logger),
		Access:        accessVerifier,
		TokenDisabled: tokenDisabled,
	}
	// 隧道凭据解析：优先配置值（含 PIEQI_AUTH_CLOUDFLARED_TUNNEL_TOKEN 覆盖），
	// 其次仓外 token 文件（默认 ~/.pieqi/cloudflared_token）。
	// 纪律：token 值绝不落日志 —— 只记来源；也不要写进 config.yaml（git 跟踪文件）。
	tunnelToken, tokenSrc, err := cfg.Auth.Cloudflared.ResolveTunnelToken()
	if err != nil {
		logger.Warn("resolve cloudflared tunnel token failed", zap.Error(err))
	}
	if cfg.Auth.Cloudflared.IsNamed() {
		if tunnelToken == "" {
			logger.Warn("named tunnel mode selected but no tunnel token found; " +
				"隧道启动会失败（请设置 PIEQI_AUTH_CLOUDFLARED_TUNNEL_TOKEN 或写 " +
				cfg.Auth.Cloudflared.TunnelTokenFile + "）")
		} else {
			logger.Info("named tunnel mode", zap.String("public_hostname", cfg.Auth.Cloudflared.PublicHostname),
				zap.String("token_source", tokenSrc))
		}
	} else {
		logger.Info("quick tunnel mode (trycloudflare 临时域名)")
	}
	tunnelMgr := auth.NewTunnelManager(auth.TunnelConfig{
		BinaryPath: cfg.Auth.Cloudflared.BinaryPath,
		LocalURL:   fmt.Sprintf("http://localhost:%d", cfg.Server.Port),
		Tokens:     authTokens,
		Logger:     logger,
		// 隧道模式：quick = trycloudflare 随机域名（默认）；named = 固定域名
		// （Zero Trust named tunnel，域名永不回收）。named 需 tunnel_token +
		// public_hostname，见 config.yaml cloudflared 段注释。
		Mode:           cfg.Auth.Cloudflared.Mode,
		TunnelToken:    tunnelToken,
		PublicHostname: cfg.Auth.Cloudflared.PublicHostname,
		// 跨重启清理孤儿 cloudflared：强杀服务时 defer Stop 不执行，PID 文件
		// 让下次 Start 能杀掉残留进程（见 auth.TunnelManager.cleanupOrphans）。
		PIDFile: filepath.Join(dataRoot, "cloudflared.pid"),
		// 自动自愈：域名被 Cloudflare 回收后重启换新域名，并推送新链接到飞书管理员。
		OnHeal:    bridge.NotifyTunnelHeal,
		OnHealErr: bridge.NotifyTunnelHealErr,
	})
	defer tunnelMgr.Stop(context.Background())
	// IM 隧道命令（绑定管理员在飞书聊天里发「隧道」/「关隧道」驱动 cloudflared）
	bridge.EnableTunnelOps(tunnelMgr, authBindings)
	// 多机器人：特权只由管理员机器人承载（Q2）。实例现在会标注 BotID
	// （见 channel/lark 的 convertMessage / 长连接回调），故这条判定**已生效**；
	// 渠道级单实例（BotID 为空）仍按"未标注来源"处理，保持单机器人行为不变。
	bridge.EnableBotRouting(botStore)
	// 启动域名存活巡检（Cloudflare 会周期性回收 trycloudflare 域名；连续失败
	// 达阈值自动重启隧道换新域名并推送）。进程生命周期运行，随 os.Exit 终止。
	tunnelMgr.StartHealthCheck(cfg.Auth.Cloudflared.HealthCheckInterval, cfg.Auth.Cloudflared.HealthCheckFailures)

	// API
	if cfg.API.Enabled {
		apiServer := api.NewServer(cfg, store, runner, hooks, bus, skills, commands)
		// 新任务页 agent 选择器的目录（与 runner 用的是同一份 availableAgents，
		// 单一事实源：能选的 agent 就是 AgentManager 真能分发的 agent）。
		apiServer.SetAgents(availableAgents, defaultAgentName)
		apiServer.SetAuth(authSvc, tunnelMgr)
		apiServer.SetFeedback(feedbackStore, previewMgr)
		apiServer.SetCheckRunner(checkRunner)
		apiServer.SetVisualCapture(visualMgr)
		apiServer.SetPushRegistry(pushRegistry)
		apiServer.SetLarkReg(larkreg.NewRegistration(), cfg.Channels.Lark.CredentialsFile)
		apiServer.SetBotStore(botStore)
		apiServer.SetSettingsStore(settingsStore)
		apiServer.SetLogDir(logDir)
		// 自重启：让服务能换掉自己的二进制并重新起来（远程自迭代的最后一环）。
		//
		// 为什么必须由 pieqi 自己做：工作区里跑的 agent 会话持受限令牌（Windows
		// 上是 Low 完整性），它起的任何子进程都继承该令牌 —— 写不了 ~/.pieqi、
		// 也管不了工作区外的进程。而 pieqi 自身以正常身份跑在工作区外，天生有
		// 这些权限。于是"agent 改完代码怎么重启服务"的答案是：交给服务自己。
		//
		// 分工：agent 把编译产物丢进**项目工作区**的约定路径（它写得到），
		// pieqi 负责搬到自己的位置并接管端口（它才有权限）。
		//
		// 交付目录取配置里的项目根，**不能取 os.Getwd()** —— 常驻服务从
		// <dataRoot>/bin 启动，cwd 是那里而不是项目目录；取 cwd 会让交付落点
		// 跑回沙箱外，正好是 agent 写不到的地方（实测踩过）。
		//
		// 编排在后台 goroutine 里跑（先回 202 再退出），见 core.SelfRestart.RestartAndExit。
		stageDir := resolveStagingDir(cfg)
		selfRestart := core.NewSelfRestart(execPath, dataRoot, stageDir, func(msg string, kv ...any) {
			logger.Info(msg, zap.Any("detail", kv))
		})
		logger.Info("self-restart staging dir resolved", zap.String("dir", stageDir))
		// 交接单要记的"被打断的会话"由 runner 提供：只有它知道哪些任务真的
		// 挂在本进程上（running / 等 approval）。running 中的 ACP 会话与
		// claude 子进程都随本进程 exit 一起死，所以这就是受影响的全集。
		selfRestart.SetAffectedFunc(runner.LiveTaskIDs)
		apiServer.SetSelfRestart(selfRestart.HasStaged, func(initiator string) {
			// 身份必须在**请求被受理的当下**记下：此刻发起者那一轮还活着；
			// 等 RestartAndExit 内部推进到落交接单时，它早已随 agent 子进程
			// 消失、状态也不再是 running —— 那时任何"现取"都找不到它。
			selfRestart.SetInitiator(initiator)
			// 端口在这里自己算，而不是捕获后面的 addr —— 那个变量在本次闭包之后
			// 才声明，闭包里读它虽然能编译，但依赖声明顺序会让这段很脆。
			selfRestart.RestartAndExit(fmt.Sprintf(":%d", cfg.Server.Port), os.Exit)
		}, selfRestart.StagingHint)
		// 机器人记录增删后重建运行中的实例（删除一台必须真的让它停止收发）。
		// 钩子是 func()（HTTP 处理流程不宜携带渠道内部错误）；重建失败只记日志，
		// 不阻塞 bots 接口返回 —— 记录已经落盘，下次重建会再试。
		if larkController != nil {
			apiServer.SetBotChangeHook(func() {
				if err := larkController.Rebuild(); err != nil {
					logger.Error("rebuild lark instances after bot change", zap.Error(err))
				}
			})
		}
		// 配置保存后热应用（lark 渠道启用且已接线控制器时）
		if larkController != nil {
			apiServer.SetLarkConfigApplier(larkController.Apply)
		}
		apiServer.Register(r)
		logger.Info("api enabled")
	}

	// 前端 PWA（嵌入）
	registerStatic(r)

	// --- 启动 ---
	// 信号处理：SIGINT/SIGTERM → 优雅关闭所有 ACP 会话（CloseAll 走优雅 Close，
	// adapter 自行 dispose 清 claude 子进程），避免关停时 adapter/claude 子树残留为孤儿。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		if acpMgr != nil {
			_ = acpMgr.CloseAll()
		}
		// 关停自动启动的桥（桥 SIGTERM 优雅关全部会话再退；Windows 直接 KILL）
		if claudeProc != nil {
			_ = claudeProc.Stop(context.Background())
		}
		// Feedback P0：服务器关停回收全部 preview dev server 进程
		previewMgr.CleanupAll()
		// Feedback P2：关停视觉采集服务（Playwright 子进程树）
		_ = visualMgr.Stop(context.Background())
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("pieqi starting", zap.String("addr", addr), zap.String("mode", cfg.Server.Mode))

	// 自重启接续：判定成功时才动手（见上面 restartOutcome 的取值逻辑）。
	//
	// 放在 r.Run 之前、goroutine 里起：接续本身要 spawn agent 并 session/load
	// 重建上下文（每个数十秒），绝不能阻塞端口监听 —— 否则 waitForListener
	// 在旧进程侧会超时，旧进程据此以为"新实例没起来"而拒绝退出，重启就假失败了。
	// 先起 goroutine 再 Run，正是为了保证监听的尽早就绪。
	if len(interruptedIDs) > 0 {
		ids := interruptedIDs
		ver := ""
		initiator := ""
		if restartOutcome.Journal != nil {
			ver = restartOutcome.Journal.StagedSHA256
			initiator = restartOutcome.Journal.InitiatorTaskID
		}
		if initiator != "" {
			logger.Info("self-restart: resuming tasks including the initiator",
				zap.String("initiator", initiator), zap.Int("total", len(ids)))
		}
		go runner.ResumeInterruptedWithInitiator(ids, ver, initiator)
	}

	if err := r.Run(addr); err != nil {
		logger.Fatal("server", zap.Error(err))
	}
}

// resolveStagingDir 解析自重启的交付目录（agent 侧可写的目录）。
//
// 为什么要显式配置而不是"猜一个"：交付目录必须是 **agent 进程写得到**的地方，
// 而那取决于沙箱策略（工作区可写、~/.pieqi 不可写），服务自己无从推断 ——
// 用 os.Getwd() 猜更是错的（常驻服务从 <dataRoot>/bin 启动，cwd 不是项目目录）。
// 猜错的后果是静默的：落点跑到写不进去的地方，agent 只会看到一个
// "staged:false"，然后以为是功能坏了。所以宁可要求显式配置。
//
// 回退顺序：配置 → 当前工作目录（开发态直接 `go run ./cmd/pieqi` 时 cwd 就是仓库）。
func resolveStagingDir(cfg *config.Config) string {
	if d := strings.TrimSpace(cfg.Pieqi.SelfUpdateDir); d != "" {
		return d
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// loadLarkChannelConfig 从凭据配置文件（~/.pieqi/lark_credentials.json）加载
// 飞书渠道运行时配置（扫码一键创建或手工配置落盘），覆盖 config 里的默认值。
// 文件不存在/损坏时静默跳过（降级到 config 里的值）。
//
// 该文件由 POST /api/larkreg/config 与 POST /api/larkreg/poll 写入，
// 见 internal/larkreg 与 internal/api/larkreg.go。
func loadLarkChannelConfig(cfg *config.Config) {
	path := cfg.Channels.Lark.CredentialsFile
	if path == "" {
		return
	}
	fileCfg, ok := larkreg.LoadConfig(path)
	if !ok {
		return // 文件不存在/损坏 = 未接入过
	}
	lc := &cfg.Channels.Lark
	if fileCfg.AppID != "" {
		lc.AppID = fileCfg.AppID
	}
	if fileCfg.AppSecret != "" {
		lc.AppSecret = fileCfg.AppSecret
	}
	if fileCfg.VerifyToken != "" {
		lc.VerifyToken = fileCfg.VerifyToken
	}
	if fileCfg.EncryptKey != "" {
		lc.EncryptKey = fileCfg.EncryptKey
	}
	if fileCfg.EventMode != "" {
		lc.EventMode = fileCfg.EventMode
	}
}

// --- pre-tool-use 子命令 ---

// runPreToolUse 作为 Claude Code PreToolUse hook 子进程运行：
// 从 stdin 读 hook 输入 -> POST /internal/hook -> 输出 permissionDecision。
func runPreToolUse(args []string) {
	fset := flag.NewFlagSet("pre-tool-use", flag.ExitOnError)
	taskID := fset.String("task", "", "task id")
	port := fset.Int("port", 3000, "server port")
	_ = fset.Parse(args)

	if *taskID == "" {
		fmt.Fprintln(os.Stderr, "pre-tool-use: --task is required")
		os.Exit(1)
	}

	// 读取 Claude Code hook 输入
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		outputHookResult("deny", "read stdin: "+err.Error())
		return
	}

	// 解析 hook 输入
	var hookInput struct {
		SessionID string          `json:"session_id"`
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &hookInput); err != nil {
			hookInput.ToolName = "unknown"
		}
	}

	summary := buildToolSummary(hookInput.ToolName, hookInput.ToolInput)

	// POST 到主进程 /internal/hook
	payload := core.HookPayload{
		TaskID:   *taskID,
		ToolName: hookInput.ToolName,
		Summary:  summary,
	}
	payloadBytes, _ := json.Marshal(payload)

	url := fmt.Sprintf("http://localhost:%d/internal/hook", *port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(payloadBytes))
	if err != nil {
		outputHookResult("deny", "pieqi server unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	var result core.HookResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		outputHookResult("deny", "invalid response: "+err.Error())
		return
	}

	outputHookResult(result.PermissionDecision, result.Reason)
}

// outputHookResult 输出 Claude Code PreToolUse hook 的 JSON 决策。
func outputHookResult(decision, reason string) {
	out := map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision,
			"permissionDecisionReason": reason,
		},
	}
	data, _ := json.Marshal(out)
	fmt.Println(string(data))
}

// buildToolSummary 从工具名和输入参数构造人类可读的摘要。
func buildToolSummary(toolName string, toolInput json.RawMessage) string {
	if toolName == "" {
		toolName = "unknown"
	}
	if len(toolInput) == 0 {
		return toolName
	}

	var input map[string]interface{}
	if err := json.Unmarshal(toolInput, &input); err != nil {
		return toolName
	}

	switch toolName {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return toolName + ": " + truncateStr(cmd, 200)
		}
	case "Write", "Edit", "Read", "NotebookEdit":
		if p, ok := input["file_path"].(string); ok {
			return toolName + ": " + p
		}
	}

	// 通用：取前几个字段
	var parts []string
	for k, v := range input {
		parts = append(parts, k+"="+truncateStr(fmt.Sprintf("%v", v), 100))
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		return toolName
	}
	return toolName + " {" + strings.Join(parts, ", ") + "}"
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
