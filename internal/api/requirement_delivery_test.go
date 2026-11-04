package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pieqi/internal/core"
	"pieqi/internal/model"
)

// gitProjectPath 给 Confirm 路径用的已 init 仓库（Confirm 会 EnsureIntegrationBranch）。
// 目录落在仓库 build/：沙箱写不了系统 Temp，且隔离父仓 .git。
func gitProjectPath(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("cwd: %v", err)
	}
	base := filepath.Clean(filepath.Join(wd, "..", "..", "build"))
	dir, err := os.MkdirTemp(base, "pieqi-apireq-*")
	if err != nil {
		t.Skipf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("-c", "user.email=t@t.com", "-c", "user.name=t", "commit", "-m", "init")
	return repo
}

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
	proj := gitProjectPath(t)

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
		Requirement *model.Requirement `json:"requirement"`
		Tasks       []*model.Task      `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conf); err != nil {
		t.Fatal(err)
	}
	if conf.Requirement == nil || conf.Requirement.IntegrationBranch == "" {
		t.Fatalf("confirm should set integration_branch: %+v", conf.Requirement)
	}
	if !strings.HasPrefix(conf.Requirement.IntegrationBranch, "req/") {
		t.Fatalf("branch=%q", conf.Requirement.IntegrationBranch)
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
	proj := gitProjectPath(t)
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
	proj := gitProjectPath(t)
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
