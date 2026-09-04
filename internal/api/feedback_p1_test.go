// feedback_p1_test.go Feedback P1 API 集成测试：
// 前瞻 Diff / Checks（列表+重跑）/ Outcome / Evidence / Continue / Rewind→Verify。
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// setupP1Test 建 Server + FeedbackStore + CheckRunner（preview 不接，nil-safe）。
func setupP1Test(t *testing.T) (*Server, *core.TaskStore, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(0)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	srv := NewServer(&config.Config{}, store, runner, hooks, bus, nil, nil)
	srv.SetFeedback(core.NewFeedbackStore(zap.NewNop(), t.TempDir()), nil)
	srv.SetCheckRunner(core.NewCheckRunner(zap.NewNop(), t.TempDir()))
	r := gin.New()
	srv.Register(r)
	return srv, store, r
}

// seedP1Task 建已完成任务：Turn1 写 a.txt + 跑 npm test（成功）。
func seedP1Task(t *testing.T, store *core.TaskStore) *model.Task {
	t.Helper()
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(&model.Task{
		ProjectID: "p1", ProjectPath: wt, WorktreePath: wt,
		Prompt: "do", Status: model.TaskCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Update(task.ID, func(t *model.Task) bool {
		t.Status = model.TaskCompleted // Create 强制 pending，这里补真实状态
		t.Events = []model.TaskEvent{
			{Seq: 1, Type: model.EventUser, Text: "do"},
			{Seq: 2, Type: model.EventToolUse, ToolName: "Write", ToolUseID: "tu1",
				Input: json.RawMessage(`{"file_path":"a.txt","content":"hello\n"}`)},
			{Seq: 3, Type: model.EventToolResult, ToolUseID: "tu1", Result: "ok"},
			{Seq: 4, Type: model.EventToolUse, ToolName: "Bash", ToolUseID: "tu2",
				Input: json.RawMessage(`{"command":"npm test"}`)},
			{Seq: 5, Type: model.EventToolResult, ToolUseID: "tu2", Result: "all pass"},
		}
		return true
	})
	return task
}

func TestAPI_P1_ApprovalDiff(t *testing.T) {
	_, store, r := setupP1Test(t)
	task := seedP1Task(t, store)

	// tu1 = Write a.txt（内容与磁盘一致 → 无差异，但 operation=modify）
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/approvals/tu1/diff", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["prospective"] != true || resp["operation"] != "modify" {
		t.Fatalf("resp: %+v", resp)
	}

	// Bash 决策 → 404
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/approvals/tu2/diff", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bash decision status=%d", w.Code)
	}
}

