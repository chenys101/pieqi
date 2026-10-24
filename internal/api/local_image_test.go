// local_image_test.go 本地图片端点的安全边界回归。
//
// 这个端点会**读磁盘上的任意图片文件并回传给浏览器**，它的安全性完全依赖两条闸门：
//   ① 路径必须落在该任务自己的 project/worktree 目录内（`..` 逃逸、兄弟目录、系统目录都要挡住）；
//   ② 扩展名必须是图片白名单（否则 `![x](config.yaml)` 就能把仓库里的密钥读到页面上）。
// 用例逐条钉住这两条 —— 它们一旦坏掉，界面上**看不出任何异常**，只会静默变成任意文件读取。
package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const localPNG = "\x89PNG\r\n\x1a\n-fake-bytes"

func setupLocalImageTest(t *testing.T) (*gin.Engine, *model.Task, string, string) {
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

	// 项目目录里放一张图、一个嵌套目录里的图、一个非图片文件
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".tmp-avatar", "final"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "a.png"), []byte(localPNG), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".tmp-avatar", "final", "_contact-6up.png"), []byte(localPNG), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "config.yaml"), []byte("token: secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 项目目录之外的同名图：越界必须挡
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte(localPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	task, _ := store.Create(&model.Task{
		ProjectID: "img", ProjectPath: proj, WorktreePath: proj,
		Prompt: "p", Status: model.TaskCompleted,
	})
	return r, task, proj, outside
}

func getLocalImage(r *gin.Engine, taskID, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/"+taskID+"/local-image?path="+url.QueryEscape(path), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestGetTaskLocalImage_ServesProjectImage(t *testing.T) {
	r, task, proj, _ := setupLocalImageTest(t)

	// ① 绝对路径（Agent 最常写的形态，结论里的 ![x](D:\...\a.png) 就是它）
	w := getLocalImage(r, task.ID, filepath.Join(proj, "a.png"))
	if w.Code != http.StatusOK {
		t.Fatalf("absolute path status = %d, body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != localPNG {
		t.Errorf("body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q：本地图片路径可复用（重新生成会覆盖），不能 immutable 缓存", cc)
	}

	// ② 相对路径（项目根相对，允许 ./ 前缀）
	for _, p := range []string{"a.png", "./a.png", ".tmp-avatar/final/_contact-6up.png"} {
		w := getLocalImage(r, task.ID, p)
		if w.Code != http.StatusOK {
			t.Errorf("relative path %q status = %d, body=%s", p, w.Code, w.Body.String())
		}
	}
}

func TestGetTaskLocalImage_RejectsEscape(t *testing.T) {
	r, task, _, outside := setupLocalImageTest(t)

	cases := []struct {
		name string
		path string
		want int
	}{
		// 项目目录之外的绝对路径：兄弟临时目录
		{"outside absolute", filepath.Join(outside, "secret.png"), http.StatusBadRequest},
		// 相对路径 `..` 逃逸（反斜杠与正斜杠都要挡）
		{"dotdot slash", "../secret.png", http.StatusBadRequest},
		{"dotdot backslash", `..\..\secret.png`, http.StatusBadRequest},
		// 非图片扩展名：把项目里的密钥当"图片"读是这条闸门要防的直接后果
		{"non image ext", "config.yaml", http.StatusBadRequest},
		// 不存在的图
		{"missing", "nope.png", http.StatusNotFound},
		{"empty", "", http.StatusBadRequest},
	}
	for _, c := range cases {
		w := getLocalImage(r, task.ID, c.path)
		if w.Code != c.want {
			t.Errorf("%s (%q): status = %d, want %d, body=%s", c.name, c.path, w.Code, c.want, w.Body.String())
		}
	}
}

// 大小写与前缀相近的目录都不能被当成"在项目内"。
func TestRelUnder(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{"file", filepath.Join(root, "a.png"), true},
		{"nested", filepath.Join(root, "sub", "b.png"), true},
		// 前缀相近但是**兄弟目录**：必须拒绝（否则 `...\pieqi2` 会被当成 `...\pieqi` 内）
		{"sibling with same prefix", root + "2" + string(filepath.Separator) + "a.png", false},
		// 逃逸
		{"escape", filepath.Join(root, "..", "other", "a.png"), false},
		// 根目录自身不是文件
		{"root itself", root, false},
	}
	for _, c := range cases {
		if _, got := relUnder(root, c.target); got != c.want {
			t.Errorf("%s: relUnder(%q) = %v, want %v", c.name, c.target, got, c.want)
		}
	}

	// Windows 路径不区分大小写：`d:` 与 `D:` 是同一个盘。这条只在 Windows 上成立，
	// 但正是本机（唯一部署平台）会踩到的形态。
	if runtime.GOOS == "windows" {
		mixed := strings.ToUpper(root[:1]) + strings.ToLower(root[1:])
		if _, got := relUnder(mixed, filepath.Join(root, "a.png")); !got {
			t.Errorf("盘符/路径大小写不同却被判为越界：root=%q", mixed)
		}
	}
}
