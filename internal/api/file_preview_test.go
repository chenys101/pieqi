// file_preview_test.go 文件预览端点测试：validRepoPath 路径校验 + getTaskFile
// 的 touched-only 与路径逃逸防护（安全边界回归）。
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestValidRepoPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"README.md", true},
		{"docs/guide.md", true},
		{"", false},
		{".", false},
		{"/etc/passwd", false},
		{"../secret", false},
		{"a/../../b", false},
		{"a\\..\\b", false}, // 反斜杠 .. 逃逸
	}
	for _, c := range cases {
		if got := validRepoPath(c.in); got != c.want {
			t.Errorf("validRepoPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFileContentType(t *testing.T) {
	if got := fileContentType("a.md", []byte("# x")); got != "text/markdown; charset=utf-8" {
		t.Errorf("md content-type = %q", got)
	}
	if got := fileContentType("a.pdf", nil); got != "application/pdf" {
		t.Errorf("pdf content-type = %q", got)
	}
	if got := fileContentType("a.unknown", []byte("plain text")); got != "text/plain; charset=utf-8" {
		t.Errorf("unknown text content-type = %q", got)
	}
}

func setupFilePreviewTest(t *testing.T) (*gin.Engine, *core.TaskStore, *model.Task) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(0)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	srv := NewServer(&config.Config{}, store, runner, hooks, bus, nil, nil)
	r := gin.New()
	srv.Register(r)

	wt := t.TempDir()
	// 落盘两个文件：README.md（Agent 触碰）、secret.env（未触碰但存在）
	_ = os.WriteFile(filepath.Join(wt, "README.md"), []byte("# Title\n"), 0644)
	_ = os.WriteFile(filepath.Join(wt, "secret.env"), []byte("TOKEN=abc\n"), 0644)

	task, _ := store.Create(&model.Task{
		ProjectID: "fp", ProjectPath: wt, WorktreePath: wt,
		Prompt: "p", Status: model.TaskCompleted,
	})
	// 事件：Turn1 写 README.md（唯一触碰文件）
	updated, _ := store.Update(task.ID, func(tk *model.Task) bool {
		tk.Events = []model.TaskEvent{
			{Seq: 1, Type: model.EventUser, Text: "t1"},
			{Seq: 2, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w1",
				Input: json.RawMessage(`{"file_path":"README.md","content":"# Title\n"}`)},
		}
		tk.Status = model.TaskCompleted
		return true
	})
	return r, store, updated
}

func TestGetTaskFile(t *testing.T) {
	r, _, task := setupFilePreviewTest(t)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet,
			"/api/tasks/"+task.ID+"/file?path="+path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// 触碰文件 → 200 + 原文 + markdown content-type
	w := get("README.md")
	if w.Code != http.StatusOK {
		t.Fatalf("touched file status = %d, body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "# Title\n" {
		t.Errorf("touched file body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Errorf("touched file content-type = %q", ct)
	}

	// 未触碰（磁盘存在）→ 404，不泄露内容
	if w := get("secret.env"); w.Code != http.StatusNotFound {
		t.Errorf("untouched file status = %d, want 404", w.Code)
	}

	// 路径逃逸 → 400
	for _, p := range []string{"../secret.env", "a/../../secret.env"} {
		if w := get(p); w.Code != http.StatusBadRequest {
			t.Errorf("traversal path %q status = %d, want 400", p, w.Code)
		}
	}
}
