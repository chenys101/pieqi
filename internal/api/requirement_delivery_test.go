package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pieqi/internal/core"
	"pieqi/internal/model"
)

func setupReqAPI(t *testing.T) (*Server, *core.RequirementStore, *core.TaskStore, http.Handler) {
	t.Helper()
	srv, store, r := setupAPITest(t)
	rs := core.NewRequirementStore(store.Backend())
	rs.SetTaskStore(store)
	srv.SetRequirementStore(rs)
	srv.runner.SetRequirementStore(rs)
	t.Cleanup(func() { _ = store.Close() })
	return srv, rs, store, r
}

func TestAPI_ConfirmInjectsDoD_AndTestNeedsDepends(t *testing.T) {
	_, _, _, r := setupReqAPI(t)
	proj := t.TempDir()

	body, _ := json.Marshal(map[string]any{
		"title":        "delivery",
		"project_path": proj,
		"acceptance":   []map[string]any{{"id": "ac1", "text": "ok", "verifiable": true}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/requirements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create req status=%d body=%s", w.Code, w.Body.String())
	}
	var created model.Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// test 无 depends_on → 400
	bad, _ := json.Marshal(map[string]any{
		"nodes": []map[string]any{
			{"temp_id": "be", "prompt": "impl", "role": "backend", "covers_ac": []string{"ac1"}},
			{"temp_id": "te", "prompt": "test", "role": "test", "covers_ac": []string{"ac1"}},
		},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/requirements/"+created.ID+"/confirm", bytes.NewReader(bad))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("expected reject test without depends_on, got 200: %s", w.Body.String())
	}

	good, _ := json.Marshal(map[string]any{
		"nodes": []map[string]any{
			{"temp_id": "be", "prompt": "impl", "role": "backend", "covers_ac": []string{"ac1"}},
			{"temp_id": "te", "prompt": "test", "role": "test", "covers_ac": []string{"ac1"}, "depends_on": []string{"be"}},
		},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/requirements/"+created.ID+"/confirm", bytes.NewReader(good))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", w.Code, w.Body.String())
	}
	var conf struct {
		Tasks []*model.Task `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conf); err != nil {
		t.Fatal(err)
	}
	var sawBE, sawTE bool
	for _, tk := range conf.Tasks {
		if tk.Role == model.RoleBackend {
			sawBE = true
			if !strings.Contains(tk.Prompt, "交付纪律") {
				t.Fatalf("backend prompt missing DoD: %s", tk.Prompt)
			}
		}
		if tk.Role == model.RoleTest {
			sawTE = true
			if !strings.Contains(tk.Prompt, "隔离") {
				t.Fatalf("test prompt missing isolation DoD: %s", tk.Prompt)
			}
		}
	}
	if !sawBE || !sawTE {
		t.Fatalf("tasks missing roles be=%v te=%v n=%d", sawBE, sawTE, len(conf.Tasks))
	}
}

func TestAPI_ConfirmReplace_NeedsForce(t *testing.T) {
	_, rs, store, r := setupReqAPI(t)
	proj := t.TempDir()
	reqObj, err := rs.Create(&model.Requirement{
		Title: "r", ProjectPath: proj,
		Acceptance: []model.AcceptanceItem{{ID: "ac1", Text: "x", Verifiable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := rs.Confirm(reqObj.ID, []model.ProposedTaskNode{
		{TempID: "a", Prompt: "old", Role: model.RoleBackend, CoversAC: []string{"ac1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(first.Tasks[0].ID, func(tk *model.Task) bool {
		tk.Status = model.TaskCompleted
		return true
	}); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{
		"replace": true,
		"nodes": []map[string]any{
			{"temp_id": "n", "prompt": "new", "role": "backend", "covers_ac": []string{"ac1"}},
		},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/requirements/"+reqObj.ID+"/confirm", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 without force, got %d %s", w.Code, w.Body.String())
	}

	payloadForce, _ := json.Marshal(map[string]any{
		"replace": true,
		"force":   true,
		"nodes": []map[string]any{
			{"temp_id": "n", "prompt": "new", "role": "backend", "covers_ac": []string{"ac1"}},
		},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/requirements/"+reqObj.ID+"/confirm", bytes.NewReader(payloadForce))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("force confirm status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestAPI_DeliveryFlow_SoftReplaceAndBlockerIssue 进程内完整链路：
// planned 可无 force 覆盖 → DoD 仍在 → blocker Issue 自动建 Fix。
func TestAPI_DeliveryFlow_SoftReplaceAndBlockerIssue(t *testing.T) {
	_, _, _, r := setupReqAPI(t)
	proj := t.TempDir()
	body, _ := json.Marshal(map[string]any{
		"title": "flow", "project_path": proj,
		"acceptance": []map[string]any{{"id": "ac1", "text": "ok", "verifiable": true}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/requirements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	var created model.Requirement
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	confirm, _ := json.Marshal(map[string]any{
		"nodes": []map[string]any{
			{"temp_id": "be", "prompt": "impl", "role": "backend", "covers_ac": []string{"ac1"}},
		},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/requirements/"+created.ID+"/confirm", bytes.NewReader(confirm))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm=%d %s", w.Code, w.Body.String())
	}

	// 仍全是 planned → replace 无需 force
	soft, _ := json.Marshal(map[string]any{
		"replace": true,
		"nodes": []map[string]any{
			{"temp_id": "n", "prompt": "new", "role": "backend", "covers_ac": []string{"ac1"}},
		},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/requirements/"+created.ID+"/confirm", bytes.NewReader(soft))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("soft replace=%d %s", w.Code, w.Body.String())
	}
	var softOut struct {
		Tasks []*model.Task `json:"tasks"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &softOut)
	if len(softOut.Tasks) != 1 || !strings.Contains(softOut.Tasks[0].Prompt, "vitest") {
		t.Fatalf("soft replace DoD missing: %+v", softOut.Tasks)
	}

	issBody, _ := json.Marshal(map[string]any{
		"title": "e2e blocker", "severity": "blocker", "covers_ac": []string{"ac1"},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/requirements/"+created.ID+"/issues", bytes.NewReader(issBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("open issue=%d %s", w.Code, w.Body.String())
	}
	var issOut struct {
		FixTask *model.Task `json:"fix_task"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &issOut)
	if issOut.FixTask == nil || issOut.FixTask.ID == "" {
		t.Fatalf("blocker should create fix_task: %s", w.Body.String())
	}
}
