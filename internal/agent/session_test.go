package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// fakeAdapter4Session 会话桥接测试用的最小 AgentAdapter。
type fakeAdapter4Session struct {
	mu         sync.Mutex
	sessionID  string
	realID     string
	promptN    int
	promptText string
	cancelN    int
	closeN     int
	permN      int
	permAllow  bool
	permOptID  string

	onD ContentDeltaFunc
	onP PermissionRequestFunc
	onT ToolCallUpdateFunc
}

var _ AgentAdapter = (*fakeAdapter4Session)(nil)

func (f *fakeAdapter4Session) NewSession(ctx context.Context, cfg SessionConfig) (string, error) {
	return f.sessionID, nil
}
func (f *fakeAdapter4Session) RealSessionID(sessionID string) string { return f.realID }
func (f *fakeAdapter4Session) SendPrompt(ctx context.Context, sessionID, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptN++
	f.promptText = prompt
	return nil
}
func (f *fakeAdapter4Session) OnContentDelta(fn ContentDeltaFunc) {
	f.mu.Lock()
	f.onD = fn
	f.mu.Unlock()
}
func (f *fakeAdapter4Session) OnPermissionRequest(fn PermissionRequestFunc) {
	f.mu.Lock()
	f.onP = fn
	f.mu.Unlock()
}
func (f *fakeAdapter4Session) OnToolCallUpdate(fn ToolCallUpdateFunc) {
	f.mu.Lock()
	f.onT = fn
	f.mu.Unlock()
}
func (f *fakeAdapter4Session) Approve(ctx context.Context, reqID, optionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permN++
	f.permAllow = true
	f.permOptID = optionID
	return nil
}
func (f *fakeAdapter4Session) Deny(ctx context.Context, reqID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permN++
	f.permAllow = false
	return nil
}
func (f *fakeAdapter4Session) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	return nil
}
func (f *fakeAdapter4Session) InjectToolResult(ctx context.Context, sessionID, toolCallID string, result string, isError bool) error {
	return nil
}
func (f *fakeAdapter4Session) Cancel(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	f.cancelN++
	f.mu.Unlock()
	return nil
}
func (f *fakeAdapter4Session) Close(ctx context.Context) error {
	f.mu.Lock()
	f.closeN++
	f.mu.Unlock()
	return nil
}
func (f *fakeAdapter4Session) Done() <-chan struct{} { return nil }

// 触发 fake 的回调（模拟底层 adapter 派发事件）。
func (f *fakeAdapter4Session) fireDelta(d ContentDelta) {
	f.mu.Lock()
	fn := f.onD
	f.mu.Unlock()
	if fn != nil {
		fn(d)
	}
}
func (f *fakeAdapter4Session) firePerm(p PermissionRequest) {
	f.mu.Lock()
	fn := f.onP
	f.mu.Unlock()
	if fn != nil {
		fn(p)
	}
}
func (f *fakeAdapter4Session) fireTool(t ToolCallUpdateInfo) {
	f.mu.Lock()
	fn := f.onT
	f.mu.Unlock()
	if fn != nil {
		fn(t)
	}
}

func TestSessionAdapterDelegation(t *testing.T) {
	fake := &fakeAdapter4Session{sessionID: "s1", realID: "real-s1"}
	sess := NewSessionAdapter(fake, "s1", Caps{MultiTurnPersistent: true, Streaming: true})
	ctx := context.Background()

	if got := sess.ID(); got != "real-s1" {
		t.Fatalf("ID() = %q, want real-s1", got)
	}
	if got := sess.Caps(); !got.MultiTurnPersistent || !got.Streaming || got.ResumeSupported {
		t.Fatalf("Caps() = %+v, want MultiTurnPersistent+Streaming", got)
	}
	if err := sess.Prompt(ctx, "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := sess.Cancel(ctx); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := sess.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.promptN != 1 || fake.promptText != "hi" {
		t.Fatalf("Prompt not delegated: n=%d text=%q", fake.promptN, fake.promptText)
	}
	if fake.cancelN != 1 || fake.closeN != 1 {
		t.Fatalf("Cancel/Close not delegated: cancel=%d close=%d", fake.cancelN, fake.closeN)
	}
}

