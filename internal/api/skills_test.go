package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"claude-bridge/internal/config"
	"claude-bridge/internal/core"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestAPI_ListSkills(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "my-skill"), 0755)
	os.WriteFile(filepath.Join(dir, "my-skill", "SKILL.md"),
		[]byte("---\nname: my-skill\ndescription: a test skill\n---\n"), 0644)

	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(0)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "m", "", "", false, "", 0, nil, 0, 0, "main")
	scanner := core.NewSkillScanner(zap.NewNop(), []string{dir})
	cfg := &config.Config{}
	srv := NewServer(cfg, store, runner, hooks, bus, scanner, nil)
	r := gin.New()
	srv.Register(r)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/skills", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Skills []core.SkillInfo `json:"skills"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Skills) != 1 || resp.Skills[0].Name != "my-skill" {
		t.Fatalf("skills=%+v", resp.Skills)
	}
	if resp.Skills[0].Description != "a test skill" {
		t.Fatalf("desc=%q", resp.Skills[0].Description)
	}
}
