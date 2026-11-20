package core

import (
	"strings"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// TestCompleteAfterTurn_PendingFailClearsOrphan：soft-fail 后 pending 必须摘掉，
// 否则 Resume 会再撞同一条 approval unresolved。
func TestCompleteAfterTurn_PendingFailClearsOrphan(t *testing.T) {
	tr, store, bus, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{ID: "t-soft-abandon", Prompt: "p", Output: "partial"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _ = store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskRunning
		x.Events = append(x.Events, model.TaskEvent{Type: model.EventText, Text: "partial", At: time.Now()})
		return true
	})

	fa := newFakePermAdapter()
	ph := WirePermission(fa, bus, store, task.ID, nil, time.Minute, nil, nil)
	defer ph.Unwire()
	registerTestWires(t, tr, task.ID, ph)
	ph.mu.Lock()
	ph.pending["req-orphan"] = &permPending{reqID: "req-orphan"}
	ph.displayed = "req-orphan"
	ph.mu.Unlock()

	tr.completeAfterTurn(task.ID, "partial")

	got, _ := store.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, errApprovalUnresolved) {
		t.Fatalf("Error=%q, want %q", got.Error, errApprovalUnresolved)
	}
	if id, ok := ph.firstUnsettledPending(); ok {
		t.Fatalf("pending still has %q after soft-fail; Resume would rehit unresolved", id)
	}
}

// TestPermissionWire_AbandonRemovesPending：ctx 取消路径会 Abandon，pending 必须清空。
func TestPermissionWire_AbandonRemovesPending(t *testing.T) {
	_, store, bus, _ := newTestTaskRunner(t)
	task, err := store.Create(&model.Task{ID: "t-abandon", Prompt: "p"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	fa := newFakePermAdapter()
	ph := WirePermission(fa, bus, store, task.ID, nil, time.Minute, nil, nil)
	defer ph.Unwire()

	opts := []agent.PermissionOption{
		{ID: "allow-once", Name: "Allow", Kind: agent.PermissionOptionAllowOnce},
		{ID: "reject-once", Name: "Reject", Kind: agent.PermissionOptionRejectOnce},
	}
	fa.emitPerm(permReq("req-a", "go test", "execute", opts))
	if _, ok := ph.firstUnsettledPending(); !ok {
		t.Fatal("expected pending after request")
	}
	ph.Abandon("req-a")
	if id, ok := ph.firstUnsettledPending(); ok {
		t.Fatalf("pending %q after Abandon", id)
	}
}
