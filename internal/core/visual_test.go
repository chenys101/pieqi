// visual_test.go VisualCaptureManager 单测（p2-design.md §2）：
// 用 fake HTTP 服务替代真 Playwright（不起 Chromium），覆盖：
// Capture 事件窗口、Console/Network since 过滤、Cleanup、EnsureRunning 对外部实例的复用。
//
// 截图相关用例（落盘 / 列表倒序 / 路径逃逸）已随功能移除（2026-10-15）；
// 现在改为**负向断言**：采集不应再产生任何截图文件。
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

// TestVisualCapture_StoresEvents 采集把 console/network 并入事件窗口。
//
// 原为 TestVisualCapture_StoresScreenshotAndEvents —— 截图断言已随功能移除
// （2026-10-15）。这里保留"截图不再落盘"的**负向断言**：采集成功后
// `~previews/<task>/screenshots/` 不应产生任何文件。
func TestVisualCapture_StoresEvents(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	if err := v.Capture(context.Background(), "task1", 3000); err != nil {
		t.Fatalf("capture: %v", err)
	}
	// 事件窗口（本次采集的唯一目的）
	cons := v.Console("task1", time.Time{})
	if len(cons) != 2 || cons[0].Level != "error" || cons[1].Text != "careful" {
		t.Fatalf("console: %+v", cons)
	}
	net := v.Network("task1", time.Time{})
	if len(net) != 1 || net[0].Status != 404 {
		t.Fatalf("network: %+v", net)
	}
	// 负向断言：不再生成截图目录/文件。
	if entries, err := os.ReadDir(filepath.Join(root, "task1")); err == nil && len(entries) > 0 {
		t.Fatalf("采集不应再落盘截图，但 %s 下有 %d 项", filepath.Join(root, "task1"), len(entries))
	}
}

func TestVisualCapture_SinceFilter(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	if err := v.Capture(context.Background(), "task1", 3000); err != nil {
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

// TestVisualCapture_Cleanup 事件窗口随 Cleanup 清空。
//
// 原测试还断言截图目录被删；截图移除后只验事件窗口（那才是现在唯一的持久化侧状态）。
func TestVisualCapture_Cleanup(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	if err := v.Capture(context.Background(), "task1", 3000); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if got := v.Console("task1", time.Time{}); len(got) == 0 {
		t.Fatal("precondition: events must be present before cleanup")
	}
	v.Cleanup("task1")
	if got := v.Console("task1", time.Time{}); len(got) != 0 {
		t.Fatalf("cleanup must clear events: %+v", got)
	}
	if got := v.Network("task1", time.Time{}); len(got) != 0 {
		t.Fatalf("cleanup must clear network: %+v", got)
	}
}

func TestVisualCapture_NoRunningPreview(t *testing.T) {
	v := NewVisualCaptureManager(nil, t.TempDir())
	if err := v.Capture(context.Background(), "task1", 0); err == nil {
		t.Fatal("port=0 must fail")
	}
}

// TestVisualCapture_AttachVisual 验证 Evidence 视觉挂载（P2 §6）：
// console/network 摘要（截图已移除，见 core.Capture 说明）。
func TestVisualCapture_AttachVisual(t *testing.T) {
	root := t.TempDir()
	v, fake := newVisualWithFake(t, root)
	defer fake.Close()

	for i := 0; i < 4; i++ {
		if err := v.Capture(context.Background(), "task1", 3000); err != nil {
			t.Fatalf("capture %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	ev := Evidence{TaskID: "task1"}
	v.AttachVisual(&ev)
	// 摘要：fake 服务每次 capture 产生 1 error + 1 warn + 1 network failure，共 4 次
	if ev.Console == nil || ev.Console.Errors != 4 || ev.Console.Warnings != 4 {
		t.Fatalf("console summary: %+v", ev.Console)
	}
	if ev.Network == nil || ev.Network.Failures != 4 {
		t.Fatalf("network summary: %+v", ev.Network)
	}
	// 负向：Evidence 上不再有截图字段（编译期已保证，这里钉住 JSON 输出不复活它）。
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "screenshot") {
		t.Fatalf("Evidence JSON 不应再含 screenshot 字段: %s", b)
	}
}
