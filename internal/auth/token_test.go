package auth

import (
	"testing"
	"time"
)

func TestToken_IssueAndValidate(t *testing.T) {
	s := NewTokenStore()
	tok, _ := s.Issue(15 * time.Minute)
	if len(tok) != 32 {
		t.Fatalf("token length = %d, want 32 (cryptographically random)", len(tok))
	}
	if !s.Validate(tok) {
		t.Fatal("freshly issued token should validate")
	}
	// Two tokens must be distinct (randomness sanity)
	tok2, _ := s.Issue(15 * time.Minute)
	if tok2 == tok {
		t.Fatal("two issues must produce different tokens")
	}
	// Both still valid (Issue does NOT auto-invalidate; only IssueForNewTunnel does)
	if !s.Validate(tok) || !s.Validate(tok2) {
		t.Fatal("both tokens should be valid")
	}
}

func TestToken_IssueForNewTunnelInvalidatesAll(t *testing.T) {
	s := NewTokenStore()
	first, _ := s.IssueForNewTunnel(15 * time.Minute)
	second, _ := s.IssueForNewTunnel(15 * time.Minute)
	if s.Validate(first) {
		t.Fatal("IssueForNewTunnel must invalidate previous tokens")
	}
	if !s.Validate(second) {
		t.Fatal("the latest (second) token must remain valid")
	}
}

func TestToken_TTLExpiry(t *testing.T) {
	// Use a custom clock we can advance manually.
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewTokenStoreWithNow(func() time.Time { return now })
	tok, _ := s.Issue(15 * time.Minute)
	if !s.Validate(tok) {
		t.Fatal("token valid before TTL")
	}
	now = now.Add(16 * time.Minute)
	if s.Validate(tok) {
		t.Fatal("token must be invalid after TTL")
	}
}

func TestToken_InvalidateSpecific(t *testing.T) {
	s := NewTokenStore()
	tok, _ := s.Issue(15 * time.Minute)
	s.Invalidate(tok)
	if s.Validate(tok) {
		t.Fatal("explicitly invalidated token must not validate")
	}
	// Invalidating unknown token is a no-op (no panic)
	s.Invalidate("no-such-token")
}

func TestToken_InvalidateAll(t *testing.T) {
	s := NewTokenStore()
	a, _ := s.Issue(15 * time.Minute)
	b, _ := s.Issue(15 * time.Minute)
	s.InvalidateAll()
	if s.Validate(a) || s.Validate(b) {
		t.Fatal("InvalidateAll must clear every token")
	}
	if _, ok := s.Current(); ok {
		t.Fatal("no current token after InvalidateAll")
	}
}

func TestToken_Current(t *testing.T) {
	s := NewTokenStore()
	if _, ok := s.Current(); ok {
		t.Fatal("empty store should have no current token")
	}
	tok, _ := s.Issue(15 * time.Minute)
	cur, ok := s.Current()
	if !ok || cur != tok {
		t.Fatalf("Current = %q ok=%v, want %q", cur, ok, tok)
	}
}

func TestToken_NotPersisted(t *testing.T) {
	// Tokens are in-memory only: a fresh store has no token even if a
	// previous store in the same process issued one.
	s1 := NewTokenStore()
	tok, _ := s1.Issue(15 * time.Minute)
	s2 := NewTokenStore()
	if s2.Validate(tok) {
		t.Fatal("new store must not carry over tokens from another store instance")
	}
}
