package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"pieqi/internal/config"

	acp "github.com/coder/acp-go-sdk"
)

func TestIsCursorACP(t *testing.T) {
	cases := []struct {
		cfg  config.ACPConfig
		want bool
	}{
		{config.ACPConfig{AgentType: "agent"}, true},
		{config.ACPConfig{AgentType: "cursor-agent"}, true},
		{config.ACPConfig{AgentType: "cursor"}, true},
		{config.ACPConfig{AgentType: "qodercli"}, false},
		{config.ACPConfig{SpawnCommand: []string{"agent", "acp"}}, true},
		{config.ACPConfig{SpawnCommand: []string{"C:\\x\\cursor-agent.cmd", "acp"}}, true},
		{config.ACPConfig{SpawnCommand: []string{"agent", "--acp"}}, false},
	}
	for _, c := range cases {
		if got := isCursorACP(c.cfg); got != c.want {
			t.Fatalf("isCursorACP(%+v)=%v want %v", c.cfg, got, c.want)
		}
	}
}

func TestHandleCursorExtension_AskQuestionSkipped(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	resp, err := a.handleCursorExtension(context.Background(), "cursor/ask_question", json.RawMessage(`{"toolCallId":"t1"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := resp.(map[string]any)
	if m["outcome"] != "skipped" || m["reason"] != "unsupported_by_client" {
		t.Fatalf("resp=%v", resp)
	}
}

func TestHandleCursorExtension_CreatePlanCancelled(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	resp, err := a.handleCursorExtension(context.Background(), "_cursor/create_plan", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := resp.(map[string]any)
	if m["outcome"] != "cancelled" {
		t.Fatalf("resp=%v", resp)
	}
}

func TestHandleCursorExtension_UnknownMethodNotFound(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	_, err := a.handleCursorExtension(context.Background(), "cursor/nope", nil, true)
	re, ok := err.(*acp.RequestError)
	if !ok || re.Code != -32601 {
		t.Fatalf("want MethodNotFound, got %v", err)
	}
}

func TestCursorExtBridge_InterceptsAskQuestion(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	var replyMu sync.Mutex
	var replied bytes.Buffer
	// Agent→client 一行请求；SDK 侧应读不到它，而是收到我们写回 stdin 的 result。
	req := `{"jsonrpc":"2.0","id":7,"method":"cursor/ask_question","params":{"toolCallId":"q1"}}` + "\n"
	pass := `{"jsonrpc":"2.0","method":"session/update","params":{}}` + "\n"
	r := io.MultiReader(strings.NewReader(req), strings.NewReader(pass))
	br := newCursorExtBridge(r, &replied, &replyMu, a)

	buf := make([]byte, 4096)
	n, err := br.Read(buf)
	if err != nil && err != io.EOF {
		// May need a second read if first only drained intercept
	}
	gotPass := string(buf[:n])
	if !strings.Contains(gotPass, "session/update") {
		// First Read might return 0 if only intercept happened; read again.
		n2, err2 := br.Read(buf)
		if err2 != nil && err2 != io.EOF {
			t.Fatalf("read pass-through: %v (first=%q)", err2, gotPass)
		}
		gotPass += string(buf[:n2])
	}
	if !strings.Contains(gotPass, "session/update") {
		t.Fatalf("expected session/update to pass through, got %q (err=%v)", gotPass, err)
	}
	if strings.Contains(gotPass, "ask_question") {
		t.Fatalf("ask_question must not reach SDK reader: %q", gotPass)
	}

	replyMu.Lock()
	out := replied.String()
	replyMu.Unlock()
	if !strings.Contains(out, `"id":7`) || !strings.Contains(out, `"skipped"`) {
		t.Fatalf("reply=%q", out)
	}
}

func TestCursorAuthMethodIDs(t *testing.T) {
	methods := []acp.AuthMethod{
		{Agent: &acp.AuthMethodAgent{Id: "cursor_login", Name: "Cursor Login"}},
	}
	ids := cursorAuthMethodIDs(methods)
	if len(ids) != 1 || ids[0] != "cursor_login" {
		t.Fatalf("ids=%v", ids)
	}
}

func TestSpawnFallbackPaths_CursorLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\test\AppData\Local`)
	cands := spawnFallbackPaths("agent")
	found := false
	for _, c := range cands {
		if strings.Contains(c, `cursor-agent`) && strings.HasSuffix(c, "agent.cmd") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected LocalAppData cursor-agent\\agent.cmd in %v", cands)
	}
}

func TestAuthenticateAfterInit_NonCursorNoop(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "qodercli"}, nil)
	if err := a.authenticateAfterInit(context.Background(), acp.InitializeResponse{
		AuthMethods: []acp.AuthMethod{{Agent: &acp.AuthMethodAgent{Id: "x"}}},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestPromoteCursorGenerateImage_InWorktree 生图路径落在任务 workDir 内 →
// 合成 GenerateImage tool_use + completed，供 DeriveFileChanges → 产物清单。
func TestPromoteCursorGenerateImage_InWorktree(t *testing.T) {
	dir := t.TempDir()
	imgRel := filepath.Join("assets", "moon.png")
	imgAbs := filepath.Join(dir, imgRel)
	if err := os.MkdirAll(filepath.Dir(imgAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgAbs, []byte("fake-png"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	a.workDir = dir
	var got []ToolCallUpdateInfo
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = append(got, u) })

	params, _ := json.Marshal(map[string]string{
		"toolCallId": "tc-img-1",
		"filePath":   imgAbs,
		"description": "moon",
	})
	resp, err := a.handleCursorExtension(context.Background(), "cursor/generate_image", params, false)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatalf("notification should return nil result, got %v", resp)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 tool updates (new+completed), got %d: %+v", len(got), got)
	}
	if !got[0].IsNew || got[0].Title != cursorGenerateImageToolTitle || got[0].ToolCallID != "tc-img-1" {
		t.Fatalf("first update wrong: %+v", got[0])
	}
	if got[1].IsNew || got[1].Status != "completed" {
		t.Fatalf("second update wrong: %+v", got[1])
	}
	var in struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(got[0].RawInput, &in); err != nil {
		t.Fatal(err)
	}
	wantRel := filepath.ToSlash(imgRel)
	if in.FilePath != wantRel {
		t.Fatalf("file_path=%q want %q", in.FilePath, wantRel)
	}
}

// TestPromoteCursorGenerateImage_OutsideWorktree 宿主临时图不进产物链。
func TestPromoteCursorGenerateImage_OutsideWorktree(t *testing.T) {
	work := t.TempDir()
	outside := t.TempDir()
	imgAbs := filepath.Join(outside, "host-assets", "moon.png")
	if err := os.MkdirAll(filepath.Dir(imgAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgAbs, []byte("fake-png"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	a.workDir = work
	var n int
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { n++ })

	params, _ := json.Marshal(map[string]string{
		"toolCallId": "tc-out",
		"filePath":   imgAbs,
	})
	if _, err := a.handleCursorExtension(context.Background(), "cursor/generate_image", params, true); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("outside workDir must not promote, got %d callbacks", n)
	}
}

func TestPromoteCursorGenerateImage_RelativePathInside(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shot.webp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	a.workDir = dir
	var got []ToolCallUpdateInfo
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = append(got, u) })

	params, _ := json.Marshal(map[string]string{"filename": "shot.webp", "toolCallId": "rel1"})
	if _, err := a.handleCursorExtension(context.Background(), "cursor/generate_image", params, false); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want promote for relative in-worktree path, got %d", len(got))
	}
}
