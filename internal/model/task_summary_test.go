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

	data, err := json.Marshal(NewTaskSummary(task))
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
	if got := NewTaskSummary(nil); got != nil {
		t.Fatalf("nil task 应返回 nil，得到 %+v", got)
	}
	if got := NewTaskSummaries(nil); len(got) != 0 {
		t.Fatalf("nil 切片应返回空，得到 %d", len(got))
	}
}

// 批量为空时不能返回 nil 切片（JSON 序列化会变成 null 而非 []，前端 .map 会炸）。
func TestNewTaskSummaries_EmptyIsEmptySlice(t *testing.T) {
	got := NewTaskSummaries([]*Task{})
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("空结果应序列化为 []，得到 %s", data)
	}
}
