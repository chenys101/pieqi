package auth

import (
	"path/filepath"
	"testing"
)

// tempPath returns a binding-file path inside a per-test temp dir.
// Used by middleware_test.go to construct throwaway BindingStores.
func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "binding.json")
}
