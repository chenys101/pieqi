// feedback_test.go Feedback API 集成测试：路由接线 + 总览/Diff/Rewind 全链路。
package api

import (
	"bytes"
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

// setupFeedbackTest 建 Server + 接线 FeedbackStore（preview 不接，验证 nil-safe）。
func setupFeedbackTest(t *testing.T) (*Server, *core.TaskStore, *gin.Engine) {
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

// seedFeedbackTask 建一个已完成任务：Turn 1 里 Write 了 a.txt，磁盘上文件存在。
func seedFeedbackTask(t *testing.T, store *core.TaskStore) *model.Task {
	t.Helper()
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(&model.Task{
		ProjectID: "fb", ProjectPath: wt, WorktreePath: wt,
		Prompt: "write file", Status: model.TaskCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 事件流：EventUser（Turn 1 起点）+ Write tool_use + 成功 tool_result
	store.Update(task.ID, func(t *model.Task) bool {
		t.Events = []model.TaskEvent{
			{Seq: 1, Type: model.EventUser, Text: "write file"},
			{Seq: 2, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "tu1",
				Input: json.RawMessage(`{"file_path":"a.txt","content":"hello"}`)},
			{Seq: 3, Type: model.EventToolResult, ToolUseID: "tu1", Result: "ok"},
		}
		return true
	})
	return task
}

func TestAPI_GetFeedbackBundle(t *testing.T) {
	_, store, r := setupFeedbackTest(t)
	task := seedFeedbackTask(t, store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/feedback", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var bundle core.FeedbackBundle
	json.Unmarshal(w.Body.Bytes(), &bundle)
	if bundle.TaskID != task.ID || len(bundle.Turns) != 1 {
		t.Fatalf("bundle: %+v", bundle)
	}
	turn := bundle.Turns[0]
	if turn.Turn != 1 || len(turn.Changes) != 1 {
		t.Fatalf("turn: %+v", turn)
	}
	// 无 git 基线：首见文件判为 create；当前文件 1 行全增
	fc := turn.Changes[0]
	if fc.Path != "a.txt" || fc.Operation != "create" {
		t.Fatalf("change: %+v", fc)
	}
	if fc.Additions != 1 {
		t.Fatalf("additions=%d", fc.Additions)
	}
	if bundle.Cumulative.Files != 1 {
		t.Fatalf("cumulative: %+v", bundle.Cumulative)
	}
}

// 同一文件被多轮改动时，累计统计只能算一次。
// 派生结果是「每轮一条 FileChange」，曾经直接按条目累加 → 「1 个文件改 2 轮」
// 变成 2 个文件、同一份 diff 再加一遍（实测线上：真值 +1 -3 / 1 文件，
// 接口返回 +2 -6 / 2 文件）。
func TestAPI_CumulativeCountsRepeatedPathOnce(t *testing.T) {
	_, store, r := setupFeedbackTest(t)

	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(&model.Task{
		ProjectID: "fb", ProjectPath: wt, WorktreePath: wt,
		Prompt: "两轮改同一个文件", Status: model.TaskCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Update(task.ID, func(tk *model.Task) bool {
		tk.Events = []model.TaskEvent{
			{Seq: 1, Type: model.EventUser, Text: "第一轮"},
			{Seq: 2, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "tu1",
				Input: json.RawMessage(`{"file_path":"a.txt","content":"hello"}`)},
			{Seq: 3, Type: model.EventToolResult, ToolUseID: "tu1", Result: "ok"},
			{Seq: 4, Type: model.EventUser, Text: "第二轮"},
			{Seq: 5, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "tu2",
				Input: json.RawMessage(`{"file_path":"a.txt","content":"hello"}`)},
			{Seq: 6, Type: model.EventToolResult, ToolUseID: "tu2", Result: "ok"},
		}
		return true
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/feedback", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var bundle core.FeedbackBundle
	json.Unmarshal(w.Body.Bytes(), &bundle)

	if len(bundle.Turns) != 2 {
		t.Fatalf("前置：应有 2 个 Turn, got %d", len(bundle.Turns))
	}
	if len(bundle.Turns[0].Changes) != 1 || len(bundle.Turns[1].Changes) != 1 {
		t.Fatalf("前置：每轮各一条变更, got %+v | %+v",
			bundle.Turns[0].Changes, bundle.Turns[1].Changes)
	}

	cum := bundle.Cumulative
	if cum.Files != 1 {
		t.Errorf("files = %d, want 1（不按改动轮次重复计）", cum.Files)
	}
	if cum.Additions != 1 {
		t.Errorf("additions = %d, want 1（同一份 diff 只加一次）", cum.Additions)
	}
	if len(cum.Entries) != 1 || cum.Entries[0].Path != "a.txt" {
		t.Fatalf("entries = %+v", cum.Entries)
	}
	// 明细与合计同源：列表里看到的数字必须等于表头合计
	if cum.Entries[0].Additions != cum.Additions || cum.Entries[0].Deletions != cum.Deletions {
		t.Errorf("明细与合计不同源: entry=%+v sum=%+v", cum.Entries[0], cum.ChangeSummary)
	}
}

func TestAPI_GetFeedbackDiff(t *testing.T) {
	_, store, r := setupFeedbackTest(t)
	task := seedFeedbackTask(t, store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/feedback/diff?path=a.txt&turn=1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Path      string `json:"path"`
		Operation string `json:"operation"`
		Diff      string `json:"diff"`
		Additions int    `json:"additions"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Operation != "create" || resp.Additions != 1 {
		t.Fatalf("diff resp: %+v", resp)
	}
	if !bytes.Contains([]byte(resp.Diff), []byte("+hello")) {
		t.Fatalf("diff content: %q", resp.Diff)
	}

	// 缺 path → 400
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/feedback/diff", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing path status=%d", w.Code)
	}
}

func TestAPI_Rewind(t *testing.T) {
	_, store, r := setupFeedbackTest(t)
	task := seedFeedbackTask(t, store)

	// running → 409（静止边界）
	store.Update(task.ID, func(t *model.Task) bool { t.Status = model.TaskRunning; return true })
	body, _ := json.Marshal(map[string]any{"to_turn": 1})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/rewind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("running rewind status=%d", w.Code)
	}

	// completed → 200：a.txt 回到 Turn 1 之前（不存在）→ 被删除，事件留痕
	store.Update(task.ID, func(t *model.Task) bool { t.Status = model.TaskCompleted; return true })
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/tasks/"+task.ID+"/rewind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rewind status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["ok"] != true {
		t.Fatalf("rewind resp: %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(task.WorktreePath, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a.txt should be removed after rewind")
	}
	// 时间线留痕：task.Events 末尾出现 rewind 事件
	got, _ := store.Get(task.ID)
	last := got.Events[len(got.Events)-1]
	if last.Type != model.EventRewind {
		t.Fatalf("last event: %+v", last)
	}
}

func TestAPI_PreviewRoute(t *testing.T) {
	_, store, r := setupFeedbackTest(t)
	task := seedFeedbackTask(t, store)

	// preview 未接线 manager → start 返回 503；status 返回 unavailable（不 panic）
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/preview/start", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("preview start status=%d", w.Code)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/preview/status", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", w.Code, w.Body.String())
	}
	var st core.PreviewStatus
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.State != core.PreviewUnavailable {
		t.Fatalf("preview state: %+v", st)
	}

	// 控制端点方法不匹配 → 405（不误代理给 dev server）
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/preview/start", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("preview wrong method status=%d", w.Code)
	}
}
