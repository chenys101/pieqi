// checks_test.go P1 Checks 单测：事件流派生 + CheckRunner 重跑。
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

// checkEvents 构造带 Bash check 的事件流。
func checkEvents(t *testing.T, cmds ...[3]any) []model.TaskEvent {
	t.Helper()
	var events []model.TaskEvent
	seq := 0
	add := func(ev model.TaskEvent) model.TaskEvent {
		seq++
		ev.Seq = seq
		ev.At = time.Now()
		events = append(events, ev)
		return ev
	}
	add(model.TaskEvent{Type: model.EventUser, Text: "run tests"})
	for _, c := range cmds {
		// c: [command string, toolUseID string, resultIsError bool]
		cmd := c[0].(string)
		id := c[1].(string)
		isErr := c[2].(bool)
		input, _ := json.Marshal(map[string]string{"command": cmd})
		add(model.TaskEvent{Type: model.EventToolUse, ToolName: "Bash", ToolUseID: id, Input: input})
		add(model.TaskEvent{Type: model.EventToolResult, ToolUseID: id, Result: "done\nexit 0", IsError: isErr})
	}
	return events
}

func TestDeriveChecks_PatternAndResult(t *testing.T) {
	events := checkEvents(t,
		[3]any{"npm test", "tu1", false},
		[3]any{"npm run dev", "tu2", false},     // 非 check：排除
		[3]any{"go vet ./...", "tu3", true},     // go 工具链：纳入且 failed
		[3]any{"ls -la", "tu4", false},          // 非 check：排除
		[3]any{"cd web && npm run build", "tu5", false}, // cd 前缀：纳入
	)

	checks := DeriveChecks(events)
	if len(checks) != 3 {
		t.Fatalf("expect 3 checks, got %d: %+v", len(checks), checks)
	}
	byID := map[string]Check{}
	for _, ck := range checks {
		byID[ck.ID] = ck
	}
	if ck := byID["tu1"]; ck.Status != CheckSuccess || ck.Turn != 1 || ck.Origin != "agent" {
		t.Fatalf("tu1: %+v", ck)
	}
	if ck := byID["tu3"]; ck.Status != CheckFailed {
		t.Fatalf("tu3 should be failed: %+v", ck)
	}
	if ck := byID["tu5"]; ck.Status != CheckSuccess {
		t.Fatalf("tu5 (cd prefix): %+v", ck)
	}
	if _, ok := byID["tu2"]; ok {
		t.Fatal("npm run dev should not be a check")
	}
	if _, ok := byID["tu4"]; ok {
		t.Fatal("ls should not be a check")
	}
}

func TestDeriveChecks_PendingWithoutResult(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"command": "npm test"})
	events := []model.TaskEvent{
		{Seq: 1, Type: model.EventUser, Text: "go"},
		{Seq: 2, Type: model.EventToolUse, ToolName: "Bash", ToolUseID: "tu9", Input: input},
	}
	checks := DeriveChecks(events)
	if len(checks) != 1 || checks[0].Status != CheckPending {
		t.Fatalf("pending check: %+v", checks)
	}
}

func TestCheckRunner_RerunLifecycle(t *testing.T) {
	dir := t.TempDir()
	task := &model.Task{ID: "t1", WorktreePath: dir, Status: model.TaskCompleted}
	cr := NewCheckRunner(zap.NewNop(), filepath.Join(dir, "checks"))

	// 成功路径：echo
	ck, err := cr.Rerun(task, "src1", "echo hi", "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if ck.Status != CheckRunning {
		t.Fatalf("should start running: %+v", ck)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cr.List(task.ID)[0].Status != CheckRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	list := cr.List(task.ID)
	if len(list) != 1 || list[0].Status != CheckSuccess {
		t.Fatalf("rerun result: %+v", list)
	}
	if list[0].Output == "" {
		t.Fatal("output should be captured")
	}

	// 失败路径：exit 3 → exit code 记录
	ck2, err := cr.Rerun(task, "src2", "exit 3", "exit 3")
	if err != nil {
		t.Fatal(err)
	}
	_ = ck2
	deadline = time.Now().Add(5 * time.Second)
	var final *Check
	for time.Now().Before(deadline) {
		for _, c := range cr.List(task.ID) {
			if c.Origin == "rerun" && c.ExitCode == 3 {
				cc := c
				final = &cc
			}
		}
		if final != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final == nil || final.Status != CheckFailed {
		t.Fatalf("failed rerun: %+v", final)
	}

	// 记录落盘（新进程可读）
	if _, err := os.Stat(filepath.Join(dir, "checks", "t1", "checks.json")); err != nil {
		t.Fatalf("persisted: %v", err)
	}
	// 防重：同源已在跑 → 报错（用一个长命令占住）
	go func() { _, _ = cr.Rerun(task, "src3", "sleep 1", "sleep 1") }()
	time.Sleep(50 * time.Millisecond)
	if _, err := cr.Rerun(task, "src3", "sleep 1", "sleep 1"); err == nil {
		t.Fatal("same-source rerun should be rejected while running")
	}
}
