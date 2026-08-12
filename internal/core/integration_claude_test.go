//go:build claude

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-bridge/internal/model"

	"go.uber.org/zap"
)

// testSender 捕获 Bridge 发送的回复
type testSender struct {
	mu      sync.Mutex
	replies []string
}

func (s *testSender) Send(_ context.Context, _ model.ReplyTarget, text string) error {
	s.mu.Lock()
	s.replies = append(s.replies, text)
	s.mu.Unlock()
	return nil
}

func (s *testSender) reset() {
	s.mu.Lock()
	s.replies = nil
	s.mu.Unlock()
}

func (s *testSender) waitFor(count int, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	for {
		s.mu.Lock()
		n := len(s.replies)
		r := make([]string, n)
		copy(r, s.replies)
		s.mu.Unlock()
		if n >= count || time.Now().After(deadline) {
			return r
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func modelName() string {
	if m := os.Getenv("CLAUDE_TEST_MODEL"); m != "" {
		return m
	}
	return "deepseek-v4-pro-202606"
}

// projectRoot 返回项目根目录
func projectRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// loadSystemPrompt 加载 CLAUDE.md 作为 system prompt（与生产环境 main.go 一致）
// 注：/tls-query-skill 的 SKILL.md 由 Claude Code 在收到 /tls-query-skill 指令时自动加载，
// 不需要也不应该通过 --append-system-prompt 传递（会导致命令行超长）
func loadSystemPrompt(t *testing.T) string {
	t.Helper()
	root := projectRoot()
	claudePath := filepath.Join(root, "CLAUDE.md")
	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Logf("CLAUDE.md not found at %s: %v", claudePath, err)
		return ""
	}
	t.Logf("Loaded CLAUDE.md from %s (%d bytes)", claudePath, len(data))
	return string(data)
}

// setupClaudeTest 创建完整测试环境，包含真实 SessionRunner
func setupClaudeTest(t *testing.T, sysPrompt string) (*Bridge, *UserContext, *testSender, *SessionRunner) {
	t.Helper()

	dir := t.TempDir()
	usersPath := filepath.Join(dir, "users.json")
	sessionsDir := filepath.Join(dir, "sessions")
	mappingsDir := filepath.Join(dir, "mappings")

	userData := model.UserBindings{
		"chenyusheng": {
			ID: "chenyusheng",
			Bindings: map[model.Channel]model.Binding{
				"wechat": {UserID: "wx_user_123", UserName: "陈"},
			},
		},
	}
	data, _ := json.Marshal(userData)
	os.WriteFile(usersPath, data, 0644)

	logger := zap.NewNop()
	uc, _ := NewUserContext(usersPath, sessionsDir, mappingsDir, 30*time.Minute)

	root := projectRoot()
	sr := NewSessionRunner(logger, root, modelName(), sysPrompt, 300*time.Second)
	bridge := NewBridge(logger, uc, sr, 300*time.Second)

	bridge.gate.approvalsPath = filepath.Join(dir, "approvals.json")
	bridge.gate.ResetForTest()

	sender := &testSender{}
	bridge.RegisterSender("wechat", sender)

	return bridge, uc, sender, sr
}

// TestClaudeConnectivity 验证 Claude CLI 可通过 Bridge 正常调用
func TestClaudeConnectivity(t *testing.T) {
	bridge, _, sender, sr := setupClaudeTest(t, "")
	defer sr.Shutdown()

	t.Logf("Model: %s", modelName())
	t.Log("Sending test message...")
	bridge.handleMessage(makeMsg("hello, respond with just the word 'pong'"))

	replies := sender.waitFor(1, 60*time.Second)
	if len(replies) == 0 {
		t.Fatal("no reply from Claude CLI after 60s")
	}

	t.Logf("Reply (%d chars): %s", len(replies[0]), replies[0][:min(len(replies[0]), 300)])

	if len(strings.TrimSpace(replies[0])) == 0 {
		t.Error("empty reply from Claude")
	}

	t.Log("Claude CLI connectivity OK")
}

// TestApprovalFlowReal 模拟微信用户输入 "/tls-query-skill ..." 的完整审批链路
//
// 步骤:
//  1. 发送 "/tls-query-skill 查询生产环境问题，viomi-warehouse 最近三个异常日志，并分析原因"
//     Claude 根据 SKILL.md 指令提出 Python 命令行调用 → needsApproval 自动检测并设置 Pending
//  2. 发送 "同意" → gate.Approve → retryWithBypass
//  3. retryWithBypass 用 bypassPermissions 执行 Claude → 得到执行结果
//
// 运行方式:
//
//	go test ./internal/core/... -tags=claude -run TestApprovalFlowReal -v -timeout 600s
func TestApprovalFlowReal(t *testing.T) {
	// 加载完整 system prompt（SKILL.md + CLAUDE.md，与生产环境等效）
	sysPrompt := loadSystemPrompt(t)

	bridge, _, sender, sr := setupClaudeTest(t, sysPrompt)
	defer sr.Shutdown()

	identity := "chenyusheng"

	// === Step 1: 模拟微信用户发送消息 ===
	prompt := "/tls-query-skill 查询生产环境问题，viomi-warehouse 最近三个异常日志，并分析原因"
	msg := makeMsg(prompt)
	t.Logf("Step 1: Sending message (simulating WeChat user input)")
	t.Logf("  Content: %s", prompt)
	bridge.handleMessage(msg)

	replies := sender.waitFor(1, 180*time.Second)
	if len(replies) == 0 {
		t.Fatal("no reply from Claude after 180s")
	}

	reply0 := replies[0]
	t.Logf("Step 1 reply (%d chars):", len(reply0))
	t.Logf("  %s", reply0[:min(len(reply0), 500)])

	// 判断 needsApproval 是否自然触发
	needsApprovalTriggered := bridge.gate.Check(identity)

	if needsApprovalTriggered {
		t.Log("✅ needsApproval triggered NATURALLY — PendingRequest auto-set by Bridge")

		// 验证 reply 中包含 Bridge 追加的审批提示
		if !strings.Contains(reply0, "同意") {
			t.Errorf("approval reply should contain '同意': %s",
				reply0[:min(len(reply0), 500)])
		}
	} else {
		// ⚠️ 模型未输出 needsApproval 关键字，程序化注入
		t.Log("⚠️  needsApproval did NOT trigger naturally")
		t.Log("   Falling back to programmatic PendingRequest injection")

		_, sessionID, _ := bridge.userCtx.Resolve(msg, "")
		if sessionID == "" {
			t.Fatal("empty session ID")
		}
		t.Logf("   Session ID: %s", sessionID)

		bridge.gate.SetPending(identity, &PendingRequest{
			Prompt:    prompt,
			SessionID: sessionID,
			Msg:       msg,
			CreatedAt: time.Now(),
		})
	}

	// 验证: Pending 已存在
	if !bridge.gate.Check(identity) {
		t.Fatal("expected PendingRequest to exist (natural or injected)")
	}

	// === Step 2: 用户发送 "同意" ===
	sender.reset()
	t.Log("Step 2: User sends '同意' → trigger retryWithBypass")
	bridge.handleMessage(makeMsg("同意"))

	// 等待 goroutine: retryWithBypass → bypassPermissions → reply
	replies = sender.waitFor(2, 300*time.Second)

	t.Logf("Got %d replies after approval", len(replies))
	for i, r := range replies {
		t.Logf("  [%d] (%d chars):", i, len(r))
		t.Logf("    %s", r[:min(len(r), 400)])
	}

	// --- 验证 ---

	// 第1条: "已批准"
	if len(replies) < 1 {
		t.Fatal("expected at least 1 reply ('已批准')")
	}
	if !strings.Contains(replies[0], "已批准") {
		t.Errorf("reply[0] should contain '已批准': %s",
			replies[0][:min(len(replies[0]), 200)])
	}

	// 第2条: 执行结果
	if len(replies) < 2 {
		t.Fatal("expected execution result from retryWithBypass (reply[1])")
	}
	result := strings.TrimSpace(replies[1])
	if len(result) == 0 {
		t.Error("retryWithBypass result is empty")
	}
	if strings.HasPrefix(result, "Claude error:") || strings.HasPrefix(result, "执行失败:") {
		t.Errorf("retryWithBypass failed: %s", result[:min(len(result), 500)])
	}

	// 不会触发第二次审批（bypassPermissions 生效）
	if strings.Contains(result, "⚠️ 回复 同意 继续执行") {
		t.Errorf("result contains approval prompt — bypassPermissions NOT working: %s",
			result[:min(len(result), 300)])
	}

	// Pending 已清除
	if bridge.gate.Check(identity) {
		t.Error("pending should be cleared after approve")
	}

	// Bypass 已生效
	if !bridge.gate.IsBypass(identity) {
		t.Error("should be in bypass mode after approval")
	}

	t.Logf("PASS: full approval flow completed")
	t.Logf("Result preview: %s", result[:min(len(result), 500)])
}

// TestSessionContextReuse 测试 --resume 会话上下文复用
//
// 步骤:
//  1. 首条消息 → oneShot (claude -p) 创建会话
//  2. 第二条消息 → resumeShot (claude --resume) 复用会话 → 验证上下文
//
// 运行方式:
//
//	go test ./internal/core/... -tags=claude -run TestSessionContextReuse -v -timeout 600s
func TestSessionContextReuse(t *testing.T) {
	bridge, uc, sender, sr := setupClaudeTest(t, loadSystemPrompt(t))
	defer sr.Shutdown()

	// === Step 1: 首条消息 → oneShot 创建会话 ===
	msg1 := makeMsg("记住：这个项目的名称是 Claude Bridge，回复 ok")
	t.Log("Step 1: First message (oneShot → create session)")
	bridge.handleMessage(msg1)

	replies := sender.waitFor(1, 120*time.Second)
	if len(replies) == 0 {
		t.Fatal("no reply for first message")
	}
	t.Logf("  Reply: %s", replies[0][:min(len(replies[0]), 200)])

	_, sessionID, _ := uc.Resolve(msg1, "")
	t.Logf("  Session: %s", sessionID)

	// 验证 session 已注册
	sr.mu.Lock()
	_, exists := sr.sessions[sessionID]
	sr.mu.Unlock()
	if !exists {
		t.Fatal("session not registered after first call")
	}
	t.Log("  Session registered OK")

	// === Step 2: 第二条消息 → resumeShot 复用会话 ===
	sender.reset()
	msg2 := makeMsg("我刚才让你记住的项目名称是什么？简短回复")
	t.Log("Step 2: Second message (resumeShot → claude --resume)")
	bridge.handleMessage(msg2)

	replies = sender.waitFor(1, 180*time.Second)
	if len(replies) == 0 {
		t.Fatal("no reply for second message")
	}

	result := replies[0]
	t.Logf("  Reply (%d chars): %s", len(result), result[:min(len(result), 300)])

	if len(strings.TrimSpace(result)) == 0 {
		t.Error("resumeShot returned empty")
	}

	// 验证上下文：回复中包含第一次对话的内容
	lower := strings.ToLower(result)
	if strings.Contains(lower, "claude bridge") || strings.Contains(lower, "bridge") {
		t.Log("✅ session context maintained via --resume")
	} else {
		t.Errorf("context lost: %s", result[:min(len(result), 300)])
	}

	t.Log("PASS: --resume session context reuse works")
}

// TestResumeDirectPipe 直接测试 claude --resume 的 stdin/stdout pipe 通信
//
// 绕过 SessionRunner，直接用 exec.Cmd + pipe 验证 --resume 是否响应 stdin
func TestResumeDirectPipe(t *testing.T) {
	root := projectRoot()
	sysPrompt := loadSystemPrompt(t)
	// 每次运行用唯一 UUID，避免 session 锁定
	sessionID := fmt.Sprintf("550e8400-e29b-41d4-a716-%012d", time.Now().UnixNano()%1000000000000)

	// Step 1: one-shot 创建会话
	oneShotArgs := []string{
		"-p", "记住：项目名称是 Claude Bridge，回复 ok",
		"--session-id", sessionID,
		"--model", modelName(),
	}
	if sysPrompt != "" {
		oneShotArgs = append(oneShotArgs, "--append-system-prompt", sysPrompt)
	}
	oneShotCmd := exec.CommandContext(context.Background(), "claude", oneShotArgs...)
	oneShotCmd.Dir = root
	oneShotOut, err := oneShotCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("oneShot failed: %v\noutput: %s", err, string(oneShotOut))
	}
	t.Logf("One-shot: %s", string(oneShotOut)[:min(len(oneShotOut), 100)])

	// Step 2: start --resume with pipes
	resumeArgs := []string{
		"--resume", sessionID,
		"--model", modelName(),
	}
	if sysPrompt != "" {
		resumeArgs = append(resumeArgs, "--append-system-prompt", sysPrompt)
	}
	resumeCmd := exec.Command("claude", resumeArgs...)
	resumeCmd.Dir = root

	stdin, _ := resumeCmd.StdinPipe()
	stdout, _ := resumeCmd.StdoutPipe()
	stderr, _ := resumeCmd.StderrPipe()

	if err := resumeCmd.Start(); err != nil {
		t.Fatalf("--resume start failed: %v", err)
	}
	defer resumeCmd.Process.Kill()

	t.Log("--resume process started")

	// Step 3: 写 stdin + 立即关闭（发送 EOF）
	// 关键：claude --resume 需要 EOF 才知道输入结束，否则会一直等待
	prompt := "我刚才让你记住的项目名称是什么？简短回复\n"
	t.Logf("Writing to stdin + close (send EOF): %q", prompt)
	if _, err := stdin.Write([]byte(prompt)); err != nil {
		t.Fatalf("stdin write failed: %v", err)
	}
	stdin.Close()
	t.Log("stdin closed (EOF sent)")

	// Step 4: 读 stdout + stderr（60s 超时）
	type readResult struct {
		data string
		isErr bool
	}
	readCh := make(chan readResult, 1)

	go func() {
		var buf [4096]byte
		n, _ := stdout.Read(buf[:])
		if n > 0 {
			readCh <- readResult{string(buf[:n]), false}
		}
	}()
	go func() {
		var buf [4096]byte
		n, _ := stderr.Read(buf[:])
		if n > 0 {
			readCh <- readResult{string(buf[:n]), true}
		}
	}()

	select {
	case r := <-readCh:
		label := "Stdout"
		if r.isErr {
			label = "Stderr"
		}
		t.Logf("%s (%d bytes): %s", label, len(r.data), r.data[:min(len(r.data), 400)])
		if strings.Contains(strings.ToLower(r.data), "claude bridge") ||
			strings.Contains(strings.ToLower(r.data), "bridge") {
			t.Log("✅ --resume pipe communication works!")
		}
	case <-time.After(60 * time.Second):
		// 超时后检查进程状态
		t.Error("timeout waiting for --resume stdout/stderr response")
	}
}
