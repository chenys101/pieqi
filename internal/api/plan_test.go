package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPI_PlanPublishAndMaterialize(t *testing.T) {
	_, _, _, r := setupReqAPI(t)
	proj := gitProjectPath(t)
	body, _ := json.Marshal(map[string]any{
		"title": "plan-eng", "project_path": proj,
		"acceptance": []map[string]any{{"id": "ac1", "text": "ok", "verifiable": true}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/requirements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	draft, _ := json.Marshal(map[string]any{
		"stage": "engineering",
		"nodes": []map[string]any{
			{"node_id": "n1", "title": "impl", "role": "backend", "covers_ac": []string{"ac1"}},
		},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", "/api/requirements/"+id+"/plan/draft", bytes.NewReader(draft))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("draft %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/requirements/"+id+"/plan/publish?materialize=1", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("publish %d %s", w.Code, w.Body.String())
	}
}

func TestAPI_PlanPublish_Blocked(t *testing.T) {
	_, _, _, r := setupReqAPI(t)
	proj := gitProjectPath(t)
	body, _ := json.Marshal(map[string]any{"title": "plan-block", "project_path": proj})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/requirements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	draft, _ := json.Marshal(map[string]any{
		"stage": "engineering",
		"nodes": []map[string]any{{"node_id": "n1", "title": "x", "role": "backend"}},
		"assertions": []map[string]any{
			{"kind": "open_question", "text": "?", "blocks_publish": true},
		},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", "/api/requirements/"+id+"/plan/draft", bytes.NewReader(draft))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/requirements/"+id+"/plan/publish", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d %s", w.Code, w.Body.String())
	}
}
