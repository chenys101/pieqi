// visual_e2e_test.go 真实链路 e2e（p2-design.md §2）：Go VisualCaptureManager
// spawn 真 services/visual-capture（node + Playwright + Chromium），对真实 HTTP 靶子
// 页面走完整 capture 协议（/health 探活 → /v1/capture → base64 解码 → 落盘 → 事件窗口）。
//
// 与 visual_test.go（fake 协议模拟）互补：本测试验证两侧协议的真实互通与部署前置。
// 前置缺失（node / chromium / 服务目录）时 SKIP，不算失败：
//
//	npx playwright-core install chromium
package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// chromiumInstalled 探测 Playwright 浏览器缓存（Linux/macOS 路径）。
func chromiumInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, cache := range []string{
		filepath.Join(home, ".cache", "ms-playwright"),
		filepath.Join(home, "Library", "Caches", "ms-playwright"),
	} {
		entries, err := os.ReadDir(cache)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "chromium") {
				return true
			}
		}
	}
	return false
}

// freeLocalPort 复用 preview.go 的实现（listen :0 后关闭）。

// newE2ETarget 起靶子页面：含 console error/warn + 一个 404 请求。
func newE2ETarget(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/missing.json", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body><h1>e2e visual</h1>
<script>
  console.error("e2e boom");
  console.warn("e2e careful");
  fetch("/missing.json").catch(function(){});
</script></body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestVisualCapture_E2E_NodeService(t *testing.T) {
	// 部署前置缺失 → SKIP（不影响 CI 单测，真实环境验收时跑）
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	if !chromiumInstalled() {
		t.Skip("chromium not installed (run: npx playwright-core install chromium)")
	}
	dir := filepath.Join("..", "..", "services", "visual-capture")
	if !dirExists(dir) {
		t.Skip("services/visual-capture not found")
	}

	target := newE2ETarget(t)
	targetPort := strings.TrimPrefix(strings.TrimPrefix(target.URL, "http://127.0.0.1:"), "")
	tp := 0
	if _, err := fmt.Sscanf(targetPort, "%d", &tp); err != nil {
		t.Fatalf("parse target port: %v", err)
	}

	root := t.TempDir()
	v := NewVisualCaptureManager(nil, root)
	v.dir = dir
	svcPort, err := freeLocalPort() // 随机端口：不与常驻实例（18791）冲突
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	v.port = svcPort
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = v.Stop(ctx)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Capture 全链路：spawn node 服务 → 打开靶子页面 → 截图落盘 + 事件并入窗口
	shot, err := v.Capture(ctx, "e2e-task", tp, false)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if shot.TaskID != "e2e-task" || shot.PreviewID != fmt.Sprintf("e2e-task:%d", tp) {
		t.Fatalf("shot: %+v", shot)
	}

	// 截图落盘：PNG magic + 真实尺寸（1280x800 视口截图不可能只有几百字节）
	p, ok := v.ScreenshotPath("e2e-task", shot.ID)
	if !ok {
		t.Fatal("screenshot file missing")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read png: %v", err)
	}
	if len(data) < 1000 || data[0] != 0x89 {
		t.Fatalf("png invalid: %d bytes, magic=0x%02x", len(data), data[0])
	}

	// console 窗口：error + warn 都采到（Chromium 真实事件）
	cons := v.Console("e2e-task", time.Time{})
	var hasErr, hasWarn bool
	for _, e := range cons {
		if e.Level == "error" && e.Text == "e2e boom" {
			hasErr = true
		}
		if e.Level == "warn" && e.Text == "e2e careful" {
			hasWarn = true
		}
	}
	if !hasErr || !hasWarn {
		t.Fatalf("console events: %+v", cons)
	}

	// network 窗口：404 请求采到
	nets := v.Network("e2e-task", time.Time{})
	var has404 bool
	for _, n := range nets {
		if n.Status == 404 && strings.Contains(n.URL, "/missing.json") {
			has404 = true
		}
	}
	if !has404 {
		t.Fatalf("network events: %+v", nets)
	}

	// AttachVisual：Evidence 挂视觉证据（P2 §6 真实数据）
	ev := Evidence{TaskID: "e2e-task"}
	v.AttachVisual(&ev, 1)
	if len(ev.Screenshots) != 1 || !strings.HasPrefix(ev.Screenshots[0], "/api/tasks/e2e-task/preview/screenshots/") {
		t.Fatalf("attach screenshots: %+v", ev.Screenshots)
	}
	if ev.Console == nil || ev.Console.Errors < 1 || ev.Network == nil || ev.Network.Failures < 1 {
		t.Fatalf("attach summaries: console=%+v network=%+v", ev.Console, ev.Network)
	}

	// Stop 后可再次 EnsureRunning（watcher 清状态 → 重新 spawn）
	if err := v.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := v.EnsureRunning(ctx); err != nil {
		t.Fatalf("restart: %v", err)
	}
}
