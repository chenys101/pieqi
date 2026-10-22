package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 回归（2026-10-06「列表加载越来越慢」）：
// 列表/快照必须**不下发**事件流。实测 24 个任务全量下发达 11.25 MB，
// 是列表慢（300ms+ 本地）与 WS 订阅缓冲溢出的共同根因。
func TestTaskSummary_OmitsEvents(t *testing.T) {
	task := &Task{
		ID:          "t1",
		ProjectPath: "G:/ws/erp",
		Status:      TaskRunning,
		Prompt:      "p",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Events: []TaskEvent{
			{Seq: 1, Type: EventUser, Text: "hi"},
			{Seq: 2, Type: EventText, Text: "hello"},
		},
	}

	data, err := json.Marshal(NewTaskSummary(task, -1))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)

	if strings.Contains(got, `"events"`) {
		t.Fatalf("轻量视图不应包含 events 字段: %s", got)
	}
	if strings.Contains(got, "hello") {
		t.Fatalf("事件正文泄漏进列表载荷: %s", got)
	}
	// 元数据必须还在：前端列表靠它渲染标题/状态
	if !strings.Contains(got, `"event_count":2`) {
		t.Fatalf("应下发 event_count 以替代事件全文: %s", got)
	}
	if !strings.Contains(got, `"prompt":"p"`) {
		t.Fatalf("元数据字段丢失: %s", got)
	}
}

// TestTaskSummary_OmitsOutput 列表视图**不下发 Output**（2026-10-22）。
//
// Output 是最近一次流式输出的累积全文，实测占列表响应的 72%
// （89 个任务：1.70 MB / 2.36 MB），而列表从不渲染它 —— 唯一读它的会话页
// 走 GET /api/tasks/:id。裁掉后列表 3.26 MB → 0.64 MB，耗时 394 ms → 10 ms。
//
// ⚠️ 不能顺手把 output 从 **Task** 上删掉：详情页的口径兜底要靠它
// （SessionTimeline 在 events 为空时显示 task.output）。这里只裁视图。
func TestTaskSummary_OmitsOutput(t *testing.T) {
	task := &Task{
		ID:        "t1",
		Status:    TaskCompleted,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Output:    strings.Repeat("OUTPUT-BODY-", 1000), // 够大，才看得出是这份载荷
	}

	data, err := json.Marshal(NewTaskSummary(task, 3))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "OUTPUT-BODY-") {
		t.Fatalf("列表视图不应包含 output 正文（占 72%% 体积且列表不渲染）: %d 字节", len(data))
	}
	if len(data) > 2000 {
		t.Fatalf("轻量视图过大: %d 字节", len(data))
	}

	// 详情口径（Task 本身）必须保留 output，否则会话页兜底显示就没了。
	full, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal full: %v", err)
	}
	if !strings.Contains(string(full), "OUTPUT-BODY-") {
		t.Fatal("Task 的 output 被连带裁掉了 —— 详情页/session 兜底要靠它")
	}
	// 原任务对象不能被改动（NewTaskSummary 走浅拷贝）。
	if task.Output == "" {
		t.Fatal("NewTaskSummary 原地改动了传入的 Task.Output（会造成落盘数据损坏）")
	}
}

// event_count 必须取调用方量好的值，而不是 len(t.Events)。
//
// 这是 2026-10-22 的性能契约：列表走 TaskStore.ListSummaryEvents，它**不克隆**事件流，
// 传给 NewTaskSummary 的 t 上 Events 恒为 nil。若这里退回 len(t.Events)，
// 列表会把每个任务的进度显示成 0 —— 不报错、静默错，是最难发现的那类回归。
func TestNewTaskSummary_UsesSuppliedEventCount(t *testing.T) {
	task := &Task{ID: "t1", CreatedAt: time.Now(), Events: nil}
	s := NewTaskSummary(task, 2120)
	if s.EventCount != 2120 {
		t.Fatalf("EventCount=%d want 2120（应取入参，不是 len(被裁空的 Events)）", s.EventCount)
	}
}

// NewTaskSummaries 必须按位置把 counts 配给任务；counts 缺失时退回就地数。
func TestNewTaskSummaries_PairsCountsByIndex(t *testing.T) {
	now := time.Now()
	ts := []*Task{
		{ID: "a", CreatedAt: now, Events: []TaskEvent{{Seq: 1}}},
		{ID: "b", CreatedAt: now.Add(time.Second), Events: []TaskEvent{{Seq: 1}, {Seq: 2}, {Seq: 3}}},
	}
	got := NewTaskSummaries(ts, []int{7, 9})
	if len(got) != 2 || got[0].EventCount != 7 || got[1].EventCount != 9 {
		t.Fatalf("counts 未按位置配对: %+v", got)
	}
	// counts 为 nil 时退回 len(t.Events)（保持旧调用方可用）
	if got := NewTaskSummaries(ts, nil); got[1].EventCount != 3 {
		t.Fatalf("counts 缺失时应退回 len(Events)，得到 %d", got[1].EventCount)
	}
}

// 全量 Task 的 events 仍必须照常序列化（详情接口依赖它）。
func TestTask_StillSerializesEvents(t *testing.T) {
	task := &Task{
		ID:        "t1",
		Status:    TaskRunning,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Events:    []TaskEvent{{Seq: 1, Type: EventText, Text: "hello"}},
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("详情视图必须保留 events: %s", data)
	}
}

// NewTaskSummary 必须不拷贝事件切片（拷贝会抵消掉省内存的意义），
// 且 nil 任务不得 panic —— store 可能返回 nil 元素。
func TestNewTaskSummary_NilSafe(t *testing.T) {
	if got := NewTaskSummary(nil, -1); got != nil {
		t.Fatalf("nil task 应返回 nil，得到 %+v", got)
	}
	if got := NewTaskSummaries(nil, nil); len(got) != 0 {
		t.Fatalf("nil 切片应返回空，得到 %d", len(got))
	}
}

// 批量为空时不能返回 nil 切片（JSON 序列化会变成 null 而非 []，前端 .map 会炸）。
func TestNewTaskSummaries_EmptyIsEmptySlice(t *testing.T) {
	got := NewTaskSummaries([]*Task{}, nil)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("空结果应序列化为 []，得到 %s", data)
	}
}
