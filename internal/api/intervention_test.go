package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"pieqi/internal/model"
)

// R6 / T8：Intervention 落盘可查 + /outcome 暴露非 nil 切片（AC-R6-01，S4 §8 R2）。

func createInterventionTestTask(t *testing.T, r http.Handler) string {
	t.Helper()
	repoDir := t.TempDir()
	body, _ := json.Marshal(createTaskReq{ProjectPath: repoDir, Prompt: "fix bug"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Task
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func TestAPI_InterventionRecorded(t *testing.T) {
	srv, _, r := setupAPITest(t)
	id := createInterventionTestTask(t, r)

	// 一个任务被干预 3 次（approve / deny / 续问）→ 落盘 3 条，task_id 正确
	srv.runner.RecordIntervention(id, model.Intervention{Kind: "decision", DecisionID: "d1", Choice: "approve"})
	srv.runner.RecordIntervention(id, model.Intervention{Kind: "decision", DecisionID: "d2", Choice: "deny"})
	srv.runner.RecordIntervention(id, model.Intervention{Kind: "append_prompt", Text: "继续"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/tasks/%s", id), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status=%d", w.Code)
	}
	var task model.Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if len(task.Interventions) != 3 {
		t.Fatalf("期望 3 条干预记录，实得 %d", len(task.Interventions))
	}
	for i, iv := range task.Interventions {
		if iv.TaskID != id {
			t.Fatalf("第 %d 条 task_id=%s，期望 %s", i, iv.TaskID, id)
		}
		if iv.ID == "" || iv.CreatedAt.IsZero() {
			t.Fatalf("第 %d 条缺 ID/CreatedAt（RecordIntervention 应补齐默认值）", i)
		}
	}
	if task.Interventions[0].Choice != "approve" || task.Interventions[1].Choice != "deny" {
		t.Fatalf("顺序/内容不符: %+v", task.Interventions)
	}
}

func TestAPI_OutcomeInterventionsNeverNull(t *testing.T) {
	_, _, r := setupAPITest(t)
	id := createInterventionTestTask(t, r)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/tasks/%s/outcome", id), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("outcome status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// nil 切片会序列化成 null，消费方按数组声明 —— "恰好没有干预"时白屏（仓库判据）
	iv, ok := body["interventions"].([]any)
	if !ok {
		t.Fatalf("interventions 应为非 nil 数组，实得 %v", body["interventions"])
	}
	if len(iv) != 0 {
		t.Fatalf("新任务应为 0 条，实得 %d", len(iv))
	}
}
