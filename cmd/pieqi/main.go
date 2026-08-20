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
	"pieqi/internal/api"
	"pieqi/internal/auth"
	"pieqi/internal/channel/lark"
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

	// 加载 Device Flow 扫码拿到的飞书凭据（若存在则覆盖 config 里的 app_id/app_secret）
	if err := loadLarkCredentials(cfg); err != nil {
		// 凭据加载失败不阻断启动，只警告（此时 logger 还没初始化，用 log.Printf）
		log.Printf("warn: load lark credentials: %v", err)
	}

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

	// --- 数据目录（默认 ~/.pieqi，PIEQI_HOME 可覆盖；运行时数据不入仓库） ---
	dataRoot := config.DefaultDataRoot()
	for _, dir := range []string{
		filepath.Join(dataRoot, "tasks"),
		filepath.Join(dataRoot, "worktrees"),
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

	// ACP 路径（Phase 2）：use_acp=true 时注入 AgentManager
	var acpMgr *agent.AgentManager
	if cfg.Pieqi.ACP.UseACP {
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

	// --- Gin ---
	gin.SetMode(cfg.Server.Mode)
	r := gin.Default()

	// 渠道
	if cfg.Channels.Lark.Enabled {
		var larkAdapter *lark.Adapter
		if cfg.Channels.Lark.EventMode == "longconn" {
			larkAdapter = lark.NewLongConn(cfg.Channels.Lark.AppID, cfg.Channels.Lark.AppSecret).
				WithLogger(logger)
		} else {
			larkAdapter = lark.New(
				cfg.Channels.Lark.AppID, cfg.Channels.Lark.AppSecret,
				cfg.Channels.Lark.VerifyToken, cfg.Channels.Lark.EncryptKey,
			)
		}
		if err := larkAdapter.Init(r); err != nil {
			logger.Fatal("init lark", zap.Error(err))
		}
		bridge.RegisterReceiver(larkAdapter)
		// 长连接模式需要后台 goroutine 启动 wss；webhook 模式 Start 是 no-op
		larkCtx, larkCancel := context.WithCancel(context.Background())
		defer larkCancel()
		go func() {
			if err := larkAdapter.Start(larkCtx); err != nil {
				logger.Error("lark long-connection exited", zap.Error(err))
			}
		}()
		logger.Info("lark channel enabled", zap.String("event_mode", cfg.Channels.Lark.EventMode))
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
	})
	defer tunnelMgr.Stop(context.Background())

	// API
	if cfg.API.Enabled {
		apiServer := api.NewServer(cfg, store, runner, hooks, bus, skills, commands)
		apiServer.SetAuth(authSvc, tunnelMgr)
		apiServer.SetLarkReg(larkreg.NewRegistration(), cfg.Channels.Lark.CredentialsFile)
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
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("pieqi starting", zap.String("addr", addr), zap.String("mode", cfg.Server.Mode))
	if err := r.Run(addr); err != nil {
		logger.Fatal("server", zap.Error(err))
	}
}

// loadLarkCredentials 从 ~/.pieqi/lark_credentials.json 加载 Device Flow
// 扫码拿到的 app_id/app_secret，覆盖 config 里的默认值。文件不存在或
// 损坏时静默跳过（降级到 config 里的 app_id/app_secret）。
//
// 该文件由 POST /api/larkreg/poll 在用户扫码确认后写入，见 internal/larkreg。
func loadLarkCredentials(cfg *config.Config) error {
	path := cfg.Channels.Lark.CredentialsFile
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在是合法状态（未接入过）
		}
		return fmt.Errorf("read lark credentials: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var c struct {
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		// 损坏文件：不阻断启动，只警告（无 logger 可用，降级静默）
		return nil
	}
	if c.AppID != "" && c.AppSecret != "" {
		cfg.Channels.Lark.AppID = c.AppID
		cfg.Channels.Lark.AppSecret = c.AppSecret
	}
	return nil
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
