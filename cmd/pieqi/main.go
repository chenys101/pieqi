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
	"net"
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
	// 相对路径一律先钉成绝对路径（2026-10-21 事故：自重启被静默关闭）。
	//
	// 这不是"顺手规范化"：resolveStagingDir 用 dir(configPath) 推导交付目录，而
	// **相对的 configPath 让这个推导恒错** —— 服务是以 cwd=~/.pieqi/bin 启动的
	// （PIEQI_CONFIG 落空时默认值就是 "config.yaml"），于是交付目录算成
	// ~/.pieqi/bin/build（不存在）⇒ staged 恒 false ⇒ POST /api/admin/restart 409。
	// 连"有产物就换、没产物就只重启"的 restart-only 也未接线（501）。
	// 失败方式完全静默：只有一条启动期 ERROR，之后 agent 每次投递都白投。
	//
	// 钉成绝对路径对重启没有副作用：SelfRestart 派生的新进程继承环境，得到的是
	// 同一个路径；而且它让"下次没人记得设 PIEQI_CONFIG"这条路径也随之正确
	// —— 新进程的 cwd 就是 exe 所在目录，默认的 config.yaml 会解析到运行位自己那份。
	cfgPath = absolutizeConfigPath(cfgPath, func() (string, error) { return os.Getwd() })
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
	// 启动时读不出来的任务文件必须**显式报出来**（原先是被静默跳过的）。
	//
	// 为什么值得单独一段日志：这类失败的表现是"任务凭空消失"，而此前日志里
	// 一个字都没有 —— 排查时无从下手。特别是"全长全零"那种，它是掉电/蓝屏在
	// rename 与数据落盘之间留下的痕迹，意味着磁盘上可能还有别的文件同样受损，
	// 需要人知道（见 core.describeBadJSON 与 TaskStore.persist 的说明）。
	if bad := store.BadFiles(); len(bad) > 0 {
		for _, b := range bad {
			logger.Error("task file unreadable at startup; task dropped",
				zap.String("file", b.Name),
				zap.String("reason", b.Reason),
				zap.String("kept_as", b.KeptAs))
		}
		logger.Warn("some task files could not be read; see the errors above",
			zap.Int("count", len(bad)))
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

	// Feedback P2（p2-design.md）：Evidence Push。
	// （视觉采集——截图/console/network——已于 2026-10-15 整体移除，见 ADR-0012/0013。）
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
	//
	// 2026-10-22 重构：目录不再来自 config.yaml 的 agents 段，而是「manifest（代码事实）
	// + Settings.Agents（运行期选择）」合成。于是接入新 agent 只改 manifest.go 一处，
	// 用户要开/关某个 agent 则改运行期设置（可热应用，不必动配置、不必重启）。
	agentRT := agentRuntimeFromSettings(settingsStore.Get())
	availableAgents := agent.AvailableAgents(agentRT)
	agentNames := make([]string, 0, len(availableAgents))
	for _, a := range availableAgents {
		agentNames = append(agentNames, a.Name)
	}
	defaultAgentName := agent.AgentClaude
	if len(agentNames) > 0 {
		defaultAgentName = agentNames[0]
	}

	// 是否进入 session 驱动路径：claude 走桥，或有任一 ACP 系 agent 被启用。
	acpProviders := agent.ACPProviderConfigFromRuntime(agentRT)
	claudeTransportRT := agentRT.ClaudeTransportOrDefault()
	if claudeTransportRT == "sdk-bridge" || len(acpProviders.Agents) > 0 {
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
			zap.String("claude_transport", claudeTransportRT))
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
		// transport 归运行期设置（"这台机器上桥能不能跑"是运行期事实）。
		cc.Transport = claudeTransportRT
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
	// ACP 系 agent（qoder / dsh / codebuddy）工厂：被启用的才进表，业务 agent.Open("<名字>") 即用。
	// 名字与 AvailableAgents 的 Name 同一个串，两处共用 ACPProviderConfigFromRuntime 的判据。
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

	// 自重启器在下面的 API 分支里构造，但 serve 处也要用（注入 listener 与 rebind），
	// 所以提前声明。为 nil = 没接线（API 未启用），此时不参与重启编排。
	var selfRestart *core.SelfRestart

	// API
	if cfg.API.Enabled {
		apiServer := api.NewServer(cfg, store, runner, hooks, bus, skills, commands)
		// 新任务页 agent 选择器的目录（与 runner 用的是同一份 availableAgents，
		// 单一事实源：能选的 agent 就是 AgentManager 真能分发的 agent）。
		apiServer.SetAgents(availableAgents, defaultAgentName)
		// 目录按**当前**设置现算：agent 开关存在 settings.json 里、可热改，
		// 只留启动快照会让"设置页打开了 codebuddy"必须重启才生效。
		apiServer.SetAgentCatalogSource(func() []agent.AgentInfo {
			return agent.AvailableAgents(agentRuntimeFromSettings(settingsStore.Get()))
		})
		apiServer.SetAuth(authSvc, tunnelMgr)
		apiServer.SetFeedback(feedbackStore, previewMgr)
		apiServer.SetCheckRunner(checkRunner)
		apiServer.SetPushRegistry(pushRegistry)
		apiServer.SetLarkReg(larkreg.NewRegistration(), cfg.Channels.Lark.CredentialsFile)
		apiServer.SetBotStore(botStore)
		apiServer.SetSettingsStore(settingsStore)
		apiServer.SetLogDir(logDir)
		// 自重启：让服务能换掉自己的二进制并重新起来（远程自迭代的最后一环）。
		//
		// 为什么必须由 pieqi 自己做：工作区里跑的 agent 会话持受限令牌（Windows
		// 上是 Low 完整性），它起的任何子进程都继承该令牌 —— 写不了 ~/.pieqi、
		// 也管不了工作区外的进程。而 pieqi 自身由 svchost/Explorer 启动，不受该
		// 令牌约束，天生有这些权限。于是"agent 改完代码怎么重启服务"的答案是：
		// 交给服务自己。
		//
		// 分工：agent 把编译产物丢进**仓库内约定的交付目录**（它写得到，因为工作区
		// 就是仓库子树），pieqi 负责把它搬到位（仓库外的运行位）并接管端口。
		//
		// 编排在后台 goroutine 里跑（先回 202 再退出），见 core.SelfRestart.RestartAndExit。
		// 交付目录由约定推导：<dataRoot>/bin（默认 ~/.pieqi/bin）。
		// 与 config.yaml 放在哪里**无关** —— 见 resolveStagingDir 的说明。
		stageDir := resolveStagingDir(dataRoot)
		selfRestart = core.NewSelfRestart(execPath, dataRoot, stageDir, func(msg string, kv ...any) {
			logger.Info(msg, zap.Any("detail", kv))
		})
		if stageDir == "" {
			// 算不出来（dataRoot 为空，或 <dataRoot>/bin 不存在/不可用）：
			// **显式报错**，绝不静默退回 dir(execPath) 那个在"运行位在仓库外"的
			// 正确部署下必然错误的路径。这条告警是刻意的 —— 静默退化的表现是
			// staged 恒 false，没人能看出是环境问题还是功能坏了。
			logger.Error("self-restart disabled: could not resolve the delivery (staging) directory",
				zap.String("data_root", dataRoot),
				zap.String("expected", filepath.Join(dataRoot, "bin")),
				zap.String("hint", "约定：交付目录 = <dataRoot>/bin（默认 ~/.pieqi/bin）。"+
					"请确认该目录存在（构建/部署一次即可）"))
		}
		logger.Info("self-restart staging dir resolved",
			zap.String("dir", stageDir),
			zap.String("source", stagingDirSource(dataRoot)))
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
		// 只重启（不换二进制）：见 internal/api/admin.go 的 postRestartOnly。
		//
		// 它与上面的换版路径共用同一套"让出端口 → 等新实例报到 → 退出/回滚"编排，
		// 区别只在 core.SelfRestart 里少掉"改名备份 + 装上 staged"那一步。
		// 交接单的摘要取自**当前** exe（SelfRestart.stagedOrSelfSHA256 的兜底），
		// 因此新进程校验通过 ⇒ 在途会话照常被接回，行为与换版重启一致。
		apiServer.SetRestartOnly(func(initiator string) {
			selfRestart.SetInitiator(initiator)
			selfRestart.RestartAndExitNoSwap(fmt.Sprintf(":%d", cfg.Server.Port), os.Exit)
		})
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

	// 补齐"有 prompt 但没标题"的任务：标题只在创建那一刻生成，失败就再也不回来
	// （2026-10-16~20 因 Claude 凭据失效连续 4 天全线为空，见 core/title.go）。
	//
	// 与上面的接续一样放 goroutine：补齐会起 agent 进程，绝不能挡端口监听。
	// 这里刻意在**每次启动**都扫一遍而不是只在"上一次失败"时扫：判据简单
	// （Title 为空 = 缺），不依赖任何跨重启的失败记忆。
	go runner.BackfillTitles(false)

	// 先自己 bind（带退避重试），再交给 gin —— 不用 r.Run(addr)。
	//
	// 为什么不让 r.Run 自己 bind：它失败即 FATAL，而自重启场景下"失败"只是
	// 旧实例还没释放端口（见 listenWithRetry 的说明）。先拿到 listener 再 serve，
	// 就把"重启把服务弄挂"降级成"重启慢几百毫秒"。
	ln, err := listenWithRetry(addr, bindRetryTimeout, logger)
	if err != nil {
		logger.Fatal("server", zap.Error(err))
	}

	// 把 listener 交给自重启编排：它需要在等新实例之前**让出端口**，
	// 否则新实例永远 bind 不上（2026-10-13 死锁事故，见 core.RestartAndExit）。
	// 赋值发生在 r.RunListener 之前，故不存在数据竞争。
	//
	// selfRestart 为 nil 表示 API 未启用 ⇒ 根本没人能触发重启，无需接线。
	if selfRestart != nil {
		selfRestart.SetListener(ln)
		// 新实例起不来时，用它抢回端口继续服务
		// （最坏情况 = "重启没成功但服务一直在"，而不是服务消失）。
		selfRestart.SetRebindFunc(func() error {
			ln2, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			logger.Info("self-restart: re-bound the port, serving with the previous binary", zap.String("addr", addr))
			if err := r.RunListener(ln2); err != nil && !core.IsListenerClosed(err) {
				return err
			}
			return nil
		})
	}

	// ★ 报到自己已开始服务 —— 这是自重启能安全退出旧进程的**唯一**依据。
	//
	// 必须在 listener 已建立之后、且尽早调用：旧进程就卡在等这个信号
	// （见 core.SelfRestart.RestartAndExit → awaitRestartReady）。
	// 早于此（如 spawn 后就写）等于告诉旧进程"可以退了"，而此时新实例还不能
	// 处理请求；晚太多则旧进程白等。listener 就绪、serve 未起，正是那个点。
	//
	// 普通启动（非自重启）也会写：无人等待，多一个几十字节的文件，无害。
	// 失败只记日志不阻断：最坏是旧进程等不到而继续存活（服务不中断），
	// 比"因为写不出信号就拒绝启动"好得多。
	if err := core.MarkRestartReady(dataRoot); err != nil {
		logger.Warn("mark restart ready failed; an awaiting old process will keep serving", zap.Error(err))
	}

	if err := r.RunListener(ln); err != nil {
		// 自重启让位时会 Close listener，RunListener 随即返回 —— 那不是错误，
		// 而是"我正在把端口交出去"的正常路径，不能再 FATAL（那会让旧实例
		// 在新实例尚未接手时就死掉，正是要消灭的"服务消失"）。
		if core.IsListenerClosed(err) {
			// 正常让位路径：RestartAndExit 已把 listener 关掉，并在后台完成了
			// "等新实例报到 → 退出 / 回滚 + 抢回端口"的决策（见那边的说明）。
			// 这里只需安静退出 serve 调用，**不要**再驱动一次决策
			// （那会让两个 goroutine 同时抢端口）。
			logger.Info("listener released for self-restart; the outcome is handled in the background")
			select {} // 让后台决策决定生死：要么 exit，要么在 rebind 里重新 serve
		}
		logger.Fatal("server", zap.Error(err))
	}
}

// bindRetryTimeout 是 listenWithRetry 的总等待上限。
//
// 取值依据：自重启换二进制时，旧实例可能尚未释放端口（它的 LISTEN 需要一点时间
// 才随进程退出而释放）。这里给到 10s，比实际需要宽两个数量级，
// 既足够覆盖最坏情况（旧实例在退出前还要清理 preview/桥等子进程），
// 又不至于让"端口真被别人占着"这种配置错误拖太久才报出来。
//
// 注：2026-10-13 起，旧实例**只在收到新实例的就绪信号后**才退出
// （见 core.MarkRestartReady / awaitRestartReady），所以这个竞态窗口已从
// "必然发生"降级为"理论上的窄窗口"。本重试保留为纯安全网 —— 它不再承担
// 修复职责，但仍能覆盖外部快速 kill + 立即重启等本进程无法协调的场景。
const bindRetryTimeout = 10 * time.Second

// listenWithRetry 在端口被瞬时占用时退避重试 bind，返回已就绪的 listener。
//
// 为什么需要它（2026-10-13 实测事故）：自重启换二进制后，新实例绑不上端口就
// 直接 FATAL 退出，而旧实例此时**已经退出** —— 服务彻底消失。
//
// 当时的根因（现已在 selfrestart 侧修掉）：旧实例的健康判据是
// canDial(addr)，而它自己还监听着该端口 ⇒ 探测第一次就返回 true ⇒ 旧实例
// 立刻 exit(0)，根本没等新实例 bind。Windows 上 Go 不设 SO_REUSEADDR，
// 旧实例的 LISTEN 尚未释放 ⇒ 新实例 EADDRINUSE。
// 实测日志特征：`new instance spawned` 与 `is serving` **同一毫秒**（真进程不可能
// 0ms 起好），随后 `FATAL listen tcp :3000: bind: Only one usage of each socket address`。
//
// **正解是换掉那个同义反复的信号源**（新实例写就绪文件报到，见
// core.awaitRestartReady），而不是靠这里重试兜住。本重试作为第二道防线保留：
// 真被别的程序长期占用时，仍会以原来的 FATAL 结束（不会无限等）。
func listenWithRetry(addr string, timeout time.Duration, logger *zap.Logger) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	delay := 50 * time.Millisecond
	var lastErr error
	for attempt := 1; ; attempt++ {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			if attempt > 1 {
				logger.Info("bind succeeded after retry",
					zap.String("addr", addr), zap.Int("attempt", attempt))
			}
			return ln, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		if attempt == 1 {
			// 只在首次失败时记一条：这是"旧实例还没走干净"的信号，
			// 有它才能把"重启慢了几百毫秒"与"端口真被占"区分开。
			logger.Info("bind busy, retrying (old instance may still be shutting down)",
				zap.String("addr", addr), zap.Error(err))
		}
		time.Sleep(delay)
		if delay < 500*time.Millisecond {
			delay *= 2
		}
	}
}

