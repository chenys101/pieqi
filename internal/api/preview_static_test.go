// preview_static_test.go 静态目录预览（core.PreviewStatic）端到端测试。
//
// 覆盖 agent 产出的方案对比页在 pieqi 里被打开的关键链路：
// start → status(running) → /preview/<id>/ 返回 index.html（注入 base）→ 资源可取 →
// 目录穿越被挡 → 任务终态不回收。
package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// setupStaticPreviewTest 建一个只有静态方案页的 worktree（无 package.json）。
func setupStaticPreviewTest(t *testing.T) (*Server, *gin.Engine, *core.EventBus, *model.Task) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	wt := t.TempDir()
	prop := filepath.Join(wt, ".pieqi", "preview")
	if err := os.MkdirAll(prop, 0755); err != nil {
		t.Fatal(err)
	}
	// 页面用根绝对引用，验证代理会把它前缀化（否则手机上样式全裂）
	html := `<!doctype html><html><head><link rel="stylesheet" href="/style.css"></head>` +
		`<body><a href="/variant-b.html">B</a><h1>方案 A</h1></body></html>`
	if err := os.WriteFile(filepath.Join(prop, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prop, "style.css"), []byte("body{color:red}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prop, "variant-b.html"), []byte("<h1>方案 B</h1>"), 0644); err != nil {
		t.Fatal(err)
	}
	// 目录之外的敏感文件：路径穿越不能被读出去
	if err := os.WriteFile(filepath.Join(wt, "secret.txt"), []byte("SECRET_CONTENT_9f3a"), 0644); err != nil {
		t.Fatal(err)
	}

	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(0)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")

	task, err := store.Create(&model.Task{ProjectID: "static", ProjectPath: wt, WorktreePath: wt, Prompt: "UI 方案"})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(&config.Config{}, store, runner, hooks, bus, nil, nil)
	srv.SetFeedback(core.NewFeedbackStore(zap.NewNop(), t.TempDir()), core.NewPreviewManager(zap.NewNop()))

	r := gin.New()
	srv.Register(r)
	return srv, r, bus, task
}

