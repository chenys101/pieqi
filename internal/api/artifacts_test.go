// artifacts_test.go 产物清单（GET /api/tasks/:id/artifacts）与 HTML 产物打开安全性。
//
// 两条约定要钉住：
//  1. 产物清单只来自**事件派生**（agent 触碰过的文件）+ 磁盘现状，未触碰的文件不出现
//     （与 /file 的 touched 校验同一口径，防止把 .env 之类塞进列表）；
//  2. agent 生成的 HTML 是**不可信内容**：以 pieqi 源打开时必须有 CSP sandbox，
//     且**不含 allow-same-origin** —— 否则它能读宿主 localStorage 里的 token、以用户
//     身份驱动 agent。"默认可访问"的前提正是不给宿主权限。
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// setupArtifactTest 造一个 worktree：静态方案页（被触碰）+ markdown + 图片 +
// 未触碰的敏感文件；返回 server / 路由 / 任务。
func setupArtifactTest(t *testing.T) (*Server, *gin.Engine, *model.Task) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	wt := t.TempDir()
	files := map[string]string{
		"proposals/index.html": "<h1>方案 A</h1>",
		"proposals/style.css":  "body{color:red}",
		"docs/notes.md":        "# 记录\n",
		"shot.png":             "PNG",
		"secret.env":           "TOKEN=abc\n",
	}
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

	task, err := store.Create(&model.Task{
		ProjectID: "art", ProjectPath: wt, WorktreePath: wt,
		Prompt: "UI 方案", Status: model.TaskCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 事件：turn1 写 index.html / notes.md / shot.png；turn2 写 style.css。
	// secret.env 存在但**从未被触碰** —— 必须不出现在清单里。
	updated, err := store.Update(task.ID, func(tk *model.Task) bool {
		tk.Events = []model.TaskEvent{
			{Seq: 1, Type: model.EventUser, Text: "出方案"},
			{Seq: 2, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w1",
				Input: json.RawMessage(`{"file_path":"proposals/index.html"}`)},
			{Seq: 3, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w2",
				Input: json.RawMessage(`{"file_path":"docs/notes.md"}`)},
			{Seq: 4, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w3",
				Input: json.RawMessage(`{"file_path":"shot.png"}`)},
			{Seq: 5, Type: model.EventUser, Text: "样式"},
			{Seq: 6, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w4",
				Input: json.RawMessage(`{"file_path":"proposals/style.css"}`)},
		}
		tk.Status = model.TaskCompleted
		return true
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(&config.Config{}, store, runner, hooks, bus, nil, nil)
	srv.SetFeedback(core.NewFeedbackStore(zap.NewNop(), t.TempDir()), core.NewPreviewManager(zap.NewNop()))

	r := gin.New()
	srv.Register(r)
	return srv, r, updated
}

type artifactListResp struct {
	Artifacts []struct {
		Path string `json:"path"`
		Name string `json:"name"`
		Kind string `json:"kind"`
		Size int64  `json:"size"`
		Turn int    `json:"turn"`
		Open string `json:"open"`
	} `json:"artifacts"`
}

func getArtifacts(t *testing.T, r *gin.Engine, taskID string) artifactListResp {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+taskID+"/artifacts", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("artifacts status=%d body=%s", w.Code, w.Body.String())
	}
	var resp artifactListResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	return resp
}

func TestAPI_Artifacts_List(t *testing.T) {
	_, r, task := setupArtifactTest(t)
	resp := getArtifacts(t, r, task.ID)

	byPath := map[string]string{} // path -> kind
	for _, a := range resp.Artifacts {
		byPath[a.Path] = a.Kind
	}
	for path, kind := range map[string]string{
		"proposals/index.html": "html",
		"docs/notes.md":        "markdown",
		"shot.png":             "image",
	} {
		if byPath[path] != kind {
			t.Errorf("%s kind = %q, want %q（清单=%v）", path, byPath[path], kind, byPath)
		}
	}
	// 未触碰的文件不得出现（安全边界：与 /file 的 touched 校验同口径）
	if _, ok := byPath["secret.env"]; ok {
		t.Error("未触碰文件不应出现在产物清单里")
	}
	// 非"可查看产物"（css）不塞进列表，避免把 changelist 当产物
	if _, ok := byPath["proposals/style.css"]; ok {
		t.Error("css 不是可查看产物")
	}
	// html 排最前（这轮迭代最常被打开的就是方案页）
	if len(resp.Artifacts) == 0 || resp.Artifacts[0].Kind != "html" {
		t.Errorf("首个产物应为 html，实际=%+v", resp.Artifacts)
	}
	// 目录内 html 的打开 URL 必须走 preview（相对资源才能一起解析），
	// 且**不依赖**"用户是否点过启动"：清单请求自己会把静态实例登记起来。
	for _, a := range resp.Artifacts {
		if a.Path != "proposals/index.html" {
			continue
		}
		if !strings.HasPrefix(a.Open, "/preview/"+task.ID+"/") {
			t.Errorf("目录内 html 应走 preview 打开: %s", a.Open)
		}
	}
	// 非 html 走 /file 原始字节（markdown 由前端渲染，图片/pdf 直接作 src）
	for _, a := range resp.Artifacts {
		if a.Path == "notes.md" && !strings.HasPrefix(a.Open, "/api/tasks/"+task.ID+"/file?path=") {
			t.Errorf("markdown 应走 /file: %s", a.Open)
		}
	}
	if resp.Artifacts[0].Size == 0 {
		t.Error("size 应为磁盘实际字节数")
	}
}

// 静态预览登记后，目录内的 html 改走 preview 路径：
// 这样同目录的 style.css / 子页面等相对资源能一起解析（多文件方案页的前提）。
func TestAPI_Artifacts_HTMLOpensInPreviewWhenStatic(t *testing.T) {
	srv, r, task := setupArtifactTest(t)
	if err := srv.preview.Start(task); err != nil {
		t.Fatal(err)
	}
	resp := getArtifacts(t, r, task.ID)
	for _, a := range resp.Artifacts {
		if a.Path != "proposals/index.html" {
			continue
		}
		if !strings.HasPrefix(a.Open, "/preview/"+task.ID+"/") {
			t.Errorf("目录内 html 应走 preview 打开: %s", a.Open)
		}
		if !strings.HasSuffix(a.Open, "/index.html") {
			t.Errorf("preview 打开路径应指到具体文件: %s", a.Open)
		}
		return
	}
	t.Fatal("清单里没有 proposals/index.html")
}

// HTML / SVG 以 pieqi 源直接返回时必须带 sandbox CSP（不带 allow-same-origin）。
// 这不是"可选加固"：agent 生成的页面能读 localStorage 里的 token、以用户身份打 /api。
func TestAPI_File_HTMLSandboxed(t *testing.T) {
	_, r, task := setupArtifactTest(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/file?path=proposals/index.html", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("file status=%d body=%s", w.Code, w.Body.String())
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") {
		t.Errorf("HTML 产物缺少 CSP sandbox（同源 XSS 面）: %q", csp)
	}
	if strings.Contains(csp, "allow-same-origin") {
		t.Errorf("CSP 不能给 allow-same-origin（等于取消隔离）: %q", csp)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}

	// 纯展示类（markdown）不需要 sandbox，别顺手加上把渲染搞坏
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/file?path=docs/notes.md", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("md status=%d", w.Code)
	}
	if csp := w.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("markdown 不该加 CSP sandbox: %q", csp)
	}
}
