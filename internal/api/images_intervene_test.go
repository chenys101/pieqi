// images_intervene_test.go 续问（intervene）路径的图片口径。
//
// 可续（IsResumable）的 append_prompt —— 含 **running 排队** —— 走 ResumeRich，可带图。
// 仍不收图、且必须**明确拒绝**而不是静默丢弃的：
//   - waiting_input(approval)：进程仍活着等卡，只能 Intervene 批/拒；
//   - decision：回答审批卡，没有"这条消息"可言。
//
// 「静默丢弃」是最坏的一种失败：用户以为 agent 看了图，而它只会对着没图的上下文
// 说些不相干的话。所以这里断言的是**明确错误**，不是"忽略图片照常执行"。
package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/core"
	"pieqi/internal/model"
)

// runningTask 造一个**真的 running** 任务。
// ⚠️ 不能直接把 Status 塞进 store.Create 的结构体：Create 里写死了 t.Status = TaskPending
// （见 core/task_store.go），所以必须建完再用 Update 改状态 —— 这一点很容易踩，
// 表现是测试拿到 409 "task not interventionable: pending" 而不是被测的图片校验。
func runningTask(t *testing.T, store *core.TaskStore, agentName string) *model.Task {
	t.Helper()
	wt := t.TempDir()
	task, err := store.Create(&model.Task{
		Source: model.SourceHTTP, Agent: agentName, ProjectID: "p", ProjectPath: wt,
		WorktreePath: wt, Prompt: "跑着", ClaudeSessionID: "sess-running",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskRunning
		x.WorktreePath = wt
		x.ClaudeSessionID = "sess-running"
		return true
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}
	return task
}

// TestAPI_InterveneRunningRoutesImagesToResume 运行中带图 → 走 ResumeRich，不再 400「运行中不支持」。
//
// setupAPITest 的 runner 未挂 ACP，ResumeRich 会在写事件前以「不支持图片」409 拒绝 ——
// 这正好证明：请求已离开旧的 stdin/Intervene 拒图分支，进入了可排队带图的 Resume 路径。
func TestAPI_InterveneRunningRoutesImagesToResume(t *testing.T) {
	srv, store, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{
		{Name: agent.AgentClaude}, {Name: agent.AgentQoder},
	}, agent.AgentClaude)

	task := runningTask(t, store, agent.AgentQoder)

	valid := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	body, _ := json.Marshal(interveneReq{Kind: "append_prompt", Text: "补一张图", Images: []imageReq{{Data: valid, MimeType: "image/png"}}})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/intervene", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code == http.StatusBadRequest && strings.Contains(w.Body.String(), "运行中的任务不支持随消息发图") {
		t.Fatalf("仍走旧 stdin 拒图分支：status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409（非 ACP 的 ResumeRich 拒图）", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "不支持图片") {
		t.Errorf("body=%s 应说明本路径不支持图片（ResumeRich 前置校验）", w.Body.String())
	}
	// 拒绝不得留下干预记录 / 假的带图 user 事件
	got, _ := store.Get(task.ID)
	if got != nil && len(got.Interventions) != 0 {
		t.Errorf("interventions=%+v，被拒绝的请求不该留下记录", got.Interventions)
	}
}

// TestAPI_InterveneApprovalRejectsImages 审批等待中带图 → 仍明确 400。
func TestAPI_InterveneApprovalRejectsImages(t *testing.T) {
	srv, store, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{{Name: agent.AgentClaude}}, agent.AgentClaude)

	task, err := store.Create(&model.Task{
		Source: model.SourceHTTP, Agent: agent.AgentClaude, ProjectID: "p",
		ProjectPath: t.TempDir(), Prompt: "等卡",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskWaitingInput
		x.CurrentDecision = &model.Decision{
			ID: "d1", Kind: model.DecisionKindApproval, ToolName: "Bash",
		}
		return true
	}); err != nil {
		t.Fatalf("set waiting: %v", err)
	}

	valid := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	body, _ := json.Marshal(interveneReq{Kind: "append_prompt", Text: "看图", Images: []imageReq{{Data: valid, MimeType: "image/png"}}})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/intervene", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "等待审批时不支持随消息发图") {
		t.Errorf("body=%s 应说明审批等待不收图", w.Body.String())
	}
}

// TestAPI_InterveneRejectsInvalidImages 续问路径的非法图片同样在建副作用之前 400。
func TestAPI_InterveneRejectsInvalidImages(t *testing.T) {
	srv, store, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{{Name: agent.AgentClaude}}, agent.AgentClaude)

	task := runningTask(t, store, agent.AgentClaude)

	cases := []struct {
		name    string
		images  []imageReq
		wantSub string
	}{
		{"空 data", []imageReq{{Data: "", MimeType: "image/png"}}, "图片数据为空"},
		{"非法 base64", []imageReq{{Data: "!!", MimeType: "image/png"}}, "不是合法的 base64"},
		{"不支持的 MIME", []imageReq{{Data: "AAAA", MimeType: "image/tiff"}}, "不支持的图片类型"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(interveneReq{Kind: "append_prompt", Text: "x", Images: tc.images})
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/intervene", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantSub) {
				t.Errorf("body=%s 应包含 %q", w.Body.String(), tc.wantSub)
			}
		})
	}
}

// TestAPI_InterveneWithoutImagesUnaffected 不带图的续问不受影响（别把校验做成一刀切）。
func TestAPI_InterveneWithoutImagesUnaffected(t *testing.T) {
	srv, store, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{{Name: agent.AgentClaude}}, agent.AgentClaude)

	task := runningTask(t, store, agent.AgentClaude)

	body, _ := json.Marshal(interveneReq{Kind: "append_prompt", Text: "纯文本追加"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks/"+task.ID+"/intervene", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	// running 纯文本现在走 ResumeRich；缺活会话 / 非 ACP 可能 202 或 409，
	// 关键是不能是 400（400 说明校验误伤了正常请求）
	if w.Code == http.StatusBadRequest {
		t.Fatalf("status=400 body=%s：不带图的续问被误拦", w.Body.String())
	}
}
