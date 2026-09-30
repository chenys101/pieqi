// agents_test.go：「新任务页选 agent」的 HTTP 面契约。
//
// 覆盖三件事：目录接口能列可选项与默认值；创建任务时 agent 落库；非法 agent 拒绝而非静默换人。
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

func TestAPI_ListAgents(t *testing.T) {
	srv, _, r := setupAPITest(t)

	t.Run("未接线：退化为只有 claude", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/agents", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Agents  []agent.AgentInfo `json:"agents"`
			Default string            `json:"default"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Agents) != 1 || resp.Agents[0].Name != agent.AgentClaude {
			t.Fatalf("agents=%+v, want 仅 claude", resp.Agents)
		}
		if resp.Default != agent.AgentClaude {
			t.Fatalf("default=%q, want %q", resp.Default, agent.AgentClaude)
		}
	})

	t.Run("接线后：下发目录与默认 agent", func(t *testing.T) {
		srv.SetAgents([]agent.AgentInfo{
			{Name: agent.AgentClaude, DisplayName: "Claude Code", Transport: "SDK Bridge"},
			{Name: agent.AgentQoder, DisplayName: "Qoder CLI", Transport: "ACP"},
		}, agent.AgentClaude)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/agents", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d", w.Code)
		}
		var resp struct {
			Agents []agent.AgentInfo `json:"agents"`
			Default string            `json:"default"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Agents) != 2 || resp.Agents[1].Name != agent.AgentQoder {
			t.Fatalf("agents=%+v, want claude+qoder", resp.Agents)
		}
		if resp.Default != agent.AgentClaude {
			t.Fatalf("default=%q, want claude（需求：默认 Claude Code）", resp.Default)
		}
	})
}

// TestAPI_CreateTaskAgent 创建任务时的 agent 落库与校验。
func TestAPI_CreateTaskAgent(t *testing.T) {
	srv, _, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{
		{Name: agent.AgentClaude, DisplayName: "Claude Code"},
		{Name: agent.AgentQoder, DisplayName: "Qoder CLI"},
	}, agent.AgentClaude)

	repoDir := t.TempDir()
	create := func(t *testing.T, body createTaskReq) (*httptest.ResponseRecorder, model.Task) {
		t.Helper()
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/tasks", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		var task model.Task
		_ = json.Unmarshal(w.Body.Bytes(), &task)
		return w, task
	}

	t.Run("不传 agent → 默认 claude（默认使用 Claude Code）", func(t *testing.T) {
		w, task := create(t, createTaskReq{ProjectPath: repoDir, Prompt: "p"})
		if w.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if task.Agent != agent.AgentClaude {
			t.Fatalf("agent=%q, want %q", task.Agent, agent.AgentClaude)
		}
	})

	t.Run("显式选 qoder → 落库 qoder", func(t *testing.T) {
		w, task := create(t, createTaskReq{ProjectPath: repoDir, Prompt: "p", Agent: agent.AgentQoder})
		if w.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if task.Agent != agent.AgentQoder {
			t.Fatalf("agent=%q, want %q", task.Agent, agent.AgentQoder)
		}
	})

	t.Run("未知 agent → 400，且不创建任务", func(t *testing.T) {
		w, _ := create(t, createTaskReq{ProjectPath: repoDir, Prompt: "p", Agent: "gpt-99"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400（未知 agent 必须报错，不能静默换成别的 agent）", w.Code)
		}
	})

	t.Run("已配置但未列入目录的 agent 也拒绝", func(t *testing.T) {
		// claude 走 print 的实例里 qoder 不在目录中：选了必须报错，而不是偷偷用 claude 跑。
		srv.SetAgents([]agent.AgentInfo{{Name: agent.AgentClaude, DisplayName: "Claude Code"}}, agent.AgentClaude)
		defer srv.SetAgents([]agent.AgentInfo{
			{Name: agent.AgentClaude}, {Name: agent.AgentQoder},
		}, agent.AgentClaude)

		w, _ := create(t, createTaskReq{ProjectPath: repoDir, Prompt: "p", Agent: agent.AgentQoder})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", w.Code)
		}
	})
}
