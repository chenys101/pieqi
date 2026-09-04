// visual_test.go VisualCaptureManager 单测（p2-design.md §2）：
// 用 fake HTTP 服务替代真 Playwright（不起 Chromium），覆盖：
// Capture 截图落盘 + 事件窗口、ListScreenshots 倒序、ScreenshotPath 防逃逸、
// Console/Network since 过滤、Cleanup、EnsureRunning 对外部实例的复用。
package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// png1x1 测试用最小 PNG（1x1 像素）。
var png1x1 = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0D, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

// newFakeVisualServer 伪造 /health + /v1/capture（返回固定 PNG + 事件）。
func newFakeVisualServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/v1/capture", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ URL string `json:"url"` }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.URL == "" || in.URL[:7] != "http://" {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"png_base64": base64.StdEncoding.EncodeToString(png1x1),
			"console": []map[string]any{
				{"level": "error", "text": "boom", "at": "2026-09-04T10:00:00Z"},
				{"level": "warn", "text": "careful", "at": "2026-09-04T10:00:01Z"},
			},
			"network": []map[string]any{
				{"url": "http://127.0.0.1:1/missing.json", "method": "GET", "status": 404, "at": "2026-09-04T10:00:02Z"},
			},
		})
	})
	return httptest.NewServer(mux)
}

// newVisualWithFake 让 manager 指向 fake 服务（不起子进程）。
// 复用 EnsureRunning 的「外部健康实例直接复用」分支。
func newVisualWithFake(t *testing.T, root string) (*VisualCaptureManager, *httptest.Server) {
	t.Helper()
	fake := newFakeVisualServer(t)
	v := NewVisualCaptureManager(nil, root)
	// 从 fake URL 提取端口，让探活/请求都打到 fake
	u, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatalf("parse fake url: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(u.Port(), "%d", &port); err != nil {
		t.Fatalf("parse fake port: %v", err)
	}
	v.port = port
	return v, fake
}

func TestVisualCapture_StoresScreenshotAndEvents(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	shot, err := v.Capture(context.Background(), "task1", 3000, false)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if shot.TaskID != "task1" || shot.PreviewID != "task1:3000" {
		t.Fatalf("shot: %+v", shot)
	}
	// PNG 落盘
	p, ok := v.ScreenshotPath("task1", shot.ID)
	if !ok {
		t.Fatal("screenshot file missing")
	}
	data, _ := os.ReadFile(p)
	if len(data) == 0 || data[0] != 0x89 {
		t.Fatalf("png content invalid: %d bytes", len(data))
	}
	// 事件窗口
	cons := v.Console("task1", time.Time{})
	if len(cons) != 2 || cons[0].Level != "error" || cons[1].Text != "careful" {
		t.Fatalf("console: %+v", cons)
	}
	net := v.Network("task1", time.Time{})
	if len(net) != 1 || net[0].Status != 404 {
		t.Fatalf("network: %+v", net)
	}
}

func TestVisualCapture_SinceFilter(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	if _, err := v.Capture(context.Background(), "task1", 3000, false); err != nil {
		t.Fatalf("capture: %v", err)
	}
	since := time.Date(2026, 9, 4, 10, 0, 0, 500000000, time.UTC) // error(10:00:00) 之后、warn(10:00:01) 之前
	if got := v.Console("task1", since); len(got) != 1 || got[0].Text != "careful" {
		t.Fatalf("since filter console: %+v", got)
	}
	if got := v.Network("task1", since); len(got) != 1 {
		t.Fatalf("since filter network: %+v", got)
	}
}

func TestVisualCapture_ListAndPathSafety(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	for i := 0; i < 3; i++ {
		if _, err := v.Capture(context.Background(), "task1", 3000, false); err != nil {
			t.Fatalf("capture %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond) // ModTime 区分
	}
	shots := v.ListScreenshots("task1")
	if len(shots) != 3 {
		t.Fatalf("list: %d", len(shots))
	}
	// 倒序：最新在前
	if !shots[0].CreatedAt.After(shots[2].CreatedAt) {
		t.Fatal("list must be newest-first")
	}
	// 路径逃逸拒绝
	if _, ok := v.ScreenshotPath("task1", "../../etc/passwd"); ok {
		t.Fatal("path traversal must be rejected")
	}
	if _, ok := v.ScreenshotPath("task1", "zzzz"); ok {
		t.Fatal("invalid id must be rejected")
	}
}

func TestVisualCapture_Cleanup(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	if _, err := v.Capture(context.Background(), "task1", 3000, false); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "task1", "screenshots")); err != nil {
		t.Fatalf("dir must exist: %v", err)
	}
	v.Cleanup("task1")
	if _, err := os.Stat(filepath.Join(root, "task1", "screenshots")); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove dir")
	}
	if got := v.Console("task1", time.Time{}); len(got) != 0 {
		t.Fatalf("cleanup must clear events: %+v", got)
	}
}

func TestVisualCapture_NoRunningPreview(t *testing.T) {
	v := NewVisualCaptureManager(nil, t.TempDir())
	if _, err := v.Capture(context.Background(), "task1", 0, false); err == nil {
		t.Fatal("port=0 must fail")
	}
}

// TestVisualCapture_AttachVisual 验证 Evidence 视觉挂载（P2 §6）：
// 截图引用（最新在前、limit 截断）+ console/network 摘要。
func TestVisualCapture_AttachVisual(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	for i := 0; i < 4; i++ {
		if _, err := v.Capture(context.Background(), "task1", 3000, false); err != nil {
			t.Fatalf("capture %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	ev := Evidence{TaskID: "task1"}
	v.AttachVisual(&ev, 2)
	// limit=2：只挂最新 2 张（ListScreenshots 倒序 → 最新在前）
	if len(ev.Screenshots) != 2 {
		t.Fatalf("screenshots: %+v", ev.Screenshots)
	}
	for _, u := range ev.Screenshots {
		if !strings.HasPrefix(u, "/api/tasks/task1/preview/screenshots/") {
			t.Fatalf("url format: %q", u)
		}
	}
	// 摘要：fake 服务每次 capture 产生 1 error + 1 warn + 1 network failure，共 4 次
	if ev.Console == nil || ev.Console.Errors != 4 || ev.Console.Warnings != 4 {
		t.Fatalf("console summary: %+v", ev.Console)
	}
	if ev.Network == nil || ev.Network.Failures != 4 {
		t.Fatalf("network summary: %+v", ev.Network)
	}

	// maxShots=0：不挂截图，摘要仍在
	ev2 := Evidence{TaskID: "task1"}
	v.AttachVisual(&ev2, 0)
	if len(ev2.Screenshots) != 0 || ev2.Console == nil {
		t.Fatalf("maxShots=0: %+v", ev2)
	}
}
