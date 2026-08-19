package larkreg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lark_credentials.json")
	if err := SaveCredentials(path, "cli_xxx", "sec_yyy"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	id, sec, ok := LoadCredentials(path)
	if !ok {
		t.Fatal("Load returned ok=false after Save")
	}
	if id != "cli_xxx" {
		t.Fatalf("AppID = %q, want cli_xxx", id)
	}
	if sec != "sec_yyy" {
		t.Fatalf("AppSecret = %q, want sec_yyy", sec)
	}
}

func TestLoadCredentials_NoFile(t *testing.T) {
	_, _, ok := LoadCredentials(filepath.Join(t.TempDir(), "missing.json"))
	if ok {
		t.Fatal("missing file should return ok=false")
	}
}

func TestLoadCredentials_Corrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte("{not json"), 0600)
	_, _, ok := LoadCredentials(path)
	if ok {
		t.Fatal("corrupt file should return ok=false")
	}
}

func TestSaveCredentials_Permissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lark_credentials.json")
	_ = SaveCredentials(path, "x", "y")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode = %o, want 0600", info.Mode().Perm())
	}
}
