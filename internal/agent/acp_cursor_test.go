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
	"time"

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
	inner, _ := m["outcome"].(map[string]any)
	if inner["outcome"] != "skipped" || inner["reason"] != "unsupported_by_client" {
		t.Fatalf("want nested skipped, got %v", resp)
	}
}

func TestHandleCursorExtension_CreatePlanAcceptedAndEmitsPlan(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	// 无 OnPermissionRequest：非 plan 也直通采纳（与 RequestPermission 无回调同口径）。
	var got PlanUpdate
	a.OnPlanUpdate(func(u PlanUpdate) { got = u })
	raw := []byte(`{"toolCallId":"c1","name":"Demo","overview":"ov","plan":"do x","todos":[{"id":"1","content":"Inspect","status":"completed"},{"id":"2","content":"Edit","status":"pending"}]}`)
	resp, err := a.handleCursorExtension(context.Background(), "cursor/create_plan", raw, true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := resp.(map[string]any)
	inner, _ := m["outcome"].(map[string]any)
	if inner["outcome"] != "accepted" {
		t.Fatalf("want nested accepted, got %v", resp)
	}
	if len(got.Entries) != 2 || got.Entries[0].Content != "Inspect" || got.Entries[0].Status != "completed" {
		t.Fatalf("plan entries=%+v", got.Entries)
	}
}

// TestHandleCursorExtension_CreatePlanInPlanModeAsksAccept 回归 b7c2f14b：
// 已在 plan 时 create_plan 必须弹「批准计划」卡，不能 already_plan 静默 accepted。
func TestHandleCursorExtension_CreatePlanInPlanModeAsksAccept(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	a.modeMu.Lock()
	a.modeState.CurrentID = "plan"
	a.modeMu.Unlock()
	a.lastSessionID = "s-plan"
	var got PlanUpdate
	a.OnPlanUpdate(func(u PlanUpdate) { got = u })
	var asked PermissionRequest
	a.OnPermissionRequest(func(req PermissionRequest) {
		asked = req
		go func() { _ = a.Approve(context.Background(), req.ReqID, "allow-once") }()
	})
	raw := []byte(`{"toolCallId":"c2","name":"InPlan","todos":[{"id":"1","content":"A","status":"pending"}]}`)
	resp, err := a.handleCursorExtension(context.Background(), "cursor/create_plan", raw, true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := resp.(map[string]any)
	inner, _ := m["outcome"].(map[string]any)
	if inner["outcome"] != "accepted" {
		t.Fatalf("want accepted after approve, got %v", resp)
	}
	if asked.ToolKind != "switch_mode" || !strings.Contains(asked.ToolTitle, "批准计划") {
		t.Fatalf("asked=%+v", asked)
	}
	if !strings.HasPrefix(asked.ReqID, "plan-accept:") {
		t.Fatalf("ReqID=%q want plan-accept: prefix", asked.ReqID)
	}
	if len(got.Entries) != 1 || got.Entries[0].Content != "A" {
		t.Fatalf("entries=%+v", got.Entries)
	}
	a.pendingModeMu.Lock()
	pending := a.pendingModeAfterExt
	a.pendingModeMu.Unlock()
	if pending == nil || pending.modeID != "agent" {
		t.Fatalf("pending=%+v want agent after accept", pending)
	}
}

func TestHandleCursorExtension_CreatePlanInPlanModeDeclineKeepsPlan(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	a.modeMu.Lock()
	a.modeState.CurrentID = "plan"
	a.modeMu.Unlock()
	var last PlanUpdate
	a.OnPlanUpdate(func(u PlanUpdate) { last = u })
	a.OnPermissionRequest(func(req PermissionRequest) {
		go func() { _ = a.Deny(context.Background(), req.ReqID) }()
	})
	raw := []byte(`{"toolCallId":"c2d","name":"KeepEditing","todos":[{"id":"1","content":"Stay","status":"pending"}]}`)
	resp, err := a.handleCursorExtension(context.Background(), "cursor/create_plan", raw, true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := resp.(map[string]any)
	inner, _ := m["outcome"].(map[string]any)
	if inner["outcome"] != "cancelled" {
		t.Fatalf("want cancelled, got %v", resp)
	}
	if len(last.Entries) != 1 || last.Entries[0].Content != "Stay" {
		t.Fatalf("decline must keep preview, got %+v", last.Entries)
	}
	a.pendingModeMu.Lock()
	pending := a.pendingModeAfterExt
	a.pendingModeMu.Unlock()
	if pending != nil {
		t.Fatalf("decline must not queue set_mode, pending=%+v", pending)
	}
	if a.Modes().CurrentID != "plan" {
		t.Fatalf("CurrentID=%q want plan", a.Modes().CurrentID)
	}
}

// TestCreatePlan_DefersSetModeUntilFlush 回归 384e5205：
// plan 下 create_plan 不得在扩展处理栈内同步 set_mode（会与 cursorExtBridge 死锁）。
func TestCreatePlan_DefersSetModeUntilFlush(t *testing.T) {
	conn := &fakeModelConn{}
	a := NewACPAgent(config.ACPConfig{AgentType: "agent", InitTimeout: 5 * time.Second}, nil)
	a.conn = conn
	a.started = true
	a.lastSessionID = "sess"
	a.modeMu.Lock()
	a.modeState = ModeState{
		CurrentID: "plan",
		Available: []ModeOption{{ID: "plan", Name: "Plan"}, {ID: "agent", Name: "Agent"}},
	}
	a.modeMu.Unlock()

	raw := []byte(`{"toolCallId":"c-defer","name":"Defer","todos":[{"id":"1","content":"A","status":"pending"}]}`)
	if _, err := a.handleCursorExtension(context.Background(), "cursor/create_plan", raw, true); err != nil {
		t.Fatal(err)
	}
	for _, s := range conn.order {
		if strings.HasPrefix(s, "set_mode:") {
			t.Fatalf("set_mode must not run before flush, order=%v", conn.order)
		}
	}
	a.pendingModeMu.Lock()
	pending := a.pendingModeAfterExt
	a.pendingModeMu.Unlock()
	if pending == nil || pending.modeID != "agent" {
		t.Fatalf("pending=%+v want agent", pending)
	}

	a.flushPendingModeAfterExtReply()
	deadline := time.Now().Add(2 * time.Second)
	for {
		found := false
		for _, s := range conn.order {
			if s == "set_mode:agent" {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deferred set_mode:agent not observed, order=%v", conn.order)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if a.Modes().CurrentID != "agent" {
		t.Fatalf("CurrentID=%q want agent", a.Modes().CurrentID)
	}
}

func TestHandleCursorExtension_CreatePlanOutsidePlanAsksConfirm(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	a.modeMu.Lock()
	a.modeState.CurrentID = "agent"
	a.modeMu.Unlock()
	a.lastSessionID = "s1"

	var asked PermissionRequest
	a.OnPermissionRequest(func(req PermissionRequest) {
		asked = req
		go func() { _ = a.Approve(context.Background(), req.ReqID, "allow-once") }()
	})
	var got PlanUpdate
	a.OnPlanUpdate(func(u PlanUpdate) { got = u })

	raw := []byte(`{"toolCallId":"c3","name":"NeedConfirm","overview":"why","todos":[{"id":"1","content":"Step","status":"pending"}]}`)
	resp, err := a.handleCursorExtension(context.Background(), "cursor/create_plan", raw, true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := resp.(map[string]any)
	inner, _ := m["outcome"].(map[string]any)
	if inner["outcome"] != "accepted" {
		t.Fatalf("want accepted after confirm, got %v", resp)
	}
	if asked.ToolKind != "switch_mode" || !strings.Contains(asked.ToolTitle, "进入计划模式") {
		t.Fatalf("asked=%+v", asked)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("entries=%+v", got.Entries)
	}
}

func TestHandleCursorExtension_CreatePlanOutsidePlanDeclineClears(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	a.modeMu.Lock()
	a.modeState.CurrentID = "agent"
	a.modeMu.Unlock()

	a.OnPermissionRequest(func(req PermissionRequest) {
		go func() { _ = a.Deny(context.Background(), req.ReqID) }()
	})
	var last PlanUpdate
	n := 0
	a.OnPlanUpdate(func(u PlanUpdate) {
		n++
		last = u
	})

	raw := []byte(`{"toolCallId":"c4","name":"Nope","todos":[{"id":"1","content":"X","status":"pending"}]}`)
	resp, err := a.handleCursorExtension(context.Background(), "cursor/create_plan", raw, true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := resp.(map[string]any)
	inner, _ := m["outcome"].(map[string]any)
	if inner["outcome"] != "cancelled" {
		t.Fatalf("want cancelled, got %v", resp)
	}
	if n < 2 {
		t.Fatalf("expect preview then clear, n=%d", n)
	}
	if len(last.Entries) != 0 {
		t.Fatalf("clear should emit empty, got %+v", last.Entries)
	}
}

func TestHandleCursorExtension_UpdateTodosMerge(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent"}, nil)
	var got PlanUpdate
	a.OnPlanUpdate(func(u PlanUpdate) { got = u })
	_, err := a.handleCursorExtension(context.Background(), "cursor/update_todos",
		json.RawMessage(`{"todos":[{"id":"1","content":"A","status":"pending"}],"merge":false}`), true)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleCursorExtension(context.Background(), "cursor/update_todos",
		json.RawMessage(`{"todos":[{"id":"1","content":"A","status":"completed"},{"id":"2","content":"B","status":"pending"}],"merge":true}`), true)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := resp.(map[string]any)
	inner, _ := m["outcome"].(map[string]any)
	if inner["outcome"] != "accepted" {
		t.Fatalf("resp=%v", resp)
	}
	if len(got.Entries) != 2 || got.Entries[0].Status != "completed" || got.Entries[1].Content != "B" {
		t.Fatalf("merged entries=%+v", got.Entries)
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
