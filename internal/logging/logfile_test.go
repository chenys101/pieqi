package logging

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDailyFileSink_RotatesByDay 跨天必须换文件，且旧文件内容留在旧文件里。
func TestDailyFileSink_RotatesByDay(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewDailyFileSink(dir)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	defer sink.Close()

	day1 := time.Date(2026, 9, 10, 23, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 9, 11, 1, 0, 0, 0, time.UTC)

	sink.nowFunc = func() time.Time { return day1 }
	if _, err := sink.Write([]byte("first-day\n")); err != nil {
		t.Fatalf("write day1: %v", err)
	}
	sink.nowFunc = func() time.Time { return day2 }
	if _, err := sink.Write([]byte("second-day\n")); err != nil {
		t.Fatalf("write day2: %v", err)
	}

	for name, want := range map[string]string{
		LogFileName(day1): "first-day\n",
		LogFileName(day2): "second-day\n",
	} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestBuildDiagnosticsZip_WindowOnly 只打包窗口内的那些天。
func TestBuildDiagnosticsZip_WindowOnly(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	// 今天、昨天、8 天前各一份
	for _, d := range []int{0, 1, 8} {
		name := LogFileName(now.AddDate(0, 0, -d))
		if err := os.WriteFile(filepath.Join(dir, name), []byte("line-"+name+"\n"), 0644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	// 一个不该被收进去的干扰文件
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("nope"), 0644); err != nil {
		t.Fatalf("seed notes: %v", err)
	}

	var buf bytes.Buffer
	if err := BuildDiagnosticsZip(&buf, dir, now, 7, map[string]any{"go_version": "test"}); err != nil {
		t.Fatalf("zip: %v", err)
	}
	names := zipNames(t, buf.Bytes())

	if !names["meta.json"] {
		t.Error("meta.json must be included")
	}
	if !names[LogFileName(now)] || !names[LogFileName(now.AddDate(0, 0, -1))] {
		t.Errorf("today/yesterday logs missing: %v", names)
	}
	if names[LogFileName(now.AddDate(0, 0, -8))] {
		t.Error("log older than the window must be excluded")
	}
	if names["notes.txt"] {
		t.Error("non-log files must be excluded")
	}
}

// TestBuildDiagnosticsZip_EmptyDirStillProducesArchive
// 「没有日志」本身就是排查结论的一部分 —— 用户拿到一个只含 meta 的包，
// 比拿到一个 500 有用得多。
func TestBuildDiagnosticsZip_EmptyDirStillProducesArchive(t *testing.T) {
	var buf bytes.Buffer
	err := BuildDiagnosticsZip(&buf, filepath.Join(t.TempDir(), "does-not-exist"), time.Now(), 7, nil)
	if err != nil {
		t.Fatalf("missing dir should not fail: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	if len(zr.File) != 0 {
		t.Fatalf("expected an empty archive, got %d entries", len(zr.File))
	}
}

func zipNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	out := map[string]bool{}
	for _, f := range zr.File {
		out[f.Name] = true
	}
	return out
}

// TestNewLogger_WritesToFile logger 建起来之后，文件里必须真的能看到那行日志。
func TestNewLogger_WritesToFile(t *testing.T) {
	dir := t.TempDir()
	logger, closeFn, err := NewLogger("debug", dir)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.Info("hello-diagnostics")
	closeFn()

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no log file produced: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	body, _ := io.ReadAll(f)
	if !bytes.Contains(body, []byte("hello-diagnostics")) {
		t.Fatalf("log file missing the message: %q", body)
	}
}
