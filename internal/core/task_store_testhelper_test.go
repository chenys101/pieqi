package core

import "testing"

// newTestTaskStore 在临时目录建 Store，并在测试结束时 Close（释放 pieqi.db 句柄）。
// Windows 上若不关库，t.TempDir 的 RemoveAll 会因文件占用失败。
func newTestTaskStore(t testing.TB, interrupted ...string) *TaskStore {
	t.Helper()
	return newTestTaskStoreDir(t, t.TempDir(), interrupted...)
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
