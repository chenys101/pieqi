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

func TestToken_Renew(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewTokenStoreWithNow(func() time.Time { return now })
	tok, _ := s.Issue(15 * time.Minute)

	// 续期保持同一 token 值，过期时间按 ttl 延长
	if !s.Renew(tok, time.Hour) {
		t.Fatal("renew on valid token must succeed")
	}
	if !s.Validate(tok) {
		t.Fatal("token must remain valid after renew")
	}
	// 15m + 1h = 1h15m 后仍有效
	now = now.Add(75 * time.Minute)
	if !s.Validate(tok) {
		t.Fatal("renew must extend expiry by ttl (15m + 1h)")
	}
	// 再往后则过期
	now = now.Add(30 * time.Minute)
	if s.Validate(tok) {
		t.Fatal("token must expire after renewed TTL passes")
	}
}

func TestToken_RenewNeverShrinks(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewTokenStoreWithNow(func() time.Time { return now })
	tok, _ := s.Issue(4 * time.Hour) // 长 TTL
	// 用更短的 ttl 续期也只增加、不缩短
	if !s.Renew(tok, 15*time.Minute) {
		t.Fatal("renew should succeed")
	}
	now = now.Add(4 * time.Hour)
	if !s.Validate(tok) {
		t.Fatal("renew must never shrink: 4h + 15m > 4h")
	}
}

func TestToken_RenewUnknownOrExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewTokenStoreWithNow(func() time.Time { return now })
	if s.Renew("no-such-token", time.Hour) {
		t.Fatal("renewing unknown token must fail")
	}
	tok, _ := s.Issue(15 * time.Minute)
	now = now.Add(16 * time.Minute) // 过期
	if s.Renew(tok, time.Hour) {
		t.Fatal("renewing expired token must fail")
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
