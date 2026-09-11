package core

import (
	"testing"

	"pieqi/internal/model"
)

// TestAppendEvent_MonotonicSeq Seq 必须单调，即使事件被裁剪过。
func TestAppendEvent_MonotonicSeq(t *testing.T) {
	s := &TaskStore{tasks: map[string]*model.Task{}}
	s.SetEventRetention(3)

	task := &model.Task{ID: "t1"}
	for i := 0; i < 10; i++ {
		s.AppendEvent(task, model.TaskEvent{Type: model.EventText, Text: "x"})
	}
	if len(task.Events) != 3 {
		t.Fatalf("retention 3 should keep 3 events, got %d", len(task.Events))
	}
	// 保留的是**最新**的三条
	for i, ev := range task.Events {
		want := 8 + i
		if ev.Seq != want {
			t.Fatalf("event[%d].Seq = %d, want %d", i, ev.Seq, want)
		}
	}
	if task.NextEventSeq != 11 {
		t.Fatalf("NextEventSeq = %d, want 11", task.NextEventSeq)
	}

	// 关键：继续追加也不能与已有 Seq 重复
	s.AppendEvent(task, model.TaskEvent{Type: model.EventText})
	if got := task.Events[len(task.Events)-1].Seq; got != 11 {
		t.Fatalf("after trim, next Seq = %d, want 11 (must not repeat)", got)
	}
}

// TestAppendEvent_LegacyTaskNoCounter 旧任务（本次改动前落盘）没有计数器，
// 起点必须由**实际序号**推出，而不是 len(Events)+1。
func TestAppendEvent_LegacyTaskNoCounter(t *testing.T) {
	s := &TaskStore{tasks: map[string]*model.Task{}}
	legacy := &model.Task{
		ID: "old",
		Events: []model.TaskEvent{
			{Seq: 100, Type: model.EventText},
			{Seq: 101, Type: model.EventText},
		},
	}
	s.AppendEvent(legacy, model.TaskEvent{Type: model.EventText})
	if got := legacy.Events[2].Seq; got != 102 {
		t.Fatalf("legacy task next Seq = %d, want 102", got)
	}
}

// TestAppendEvent_ZeroLimitKeepsAll 0 = 全部保留（"全部保留"这一档必须真的不裁）。
func TestAppendEvent_ZeroLimitKeepsAll(t *testing.T) {
	s := &TaskStore{tasks: map[string]*model.Task{}}
	s.SetEventRetention(0)
	task := &model.Task{ID: "t"}
	for i := 0; i < 50; i++ {
		s.AppendEvent(task, model.TaskEvent{Type: model.EventText})
	}
	if len(task.Events) != 50 {
		t.Fatalf("retention 0 should keep everything, got %d", len(task.Events))
	}
}

// TestSetEventRetention_NegativeIsUnlimited 负数归零（调用方传错值时不至于
// 变成"一条都不留"—— 那会让时间线整片消失，是最坏的一种失败）。
func TestSetEventRetention_NegativeIsUnlimited(t *testing.T) {
	s := &TaskStore{tasks: map[string]*model.Task{}}
	s.SetEventRetention(-5)
	if got := s.EventRetention(); got != 0 {
		t.Fatalf("negative retention should clamp to 0, got %d", got)
	}
}

// TestAppendEvent_TrimReleasesBackingArray 裁剪必须换底层数组，
// 否则被裁掉的部分一直挂在同一个数组上，等于这个开关没生效。
func TestAppendEvent_TrimReleasesBackingArray(t *testing.T) {
	s := &TaskStore{tasks: map[string]*model.Task{}}
	s.SetEventRetention(2)
	task := &model.Task{ID: "t"}
	for i := 0; i < 100; i++ {
		s.AppendEvent(task, model.TaskEvent{Type: model.EventText})
	}
	if cap(task.Events) > 8 {
		t.Fatalf("trim should reallocate a compact slice, cap = %d", cap(task.Events))
	}
}

// TestAppendEvent_ThroughUpdate 走真实的 Update 路径：
// AppendEvent 只在 Update 的 mutator 内被调用，这条链路必须通。
func TestAppendEvent_ThroughUpdate(t *testing.T) {
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	store.SetEventRetention(2)
	task, err := store.Create(&model.Task{Prompt: "p"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := store.Update(task.ID, func(t *model.Task) bool {
			store.AppendEvent(t, model.TaskEvent{Type: model.EventText, Text: "e"})
			return true
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
	}
	got, ok := store.Get(task.ID)
	if !ok {
		t.Fatal("task should exist")
	}
	if len(got.Events) != 2 {
		t.Fatalf("persisted events = %d, want 2", len(got.Events))
	}
	// 重新载入后计数器也要还在，否则重启会退回 len+1 推序号
	reopened, err := NewTaskStore(store.tasksDir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	again, _ := reopened.Get(task.ID)
	if again.NextEventSeq != got.NextEventSeq {
		t.Fatalf("NextEventSeq not persisted: %d vs %d", again.NextEventSeq, got.NextEventSeq)
	}
}