func TestAPI_PreviewStatic_Serve(t *testing.T) {
	srv, r, _, task := setupStaticPreviewTest(t)

	// 静态产物**默认可访问**：没点过「启动」也应直接能开。
	// 「启动/停止」是 dev server 的概念（spawn + 端口探测），静态产物没有准备期，
	// 让用户先点一次启动只是把产物当成了服务。
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/preview/"+task.ID+"/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("静态产物应免启动直接可访问, got %d body=%s", w.Code, w.Body.String())
	}
	if !srv.preview.Running(task.ID) {
		t.Fatal("首次访问应自动登记为 running")
	}

	// start 仍然可用（幂等）：显式启动不该报错
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/tasks/"+task.ID+"/preview/start", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted && w.Code != http.StatusOK {
		t.Fatalf("start → 2xx, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"state":"running"`) {
		t.Fatalf("静态 preview start 应报 running: %s", w.Body.String())
	}
	if !srv.preview.Running(task.ID) {
		t.Fatal("Running 应为 true")
	}

	// status
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/preview/status", nil)
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"state":"running"`) || !strings.Contains(w.Body.String(), `"framework":"static"`) {
		t.Fatalf("status = %s", w.Body.String())
	}

	// 根路径 → index.html（注入 base + 根绝对引用前缀化）
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/preview/"+task.ID+"/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / → 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	base := `<base href="/preview/` + task.ID + `/">`
	if !strings.Contains(body, base) {
		t.Errorf("缺少 base 注入: %s", body)
	}
	if !strings.Contains(body, `href="/preview/`+task.ID+`/style.css"`) {
		t.Errorf("根绝对引用未前缀化: %s", body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control = %q, want no-store（方案页会反复重写）", cc)
	}

	// 子页面 + 静态资源
	for _, p := range []string{"/variant-b.html", "/style.css"} {
		w = httptest.NewRecorder()
		req, _ = http.NewRequest("GET", "/preview/"+task.ID+p, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s → %d body=%s", p, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/preview/"+task.ID+"/style.css", nil)
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Header().Get("Content-Type"), "text/css") {
		t.Errorf("css content-type = %q", w.Header().Get("Content-Type"))
	}

	// 目录穿越：不能读到 .pieqi/preview 之外的文件（断言读不到**内容**，
	// 404 JSON 里回显路径属于预期，不算泄漏）
	for _, p := range []string{"/../secret.txt", "/..%2fsecret.txt", "/../../secret.txt"} {
		w = httptest.NewRecorder()
		req, _ = http.NewRequest("GET", "/preview/"+task.ID+p, nil)
		r.ServeHTTP(w, req)
		if strings.Contains(w.Body.String(), "SECRET_CONTENT_9f3a") {
			t.Fatalf("路径穿越泄漏: %s → %d %s", p, w.Code, w.Body.String())
		}
	}

	// 不存在 → 404
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/preview/"+task.ID+"/nope.html", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("不存在文件 → 404, got %d", w.Code)
	}
}

// setupPreviewTestWithFiles 建一个只含指定文件的 worktree（key 为相对路径，"/" 分隔）。
// 用于验证"agent 把方案页写到哪"的各种分支，避免复制整段 server 装配。
func setupPreviewTestWithFiles(t *testing.T, files map[string]string) (*Server, *gin.Engine, *model.Task) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	wt := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(wt, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(0)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")

	task, err := store.Create(&model.Task{ProjectID: "wt", ProjectPath: wt, WorktreePath: wt, Prompt: "UI 方案"})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(&config.Config{}, store, runner, hooks, bus, nil, nil)
	srv.SetFeedback(core.NewFeedbackStore(zap.NewNop(), t.TempDir()), core.NewPreviewManager(zap.NewNop()))

	r := gin.New()
	srv.Register(r)
	return srv, r, task
}

// 根路径没有 index.html、只有一个嵌套入口 → 302 过去。
// 场景：agent 把多方案写成 <dir>/variant-a/index.html，用户点预览看到的不该是 404 JSON。
func TestAPI_PreviewStatic_RedirectsToSingleNestedHTML(t *testing.T) {
	srv, r, task := setupPreviewTestWithFiles(t, map[string]string{
		".pieqi/preview/variant-a/index.html": "<h1>A</h1>",
	})
	if err := srv.preview.Start(task); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/preview/"+task.ID+"/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("单入口目录 → 302, got %d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.HasSuffix(loc, "/variant-a/index.html") {
		t.Errorf("Location = %q", loc)
	}
}

// 多个入口且无 index.html → 生成索引页（纯链接），手机上能直接点进各方案。
func TestAPI_PreviewStatic_IndexListingWhenMultiple(t *testing.T) {
	srv, r, task := setupPreviewTestWithFiles(t, map[string]string{
		".pieqi/preview/variant-a.html": "<h1>A</h1>",
		".pieqi/preview/variant-b.html": "<h1>B</h1>",
	})
	if err := srv.preview.Start(task); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/preview/"+task.ID+"/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("多入口目录 → 200 索引页, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"variant-a.html", "variant-b.html"} {
		if !strings.Contains(body, want) {
			t.Errorf("索引页缺少链接 %s: %s", want, body)
		}
		if !strings.Contains(body, "/preview/"+task.ID+"/"+want) {
			t.Errorf("索引页链接未带预览前缀（手机上会 404）: %s", body)
		}
	}
}

// 散落在候选目录之外的方案页：status/start 必须给出可行动的指路信息。
// 这条钉住"用户不需要记住路径"的兜底 —— 认不出时至少知道该让 agent 往哪放。
func TestAPI_Preview_UnavailableGivesHint(t *testing.T) {
	_, r, task := setupPreviewTestWithFiles(t, map[string]string{
		"docs/ui/proposal-draft.html": "<h1>draft</h1>",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/preview/status", nil)
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"state":"unavailable"`) {
		t.Fatalf("status = %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), core.PreviewStaticDir) {
		t.Errorf("status 必须返回权威目录（供 UI 空态展示）: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "proposal-draft.html") {
		t.Errorf("status 应报告疑似方案页: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/tasks/"+task.ID+"/preview/start", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("start → 409, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "hint") || !strings.Contains(w.Body.String(), core.PreviewStaticDir) {
		t.Errorf("start 失败也应带 hint（否则用户不知道该做什么）: %s", w.Body.String())
	}

	// 认不出产物时，代理不能白给文件（免启动只对**已发现的静态目录**生效）
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/preview/"+task.ID+"/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("无可预览产物 → 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// 任务终态（completed/failed/cancelled）只收进程，静态预览必须继续可访问 ——
// 这是"任务完成后还能回看方案页"的核心约定，改坏会让整条 UI 迭代流程失效。
func TestAPI_PreviewStatic_SurvivesTerminalState(t *testing.T) {
	srv, r, bus, task := setupStaticPreviewTest(t)
	// main.go 里接线的那一步：订阅任务事件（终态/删除）
	srv.preview.WatchBus(bus)

	if err := srv.preview.Start(task); err != nil {
		t.Fatal(err)
	}

	// 任务完成 → 事件走 WatchBus
	completed := *task
	completed.Status = model.TaskCompleted
	bus.Publish(core.Event{Type: "task_completed", TaskID: task.ID, Task: &completed})

	// WatchBus 是 goroutine 消费，轮询等一小会儿再断言
	deadline := time.Now().Add(2 * time.Second)
	for {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/preview/"+task.ID+"/", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			if time.Now().After(deadline) {
				t.Fatalf("任务完成后静态预览应仍可访问，got %d", w.Code)
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		break
	}
	if !srv.preview.Running(task.ID) {
		t.Error("任务终态后静态预览应保持 running")
	}
}
