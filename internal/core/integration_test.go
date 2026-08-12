package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

func setupTestEnv(t *testing.T) (*Bridge, *UserContext, string) {
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
	sr := NewSessionRunner(logger, ".", "test", "", 60*time.Second)
	bridge := NewBridge(logger, uc, sr, 60*time.Second)

	return bridge, uc, dir
}

func makeMsg(content string) model.Message {
	return model.Message{Channel: "wechat", ChatID: "wx_user_123", UserID: "wx_user_123", Content: content}
}

// waitSender 捕获 Bridge 回复（非 claude 构建用；claude 构建用 testSender）。
// 用于等待异步 retryWithBypass goroutine 落定，避免 t.TempDir 清理时的文件写竞争。
type waitSender struct {
	replies []string
	done    chan struct{}
}

func newWaitSender() *waitSender { return &waitSender{done: make(chan struct{})} }

func (s *waitSender) Send(_ context.Context, _ model.ReplyTarget, text string) error {
	s.replies = append(s.replies, text)
	select {
	case s.done <- struct{}{}:
	default:
	}
	return nil
}

// Test 1: Auto-binding — 第一次消息创建会话，30min内复用
func TestIntegrationAutoBind(t *testing.T) {
	_, uc, _ := setupTestEnv(t)

	msg1 := makeMsg("查物流日志")
	id1, sid1, _ := uc.Resolve(msg1, "")
	if id1 != "chenyusheng" {
		t.Fatalf("identity = %q", id1)
	}
	if sid1 == "" {
		t.Fatal("empty session UUID")
	}

	// 30min内第二条消息 → 自动复用会话
	msg2 := makeMsg("继续查")
	_, sid2, _ := uc.Resolve(msg2, "")
	if sid2 != sid1 {
		t.Fatal("30min内应该复用同一会话")
	}

	// 会话列表应该只有1个
	ss := uc.ListSessions("chenyusheng")
	if len(ss) != 1 || ss[0].Index != 1 {
		t.Fatalf("expected 1 session at index 1, got %v", ss)
	}
}

// Test 2: Explicit binding — @2 切到会话2，@1 回到会话1
func TestIntegrationExplicitBind(t *testing.T) {
	_, uc, _ := setupTestEnv(t)

	// 会话1：默认消息
	msg1 := makeMsg("hello")
	_, sid1, _ := uc.Resolve(msg1, "")

	// 会话2：@2 显式指定
	msg2 := makeMsg("@2 world")
	_, sid2, _ := uc.Resolve(msg2, "2")
	if sid2 == sid1 {
		t.Fatal("会话2应该是新UUID")
	}

	// @1 回到会话1
	msg3 := makeMsg("@1 继续")
	_, sid3, _ := uc.Resolve(msg3, "1")
	if sid3 != sid1 {
		t.Fatal("@1应该回到会话1")
	}

	// 列表应有2个
	ss := uc.ListSessions("chenyusheng")
	if len(ss) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(ss))
	}
	if ss[0].Index != 1 || ss[1].Index != 2 {
		t.Fatalf("wrong indices: %+v", ss)
	}
}

// Test 3: TTL过期 → 创建新文件，新文件映射到同一逻辑编号
func TestIntegrationExpiredTTL(t *testing.T) {
	dir := t.TempDir()
	usersPath := filepath.Join(dir, "users.json")
	sessionsDir := filepath.Join(dir, "sessions")
	mappingsDir := filepath.Join(dir, "mappings")

	userData := model.UserBindings{
		"chenyusheng": {
			ID:       "chenyusheng",
			Bindings: map[model.Channel]model.Binding{"wechat": {UserID: "wx_user_123"}},
		},
	}
	data, _ := json.Marshal(userData)
	os.WriteFile(usersPath, data, 0644)

	uc, _ := NewUserContext(usersPath, sessionsDir, mappingsDir, 0)

	msg1 := makeMsg("first")
	_, sid1, _ := uc.Resolve(msg1, "")

	msg2 := makeMsg("second")
	_, sid2, _ := uc.Resolve(msg2, "")
	if sid2 == sid1 {
		t.Fatal("过期应创建新UUID")
	}

	// 两个都在映射中（TTL 不驱逐，只有满 50 才驱逐）
	ss := uc.ListSessions("chenyusheng")
	if len(ss) != 2 {
		t.Fatalf("expected 2 mapped sessions (TTL doesn't evict), got %d", len(ss))
	}
}

