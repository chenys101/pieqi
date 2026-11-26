// capabilities_test.go：会话页加图入口的能力回落。
//
// 终态任务会话常被空闲回收，SupportsImagePrompt 会因"无活 adapter"变 false。
// 若 capabilities 原样转发，前端会把加图按钮藏掉 —— 即使用户 agent（如 cursor）
// 目录里明确 image_prompt=true、续问也会重新 Open。本文件钉住回落口径。
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

func TestAPI_TaskCapabilities_FallsBackToCatalogWhenNoLiveSession(t *testing.T) {
	srv, store, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{
		{Name: agent.AgentCursor, DisplayName: "Cursor", ImagePrompt: true},
		{Name: agent.AgentClaude, DisplayName: "Claude", ImagePrompt: false},
	}, agent.AgentCursor)

	task, err := store.Create(&model.Task{
		Source: model.SourceHTTP, Agent: agent.AgentCursor,
		ProjectID: "p", ProjectPath: t.TempDir(), Prompt: "已完成",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Update(task.ID, func(x *model.Task) bool {
		x.Status = model.TaskCompleted
		return true
	}); err != nil {
		t.Fatalf("set completed: %v", err)
	}

	// setupAPITest 的 runner 无 ACP 会话 → SupportsImagePrompt=false、无活 adapter。
	// 目录里 cursor.ImagePrompt=true → 应回落为 true。
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/capabilities", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		ImagePrompt bool `json:"image_prompt"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !got.ImagePrompt {
		t.Fatal("image_prompt=false：终态无活会话应回落 cursor 目录的 ImagePrompt=true")
	}
}

func TestAPI_TaskCapabilities_CatalogFalseStaysFalse(t *testing.T) {
	srv, store, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{
		{Name: "print-only", DisplayName: "Print", ImagePrompt: false},
	}, "print-only")

	task, err := store.Create(&model.Task{
		Source: model.SourceHTTP, Agent: "print-only",
		ProjectID: "p", ProjectPath: t.TempDir(), Prompt: "x",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/capabilities", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		ImagePrompt bool `json:"image_prompt"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.ImagePrompt {
		t.Fatal("目录 ImagePrompt=false 时不应乐观成 true")
	}
}
