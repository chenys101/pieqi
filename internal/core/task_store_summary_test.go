// task_store_summary_test.go：列表/快照视图的"只数不拷"路径。
//
// 守的是什么（2026-10-22）：`GET /api/tasks` 与 WS 快照只需要事件**条数**，
// 但为了数它而克隆事件流等于把全库 TaskEvent 结构体（实测 3.9 万条）白拷一遍。
// 现在改走 ListSummaryEvents：在锁内数完条数，副本上 Events 置 nil。
//
// ⚠️ 这条优化本身**不是**列表慢的解药（实测新旧均在 15~19ms，差异在噪声内 ——
// 克隆 3.9 万条 ~128B 的结构体对列表的内嵌事件数几乎不可测）。它真正的作用是
// **削掉数据增长时的线性代价**：事件按"保留上限"逐任务增长，而列表每次刷新都要付。
// 写这条测试是为了钉住语义（计数对、内容不下发、切片不共享），不是为守住一个耗时数字。
package core

import (
	"testing"
	"time"

	"pieqi/internal/model"
)

// TestListSummaryEvents_CountsWithoutCloning 计数字段必须等于真实事件数，
// 且返回的副本上 Events 必须为空（调用方读不到内容，也就无法误用）。
func TestListSummaryEvents_CountsWithoutCloning(t *testing.T) {
	s, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}

	now := time.Now()
	a := &model.Task{ID: "a", ProjectPath: "G:/ws", Prompt: "p", CreatedAt: now}
	b := &model.Task{ID: "b", ProjectPath: "G:/ws", Prompt: "p", CreatedAt: now.Add(time.Second)}
	if _, err := s.Create(a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := s.Create(b); err != nil {
		t.Fatalf("create b: %v", err)
	}
	// 给 b 追加 3 条事件（走 store 的更新路径，模拟真实追加）
	for i := 0; i < 3; i++ {
		if _, err := s.Update("b", func(tk *model.Task) bool {
			tk.Events = append(tk.Events, model.TaskEvent{Seq: i + 1, Type: model.EventText, Text: "x"})
			return true
		}); err != nil {
			t.Fatalf("update b: %v", err)
		}
	}

	tasks, counts := s.ListSummaryEvents()
	if len(tasks) != len(counts) {
		t.Fatalf("任务数 %d 与计数数 %d 不一致（成对排序出错会错位）", len(tasks), len(counts))
	}
	if len(tasks) != 2 {
		t.Fatalf("任务数 %d want 2", len(tasks))
	}
	// 按 CreatedAt 升序：a 在前
	if tasks[0].ID != "a" || tasks[1].ID != "b" {
		t.Fatalf("排序错误: %s, %s", tasks[0].ID, tasks[1].ID)
	}
	if counts[0] != 0 || counts[1] != 3 {
		t.Fatalf("计数与任务错位或数值错: %v", counts)
	}
	for _, tk := range tasks {
		if tk.Events != nil {
			t.Fatalf("任务的 %s 副本仍带 Events —— 调用方可能误读被裁掉的内容", tk.ID)
		}
	}

	// 详情路径（List/Get）仍必须带完整事件：判据是"详情页靠它渲染时间线"
	full := s.List()
	byID := map[string]*model.Task{}
	for _, tk := range full {
		byID[tk.ID] = tk
	}
	if got := len(byID["b"].Events); got != 3 {
		t.Fatalf("List() 的 b.Events=%d want 3（详情视图不能丢事件）", got)
	}
}