// Test 4: 新对话命令
func TestIntegrationNewSession(t *testing.T) {
	_, uc, _ := setupTestEnv(t)

	// 先建会话1
	msg1 := makeMsg("hello")
	_, sid1, _ := uc.Resolve(msg1, "")

	// 新对话 → 新文件映射到 index 2
	sid2 := uc.NewSession("chenyusheng")
	if sid2 == sid1 {
		t.Fatal("新对话应创建新会话")
	}

	// 再发消息 → 自动绑到最新的（会话2）
	msg3 := makeMsg("after new")
	_, sid3, _ := uc.Resolve(msg3, "")
	if sid3 != sid2 {
		t.Fatal("应自动绑到最新会话")
	}
}

// Test 5: 命令 — 列表
func TestIntegrationCommands(t *testing.T) {
	bridge, uc, _ := setupTestEnv(t)

	// Create 2 sessions
	uc.Resolve(makeMsg("查日志"), "")  // mapped to index 1
	uc.Resolve(makeMsg("看代码"), "2") // mapped to index 2

	var sid string
	handled, reply := bridge.handleCommand(
		model.Message{Content: "列表"}, "chenyusheng", &sid,
	)
	if !handled {
		t.Fatal("列表 should be handled")
	}
	if !strings.Contains(reply, "会话1") || !strings.Contains(reply, "会话2") {
		t.Errorf("列表 should show sessions: %s", reply)
	}

	// /whoami
	handled, reply = bridge.handleCommand(
		model.Message{Content: "/whoami"}, "chenyusheng", &sid,
	)
	if !handled || !strings.Contains(reply, "chenyusheng") {
		t.Errorf("/whoami failed: %s", reply)
	}

	// /new
	sid = "old-id"
	handled, reply = bridge.handleCommand(
		model.Message{Content: "新对话"}, "chenyusheng", &sid,
	)
	if !handled || sid == "old-id" {
		t.Error("新对话 should change session ID")
	}
}

