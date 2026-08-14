package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteHookSettings_CreatesFileAndDir(t *testing.T) {
	wt := t.TempDir()
	hookCmd := `"/path/to/pieqi" pre-tool-use --task t1 --port 3000`
	tools := []string{"Bash", "Write", "Edit", "NotebookEdit"}

	if err := WriteHookSettings(wt, hookCmd, tools, 1800); err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(wt, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	var s settingsFile
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}

	if len(s.Hooks.PreToolUse) != len(tools) {
		t.Fatalf("got %d matcher-groups, want %d", len(s.Hooks.PreToolUse), len(tools))
	}

	// 每个工具一个 matcher-group，共享同一 hookCmd，timeout=1800
	seen := map[string]bool{}
	for _, m := range s.Hooks.PreToolUse {
		seen[m.Matcher] = true
		if len(m.Hooks) != 1 {
			t.Fatalf("matcher %s: got %d hooks, want 1", m.Matcher, len(m.Hooks))
		}
		h := m.Hooks[0]
		if h.Type != "command" {
			t.Fatalf("type=%s, want command", h.Type)
		}
		if h.Command != hookCmd {
			t.Fatalf("matcher %s: command=%q, want %q", m.Matcher, h.Command, hookCmd)
		}
		if h.Timeout != 1800 {
			t.Fatalf("matcher %s: timeout=%d, want 1800", m.Matcher, h.Timeout)
		}
	}
	for _, tool := range tools {
		if !seen[tool] {
			t.Fatalf("matcher %s missing", tool)
		}
	}
}

func TestWriteHookSettings_OverwritesExisting(t *testing.T) {
	wt := t.TempDir()
	claudeDir := filepath.Join(wt, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	// 预置一个旧 settings.json（模拟 worktree 已有配置，虽然 worktree 新建时通常没有）
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"old": true}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteHookSettings(wt, "cmd", []string{"Bash"}, 60); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var s settingsFile
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("overwritten file should be valid hook settings: %v\n%s", err, data)
	}
	if len(s.Hooks.PreToolUse) != 1 || s.Hooks.PreToolUse[0].Matcher != "Bash" {
		t.Fatalf("expected single Bash matcher, got %+v", s.Hooks.PreToolUse)
	}
}

func TestBuildHookCmd(t *testing.T) {
	got := buildHookCmd(`C:\app\pieqi.exe`, "task-abc", 39998)
	want := `"C:\app\pieqi.exe" pre-tool-use --task task-abc --port 39998`
	if got != want {
		t.Fatalf("got=%q, want=%q", got, want)
	}
}
