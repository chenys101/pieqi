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
	"path/filepath"
	"strings"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/api"
	"pieqi/internal/channel/lark"
	"pieqi/internal/channel/wechat"
	"pieqi/internal/config"
	"pieqi/internal/core"

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

	// --- 数据目录 ---
	for _, dir := range []string{"data/tasks", "data/sessions", "data/mappings", "data/worktrees"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			logger.Fatal("mkdir data dir", zap.String("dir", dir), zap.Error(err))
		}
	}

	// --- 核心组件 ---
	store, err := core.NewTaskStore("data/tasks")
	if err != nil {
		logger.Fatal("init task store", zap.Error(err))
	}

	bus := core.NewEventBus()
	wm := core.NewWorktreeManager(logger, cfg.Pieqi.WorktreeBase)
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
		cfg.Claude.Model, "", cfg.Pieqi.PermissionMode, cfg.Pieqi.CleanupWorktrees,
		execPath, cfg.Server.Port, cfg.Pieqi.HookTools, hookTimeoutSec,
		cfg.Pieqi.MaxConcurrentPerProject, cfg.Pieqi.BaseBranch,
	)

	// ACP 路径（Phase 2）：use_acp=true 时注入 AgentManager
	if cfg.Pieqi.ACP.UseACP {
		mgr := agent.NewAgentManager(agent.ManagerConfigFromPieqi(cfg.Pieqi, cfg.Claude), logger)
		runner.SetAgentManager(mgr, cfg.Pieqi.ACP.UseACP, cfg.Pieqi.HookTimeout)
		logger.Info("acp agent manager enabled", zap.String("agent_type", cfg.Pieqi.ACP.AgentType))
	}

	// --- Bridge（IM 渠道编排） ---
	userCtx, err := core.NewUserContext("data/users.json", "data/sessions", "data/mappings", cfg.Session.TTL)
	if err != nil {
		logger.Fatal("init user context", zap.Error(err))
	}

	sessionRunner := core.NewSessionRunner(logger, cfg.Claude.WorkDir, cfg.Claude.Model, "", cfg.Claude.Timeout)

	bridge := core.NewBridge(logger, userCtx, sessionRunner, cfg.Claude.Timeout)
	if cfg.Pieqi.Enabled {
		bridge.EnablePieqi(store, runner, bus)
	}
	runner.SetNotifier(bridge.NotifyOrigin)

	// --- Gin ---
	gin.SetMode(cfg.Server.Mode)
	r := gin.Default()

	// 渠道
	if cfg.Channels.Lark.Enabled {
		larkAdapter := lark.New(
			cfg.Channels.Lark.AppID, cfg.Channels.Lark.AppSecret,
			cfg.Channels.Lark.VerifyToken, cfg.Channels.Lark.EncryptKey,
		)
		if err := larkAdapter.Init(r); err != nil {
			logger.Fatal("init lark", zap.Error(err))
		}
		bridge.RegisterReceiver(larkAdapter)
		logger.Info("lark channel enabled")
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

	// API
	if cfg.API.Enabled {
		apiServer := api.NewServer(cfg, store, runner, hooks, bus, skills, commands)
		apiServer.Register(r)
		logger.Info("api enabled")
	}

	// 前端 PWA（嵌入）
	registerStatic(r)

	// --- 启动 ---
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Info("pieqi starting", zap.String("addr", addr), zap.String("mode", cfg.Server.Mode))
	if err := r.Run(addr); err != nil {
		logger.Fatal("server", zap.Error(err))
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
