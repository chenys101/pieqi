package agent

import (
	"context"
	"sync"
	"testing"
)

// fakeSession4Adapter 实现 AgentSession 的最小 fake（可带 ResumeID）。
type fakeSession4Adapter struct {
	mu        sync.Mutex
	id        string
	resumeID  string
	promptN   int
	lastText  string
	cancelN   int
	closeN    int
	permN     int
	permReq   string
	permAllow bool
	onEvent   func(Event)
	onErr     func(Event) // 测试注入：EventError 时联动（模拟桥崩 dispatch）
}

var _ AgentSession = (*fakeSession4Adapter)(nil)

func (f *fakeSession4Adapter) ID() string { return f.id }
func (f *fakeSession4Adapter) ResumeID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumeID
}
func (f *fakeSession4Adapter) Prompt(ctx context.Context, text string) error {
	f.mu.Lock()
	f.promptN++
	f.lastText = text
	f.mu.Unlock()
	return nil
}
func (f *fakeSession4Adapter) Cancel(ctx context.Context) error {
	f.mu.Lock()
	f.cancelN++
	f.mu.Unlock()
	return nil
}
func (f *fakeSession4Adapter) Close(ctx context.Context) error {
	f.mu.Lock()
	f.closeN++
	f.mu.Unlock()
	return nil
}
func (f *fakeSession4Adapter) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	f.mu.Lock()
	f.permN++
	f.permReq = reqID
	f.permAllow = allow
	f.mu.Unlock()
	return nil
}
func (f *fakeSession4Adapter) OnEvent(fn func(Event)) {
	f.mu.Lock()
	f.onEvent = fn
	f.mu.Unlock()
}
func (f *fakeSession4Adapter) Caps() Caps { return Caps{} }