// agentRuntimeFromSettings 把运行期设置里的 agent 选择转成 agent 包的运行时配置。
//
// 为什么放在 main 而不是 agent 或 core 包：agent 包不能 import core（core 用 agent），
// core 也不该知道 agent 的 manifest 形态。main 是**唯一**同时依赖两者的地方，
// 转换逻辑放这里既不断开依赖方向，也不制造第三份真相。
func agentRuntimeFromSettings(s core.Settings) agent.AgentRuntimeConfig {
	rt := agent.AgentRuntimeConfig{
		Enabled:         s.Agents.Enabled,
		ClaudeTransport: s.Agents.ClaudeTransport,
	}
	if len(s.Agents.SpawnOverrides) > 0 {
		rt.Overrides = make(map[string]config.ACPConfig, len(s.Agents.SpawnOverrides))
		for name, cmd := range s.Agents.SpawnOverrides {
			if len(cmd) == 0 {
				continue // 空数组 = 不覆盖，用 manifest 默认（与"显式清空"同义）
			}
			rt.Overrides[name] = config.ACPConfig{SpawnCommand: cmd}
		}
	}
	return rt
}

// absolutizeConfigPath 把相对路径的 config.yaml 钉成绝对路径；已是绝对路径时原样返回。
//
// 存在理由见 main 里调用点的注释：**相对路径会让交付目录推导恒错**（dir("config.yaml")
// = "." ⇒ 交付目录 = ./build ⇒ 实际是 cwd/build ⇒ 在"运行副本在仓库外"的正确部署下
// 那个目录压根不存在），而这正是"发版流程已固化、自重启却突然 409"的根因。
//
// cwdFn 可注入，纯函数便于单测；cwd 取不到（进程被删除、权限异常）时退回原值并
// 不报错 —— 那种情况下 resolveStagingDir 本来也会返回空串，由它自己的告警兜底。
func absolutizeConfigPath(path string, cwdFn func() (string, error)) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	wd, err := cwdFn()
	if err != nil || wd == "" {
		return path
	}
	return filepath.Join(wd, path)
}

