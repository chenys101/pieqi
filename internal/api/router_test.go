package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

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
			ProjectID   string         `json:"project_id"`
			ProjectPath string         `json:"project_path"`
			Counts      map[string]int `json:"counts"`
			Tasks       []*model.Task  `json:"tasks"`
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
	_ = store
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
