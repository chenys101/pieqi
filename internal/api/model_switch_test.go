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

// 会话内切换模型（model_switch.go）：落库 + 时间线留痕。

// createSwitchableTask 建一个**终态**任务（可切换模型）。
//
// createInterventionTestTask 建出来的任务是 pending/running（createTask 真的起了轮次），
// 而运行中按设计就该拒绝切换 —— 所以这里先把它推到 completed。
func createSwitchableTask(t *testing.T, store interface {
	Update(string, func(*model.Task) bool) (*model.Task, error)
}, r http.Handler) string {
	t.Helper()
	id := createInterventionTestTask(t, r)
	if _, err := store.Update(id, func(tk *model.Task) bool {
		tk.Status = model.TaskCompleted
		return true
	}); err != nil {
		t.Fatalf("把任务置为终态失败: %v", err)
	}
	return id
}

// 切到指定模型 → Task.Model 更新 + 追加一条 model_switch 事件（载荷 from/to 可解析）。
func TestAPI_SetTaskModel_PersistsAndAppendsEvent(t *testing.T) {
	srv, store, r := setupAPITest(t)
	id := createSwitchableTask(t, store, r)

	body, _ := json.Marshal(setTaskModelReq{Model: `["magpie","workbuddy/glm-5.3-flash"]`})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", fmt.Sprintf("/api/tasks/%s/model", id), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set model status=%d body=%s", w.Code, w.Body.String())
	}

	task, ok := store.Get(id)
	if !ok {
		t.Fatal("task not found after switch")
	}
	if task.Model != `["magpie","workbuddy/glm-5.3-flash"]` {
		t.Fatalf("Task.Model=%q，期望原样存下不透明值", task.Model)
	}

	// 时间线上必须有一条 model_switch，且 from 为空（此前用 agent 默认）
	var found *model.TaskEvent
	for i := range task.Events {
		if task.Events[i].Type == model.EventModelSwitch {
			found = &task.Events[i]
		}
	}
	if found == nil {
		t.Fatalf("期望一条 model_switch 事件，实得事件类型：%v", eventTypes(task.Events))
	}
	var payload model.ModelSwitchPayload
	if err := json.Unmarshal(found.Input, &payload); err != nil {
		t.Fatalf("model_switch input 不是合法 JSON: %v", err)
	}
	if payload.From != "" {
		t.Fatalf("from=%q，期望空（此前用 agent 默认路由）", payload.From)
	}
	if payload.To != `["magpie","workbuddy/glm-5.3-flash"]` {
		t.Fatalf("to=%q", payload.To)
	}
	if found.Text == "" {
		t.Fatal("model_switch 事件缺人读摘要（Text）")
	}
	_ = srv
}

// 切回空串 = 交还 agent 默认；连续两次切同一个值只写**一条**事件。
func TestAPI_SetTaskModel_NoopDoesNotAppendEvent(t *testing.T) {
	_, store, r := setupAPITest(t)
	id := createSwitchableTask(t, store, r)

	post := func(model string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(setTaskModelReq{Model: model})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/tasks/%s/model", id), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}

	// 第一次：切过去
	if w := post("m1"); w.Code != http.StatusOK {
		t.Fatalf("first switch status=%d body=%s", w.Code, w.Body.String())
	}
	// 第二次：切到同一个值 → changed=false，且**不再**追加事件
	w := post("m1")
	if w.Code != http.StatusOK {
		t.Fatalf("noop switch status=%d", w.Code)
	}
	var resp struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Changed {
		t.Fatal("切到同一个值不该报 changed=true")
	}

	task, _ := store.Get(id)
	if n := countEventType(task.Events, model.EventModelSwitch); n != 1 {
		t.Fatalf("model_switch 事件数=%d，期望 1（时间线只记真实发生过的切换）", n)
	}

	// 切回空串 = Agent 默认，且 from 记的是上一个值
	if w := post(""); w.Code != http.StatusOK {
		t.Fatalf("switch to default status=%d body=%s", w.Code, w.Body.String())
	}
	task, _ = store.Get(id)
	if task.Model != "" {
		t.Fatalf("切回默认后 Task.Model=%q，期望空", task.Model)
	}
	if n := countEventType(task.Events, model.EventModelSwitch); n != 2 {
		t.Fatalf("model_switch 事件数=%d，期望 2", n)
	}
	last := task.Events[len(task.Events)-1]
	var payload model.ModelSwitchPayload
	if err := json.Unmarshal(last.Input, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.From != "m1" || payload.To != "" {
		t.Fatalf("切回默认的 from/to=%q/%q，期望 m1/空", payload.From, payload.To)
	}
}

// 运行中拒绝切换：这一轮的 setConfig 早已发生，改了也不会生效，
// 与其让界面显示"已切换"而实际没换，不如明确报错。
func TestAPI_SetTaskModel_ConflictWhileRunning(t *testing.T) {
	_, store, r := setupAPITest(t)
	id := createInterventionTestTask(t, r)

	if _, err := store.Update(id, func(tk *model.Task) bool {
		tk.Status = model.TaskRunning
		return true
	}); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(setTaskModelReq{Model: "m1"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", fmt.Sprintf("/api/tasks/%s/model", id), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("运行中切换期望 409，实得 %d body=%s", w.Code, w.Body.String())
	}
	task, _ := store.Get(id)
	if task.Model != "" {
		t.Fatalf("被拒的切换不该改 Task.Model，实得 %q", task.Model)
	}
}

func countEventType(events []model.TaskEvent, typ model.TaskEventType) int {
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func eventTypes(events []model.TaskEvent) []model.TaskEventType {
	out := make([]model.TaskEventType, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}