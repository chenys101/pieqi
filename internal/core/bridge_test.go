package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

func TestNeedsApproval(t *testing.T) {
	tests := []struct {
		output   string
		expected bool
	}{
		{"", false},
		{"hello world", false},
		{"Bash(ls)", true},
		{"python analyze.py", true},
		{"Please Approve", true},
		{"请批准命令", true},
		{"需要审批执行", true},
	}
	for _, tt := range tests {
		if needsApproval(tt.output) != tt.expected {
			t.Errorf("needsApproval(%q) != %v", tt.output, tt.expected)
		}
	}
}

func TestParseSessionIndex(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"@1 查日志", "1"},
		{"@2", "2"},
		{"会话3 你好", "3"},
		{"切5 code review", "5"},
		{"普通消息", ""},
		{"@abc", ""},
	}
	for _, tt := range tests {
		got := parseSessionIndex(tt.input)
		if got != tt.want {
			t.Errorf("parseSessionIndex(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCleanPreview(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"@1 查日志", "查日志"},
		{"@2 hello world", "hello world"},
		{"会话3 你好", "你好"},
		{"切5 code review", "code review"},
		{"普通消息", "普通消息"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := cleanPreview(tt.in); got != tt.want {
			t.Errorf("cleanPreview(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPendingBlocking(t *testing.T) {
	dir := t.TempDir()
	usersPath := filepath.Join(dir, "users.json")
	sessionsDir := filepath.Join(dir, "sessions")
	mappingsDir := filepath.Join(dir, "mappings")

	userData := model.UserBindings{
		"testuser": {
			ID: "testuser",
			Bindings: map[model.Channel]model.Binding{
				"wechat": {UserID: "wx123", UserName: "T"},
			},
		},
	}
	data, _ := json.Marshal(userData)
	os.WriteFile(usersPath, data, 0644)

	logger := zap.NewNop()
	uc, _ := NewUserContext(usersPath, sessionsDir, mappingsDir, 30*time.Minute)
	sr := NewSessionRunner(logger, ".", "test", "", 60*time.Second)
	bridge := NewBridge(logger, uc, sr, 60*time.Second)

	// Set up pending request via gate
	bridge.gate.SetPending("testuser", &PendingRequest{
		Prompt: "delete files", SessionID: "uuid-1",
		Msg: model.Message{Channel: "wechat", ChatID: "wx123", UserID: "wx123", Content: "delete files"},
	})

	// gate.Check returns true
	if !bridge.gate.Check("testuser") {
		t.Fatal("should have pending")
	}

	// isApprovalCmd
	if !isApprovalCmd("同意") || !isApprovalCmd("拒绝") {
		t.Fatal("同意/拒绝 should be approval commands")
	}
	if isApprovalCmd("hello") {
		t.Fatal("hello should not be approval command")
	}
}

func TestMultiSession(t *testing.T) {
	dir := t.TempDir()
	usersPath := filepath.Join(dir, "users.json")
	sessionsDir := filepath.Join(dir, "sessions")
	mappingsDir := filepath.Join(dir, "mappings")

	userData := model.UserBindings{
		"testuser": {
			ID: "testuser",
			Bindings: map[model.Channel]model.Binding{
				"wechat": {UserID: "wx123", UserName: "T"},
			},
		},
	}
	data, _ := json.Marshal(userData)
	os.WriteFile(usersPath, data, 0644)

	logger := zap.NewNop()
	uc, _ := NewUserContext(usersPath, sessionsDir, mappingsDir, 30*time.Minute)
	sr := NewSessionRunner(logger, ".", "test", "", 60*time.Second)
	bridge := NewBridge(logger, uc, sr, 60*time.Second)
	_ = bridge

	// Send 1st msg → auto-create mapped to index 1
	msg1 := model.Message{Channel: "wechat", ChatID: "wx123", UserID: "wx123", Content: "查日志"}
	id, sid, _ := uc.Resolve(msg1, "")
	if id != "testuser" {
		t.Fatalf("identity = %q", id)
	}
	if sid == "" {
		t.Fatal("session UUID empty")
	}
	if ss := uc.ListSessions("testuser"); len(ss) != 1 || ss[0].Index != 1 {
		t.Fatalf("expected 1 session at index 1, got %d", len(ss))
	}

	// Send 2nd msg with @2 → create mapped to index 2
	msg2 := model.Message{Channel: "wechat", ChatID: "wx123", UserID: "wx123", Content: "查日志"}
	_, sid2, _ := uc.Resolve(msg2, "2")
	if sid2 == sid {
		t.Fatal("session 2 should be different UUID")
	}

	// @1 → returns session 1
	msg3 := model.Message{Channel: "wechat", ChatID: "wx123", UserID: "wx123", Content: "继续"}
	_, sid3, _ := uc.Resolve(msg3, "1")
	if sid3 != sid {
		t.Fatal("@1 should return session 1 UUID")
	}

	// List
	ss := uc.ListSessions("testuser")
	if len(ss) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(ss))
	}

	// /new → creates new file mapped to index 3
	sid4 := uc.NewSession("testuser")
	if sid4 == sid || sid4 == sid2 {
		t.Fatal("new session should have new UUID")
	}

	// 列表 command
	handled, reply := bridge.handleCommand(
		model.Message{Content: "列表"}, "testuser", &sid,
	)
	if !handled || !strings.Contains(reply, "会话1") {
		t.Errorf("列表 should show sessions, got: %s", reply)
	}

	// parseSessionIndex
	if idx := parseSessionIndex("@1 你好"); idx != "1" {
		t.Errorf("@1 你好 → %q", idx)
	}

	// TTL=0: always expired → autoBind creates new file each time
	// Both files stay in mapping (TTL does not evict)
	dir2 := t.TempDir()
	uc2, _ := NewUserContext(usersPath, filepath.Join(dir2, "sessions"), filepath.Join(dir2, "maps"), 0)
	_, sidExp, _ := uc2.Resolve(msg1, "")
	time.Sleep(1 * time.Millisecond)
	_, sidNew, _ := uc2.Resolve(msg2, "")
	if sidNew == sidExp {
		t.Fatal("TTL=0 should create new UUID")
	}
	// Both mapped (TTL does not evict, only capacity does)
	if ss := uc2.ListSessions("testuser"); len(ss) != 2 {
		t.Fatalf("expected 2 mapped sessions (TTL doesn't evict), got %d", len(ss))
	}
}
