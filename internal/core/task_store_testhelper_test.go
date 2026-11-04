package core

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestTaskStore 在临时目录建 Store，并在测试结束时 Close（释放 pieqi.db 句柄）。
// Windows 上若不关库，RemoveAll 会因文件占用失败。
// 目录落在仓库 build/：沙箱常写不了系统 Temp，t.TempDir 会直接 Access denied。
func newTestTaskStore(t testing.TB, interrupted ...string) *TaskStore {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	base := filepath.Clean(filepath.Join(wd, "..", "..", "build"))
	dir, err := os.MkdirTemp(base, "pieqi-store-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return newTestTaskStoreDir(t, dir, interrupted...)
}

func newTestTaskStoreDir(t testing.TB, dir string, interrupted ...string) *TaskStore {
	t.Helper()
	s, err := NewTaskStore(dir, interrupted...)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
