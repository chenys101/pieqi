package auth

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBinding_EmptyByDefault(t *testing.T) {
	s := newTestBindingStore(t)
	if b, ok := s.Get(); ok {
		t.Fatalf("new store should have no binding, got %+v", b)
	}
	if s.IsBound() {
		t.Fatal("new store should report IsBound=false")
	}
}

func TestBinding_BindAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binding.json")
	s, err := NewBindingStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	b, err := s.Bind(Binding{
		OpenID:   "ou_test_123",
		UserID:   "u_test",
		Nickname: "Alice",
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if b.OpenID != "ou_test_123" || !b.Active || b.BoundAt.IsZero() {
		t.Fatalf("binding fields wrong: %+v", b)
	}
	if !s.IsBound() {
		t.Fatal("should be bound after Bind")
	}
	got, ok := s.Get()
	if !ok || got.OpenID != "ou_test_123" {
		t.Fatalf("get after bind: %+v ok=%v", got, ok)
	}

	// Reload from disk: persistence must survive process restart
	s2, err := NewBindingStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got2, ok := s2.Get()
	if !ok || got2.OpenID != "ou_test_123" || got2.Nickname != "Alice" {
		t.Fatalf("reload binding: %+v ok=%v", got2, ok)
	}
}

func TestBinding_RebindReplaces(t *testing.T) {
	s := newTestBindingStore(t)
	_, _ = s.Bind(Binding{OpenID: "ou_a", UserID: "u_a", Nickname: "A"})
	_, err := s.Bind(Binding{OpenID: "ou_b", UserID: "u_b", Nickname: "B"})
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	got, _ := s.Get()
	if got.OpenID != "ou_b" {
		t.Fatalf("rebind should replace, got %+v", got)
	}
}

func TestBinding_Unbind(t *testing.T) {
	s := newTestBindingStore(t)
	_, _ = s.Bind(Binding{OpenID: "ou_x"})
	if !s.IsBound() {
		t.Fatal("should be bound")
	}
	if err := s.Unbind(); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if s.IsBound() {
		t.Fatal("should not be bound after Unbind")
	}
	// Unbind twice is a no-op (idempotent)
	if err := s.Unbind(); err != nil {
		t.Fatalf("double unbind: %v", err)
	}
}

func TestBinding_Match(t *testing.T) {
	s := newTestBindingStore(t)
	if s.Match("anything") {
		t.Fatal("empty store should not match anything")
	}
	_, _ = s.Bind(Binding{OpenID: "ou_match"})
	if !s.Match("ou_match") {
		t.Error("bound OpenID should match")
	}
	if s.Match("ou_match") { // second call should still match
		// ok
	} else {
		t.Error("match should be repeatable")
	}
	if s.Match("ou_other") {
		t.Error("different OpenID must not match")
	}
	if s.Match("") {
		t.Error("empty OpenID must not match")
	}
	if s.Match("ou_match ") {
		t.Error("match must be exact (no trim)")
	}
}

func TestBinding_CorruptFileIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binding.json")
	if err := writeFile(path, []byte("{not json")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewBindingStore(path); err == nil {
		t.Fatal("corrupt file should error on load")
	}
}

func newTestBindingStore(t *testing.T) *BindingStore {
	t.Helper()
	s, err := NewBindingStore(filepath.Join(t.TempDir(), "binding.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

// writeFile helper avoids pulling extra imports into the SUT file.
func writeFile(path string, data []byte) error {
	return writeFileImpl(path, data)
}

// Add a sanity check that time is referenced (used in Binding.BoundAt comparisons
// in the tests above) — keeps gofmt's import linter happy if anything is
// later removed.
var _ = time.Time{}
