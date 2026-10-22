package agent

import (
	"encoding/json"
	"os"
	"testing"
)

// writeCatalogFileForTest 直接落一份磁盘档（绕过 saveCatalogToDisk 的 agent_type 取值）。
func writeCatalogFileForTest(t *testing.T, path string, f catalogCacheFile) {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
