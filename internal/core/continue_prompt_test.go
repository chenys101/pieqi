package core

import (
	"strings"
	"testing"

	"pieqi/internal/model"
)

func TestContinuePromptForFreshSession_AnchorsTask(t *testing.T) {
	got := continuePromptForFreshSession(&model.Task{
		Prompt: "approval unresolved: 怎么导致 acp 中断",
		Events: []model.TaskEvent{
			{Type: model.EventUser, Text: "approval unresolved: 怎么导致 acp 中断"},
			{Type: model.EventText, Text: "先查审批卡未决原因"},
			{Type: model.EventUser, Text: "继续"},
		},
	}, "继续")

	if !strings.Contains(got, "不要查找其它会话") {
		t.Fatalf("missing anti-wander head: %q", got)
	}
	if !strings.Contains(got, "approval unresolved") {
		t.Fatalf("missing original task: %q", got)
	}
	if !strings.Contains(got, "先查审批卡未决原因") {
		t.Fatalf("missing recent assistant text: %q", got)
	}
	if !strings.Contains(got, "当前请求：继续") {
		t.Fatalf("missing current request: %q", got)
	}
	// 裸「继续」user 事件不应占近期要点。
	if strings.Count(got, "用户：继续") != 0 {
		t.Fatalf("bare continue should be skipped in recent context: %q", got)
	}
}

func TestContinuePromptForFreshSession_NilTask(t *testing.T) {
	if got := continuePromptForFreshSession(nil, "继续"); got != "继续" {
		t.Fatalf("got %q", got)
	}
}

func TestIsBareContinue(t *testing.T) {
	for _, s := range []string{"继续", " Continue ", "接着做"} {
		if !isBareContinue(s) {
			t.Fatalf("%q should be bare continue", s)
		}
	}
	if isBareContinue("继续查 ACP 审批未决") {
		t.Fatal("substantive continue must not be bare")
	}
}
