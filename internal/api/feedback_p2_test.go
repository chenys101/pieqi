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

	// Turn2：改 a.txt = v2，同时新建 b.txt = b2 → 快照
	// b.txt 用于验证文件级回退「不影响其他文件」（p2-design.md §10-5）
	turn2 := append(turn1,
		model.TaskEvent{Seq: 4, Type: model.EventUser, Text: "turn2"},
		model.TaskEvent{Seq: 5, Type: model.EventToolUse, ToolName: "Edit", ToolUseID: "e1",
			Input: json.RawMessage(`{"file_path":"a.txt","new_string":"v2"}`)},
		model.TaskEvent{Seq: 6, Type: model.EventToolResult, ToolUseID: "e1", Result: "ok"},
		model.TaskEvent{Seq: 7, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "w2",
			Input: json.RawMessage(`{"file_path":"b.txt","content":"b2\n"}`)},
		model.TaskEvent{Seq: 8, Type: model.EventToolResult, ToolUseID: "w2", Result: "ok"},
	)
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("v2\n"), 0644)
	_ = os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b2\n"), 0644)
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

// TestAPI_P2_VisualSubpathsGone 视觉采集子路径**全部**返回 410 Gone。
//
// 背景：截图（ADR-0012）与 console/network（ADR-0013）先后移除，
// 四个 preview 子路径（capture / console / network / screenshots）都没了实现。
//
// 为什么必须钉住 410 而不是"删掉这些 case 让它 404"：
// `previewRoute` 是 catch-all，未命中的路径会走 **default 分支的反向代理**，
// 于是 `/preview/console` 会被当作**页面路径**代理给被预览的 dev server ——
// 调用方拿到一个莫名其妙的 HTML/404，**分不清"功能没了"还是"服务坏了"**。
// 410 Gone 语义正确（曾经存在、现已移除）且带可操作 hint。
func TestAPI_P2_VisualSubpathsGone(t *testing.T) {
	_, store, r := setupP2Test(t)
	task := seedP2Task(t, store, core.NewFeedbackStore(zap.NewNop(), t.TempDir()))

	for _, tc := range []struct{ method, path string }{
		{"POST", "/preview/capture"},
		{"GET", "/preview/console"},
		{"GET", "/preview/network"},
		{"POST", "/preview/screenshots"},
		{"GET", "/preview/screenshots"},
		{"GET", "/preview/screenshots/deadbeefdeadbeef.png"},
	} {
		w := doReq(r, tc.method, "/api/tasks/"+task.ID+tc.path, nil)
		if w.Code != http.StatusGone {
			t.Fatalf("%s %s: status=%d want 410, body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("removed")) {
			t.Fatalf("%s %s: 410 应说明功能已移除, body=%s", tc.method, tc.path, w.Body.String())
		}
	}
}

// TestAPI_P2_ConsoleNetworkDegraded 原断言"未接线 → 200 零值摘要""方法不匹配 → 405"。
//
// 这两个端点已随视觉采集层整体移除（ADR-0013），行为变成 **410 Gone**
// （无论方法、无论是否带 ?since=）。410 语义比 405 更准确：功能不再存在，
// 而不是"这个方法不允许"。此用例现已并入 TestAPI_P2_VisualSubpathsGone，
// 但**保留一条带 query 的用例**——411/410 分支不应因 query 参数而改变行为。
func TestAPI_P2_VisualGoneWithQuery(t *testing.T) {
	_, store, r := setupP2Test(t)
	task := seedP2Task(t, store, core.NewFeedbackStore(zap.NewNop(), t.TempDir()))

	for _, path := range []string{"console", "network"} {
		w := doReq(r, "GET", "/api/tasks/"+task.ID+"/preview/"+path+"?since=2026-09-04T00:00:00Z", nil)
		if w.Code != http.StatusGone {
			t.Fatalf("%s（带 since）status=%d want 410, body=%s", path, w.Code, w.Body.String())
		}
	}
	// 方法不匹配也不再是 405：功能没了就是 410。
	if w := doReq(r, "POST", "/api/tasks/"+task.ID+"/preview/console", nil); w.Code != http.StatusGone {
		t.Fatalf("console POST status=%d want 410", w.Code)
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
	// 不影响其他文件：b.txt 保持 Turn2 态（b2），不随 a.txt 回退
	if b, _ := os.ReadFile(filepath.Join(task.WorktreePath, "b.txt")); string(b) != "b2\n" {
		t.Fatalf("b.txt should stay b2 (untouched), got %q", b)
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
