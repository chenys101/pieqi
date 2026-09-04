package core

import (
	"encoding/json"
	"testing"

	"pieqi/internal/model"
)

func toolUseEvent(tool, id, path string) model.TaskEvent {
	in, _ := json.Marshal(map[string]string{"file_path": path})
	return model.TaskEvent{Type: model.EventToolUse, ToolName: tool, ToolUseID: id, Input: in}
}

func TestDeriveFileChanges_TurnSplitAndMerge(t *testing.T) {
	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "改登录页", Seq: 1},
		toolUseEvent("Edit", "t1", "src/Login.vue"),
		toolUseEvent("Edit", "t2", "src/Login.vue"), // 同 Turn 同路径合并
		{Type: model.EventToolResult, ToolUseID: "t1", ToolName: "Edit"},
		{Type: model.EventToolResult, ToolUseID: "t2", ToolName: "Edit", IsError: true}, // 后到的失败覆盖
		toolUseEvent("Write", "t3", "src/new.ts"),
		{Type: model.EventToolResult, ToolUseID: "t3", ToolName: "Write"},
		{Type: model.EventUser, Text: "继续", Seq: 8}, // Turn 2
		toolUseEvent("Edit", "t4", "src/Login.vue"), // 跨 Turn 不合并
		{Type: model.EventToolResult, ToolUseID: "t4", ToolName: "Edit"},
	}

	changes := DeriveFileChanges(events, "", nil)

	if len(changes) != 3 {
		t.Fatalf("want 3 changes, got %d: %+v", len(changes), changes)
	}
	login1 := changes[0]
	if login1.Turn != 1 || len(login1.ToolUseIDs) != 2 || login1.Status != "failed" {
		t.Errorf("Login.vue turn1 wrong: %+v", login1)
	}
	login2 := changes[2]
	if login2.Turn != 2 || len(login2.ToolUseIDs) != 1 || login2.Status != "success" {
		t.Errorf("Login.vue turn2 wrong: %+v", login2)
	}
	// Write 新路径 + seenBefore=nil：之前未出现过 → create（baseline 不可知时按事件史判定）
	if changes[1].Operation != "create" {
		t.Errorf("first-seen write should be create, got %s", changes[1].Operation)
	}
}

func TestDeriveFileChanges_CreateVsModify(t *testing.T) {
	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "go", Seq: 1},
		toolUseEvent("Write", "t1", "src/a.ts"),
		toolUseEvent("Write", "t2", "src/b.ts"), // b 之前被 Edit 过 → modify
		toolUseEvent("Edit", "t3", "src/b.ts"),
	}
	// seenBefore：a.ts 不存在（create），b.ts 存在（modify）
	changes := DeriveFileChanges(events, "", func(p string) bool { return p == "src/b.ts" })
	byPath := map[string]FileChange{}
	for _, c := range changes {
		byPath[c.Path] = c
	}
	if byPath["src/a.ts"].Operation != "create" {
		t.Errorf("a.ts should be create, got %s", byPath["src/a.ts"].Operation)
	}
	if byPath["src/b.ts"].Operation != "modify" {
		t.Errorf("b.ts should be modify, got %s", byPath["src/b.ts"].Operation)
	}
}

func TestDeriveFileChanges_SkipsBashAndUnknownPaths(t *testing.T) {
	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "go", Seq: 1},
		{Type: model.EventToolUse, ToolName: "Bash", ToolUseID: "b1"},
		toolUseEvent("Edit", "", ""), // 无路径
	}
	if changes := DeriveFileChanges(events, "", nil); len(changes) != 0 {
		t.Fatalf("bash/no-path should not derive, got %+v", changes)
	}
}

func TestDeriveFileChanges_RelativizesAbsolutePaths(t *testing.T) {
	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "改", Seq: 1},
		toolUseEvent("Edit", "t1", `G:\repo\src\App.vue`),
		{Type: model.EventToolResult, ToolUseID: "t1", ToolName: "Edit"},
		toolUseEvent("Write", "t2", `G:\repo\new.ts`),
		{Type: model.EventToolResult, ToolUseID: "t2", ToolName: "Write"},
		toolUseEvent("Edit", "t3", "src/rel.vue"), // 已相对 → 原样保留
	}
	changes := DeriveFileChanges(events, `G:\repo`, nil)
	byPath := map[string]FileChange{}
	for _, c := range changes {
		byPath[c.Path] = c
	}
	if _, ok := byPath["src/App.vue"]; !ok {
		t.Errorf("G:\\repo\\src\\App.vue 应裁剪为 src/App.vue, got %+v", changes)
	}
	if _, ok := byPath["new.ts"]; !ok {
		t.Errorf("G:\\repo\\new.ts 应裁剪为 new.ts, got %+v", changes)
	}
	if _, ok := byPath["src/rel.vue"]; !ok {
		t.Errorf("相对路径 src/rel.vue 应保留, got %+v", changes)
	}
}

func TestBuildTurnInfos(t *testing.T) {
	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "第一轮", Seq: 1},
		toolUseEvent("Edit", "t1", "a.vue"),
		{Type: model.EventUser, Text: "第二轮", Seq: 3},
		toolUseEvent("Write", "t2", "b.ts"),
	}
	changes := DeriveFileChanges(events, "", nil)
	infos := BuildTurnInfos(events, changes)
	if len(infos) != 2 {
		t.Fatalf("want 2 turns, got %d", len(infos))
	}
	if infos[0].Turn != 1 || infos[0].StartEventSeq != 1 || infos[0].UserPrompt != "第一轮" {
		t.Errorf("turn1 wrong: %+v", infos[0])
	}
	if infos[1].Turn != 2 || infos[1].StartEventSeq != 3 {
		t.Errorf("turn2 wrong: %+v", infos[1])
	}
	if infos[0].Summary.Files != 1 || infos[1].Summary.Files != 1 {
		t.Errorf("summary files wrong: %+v %+v", infos[0].Summary, infos[1].Summary)
	}
	if s := SummarizeAll(changes); s.Files != 2 {
		t.Errorf("cumulative files = %d", s.Files)
	}
}

func TestCurrentTurnCount(t *testing.T) {
	if n := CurrentTurnCount(nil); n != 0 {
		t.Errorf("empty = %d", n)
	}
	events := []model.TaskEvent{{Type: model.EventUser}, {Type: model.EventText}, {Type: model.EventUser}}
	if n := CurrentTurnCount(events); n != 2 {
		t.Errorf("want 2, got %d", n)
	}
}