// Test 6: parseSessionIndex
func TestIntegrationParseSessionIndex(t *testing.T) {
	tests := []struct{ in, want string }{
		{"@1 查日志", "1"},
		{"@2", "2"},
		{"会话3 你好", "3"},
		{"切5 code", "5"},
		{"@99", "99"},
		{"普通消息", ""},
		{"@abc", ""},
		{"@", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := parseSessionIndex(tt.in)
		if got != tt.want {
			t.Errorf("parseSessionIndex(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Test 7: needsApproval
func TestIntegrationNeedsApproval(t *testing.T) {
	tests := []struct {
		out  string
		want bool
	}{
		{"", false},
		{"hello", false},
		{"I'll help you", false},
		{"Bash(ls -la)", true},
		{"Use Grep(code)", true},
		{"python analyze.py", true},
		{"Please Approve this action", true},
		{"请批准以上命令以继续", true},
		{"需要审批执行", true},
		{"查询命令已提交，等待审批", true},
		{"正常回复无工具调用", false},
	}
	for _, tt := range tests {
		if needsApproval(tt.out) != tt.want {
			t.Errorf("needsApproval(%q) != %v", tt.out, tt.want)
		}
	}
}

// Test 8: Permission flow — 同意后标记已批准
func TestIntegrationPermissionFlow(t *testing.T) {
	bridge, _, _ := setupTestEnv(t)

	sender := newWaitSender()
	bridge.RegisterSender("wechat", sender)

	identity := "chenyusheng"
	var sid string

	// Store pending request (simulating needsApproval=true)
	bridge.gate.SetPending(identity, &PendingRequest{
		Prompt: "查日志", SessionID: "uuid-1",
		Msg: makeMsg("查日志"), CreatedAt: time.Now(),
	})

	// 用户回复 "同意"
	handled, reply := bridge.handleCommand(
		model.Message{Content: "同意"}, identity, &sid,
	)
	if !handled {
		t.Fatal("同意 should be handled")
	}
	if !strings.Contains(reply, "已批准") {
		t.Errorf("should say approved: %s", reply)
	}

	// 验证已批准（gate.IsBypass 返回 true）
	if !bridge.gate.IsBypass(identity) {
		t.Fatal("should be approved after 同意")
	}

	// 验证 pending 已清除
	if bridge.gate.Check(identity) {
		t.Fatal("pending should be cleared")
	}

	// 等 retryWithBypass goroutine 完成（拿到回复即结束），防止清理竞争
	select {
	case <-sender.done:
	case <-time.After(10 * time.Second):
	}
}

// Test 9: 拒绝命令
func TestIntegrationDenyFlow(t *testing.T) {
	bridge, _, _ := setupTestEnv(t)

	identity := "chenyusheng"
	var sid string

	bridge.gate.SetPending(identity, &PendingRequest{
		Prompt: "查日志", SessionID: "uuid-1",
		Msg: makeMsg("查日志"), CreatedAt: time.Now(),
	})

	handled, reply := bridge.handleCommand(
		model.Message{Content: "拒绝"}, identity, &sid,
	)
	if !handled || !strings.Contains(reply, "拒绝") {
		t.Error("拒绝 should work")
	}

	// Pending cleared
	if bridge.gate.Check(identity) {
		t.Fatal("pending should be cleared after 拒绝")
	}
}

// Test 10: Pending 期间新消息被拒绝
func TestIntegrationPendingBlocksNewMessage(t *testing.T) {
	bridge, _, _ := setupTestEnv(t)

	identity := "chenyusheng"

	// Set pending
	bridge.gate.SetPending(identity, &PendingRequest{
		Prompt: "delete files", SessionID: "uuid-1",
		Msg: makeMsg("delete files"), CreatedAt: time.Now(),
	})

	// New non-approval message should be blocked
	if !bridge.gate.Check(identity) {
		t.Fatal("should have pending")
	}

	// isApprovalCmd check
	if !isApprovalCmd("同意") {
		t.Fatal("同意 is approval cmd")
	}
	if isApprovalCmd("hello") {
		t.Fatal("hello is not approval cmd")
	}
}

// Test 11: cleanPreview
func TestIntegrationCleanPreview(t *testing.T) {
	tests := []struct{ in, want string }{
		{"@1 查日志", "查日志"},
		{"会话3 你好", "你好"},
		{"切5 code review", "code review"},
		{"普通消息", "普通消息"},
		{"@2", ""},
	}
	for _, tt := range tests {
		if got := cleanPreview(tt.in); got != tt.want {
			t.Errorf("cleanPreview(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Test 12: bypass 重试复用原 sessionID（不创建新会话）
func TestIntegrationRetryWithBypassReusesSessionID(t *testing.T) {
	bridge, uc, _ := setupTestEnv(t)

	identity := "chenyusheng"

	// 模拟 approve flow：创建 pending 请求，记录原始 sessionID
	msg := makeMsg("delete files")
	_, originalSID, _ := uc.Resolve(msg, "")
	if originalSID == "" {
		t.Fatal("expected session UUID")
	}

	req := &PendingRequest{
		Prompt:    "delete files",
		SessionID: originalSID,
		Msg:       msg,
		CreatedAt: time.Now(),
	}
	bridge.gate.SetPending(identity, req)

	// 验证 gate 中存储的 SessionID 是原始值
	retrieved := bridge.gate.Approve(identity)
	if retrieved == nil {
		t.Fatal("approve should return pending request")
	}
	if retrieved.SessionID != originalSID {
		t.Errorf("SessionID should be preserved: got %q, want %q", retrieved.SessionID, originalSID)
	}

	// 验证 bypass 已生效
	if !bridge.gate.IsBypass(identity) {
		t.Fatal("should be in bypass after approve")
	}

	// 验证 pending 已清除
	if bridge.gate.Check(identity) {
		t.Fatal("pending should be cleared after approve")
	}

	// retryWithBypass 会使用 req.SessionID（原 session），而不是 NewSession()
	// 实际 Claude 调用无法在单元测试中验证，这里验证的是 PendingRequest 的
	// SessionID 在整个 approve 流程中保持不变（即不丢失原会话上下文）
}

// Test 13: 会话文件命名包含日期
func TestIntegrationSessionFilenameHasDate(t *testing.T) {
	_, uc, _ := setupTestEnv(t)

	uc.Resolve(makeMsg("test"), "")

	// Check session files have date format
	entries, _ := os.ReadDir(filepath.Join(uc.sessionsDir))
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "user-chenyusheng-") && strings.HasSuffix(e.Name(), ".json") {
			found = true
			// Should contain today's date: user-chenyusheng-250622-1.json
			if !strings.Contains(e.Name(), time.Now().Format("060102")) {
				t.Errorf("filename should contain today's date: %s", e.Name())
			}
		}
	}
	if !found {
		t.Fatal("no session file found")
	}
}
