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

// 同一路径被多轮改动时，任务级统计必须**按路径折叠**：派生结果是「每轮一条」，
// 直接数条目会把「1 个文件改 2 轮」说成 2 个文件、同一份 diff 再加一遍。
// 实测线上任务真值 +1 -3 / 1 文件，接口返回 +2 -6 / 2 文件。
func TestSummarizeDedupesRepeatedPath(t *testing.T) {
	events := []model.TaskEvent{
		{Type: model.EventUser, Text: "第一轮", Seq: 1},
		toolUseEvent("Edit", "t1", "src/App.vue"),
		{Type: model.EventUser, Text: "第二轮", Seq: 3},
		toolUseEvent("Edit", "t2", "src/App.vue"),
	}
	changes := DeriveFileChanges(events, "", nil)
	if len(changes) != 2 {
		t.Fatalf("前置：同路径两轮应派生两条 FileChange, got %d", len(changes))
	}
	changes[0].Additions, changes[0].Deletions = 1, 3
	changes[1].Additions, changes[1].Deletions = 1, 4

	s := SummarizeAll(changes)
	if s.Files != 1 {
		t.Errorf("files = %d, want 1（按路径折叠，不按改动轮次）", s.Files)
	}
	if s.Modifies != 1 {
		t.Errorf("modifies = %d, want 1", s.Modifies)
	}
	// 折叠保留最后一次出现 = 该路径的最终态
	if s.Additions != 1 || s.Deletions != 4 {
		t.Errorf("+%d -%d, want +1 -4（保留最后一条）", s.Additions, s.Deletions)
	}
}

func TestUniqueByPath(t *testing.T) {
	in := []FileChange{
		{Path: "a.vue", Turn: 1, Additions: 1},
		{Path: "b.ts", Turn: 1},
		{Path: "a.vue", Turn: 2, Additions: 9},
	}
	got := UniqueByPath(in)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(got), got)
	}
	if got[0].Path != "a.vue" || got[0].Turn != 2 || got[0].Additions != 9 {
		t.Errorf("同路径应保留最后一次出现, got %+v", got[0])
	}
	if got[1].Path != "b.ts" {
		t.Errorf("首次出现的顺序应保持, got %+v", got[1])
	}
	if n := len(UniqueByPath(nil)); n != 0 {
		t.Errorf("nil → 0, got %d", n)
	}
}
