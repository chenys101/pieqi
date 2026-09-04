// feedback_p2_test.go Feedback P2 API 集成测试（p2-design.md §9）：
// 截图端点分支（409/503/404）、console/network 降级、push 手动推送、rewind scope:file。
// 截图成功路径与事件窗口在 core/visual_test.go 覆盖（fake 采集服务）。
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// fakePushProvider 记录推送内容的假 provider。
type fakePushProvider struct {
	mu       sync.Mutex
	received []core.EvidencePushContent
}

func (f *fakePushProvider) Name() string { return "fake" }

func (f *fakePushProvider) Push(_ context.Context, _ core.PushTarget, content core.EvidencePushContent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.received = append(f.received, content)
	return nil
}

func (f *fakePushProvider) last() core.EvidencePushContent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.received) == 0 {
		return core.EvidencePushContent{}
	}
	return f.received[len(f.received)-1]
}

// setupP2Test 建 Server（feedback 接线；visual/push 按需注入）。
func setupP2Test(t *testing.T) (*Server, *core.TaskStore, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(0)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	srv := NewServer(&config.Config{}, store, runner, hooks, bus, nil, nil)
	srv.SetFeedback(core.NewFeedbackStore(zap.NewNop(), t.TempDir()), nil)
	r := gin.New()
	srv.Register(r)
	return srv, store, r
}

// seedP2Task 建已完成任务：Turn1 写 a.txt，Turn2 改 a.txt（快照已捕获）。
func seedP2Task(t *testing.T, store *core.TaskStore, fs *core.FeedbackStore) *model.Task {
	t.Helper()
	wt := t.TempDir()
	task, err := store.Create(&model.Task{
		ProjectID: "p2", ProjectPath: wt, WorktreePath: wt,
		Prompt: "do", Status: model.TaskCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	// setEvents 更新事件后重新取最新 task 引用（Update 返回副本语义）。
	setEvents := func(evs []model.TaskEvent) *model.Task {
		updated, _ := store.Update(task.ID, func(tk *model.Task) bool {
			tk.Events = evs
			tk.Status = model.TaskCompleted
			return true
		})
		return updated
	}

	// Turn1：新建 a.txt = v1 → 快照
	turn1 := []model.TaskEvent{
		{Seq: 1, Type: model.EventUser, Text: "turn1"},
		{Seq: 2, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w1",
			Input: json.RawMessage(`{"file_path":"a.txt","content":"v1\n"}`)},
		{Seq: 3, Type: model.EventToolResult, ToolUseID: "w1", Result: "ok"},
	}
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("v1\n"), 0644)
	fs.CaptureTurnEnd(setEvents(turn1), 1)

	// Turn2：改 a.txt = v2 → 快照
	turn2 := append(turn1,
		model.TaskEvent{Seq: 4, Type: model.EventUser, Text: "turn2"},
		model.TaskEvent{Seq: 5, Type: model.EventToolUse, ToolName: "Edit", ToolUseID: "e1",
			Input: json.RawMessage(`{"file_path":"a.txt","new_string":"v2"}`)},
		model.TaskEvent{Seq: 6, Type: model.EventToolResult, ToolUseID: "e1", Result: "ok"},
	)
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("v2\n"), 0644)
	fs.CaptureTurnEnd(setEvents(turn2), 2)

	final, _ := store.Get(task.ID)
	return final
}

