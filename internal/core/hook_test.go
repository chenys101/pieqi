package core

import (
	"testing"
	"time"
)

func TestHookService_ApproveFlow(t *testing.T) {
	h := NewHookService(5 * time.Second)

	// 模拟 hook 子进程注册待决策（异步，因为 RegisterPending 阻塞）
	resultCh := make(chan HookResult, 1)
	go func() {
		resultCh <- h.RegisterPending(HookPayload{
			TaskID: "t1", ToolName: "Bash", ToolUseID: "call_1", Summary: "rm -rf x",
		})
	}()

	// 等 pending 注册（轮询 HasPending）
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h.HasPending("t1") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !h.HasPending("t1") {
		t.Fatal("pending not registered")
	}

	// 用户批准
	if err := h.Resolve("t1", "call_1", "approve"); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-resultCh:
		if r.PermissionDecision != "allow" {
			t.Fatalf("decision=%s, want allow", r.PermissionDecision)
		}
	case <-time.After(time.Second):
		t.Fatal("RegisterPending did not return after Resolve")
	}
	if h.HasPending("t1") {
		t.Fatal("pending should clear after resolve")
	}
}

func TestHookService_DenyFlow(t *testing.T) {
	h := NewHookService(5 * time.Second)
	resultCh := make(chan HookResult, 1)
	go func() {
		resultCh <- h.RegisterPending(HookPayload{TaskID: "t2", ToolName: "Edit", ToolUseID: "c2"})
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h.HasPending("t2") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.Resolve("t2", "c2", "deny")
	r := <-resultCh
	if r.PermissionDecision != "deny" {
		t.Fatalf("decision=%s, want deny", r.PermissionDecision)
	}
}

func TestHookService_Timeout(t *testing.T) {
	h := NewHookService(50 * time.Millisecond)
	r := h.RegisterPending(HookPayload{TaskID: "t3", ToolName: "Bash", ToolUseID: "c3"})
	if r.PermissionDecision != "deny" {
		t.Fatalf("timeout decision=%s, want deny", r.PermissionDecision)
	}
	if r.Reason != "decision timeout" {
		t.Fatalf("reason=%s", r.Reason)
	}
}

func TestHookService_ResolveNoPending(t *testing.T) {
	h := NewHookService(5 * time.Second)
	if err := h.Resolve("nope", "x", "approve"); err == nil {
		t.Fatal("Resolve on missing pending should error")
	}
}

func TestHookService_DuplicatePendingDenied(t *testing.T) {
	h := NewHookService(5 * time.Second)
	go func() { h.RegisterPending(HookPayload{TaskID: "t4", ToolUseID: "c4"}) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h.HasPending("t4") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 第二次注册同 task 应立即 deny
	r := h.RegisterPending(HookPayload{TaskID: "t4", ToolUseID: "c4"})
	if r.PermissionDecision != "deny" {
		t.Fatalf("duplicate decision=%s, want deny", r.PermissionDecision)
	}
}

func TestHookService_OnPendingCallback(t *testing.T) {
	h := NewHookService(5 * time.Second)
	called := make(chan [4]string, 1)
	h.SetOnPending(func(taskID, toolUseID, toolName, summary string) {
		called <- [4]string{taskID, toolUseID, toolName, summary}
	})
	go func() { h.RegisterPending(HookPayload{TaskID: "t5", ToolUseID: "c5", ToolName: "Write", Summary: "write file"}) }()
	select {
	case args := <-called:
		if args[0] != "t5" || args[1] != "c5" || args[2] != "Write" || args[3] != "write file" {
			t.Fatalf("callback args: %v", args)
		}
	case <-time.After(time.Second):
		t.Fatal("onPending callback not fired")
	}
	h.Resolve("t5", "c5", "approve")
}