// resolveStagingDir 解析自重启的**交付目录**（agent 往哪投递 pieqi.new[.exe]）。
//
// 约定优于配置：**交付目录 = <config.yaml 所在目录>/build**，不引入任何新配置项。
//
// # 为什么不能取 dir(execPath)（2026-10-14 修订；此前只有这一条，在正确部署下恒错）
//
// 它只在"运行产物与交付产物同目录"时成立，而**这两者恰恰必须分开**：
//
//	agent  持受限令牌（Windows 上 Low 完整性），只能写工作区
//	服务   持正常令牌，写得了 ~/.pieqi，因而**必须**在仓库外运行
//
// 仓库目录本身带 Low 强制完整性标签（icacls 可见 Mandatory Label\Low
// Mandatory Level:(OI)(CI)(NW)），区内新建的每个文件都继承它；而新进程的完整性
// = min(父令牌, **镜像文件标签**) ⇒ 跑仓库内的 exe 必然是 Low 进程 ⇒ 写不了
// ~/.pieqi（POST /api/tasks 500、日志零增长）。所以：
//
//	运行产物必须在仓库外  %USERPROFILE%\.pieqi\bin\pieqi.exe   （见 build/restart.bat）
//	交付产物必须在仓库内  <repo>\build\pieqi.new.exe           （agent 写得进去）
//
// 两个需求指向**不同**目录，所以 dir(execPath) 在正确部署下永远算错：它会算出
// ~/.pieqi\bin\pieqi.new.exe —— 那正是 agent 写不到的地方。症状是静默的：
// agent 投递失败 → GET /api/admin/restart 恒为 staged:false → 看起来"功能坏了"。
//
// # 为什么"config.yaml 所在目录"就是仓库根（而不是又一个需要人告诉服务的值）
//
// configPath 不是新增的耦合，它是**启动服务本来就必需的输入**：没有它服务起不来
// （见 main 开头的 config.Load）。而在本项目的部署方式里，它与仓库根是同一个目录
// —— 由仓库自身的约定固定：config.yaml 与 go.mod 同级（config.yaml 不入库，
// 但它的模板 config.example.yaml 与 go.mod 一起在仓库根）。
//
// 因此交付目录可以由"已经必须存在的那个输入"推导出来，无需新的环境变量：
//
//	PIEQI_CONFIG=D:\code\go\pieqi\config.yaml  ⇒  交付目录 D:\code\go\pieqi\build
//
// 这正是"约定优于配置"该有的形态：**不新增旋钮，只依赖既有约定**。相比之下
// 环境变量方案（曾短暂采用）要求每个启动脚本都记得设它，漏设即静默退化 ——
// 把一条本可推导的约定变成了一个必须维护的配置。
//
// # 校验（宁可关闭功能，也不要指向一个假目录）
//
// 交付目录由推导而来，但仍要校验其可用性：不可用则返回空串，由调用方明确报错并
// 关闭自重启，**绝不静默退回 dir(execPath)** 那个必然错误的路径。判据见
// usableStagingDir（存在、是目录、非符号链接）。
//
// # 为什么 2026-10-22 改为由 <dataRoot>/bin 推导（原来的 dir(config.yaml)/build 不再使用）
//
// 原方案把"config.yaml 所在目录"当作仓库根，隐含假设 **config.yaml 与 go.mod 同级**。
// 但 config.yaml 实际上是**运行时产物**：它含飞书凭据路径、隧道 token 路径、端口、
// 个人域名，这些东西根本不该随代码走，实践中它也确实被放进了运行位
// （~/.pieqi/bin/config.yaml）。一旦如此，假设当场失效：
//
//	dir(~/.pieqi/bin/config.yaml) = ~/.pieqi/bin  ⇒  交付目录 ~/.pieqi/bin/build（不存在）
//
// 症状是**静默的**：stagingDir="" ⇒ POST /api/admin/restart 退化为 restart-only，
// 看起来"重启成功了"，其实新版本从未被装上。
//
// 新判据只依赖 dataRoot（= PIEQI_HOME 或 ~/.pieqi）——它本来就必须存在（任务、
// 日志、worktree 全在下面），且与 config.yaml 放在哪里完全无关：
//
//	交付目录 = <dataRoot>/bin    ⇒  默认 ~/.pieqi/bin/pieqi.new.exe
//
// 代价与取舍（刻意接受的）：
//   - 交付位从"仓库内"移到"运行位旁边"。这**不违反**"运行产物必须在仓库外"那条纪律
//     —— 那条纪律是为了避免 Low 完整性标签，而 <dataRoot> 在仓库外，天然是 Medium。
//   - 但交付物必须用 copy 投放（不能 move/rename）：move 会把源文件的 Low 标签一起
//     搬过来。deploy-dev.sh 已按此实现。
//   - agent 若在受限沙箱里跑，可能写不到 ~/.pieqi。此时仍可把产物放到仓库 build/
//     再手工 copy —— 但那属于部署动作，不再由服务端推导。
//
// 仍然保留的校验：目录必须存在、是真目录、非链接（见 usableStagingDir）。
// 与旧实现一致，**不代为创建** —— 那会掩盖"根本没部署过"。
func resolveStagingDir(dataRoot string) string {
	if strings.TrimSpace(dataRoot) == "" {
		return ""
	}
	return usableStagingDir(filepath.Join(dataRoot, "bin"))
}

// stagingDirSource 说明交付目录是怎么算出来的，仅供日志取证。
//
// 存在的理由：排查"agent 投递了但服务说 staged:false"时，第一件要问的事就是
// "服务到底在看哪个目录、依据是什么"。把推导依据打进启动日志，这个问题无需再猜。
func stagingDirSource(dataRoot string) string {
	if strings.TrimSpace(dataRoot) == "" {
		return "unresolved:empty data root"
	}
	return "convention:dataRoot/bin"
}

// usableStagingDir 校验推导出的交付目录是否可用；不可用返回空串（调用方据此明确报错）。
//
// 抽成独立函数是为了让"算出来但不可用 ⇒ 返回空"这条判据能被单元测试直接钉住 ——
// 它是本文件最容易写错、且错了完全静默的一点。
//
// Lstat 而非 Stat：要识破符号链接/junction **本身**（Stat 会跟过去，看不出差别）。
// 沙箱按解析后的最终路径判定写权限，经链接绕道会把交付物写到预期之外的地方，
// 并连带丢掉完整性标签语义，所以链接一律不接受。
func usableStagingDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	st, err := os.Lstat(abs)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	return abs
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