func (f *fakeSession4Adapter) fire(ev Event) {
	f.mu.Lock()
	fn := f.onEvent
	f.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

func openFake(name string, sess AgentSession) func(context.Context, OpenParams) (AgentSession, error) {
	return func(ctx context.Context, p OpenParams) (AgentSession, error) { return sess, nil }
}

func TestSessionBackedAdapterDelegation(t *testing.T) {
	fake := &fakeSession4Adapter{id: "sess-1"}
	adapter := newSessionBackedAdapter("claude", openFake("claude", fake))
	ctx := context.Background()

	sid, err := adapter.NewSession(ctx, SessionConfig{Cwd: "/tmp", ResumeFrom: "r1"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sid != "sess-1" {
		t.Fatalf("sid = %q, want sess-1", sid)
	}

	if err := adapter.SendPrompt(ctx, sid, "hi"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	if err := adapter.Cancel(ctx, sid); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := adapter.Approve(ctx, "req1", "allow"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := adapter.Deny(ctx, "req2"); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if err := adapter.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.promptN != 1 || fake.lastText != "hi" {
		t.Fatalf("Prompt not delegated: n=%d text=%q", fake.promptN, fake.lastText)
	}
	if fake.cancelN != 1 || fake.closeN != 1 {
		t.Fatalf("Cancel/Close not delegated: cancel=%d close=%d", fake.cancelN, fake.closeN)
	}
	if fake.permN != 2 {
		t.Fatalf("permission responses = %d, want 2", fake.permN)
	}
	if fake.permAllow {
		t.Fatal("last permission response should be deny (allow=false)")
	}

	// Done 在 Close 后关闭
	select {
	case <-adapter.Done():
	default:
		t.Fatal("Done should be closed after Close")
	}
}

func TestSessionBackedAdapterEventTranslation(t *testing.T) {
	fake := &fakeSession4Adapter{id: "sess-1"}
	adapter := newSessionBackedAdapter("claude", openFake("claude", fake))
	if _, err := adapter.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var deltas []ContentDelta
	var tools []ToolCallUpdateInfo
	var perms []PermissionRequest
	adapter.OnContentDelta(func(d ContentDelta) { deltas = append(deltas, d) })
	adapter.OnToolCallUpdate(func(t ToolCallUpdateInfo) { tools = append(tools, t) })
	adapter.OnPermissionRequest(func(p PermissionRequest) { perms = append(perms, p) })

	fake.fire(Event{Kind: EventTextDelta, Text: "a"})
	fake.fire(Event{Kind: EventThinkingDelta, Text: "t", IsThought: true})
	fake.fire(Event{Kind: EventToolStart, ToolCallID: "tc1", ToolTitle: "Bash", RawInput: []byte(`{"cmd":"ls"}`)})
	fake.fire(Event{Kind: EventToolEnd, ToolCallID: "tc1", ToolTitle: "Bash", ToolStatus: "completed"})
	fake.fire(Event{Kind: EventPermissionNeeded, Permission: PermissionRequest{ReqID: "r1", ToolCallID: "tc1", ToolTitle: "Bash"}})

	if len(deltas) != 2 || deltas[0].Text != "a" || deltas[0].IsThought || !deltas[1].IsThought {
		t.Fatalf("deltas wrong: %+v", deltas)
	}
	if len(tools) != 2 || !tools[0].IsNew || tools[0].Title != "Bash" || tools[1].Status != "completed" {
		t.Fatalf("tools wrong: %+v", tools)
	}
	if len(perms) != 1 || perms[0].ReqID != "r1" {
		t.Fatalf("perms wrong: %+v", perms)
	}
	// 权限必须带合成 allow 选项（PermissionWire.Resolve 的 approve 路径依赖它）
	if len(perms[0].Options) != 1 || perms[0].Options[0].Kind != PermissionOptionAllowOnce {
		t.Fatalf("permission options wrong: %+v", perms[0].Options)
	}
}

func TestSessionBackedAdapterDoneOnError(t *testing.T) {
	fake := &fakeSession4Adapter{id: "sess-1"}
	adapter := newSessionBackedAdapter("claude", openFake("claude", fake))
	if _, err := adapter.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	select {
	case <-adapter.Done():
		t.Fatal("Done should be open before error")
	default:
	}

	fake.fire(Event{Kind: EventError, Err: context.DeadlineExceeded})
	select {
	case <-adapter.Done():
	default:
		t.Fatal("Done should be closed on error event")
	}
}

func TestSessionBackedAdapterRealSessionID(t *testing.T) {
	// 桥会话（带 ResumeID）：turn_end 后有 SDK resume id → 优先返回
	fake := &fakeSession4Adapter{id: "bridge-sess", resumeID: "sdk-42"}
	adapter := newSessionBackedAdapter("claude", openFake("claude", fake))
	if _, err := adapter.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := adapter.ResumeID(); got != "sdk-42" {
		t.Fatalf("ResumeID = %q, want sdk-42", got)
	}
	if got := adapter.RealSessionID("bridge-sess"); got != "sdk-42" {
		t.Fatalf("RealSessionID = %q, want sdk-42 (resume id)", got)
	}

	// 无 ResumeID 的会话（qoder sessionAdapter）→ 回退 sessionID
	plain := &fakeSession4Adapter{id: "qoder-sess"}
	adapter2 := newSessionBackedAdapter("qoder", openFake("qoder", plain))
	if _, err := adapter2.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := adapter2.RealSessionID("qoder-sess"); got != "qoder-sess" {
		t.Fatalf("RealSessionID = %q, want qoder-sess (fallback)", got)
	}
}

func TestNewAgentSessionManager(t *testing.T) {
	m := NewAgentSessionManager("claude", ManagerConfig{}, nil)
	if m.fallback != nil {
		t.Fatal("session manager fallback should be nil (agent.Open handles fallback internally)")
	}
	a, kind, err := m.primary()
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	if kind != AgentKindSession {
		t.Fatalf("primary kind = %q, want session", kind)
	}
	_, ok := a.(*sessionBackedAdapter)
	if !ok {
		t.Fatalf("primary adapter type = %T, want *sessionBackedAdapter", a)
	}
}
