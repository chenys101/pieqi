package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/auth"
	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func setupAPITest(t *testing.T) (*Server, *core.TaskStore, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(5 * time.Second)
	// TaskRunner 用真构造但 Start 不会在本测试真跑（不调 createTask 的 Start 路径校验）
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	cfg := &config.Config{}
	srv := NewServer(cfg, store, runner, hooks, bus, nil, nil)
	r := gin.New()
	srv.Register(r)
	return srv, store, r
}

func TestAPI_CreateAndListTasks(t *testing.T) {
	_, store, r := setupAPITest(t)

	// create：project_path 必须是真实存在的目录（createTask 会 os.Stat）
	repoDir := t.TempDir()
	body, _ := json.Marshal(createTaskReq{ProjectPath: repoDir, Prompt: "fix bug"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Task
	json.Unmarshal(w.Body.Bytes(), &created)
	// project_id 由路径目录名派生
	if created.ProjectID != filepath.Base(repoDir) || created.Status != model.TaskPending {
		t.Fatalf("created task: %+v", created)
	}

	// list grouped
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status=%d", w.Code)
	}
	var listResp struct {
		Projects []struct {
			ProjectID   string               `json:"project_id"`
			ProjectPath string               `json:"project_path"`
			Counts      map[string]int       `json:"counts"`
			Tasks       []*model.TaskSummary `json:"tasks"`
		} `json:"projects"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Projects) != 1 {
		t.Fatalf("groups=%d", len(listResp.Projects))
	}
	g := listResp.Projects[0]
	if g.ProjectPath != repoDir || len(g.Tasks) != 1 {
		t.Fatalf("group: %+v", g)
	}
	if g.Counts["pending"] != 1 {
		t.Fatalf("counts=%+v", g.Counts)
	}
	// 回归：列表**不得**下发事件流（11MB 载荷的根因，见 model.TaskSummary）
	if body := w.Body.String(); strings.Contains(body, `"events"`) {
		t.Fatalf("列表响应不应包含 events 字段: %.300s", body)
	}
	_ = store
}

// TestAPI_ListTasksOmitsBaselineDirtyPaths 回归（2026-10-22 性能修复）：
// 列表**不得**下发 baseline.dirty_paths。
//
// 为什么要守：`Baseline.DirtyPaths` 是任务起始时整个工作区的脏文件清单，实测单个任务
// 最多 14 212 条，87 个任务的列表响应里它占 2.27 MB / 5.42 MB（42%）。而前端根本不读它
// —— 唯一读 baseline 的反馈面板走 GET /api/tasks/:id/feedback，直读 task.Baseline。
//
// 判据有两个层次，缺一不可：
//  1. **序列化层面**：响应体里不出现 `dirty_paths`（这是省下那 2.27MB 的直接原因）；
//  2. **内存层面**：`NewTaskSummary` 返回的是浅拷贝，**不可以**把 store 里那份的
//     Baseline 原地置 nil（那会改到落盘数据，属于静默损坏）。
//
// ⚠️ `store.Create` 出来的 task 没有 Baseline，所以这里必须**显式构造**一个带
// dirty_paths 的 Task 再塞进 store，否则这条测试测不出任何东西。
func TestAPI_ListTasksOmitsBaselineDirtyPaths(t *testing.T) {
	_, store, r := setupAPITest(t)

	dirty := []string{"a.go", "b.go", "untracked-dir/c.go"}
	task, err := store.Create(&model.Task{
		ProjectID:   "perf",
		ProjectPath: `D:\code\demo`,
		Prompt:      "p",
		Baseline: &model.TaskBaseline{
			HeadSHA:    "abc1234",
			CapturedAt: time.Now(),
			DirtyPaths: dirty,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status=%d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "dirty_paths") {
		t.Fatalf("列表响应不应包含 dirty_paths（它是 5.42MB 里占 42%% 的那个字段）: %.300s", body)
	}
	if strings.Contains(body, "untracked-dir/c.go") {
		t.Fatalf("列表响应不应包含任何 dirty path 内容: %.300s", body)
	}

	// 内存层面：store 里那份必须原样还在（浅拷贝，不是原地裁剪）。
	if got, ok := store.Get(task.ID); !ok || got.Baseline == nil {
		t.Fatalf("store 内的 Baseline 被改动了 —— NewTaskSummary 必须是浅拷贝，不能原地置 nil")
	} else if len(got.Baseline.DirtyPaths) != len(dirty) {
		t.Fatalf("store 内的 DirtyPaths 条数=%d，want %d（落盘数据不得被下发视图改动）",
			len(got.Baseline.DirtyPaths), len(dirty))
	}
}

func TestAPI_GetTask(t *testing.T) {
	_, store, r := setupAPITest(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var got model.Task
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.ID != task.ID {
		t.Fatalf("id=%s", got.ID)
	}

	// 404
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/nope", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("404 status=%d", w.Code)
	}
}

func TestAPI_CreateInvalidPath(t *testing.T) {
	_, _, r := setupAPITest(t)
	// project_path 指向不存在的目录 -> 400
	body, _ := json.Marshal(createTaskReq{ProjectPath: "/no/such/dir/xyz", Prompt: "x"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestAPI_InterveneNotWaiting(t *testing.T) {
	_, store, r := setupAPITest(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})

	body, _ := json.Marshal(interveneReq{Kind: "decision", Choice: "approve"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/intervene", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	// pending 任务非 waiting_input/running -> 409
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", w.Code)
	}
}

func TestAPI_DeleteTask(t *testing.T) {
	_, store, r := setupAPITest(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/tasks/"+task.ID, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if _, ok := store.Get(task.ID); ok {
		t.Fatal("task should be deleted")
	}
}

func TestAPI_AuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(5 * time.Second)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "", false, "", 0, nil, 0, 0, "main")
	cfg := &config.Config{}
	cfg.API.Token = "secret"
	srv := NewServer(cfg, store, runner, hooks, bus, nil, nil)
	r := gin.New()
	srv.Register(r)

	// 无 token -> 401
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token status=%d, want 401", w.Code)
	}
	// 正确 token -> 200
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("with token status=%d, want 200", w.Code)
	}
	// /internal/hook 不需要 auth
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/internal/hook", bytes.NewReader([]byte(`{"task_id":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("internal/hook should not require auth")
	}
}

func TestAPI_HookCallback(t *testing.T) {
	_, store, r := setupAPITest(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})

	// 异步发起 hook 回调（会阻塞），用 goroutine + 超时
	resultCh := make(chan int, 1)
	go func() {
		body, _ := json.Marshal(core.HookPayload{TaskID: task.ID, ToolName: "Bash", ToolUseID: "c1", Summary: "rm"})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/internal/hook", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		resultCh <- w.Code
	}()

	// 等 hook 注册
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		// 通过 store 看 task 是否进 waiting_input
		if t2, _ := store.Get(task.ID); t2 != nil && t2.Status == model.TaskWaitingInput {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 投递 approve（经 intervene -> runner.Intervene -> hooks.Resolve）
	body, _ := json.Marshal(interveneReq{Kind: "decision", DecisionID: "c1", Choice: "approve"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/intervene", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("intervene status=%d body=%s", w.Code, w.Body.String())
	}

	select {
	case code := <-resultCh:
		if code != http.StatusOK {
			t.Fatalf("hook callback status=%d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook callback did not return after intervene")
	}
}

func mustNewBindingStore(t *testing.T) *auth.BindingStore {
	t.Helper()
	b, err := auth.NewBindingStore(filepath.Join(t.TempDir(), "b.json"))
	if err != nil {
		t.Fatalf("binding store: %v", err)
	}
	return b
}

func TestAPI_AuthRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(5 * time.Second)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	cfg := &config.Config{}
	cfg.API.Enabled = true
	srv := NewServer(cfg, store, runner, hooks, bus, nil, nil)
	// Wire auth — debug ON for this test so the ExternalAuthMiddleware
	// on the /api business routes short-circuits (no token needed).
	authSvc := &auth.Service{
		Debug:    auth.NewDebugSwitch(true),
		Bindings: mustNewBindingStore(t),
		Tokens:   auth.NewTokenStore(),
		Limiter:  auth.NewIPLimiter(5, 10*time.Minute),
	}
	srv.SetAuth(authSvc, nil)
	r := gin.New()
	srv.Register(r)

	// GET /api/auth/status should work (public route, no gate)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/auth/status", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("auth/status = %d body=%s", w.Code, w.Body.String())
	}
}