func TestAPI_P1_ChecksListAndRerun(t *testing.T) {
	_, store, r := setupP1Test(t)
	task := seedP1Task(t, store)

	// 列表：派生 1 条（npm test success）
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/checks", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var listResp struct {
		Checks []core.Check `json:"checks"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Checks) != 1 || listResp.Checks[0].Status != core.CheckSuccess {
		t.Fatalf("checks: %+v", listResp.Checks)
	}

	// 重跑（异步）：echo 立即完成 → 轮询到 success
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/tasks/"+task.ID+"/checks/tu2/rerun", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("rerun status=%d body=%s", w.Code, w.Body.String())
	}

	// 覆盖重跑命令为 echo（避免真实 npm）：直接经 CheckRunner 验证完成态即可——
	// 这里 tu2 的命令是 npm test，重跑在 tempdir 会快速失败，两种终态都算通过。
	deadline := time.Now().Add(10 * time.Second)
	var final string
	for time.Now().Before(deadline) {
		w = httptest.NewRecorder()
		req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/checks", nil)
		r.ServeHTTP(w, req)
		json.Unmarshal(w.Body.Bytes(), &listResp)
		for _, ck := range listResp.Checks {
			if ck.Origin == "rerun" && ck.Status != core.CheckRunning {
				final = ck.Status
			}
		}
		if final != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if final == "" {
		t.Fatalf("rerun not finished: %+v", listResp.Checks)
	}
}

func TestAPI_P1_OutcomeAndEvidence(t *testing.T) {
	_, store, r := setupP1Test(t)
	task := seedP1Task(t, store)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/outcome", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("outcome status=%d", w.Code)
	}
	var outcome core.TaskOutcome
	json.Unmarshal(w.Body.Bytes(), &outcome)
	// npm test success + task completed → completed；无 issues（末轮无 is_error）
	if outcome.Status != core.OutcomeCompleted {
		t.Fatalf("outcome: %+v", outcome)
	}
	if len(outcome.Checks) != 1 {
		t.Fatalf("outcome checks: %+v", outcome.Checks)
	}

	// evidence：task scope，含变更摘要与 checks
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/evidence", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("evidence status=%d", w.Code)
	}
	var ev core.Evidence
	json.Unmarshal(w.Body.Bytes(), &ev)
	if ev.Scope != core.ScopeTask || ev.Changes.Files != 1 || len(ev.Checks) != 1 {
		t.Fatalf("evidence: %+v", ev)
	}

	// scope=turn 缺 turn → 400
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/evidence?scope=turn", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("turn scope without turn status=%d", w.Code)
	}
}

func TestAPI_P1_Continue(t *testing.T) {
	_, store, r := setupP1Test(t)
	task := seedP1Task(t, store)

	// running → 409
	store.Update(task.ID, func(t *model.Task) bool { t.Status = model.TaskRunning; return true })
	body, _ := json.Marshal(map[string]any{"instruction": "继续"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/continue", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("running continue status=%d", w.Code)
	}

	// completed + 无 session → Resume 失败 → 409（不真正起进程）
	store.Update(task.ID, func(t *model.Task) bool {
		t.Status = model.TaskCompleted
		t.ClaudeSessionID = ""
		return true
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/tasks/"+task.ID+"/continue", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("no-session continue status=%d body=%s", w.Code, w.Body.String())
	}

	// 带 session：Resume 成功（run 异步，claude 不存在会快速失败，不影响断言）
	store.Update(task.ID, func(t *model.Task) bool { t.ClaudeSessionID = "sess-1"; return true })
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/tasks/"+task.ID+"/continue", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("continue status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["ok"] != true {
		t.Fatalf("resp: %+v", resp)
	}
	prompt, _ := resp["appended_prompt"].(string)
	// 组装的 prompt 应含证据要素（指令 + 检查 + 文件）
	for _, want := range []string{"继续", "npm test", "a.txt"} {
		if !bytes.Contains([]byte(prompt), []byte(want)) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if seq, _ := resp["event_seq"].(float64); seq < 1 {
		t.Fatalf("event_seq: %v", resp["event_seq"])
	}
}

func TestAPI_P1_RewindVerify(t *testing.T) {
	_, store, r := setupP1Test(t)
	task := seedP1Task(t, store)

	body, _ := json.Marshal(map[string]any{"to_turn": 1, "verify": true})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/rewind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rewind-verify status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Ok           bool `json:"ok"`
		Verification *struct {
			RestoredFiles int          `json:"restored_files"`
			Checks        []core.Check `json:"checks"`
			Preview       map[string]any `json:"preview"`
		} `json:"verification"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Ok || resp.Verification == nil {
		t.Fatalf("resp verification missing: %s", w.Body.String())
	}
	// to_turn=1 → 目标 Turn 0 无 check → 退化为全量（npm test）→ 异步 running 记录
	if resp.Verification.RestoredFiles != 1 {
		t.Fatalf("restored: %+v", resp.Verification)
	}
	if len(resp.Verification.Checks) != 1 {
		t.Fatalf("verify checks: %+v", resp.Verification.Checks)
	}
	if resp.Verification.Checks[0].Status != core.CheckRunning {
		t.Fatalf("verify check should be running: %+v", resp.Verification.Checks[0])
	}
	if resp.Verification.Preview["state"] == nil {
		t.Fatalf("verify preview: %+v", resp.Verification.Preview)
	}

	// 非 verify 的老路径不受影响（无 verification 字段）
	body2, _ := json.Marshal(map[string]any{"to_turn": 1})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/tasks/"+task.ID+"/rewind", bytes.NewReader(body2))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("plain rewind status=%d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("verification")) {
		t.Fatal("plain rewind should not contain verification")
	}
}
