package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// 项目不再是必选项（用户要求）：POST /api/tasks 可以不带 project_path，
// 此时用设置里的 default_project_path。
//
// 这两条用例守的是**同一个决定的两面**：
//   - 配了默认目录 → 建任务成功，且落库的 project_path 就是那个目录
//     （否则后续 worktree / diff / 反馈全都会指向空路径）；
//   - 没配默认目录 → **明确 400 并指向设置页**，绝不猜一个目录去跑 agent。

func postTask(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/tasks", bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = r
	srv.createTask(c)
	return w
}

// TestCreateTask_UsesDefaultProjectPath 不带路径时用设置里的默认目录。
func TestCreateTask_UsesDefaultProjectPath(t *testing.T) {
	srv, store, _ := setupAPITest(t)
	settings, err := core.NewSettingsStore("") // 纯内存
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	ws := t.TempDir()
	if _, err := settings.Patch(core.SettingsPatch{DefaultProjectPath: &ws}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	srv.SetSettingsStore(settings)

	w := postTask(t, srv, `{"prompt":"在没有项目的目录里干活"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Task
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ProjectPath != filepath.Clean(ws) {
		t.Fatalf("project_path = %q, want 默认目录 %q", created.ProjectPath, ws)
	}
	if created.ProjectID != filepath.Base(filepath.Clean(ws)) {
		t.Fatalf("project_id = %q（应由默认目录名派生）", created.ProjectID)
	}
	// 落库的必须是真实存在的目录：下游（worktree / baseline / diff）全都按它跑。
	if info, err := os.Stat(created.ProjectPath); err != nil || !info.IsDir() {
		t.Fatalf("落库路径不是可访问的目录: %q (%v)", created.ProjectPath, err)
	}
	if got, ok := store.Get(created.ID); !ok || got.ProjectPath != created.ProjectPath {
		t.Fatalf("store 里的任务 project_path 不一致")
	}
}

// TestCreateTask_NoDefaultProjectPath 没配默认目录且没显式传路径 → 400，且错误指路设置页。
func TestCreateTask_NoDefaultProjectPath(t *testing.T) {
	srv, _, _ := setupAPITest(t)
	settings, _ := core.NewSettingsStore("")
	srv.SetSettingsStore(settings)

	w := postTask(t, srv, `{"prompt":"p"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// 判据是「用户看完知道下一步去哪」：必须同时提到设置与"路径"。
	if !strings.Contains(body, "默认工作目录") {
		t.Fatalf("错误应点明缺的是默认工作目录: %s", body)
	}
	if !strings.Contains(body, "设置") {
		t.Fatalf("错误应指路设置页: %s", body)
	}
}

// TestCreateTask_DefaultPathGone 默认目录被删掉后，报错要说清**值来自设置**。
//
// 只说 "path not found" 会让用户去查前端选的项目，而真正要改的地方在设置页。
func TestCreateTask_DefaultPathGone(t *testing.T) {
	srv, _, _ := setupAPITest(t)
	settings, _ := core.NewSettingsStore("")
	ws := t.TempDir()
	dir := ws
	if _, err := settings.Patch(core.SettingsPatch{DefaultProjectPath: &dir}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	srv.SetSettingsStore(settings)
	// 目录在建任务之前消失（用户手动删了 / 换机器恢复了 settings.json）
	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}

	w := postTask(t, srv, `{"prompt":"p"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "来自设置") {
		t.Fatalf("错误应说明该路径来自设置: %s", w.Body.String())
	}
}

// TestCreateTask_ExplicitPathWins 显式路径优先于默认目录（默认只是缺省）。
func TestCreateTask_ExplicitPathWins(t *testing.T) {
	srv, _, _ := setupAPITest(t)
	settings, _ := core.NewSettingsStore("")
	def := t.TempDir()
	if _, err := settings.Patch(core.SettingsPatch{DefaultProjectPath: &def}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	srv.SetSettingsStore(settings)

	explicit := t.TempDir()
	body, _ := json.Marshal(createTaskReq{ProjectPath: explicit, Prompt: "p"})
	w := postTask(t, srv, string(body))
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Task
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.ProjectPath != filepath.Clean(explicit) {
		t.Fatalf("project_path = %q, want 显式路径 %q", created.ProjectPath, explicit)
	}
}
