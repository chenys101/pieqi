package core

import (
	"testing"
	"time"
)

func TestApprovalGate_Lifecycle(t *testing.T) {
	g := NewApprovalGate("", 30*time.Minute)

	// Initially clean
	if g.Check("alice") {
		t.Fatal("should not have pending initially")
	}
	if g.IsBypass("alice") {
		t.Fatal("should not be bypass initially")
	}

	// Set pending
	g.SetPending("alice", &PendingRequest{
		Prompt: "delete files", SessionID: "s1",
		Msg: makeMsg("delete files"), CreatedAt: time.Now(),
	})
	if !g.Check("alice") {
		t.Fatal("should have pending after SetPending")
	}

	// Approve
	req := g.Approve("alice")
	if req == nil {
		t.Fatal("approve should return pending request")
	}
	if g.Check("alice") {
		t.Fatal("pending should be cleared after approve")
	}
	if !g.IsBypass("alice") {
		t.Fatal("should be in bypass after approve")
	}
}

func TestApprovalGate_Deny(t *testing.T) {
	g := NewApprovalGate("", 30*time.Minute)

	g.SetPending("bob", &PendingRequest{
		Prompt: "run script", SessionID: "s2",
		Msg: makeMsg("run script"), CreatedAt: time.Now(),
	})

	if !g.Deny("bob") {
		t.Fatal("deny should return true when pending exists")
	}
	if g.Check("bob") {
		t.Fatal("pending should be cleared after deny")
	}
	if g.IsBypass("bob") {
		t.Fatal("should NOT be bypass after deny")
	}
}

func TestApprovalGate_BypassExpiry(t *testing.T) {
	g := NewApprovalGate("", 1*time.Millisecond) // 1ms window

	g.SetPending("carol", &PendingRequest{
		Prompt: "test", SessionID: "s3",
		Msg: makeMsg("test"), CreatedAt: time.Now(),
	})
	g.Approve("carol")

	// Immediately: should be bypass
	if !g.IsBypass("carol") {
		t.Fatal("should be bypass immediately after approve")
	}

	// Wait for expiry
	time.Sleep(5 * time.Millisecond)
	if g.IsBypass("carol") {
		t.Fatal("should expire after window")
	}
}

func TestApprovalGate_ApproveWithoutPending(t *testing.T) {
	g := NewApprovalGate("", 30*time.Minute)

	if req := g.Approve("nobody"); req != nil {
		t.Fatal("should return nil when no pending")
	}
}

func TestApprovalGate_DenyWithoutPending(t *testing.T) {
	g := NewApprovalGate("", 30*time.Minute)

	if g.Deny("nobody") {
		t.Fatal("should return false when no pending")
	}
}

// makeMsg is defined in integration_test.go (same package)
