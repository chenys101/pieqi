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

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
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

	// --- 日志 ---
	var logger *zap.Logger
	if cfg.Server.Mode == "release" {
		logger, err = zap.NewProduction()
	} else {
		logger, err = zap.NewDevelopment()
	}
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}
	defer logger.Sync()

	// P5：pieqi.acp.* 旧字段迁移告警（仅显式配置时触发，旧语义仍生效）
	for _, d := range cfg.Deprecations {
		logger.Warn("config deprecated field", zap.String("hint", d))
	}

	// --- 数据目录（默认 ~/.pieqi，PIEQI_HOME 可覆盖；运行时数据不入仓库） ---
	dataRoot := config.DefaultDataRoot()
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
	store, err := core.NewTaskStore(filepath.Join(dataRoot, "tasks"))
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

	// pieqi 可执行文件绝对路径（PreToolUse hook 子进程回连用）
	execPath, err := os.Executable()
	if err != nil {
		logger.Fatal("get executable path", zap.Error(err))
	}
	execPath, _ = filepath.Abs(execPath)

	hookTimeoutSec := int(cfg.Pieqi.HookTimeout / time.Second)

	runner := core.NewTaskRunner(
		logger, store, wm, bus, hooks,
		"", cfg.Pieqi.PermissionMode, cfg.Pieqi.CleanupWorktrees,
		execPath, cfg.Server.Port, cfg.Pieqi.HookTools, hookTimeoutSec,
		cfg.Pieqi.MaxConcurrentPerProject, cfg.Pieqi.BaseBranch,
	)

	// Feedback P0（p0-design.md）：Checkpoint 存储 + Preview 管理器。
	// runner 挂钩（baseline / Turn 快照捕获），API 侧经 SetFeedback 接线。
	feedbackStore := core.NewFeedbackStore(logger, filepath.Join(dataRoot, "checkpoints"))
	runner.SetFeedbackStore(feedbackStore)
	previewMgr := core.NewPreviewManager(logger)
	// Task 终态/删除时自动回收 preview 进程
	previewMgr.WatchBus(bus)

	// Feedback P1（p1-design.md）：Checks 重跑 runner（事件流复用派生无需状态）。
	checkRunner := core.NewCheckRunner(logger, filepath.Join(dataRoot, "checks"))

	// ACP 路径（Phase 2）→ 已由多 Agent 默认驱动取代（#2）：
	//   agents.claude.transport=sdk-bridge（默认）→ 任务经 agent.Open("claude") 驱动
	//     （桥为主力，桥不可用自动回退 print）；transport=print 且 qoder 已配置 → "qoder"。
	//   以上均不命中时回退旧 use_acp AgentManager（transport=print + use_acp=true）。
	//   默认路径（use_acp=false + transport=print）保持 Phase 1 claude -p 不变。
	var acpMgr *agent.AgentManager
	sessionAgent := ""
	if cfg.Agents.Claude.Transport == "sdk-bridge" {
		sessionAgent = "claude"
	} else if cfg.Agents.Qoder.Transport == "acp" && cfg.Agents.Qoder.ACPConfig().AgentType != "" {
		sessionAgent = "qoder"
	}
	if sessionAgent != "" {
		mgr := agent.NewAgentSessionManager(sessionAgent, agent.ManagerConfig{
			MaxConcurrent: cfg.Pieqi.MaxConcurrentPerProject,
			// 会话空闲回收阈值：复用旧 acp.idle_timeout（默认 15m），轮间保活上限
			IdleTimeout: cfg.Pieqi.ACP.IdleTimeout,
		}, logger)
		acpMgr = mgr
		runner.SetAgentManager(mgr, true, cfg.Pieqi.HookTimeout)
		logger.Info("agent session manager enabled",
			zap.String("agent", sessionAgent), zap.String("transport", cfg.Agents.Claude.Transport))
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
	// qoder：ACP 系 agent 工厂（transport=acp 时注册，业务 agent.Open("qoder") 即用）
	if qc := agent.ACPProviderConfigFromAgents(cfg.Agents); qc.Qoder.AgentType != "" {
		qc.Logger = logger
		agent.ConfigureACPProviders(qc)
		logger.Info("qoder agent provider configured",
			zap.String("transport", cfg.Agents.Qoder.Transport))
	}

	// --- Gin ---
	gin.SetMode(cfg.Server.Mode)
	r := gin.Default()

	// 渠道：lark 走控制器（支持配置热应用）；wechat 保持原样
	var larkController *larkChannelController
	if cfg.Channels.Lark.Enabled {
		larkController = newLarkChannelController(logger, bridge, r)
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

	// --- Auth (Feishu binding + Cloudflared tunnel) ---
	authBindings, err := auth.NewBindingStore(cfg.Auth.FeishuBindingFile)
	if err != nil {
		logger.Fatal("init binding store", zap.Error(err))
	}
	authTokens := auth.NewTokenStore()
	authSvc := &auth.Service{
		Debug:    auth.NewDebugSwitch(cfg.Auth.DebugSkipAllAuth),
		Bindings: authBindings,
		Tokens:   authTokens,
		Limiter:  auth.NewIPLimiter(cfg.Auth.RateLimit.MaxFailuresPerMin, cfg.Auth.RateLimit.BlacklistDuration),
		Audit:    auth.NewAuditLogger(logger),
	}
	tunnelMgr := auth.NewTunnelManager(auth.TunnelConfig{
		BinaryPath: cfg.Auth.Cloudflared.BinaryPath,
		LocalURL:   fmt.Sprintf("http://localhost:%d", cfg.Server.Port),
		Tokens:     authTokens,
		Logger:     logger,
		// 跨重启清理孤儿 cloudflared：强杀服务时 defer Stop 不执行，PID 文件
		// 让下次 Start 能杀掉残留进程（见 auth.TunnelManager.cleanupOrphans）。
		PIDFile: filepath.Join(dataRoot, "cloudflared.pid"),
	})
	defer tunnelMgr.Stop(context.Background())
	// IM 隧道命令（绑定管理员在飞书聊天里发「隧道」/「关隧道」驱动 cloudflared）
	bridge.EnableTunnelOps(tunnelMgr, authBindings)

	// API
	if cfg.API.Enabled {
		apiServer := api.NewServer(cfg, store, runner, hooks, bus, skills, commands)
		apiServer.SetAuth(authSvc, tunnelMgr)
		apiServer.SetFeedback(feedbackStore, previewMgr)
		apiServer.SetCheckRunner(checkRunner)
		apiServer.SetLarkReg(larkreg.NewRegistration(), cfg.Channels.Lark.CredentialsFile)
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
	if err := r.Run(addr); err != nil {
		logger.Fatal("server", zap.Error(err))
	}
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