func TestSessionAdapterRespondPermission(t *testing.T) {
	fake := &fakeAdapter4Session{}
	sess := NewSessionAdapter(fake, "s1", Caps{})
	ctx := context.Background()

	if err := sess.RespondPermission(ctx, "r1", true, "allow_once"); err != nil {
		t.Fatalf("RespondPermission allow: %v", err)
	}
	if err := sess.RespondPermission(ctx, "r2", false, ""); err != nil {
		t.Fatalf("RespondPermission deny: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.permN != 2 {
		t.Fatalf("permission responses = %d, want 2", fake.permN)
	}
	if fake.permAllow {
		t.Fatalf("last response should be deny (allow=false), got allow")
	}
}

func TestSessionAdapterEventBridge(t *testing.T) {
	fake := &fakeAdapter4Session{}
	sess := NewSessionAdapter(fake, "s1", Caps{})

	var got []Event
	sess.OnEvent(func(ev Event) { got = append(got, ev) })

	// 模拟底层派发：delta / thinking / tool start / tool end / permission
	fake.fireDelta(ContentDelta{SessionID: "s1", Text: "a", IsThought: false})
	fake.fireDelta(ContentDelta{SessionID: "s1", Text: "t", IsThought: true})
	fake.fireTool(ToolCallUpdateInfo{SessionID: "s1", ToolCallID: "tc1", Title: "Bash", Status: "in_progress", IsNew: true})
	fake.fireTool(ToolCallUpdateInfo{SessionID: "s1", ToolCallID: "tc1", Title: "Bash", Status: "completed", IsNew: false})
	fake.firePerm(PermissionRequest{ReqID: "r1", SessionID: "s1", ToolCallID: "tc1"})

	if len(got) != 5 {
		t.Fatalf("events = %d, want 5: %+v", len(got), got)
	}
	wantKinds := []EventKind{EventTextDelta, EventThinkingDelta, EventToolStart, EventToolEnd, EventPermissionNeeded}
	for i, k := range wantKinds {
		if got[i].Kind != k {
			t.Errorf("event[%d].Kind = %q, want %q", i, got[i].Kind, k)
		}
	}
	if got[0].Text != "a" || got[1].IsThought != true {
		t.Errorf("delta payload mismatch: %+v / %+v", got[0], got[1])
	}
	if got[2].ToolCallID != "tc1" || got[2].ToolTitle != "Bash" {
		t.Errorf("tool start payload mismatch: %+v", got[2])
	}
	if got[3].ToolStatus != "completed" {
		t.Errorf("tool end status = %q, want completed", got[3].ToolStatus)
	}
	if got[4].Permission.ReqID != "r1" {
		t.Errorf("permission payload mismatch: %+v", got[4])
	}
}

func TestOpenRegistry(t *testing.T) {
	// 用唯一 agent 名，避免污染其它测试。
	const agentName = "session-test-agent"
	RegisterSessionProvider(agentName, func(ctx context.Context, p OpenParams) (AgentSession, error) {
		return NewSessionAdapter(&fakeAdapter4Session{sessionID: p.Cwd, realID: p.ResumeFrom}, p.Cwd, Caps{}), nil
	})
	defer func() {
		sessionProvidersMu.Lock()
		delete(sessionProviders, agentName)
		sessionProvidersMu.Unlock()
	}()

	sess, err := Open(context.Background(), OpenParams{Agent: agentName, Cwd: "/tmp", ResumeFrom: "r"})
	if err != nil {
		t.Fatalf("Open known agent: %v", err)
	}
	if sess.ID() != "r" {
		t.Fatalf("Open ID() = %q, want r (ResumeFrom)", sess.ID())
	}

	if _, err := Open(context.Background(), OpenParams{Agent: "no-such-agent"}); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("Open unknown agent err = %v, want ErrUnknownAgent", err)
	}
}