func doReq(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAPI_P2_ScreenshotBranches(t *testing.T) {
	_, store, r := setupP2Test(t)
	task := seedP2Task(t, store, core.NewFeedbackStore(zap.NewNop(), t.TempDir()))

	// visual 未接线：POST → 503
	w := doReq(r, "POST", "/api/tasks/"+task.ID+"/preview/screenshots", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no visual status=%d body=%s", w.Code, w.Body.String())
	}

	// visual 接线但 preview 未运行 → 409（设计 §3.1）
	srv2, store2, r2 := setupP2Test(t)
	task2 := seedP2Task(t, store2, core.NewFeedbackStore(zap.NewNop(), t.TempDir()))
	srv2.SetVisualCapture(core.NewVisualCaptureManager(zap.NewNop(), t.TempDir()))
	w = doReq(r2, "POST", "/api/tasks/"+task2.ID+"/preview/screenshots", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("no preview status=%d body=%s", w.Code, w.Body.String())
	}

	// 列表（未接线）→ 200 空数组
	w = doReq(r, "GET", "/api/tasks/"+task.ID+"/preview/screenshots", nil)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"screenshots":[]`)) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}

	// PNG 不存在 → 404（visual 已接线）
	w = doReq(r2, "GET", "/api/tasks/"+task2.ID+"/preview/screenshots/deadbeefdeadbeef.png", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("png status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPI_P2_ConsoleNetworkDegraded(t *testing.T) {
	_, store, r := setupP2Test(t)
	task := seedP2Task(t, store, core.NewFeedbackStore(zap.NewNop(), t.TempDir()))

	// 未接线 → 200 零值摘要（前端渲染「无错误」）
	for _, path := range []string{"console", "network"} {
		w := doReq(r, "GET", "/api/tasks/"+task.ID+"/preview/"+path+"?since=2026-09-04T00:00:00Z", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}

	// 方法不匹配 → 405
	w := doReq(r, "POST", "/api/tasks/"+task.ID+"/preview/console", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("console POST status=%d", w.Code)
	}
}

func TestAPI_P2_PushOutcomeAndEvidence(t *testing.T) {
	srv, store, r := setupP2Test(t)
	task := seedP2Task(t, store, core.NewFeedbackStore(zap.NewNop(), t.TempDir()))

	// 先接线注册表（无 provider）：task 无来源渠道 → 409
	srv.SetPushRegistry(core.NewPushRegistry(zap.NewNop(), store))
	w := doReq(r, "POST", "/api/tasks/"+task.ID+"/push", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("no origin status=%d body=%s", w.Code, w.Body.String())
	}

	// 补 OriginChannel + 注册 fake provider
	store.Update(task.ID, func(t *model.Task) bool {
		t.OriginChannel, t.OriginChatID = "fake", "chat-1"
		return true
	})
	provider := &fakePushProvider{}
	registry := core.NewPushRegistry(zap.NewNop(), store)
	registry.Register(provider)
	srv.SetPushRegistry(registry)

	// kind 缺省 = outcome
	w = doReq(r, "POST", "/api/tasks/"+task.ID+"/push", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("push outcome status=%d body=%s", w.Code, w.Body.String())
	}
	got := provider.last()
	if got.Kind != "outcome" || got.Outcome == nil || got.Text == "" {
		t.Fatalf("outcome content: %+v", got)
	}

	// kind=evidence：携带变更摘要与检查
	w = doReq(r, "POST", "/api/tasks/"+task.ID+"/push",
		map[string]string{"kind": "evidence", "instruction": "请检查"})
	if w.Code != http.StatusOK {
		t.Fatalf("push evidence status=%d body=%s", w.Code, w.Body.String())
	}
	got = provider.last()
	if got.Kind != "evidence" || got.Evidence == nil {
		t.Fatalf("evidence content: %+v", got)
	}
	if !bytes.Contains([]byte(got.Text), []byte("请检查")) {
		t.Fatalf("evidence text missing instruction: %q", got.Text)
	}

	// 未知 kind → 400
	w = doReq(r, "POST", "/api/tasks/"+task.ID+"/push", map[string]string{"kind": "bogus"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bogus kind status=%d", w.Code)
	}
}

func TestAPI_P2_RewindFileScope(t *testing.T) {
	srv, store, r := setupP2Test(t)
	// Server 的 feedback 与 seed 必须同一实例（快照读写一致）
	fs := core.NewFeedbackStore(zap.NewNop(), t.TempDir())
	srv.SetFeedback(fs, nil)
	task := seedP2Task(t, store, fs)

	// 缺 path → 400
	w := doReq(r, "POST", "/api/tasks/"+task.ID+"/rewind",
		map[string]any{"to_turn": 2, "scope": "file"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("no path status=%d body=%s", w.Code, w.Body.String())
	}

	// 单文件回退：a.txt 回到 Turn1 态（v1）
	w = doReq(r, "POST", "/api/tasks/"+task.ID+"/rewind",
		map[string]any{"to_turn": 2, "scope": "file", "path": "a.txt"})
	if w.Code != http.StatusOK {
		t.Fatalf("file rewind status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Restored []string `json:"restored"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Restored) != 1 || resp.Restored[0] != "a.txt" {
		t.Fatalf("restored: %+v", resp.Restored)
	}
	if b, _ := os.ReadFile(filepath.Join(task.WorktreePath, "a.txt")); string(b) != "v1\n" {
		t.Fatalf("a.txt should be v1, got %q", b)
	}

	// rewind 事件入 Timeline
	updated, _ := store.Get(task.ID)
	found := false
	for _, ev := range updated.Events {
		if ev.Type == model.EventRewind {
			found = true
		}
	}
	if !found {
		t.Fatal("rewind event not appended")
	}
}
