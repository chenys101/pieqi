package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Binding is the single bound Feishu admin account. Persisted locally only;
// never uploaded. OpenID is the canonical identity field.
type Binding struct {
	OpenID   string    `json:"openid"`    // core unique identity (exact-match)
	UserID   string    `json:"user_id"`
	Nickname string    `json:"nickname"`
	BoundAt  time.Time `json:"bound_at"`
	Active   bool      `json:"active"`
}

// BindingStore persists exactly one Feishu account binding to a local JSON
// file. Bind replaces any existing binding; Unbind clears it. All ops are
// goroutine-safe via a mutex; persistence uses atomic rename.
//
// Security note: bind/unbind MUST be gated to internal IPs by the HTTP
// handler layer; the store itself enforces no network policy.
type BindingStore struct {
	mu   sync.RWMutex
	path string
	cur  *Binding
}

// NewBindingStore opens (or creates) the binding file at path. If the file
// exists and is corrupt, returns an error rather than silently losing the
// binding — operators must decide whether to recover or rebind.
func NewBindingStore(path string) (*BindingStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("mkdir binding dir: %w", err)
	}
	s := &BindingStore{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *BindingStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no binding yet — valid
		}
		return fmt.Errorf("read binding: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var b Binding
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("parse binding file %s: %w", s.path, err)
	}
	if b.OpenID != "" {
		b.Active = true
		s.cur = &b
	}
	return nil
}

// Get returns a copy of the current binding, or ok=false if unbound.
func (s *BindingStore) Get() (Binding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur == nil {
		return Binding{}, false
	}
	cp := *s.cur
	return cp, true
}

// IsBound reports whether an account is currently bound.
func (s *BindingStore) IsBound() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur != nil
}

// Match reports whether openid exactly equals the bound OpenID. Returns
// false when unbound or when openid is empty.
func (s *BindingStore) Match(openid string) bool {
	if openid == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur != nil && s.cur.OpenID == openid
}

// Bind persists b as the single bound account, replacing any existing
// binding. BoundAt is set to now if zero; Active is forced true.
func (s *BindingStore) Bind(b Binding) (Binding, error) {
	if b.OpenID == "" {
		return Binding{}, fmt.Errorf("openid is required")
	}
	if b.BoundAt.IsZero() {
		b.BoundAt = time.Now()
	}
	b.Active = true
	if err := s.persist(b); err != nil {
		return Binding{}, err
	}
	return b, nil
}

// Unbind clears the binding (writes an empty file to disk to make the
// state change durable). Idempotent.
func (s *BindingStore) Unbind() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur = nil
	return s.persistUnlocked(nil)
}

func (s *BindingStore) persist(b Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur = &b
	return s.persistUnlocked(&b)
}

func (s *BindingStore) persistUnlocked(b *Binding) error {
	var data []byte
	if b != nil {
		var err error
		data, err = json.MarshalIndent(b, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal binding: %w", err)
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write binding tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename binding: %w", err)
	}
	return nil
}

// writeFileImpl is the backing impl of the test helper writeFile in
// binding_test.go. Lives here to keep SUT I/O in one file.
func writeFileImpl(path string, data []byte) error {
	return os.WriteFile(path, data, 0600)
}
