package auth

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TokenStore holds in-memory, time-limited tunnel access tokens. Tokens
// are NEVER persisted: not to disk, not to logs. A token is valid only
// while (a) it is in this map, (b) it has not passed its TTL, and (c)
// InvalidateAll has not been called since issue.
//
// Lifecycle triggers for invalidation (per PRD §4.4):
//   - TTL expiry (lazy on next Validate)
//   - manual Invalidate(token)
//   - manual InvalidateAll
//   - IssueForNewTunnel (start a new tunnel → all old tokens die)
//   - program restart (in-memory → gone by definition)
//   - cloudflared process crash (handled by TunnelManager)
//   - debug switch true→false (handled by Service.WatchDebugSwitch)
type TokenStore struct {
	mu      sync.RWMutex
	now     func() time.Time
	tokens  map[string]time.Time // token → expiry
	current string               // last issued (for "current token" API)
}

// NewTokenStore creates a token store using the real wall clock.
func NewTokenStore() *TokenStore {
	return &TokenStore{now: time.Now, tokens: make(map[string]time.Time)}
}

// NewTokenStoreWithNow injects a custom clock — used for TTL tests.
func NewTokenStoreWithNow(now func() time.Time) *TokenStore {
	return &TokenStore{now: now, tokens: make(map[string]time.Time)}
}

// Issue creates a new random 32-char token valid for ttl. Multiple Issue
// calls do NOT invalidate each other (call IssueForNewTunnel for that
// semantics — see PRD §4.1.4).
func (s *TokenStore) Issue(ttl time.Duration) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.tokens[tok] = s.now().Add(ttl)
	s.current = tok
	s.mu.Unlock()
	return tok, nil
}

// IssueForNewTunnel issues a token AND invalidates all previously issued
// tokens atomically. Use this when a new cloudflared tunnel is started —
// per PRD §4.1.4 each new tunnel auto-invalidates old tokens.
func (s *TokenStore) IssueForNewTunnel(ttl time.Duration) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.tokens = make(map[string]time.Time, 1) // drop all old
	s.tokens[tok] = s.now().Add(ttl)
	s.current = tok
	s.mu.Unlock()
	return tok, nil
}

// Validate reports whether tok is currently valid. Returns false for
// expired tokens (lazy GC of expired entries happens here too).
func (s *TokenStore) Validate(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.tokens[tok]
	if !ok {
		return false
	}
	if s.now().After(expiry) {
		delete(s.tokens, tok) // lazy GC
		if s.current == tok {
			s.current = ""
		}
		return false
	}
	return true
}

// Renew extends the expiry of an existing still-valid token BY ttl (never
// shrinks it). Keeps the same token VALUE — links that already embed the
// token keep working, which is the point of 续期 vs reset (which rotates).
// Returns false for unknown/expired tokens (caller should start a tunnel).
func (s *TokenStore) Renew(tok string, ttl time.Duration) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.tokens[tok]
	if !ok {
		return false
	}
	if s.now().After(expiry) {
		delete(s.tokens, tok)
		if s.current == tok {
			s.current = ""
		}
		return false
	}
	s.tokens[tok] = expiry.Add(ttl)
	return true
}

// Invalidate marks tok as no-longer-valid. Idempotent.
func (s *TokenStore) Invalidate(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, tok)
	if s.current == tok {
		s.current = ""
	}
}

// InvalidateAll drops every token. Used on tunnel stop, program shutdown,
// and debug-switch true→false transitions.
func (s *TokenStore) InvalidateAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = make(map[string]time.Time)
	s.current = ""
}

// Current returns the most recently issued token (still valid), or
// ok=false if none. Convenience for the status endpoint.
func (s *TokenStore) Current() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == "" {
		return "", false
	}
	if s.now().After(s.tokens[s.current]) {
		return "", false
	}
	return s.current, true
}

// randomToken returns a 32-char cryptographically-random lowercase
// alphanumeric string (base32 without padding). Uses crypto/rand so the
// output is unpredictable — brute force is infeasible within the short TTL.
func randomToken() (string, error) {
	b := make([]byte, 20) // 20 bytes → 32 base32 chars
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.EncodeToString(b)), nil
}
