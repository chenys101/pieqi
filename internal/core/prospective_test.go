// prospective_test.go P1 前瞻性 Diff 单测：Edit/Write/Delete/NotebookEdit/Bash。
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pieqi/internal/model"
)

// prospectiveTask 建 worktree + 写入既有文件，返回 task。
func prospectiveTask(t *testing.T) *model.Task {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return &model.Task{ID: "t1", WorktreePath: dir, Status: model.TaskWaitingInput}
}

func prospectiveEvent(toolName string, input map[string]any) model.TaskEvent {
	raw, _ := json.Marshal(input)
	return model.TaskEvent{Type: model.EventToolUse, ToolName: toolName, ToolUseID: "dec1", Input: raw}
}

func TestProspectiveDiff_Edit(t *testing.T) {
	task := prospectiveTask(t)
	task.Events = []model.TaskEvent{prospectiveEvent("Edit", map[string]any{
		"file_path": "a.txt", "old_string": "world", "new_string": "pieqi",
	})}

	res, ok := ProspectiveDiff(task, "dec1")
	if !ok {
		t.Fatal("should produce diff")
	}
	if res.Operation != "modify" || !strings.Contains(res.Diff, "-world") || !strings.Contains(res.Diff, "+pieqi") {
		t.Fatalf("res: %+v diff:\n%s", res, res.Diff)
	}
}

func TestProspectiveDiff_WriteNewFile(t *testing.T) {
	task := prospectiveTask(t)
	task.Events = []model.TaskEvent{prospectiveEvent("Write", map[string]any{
		"file_path": "new.txt", "content": "line1\nline2\n",
	})}

	res, ok := ProspectiveDiff(task, "dec1")
	if !ok || res.Operation != "create" || res.Additions != 2 {
		t.Fatalf("res: %+v", res)
	}
}

func TestProspectiveDiff_WriteExisting(t *testing.T) {
	task := prospectiveTask(t)
	task.Events = []model.TaskEvent{prospectiveEvent("Write", map[string]any{
		"file_path": "a.txt", "content": "replaced\n",
	})}

	res, ok := ProspectiveDiff(task, "dec1")
	if !ok || res.Operation != "modify" || res.Deletions != 2 {
		t.Fatalf("res: %+v", res)
	}
}

func TestProspectiveDiff_Delete(t *testing.T) {
	task := prospectiveTask(t)
	task.Events = []model.TaskEvent{prospectiveEvent("Delete", map[string]any{
		"file_path": "a.txt",
	})}

	res, ok := ProspectiveDiff(task, "dec1")
	if !ok || res.Operation != "delete" || res.Deletions != 2 {
		t.Fatalf("res: %+v", res)
	}
}

func TestProspectiveDiff_BashUnsupported(t *testing.T) {
	task := prospectiveTask(t)
	task.Events = []model.TaskEvent{prospectiveEvent("Bash", map[string]any{
		"command": "rm -rf /",
	})}
	if _, ok := ProspectiveDiff(task, "dec1"); ok {
		t.Fatal("Bash should have no prospective diff")
	}
}

func TestProspectiveDiff_UnknownDecision(t *testing.T) {
	task := prospectiveTask(t)
	if _, ok := ProspectiveDiff(task, "nope"); ok {
		t.Fatal("unknown decision should not be found")
	}
}
