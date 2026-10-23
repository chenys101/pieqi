// 2026-10-24 线上死锁的回归测试。
//
// 事故链：toolCallWire.Flush 持 store 写锁跑 mutator → applyToolCallUpdate →
// saveToolImages / warnImagesDropped 内部又对**同一把** RWMutex 取 RLock。
// Go 的 sync.RWMutex 不可重入：持写锁的 goroutine 自己再 RLock 必然永久阻塞，
// 于是 store.mu 永远放不掉 —— GET /api/tasks、WS 快照、流式落盘、审批全部冻结，
// 而不碰 store 的端点（如 /api/agents）正常，表现成"服务半死"。
//
// 触发条件是"工具结果真的带图"：中转层丢字段的 bug（5f502d2 修）让 Images 在
// 修复前根本到不了这里，死锁因此潜伏；修好中转层后第一张真图（02:26 的
// "设计好看的猫头像"任务）当场引爆。
//
// 本用例按生产形状复现：带图的 tool_result 走真实 Flush 路径，用看门狗判定
// —— 修复前 Flush 3s 内不返回（死锁），修复后正常落盘且 /api/tasks 同款读路径不被卡。
//
// 失败路径的清理纪律：死锁时 Unwire 会先 Flush（正是死锁点），defer 它会让测试
// 卡到超时 —— 所以只在成功路径末尾显式 Unwire；看门狗触发的失败路径直接放弃清理，
// 泄漏的阻塞 goroutine 不影响测试进程退出。
package core

import (
	"encoding/json"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// deadlockPNG 与 attachments_test.go 的 attachPNG 同一张 1x1 PNG（各自独立，
// 免得两个测试文件互相牵连）。
const deadlockPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// emitImageToolCall 走完一次"带图工具调用"：开工通知 + 带图完成通知。
func emitImageToolCall(fa *fakePermAdapter, id string) {
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: id, Title: "gen_image", Status: "pending", IsNew: true,
		RawInput: json.RawMessage(`{"prompt":"cat"}`),
	})
	fa.emitToolCall(agent.ToolCallUpdateInfo{
		ToolCallID: id, Status: "completed", IsNew: false,
		RawOutput: json.RawMessage(`{"ok":true}`),
		Images:    []agent.ImageInput{{Data: deadlockPNG, MimeType: "image/png"}},
	})
}

func TestWireToolCall_ImageResultDoesNotSelfDeadlock(t *testing.T) {
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 接线附件存储：覆盖 saveToolImages 的"落盘成功"分支。
	store.SetAttachments(NewAttachmentStore(t.TempDir()), zap.NewNop())

	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "画猫"})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakePermAdapter()
	h := WireToolCall(fa, bus, store, tt.ID)
	emitImageToolCall(fa, "tc-1")

	flushed := make(chan struct{})
	go func() { h.Flush(); close(flushed) }()

	// 并发走 /api/tasks 同款读路径：修复前它被永远不放掉的写锁卡死。
	listed := make(chan struct{})
	go func() { store.ListSummaryEvents(); close(listed) }()

	select {
	case <-flushed:
	case <-time.After(3 * time.Second):
		t.Fatal("Flush 3s 未返回：mutator 持写锁期间再取读锁的自死锁复现（见文件头注释）")
	}
	select {
	case <-listed:
	case <-time.After(2 * time.Second):
		t.Fatal("ListSummaryEvents 被卡死：store 写锁未随 Flush 释放")
	}

	// 落盘正确性顺手钉住：tool_result 带已落盘的图元数据。
	tt, _ = store.Get(tt.ID)
	var found bool
	for _, ev := range tt.Events {
		if ev.Type == model.EventToolResult && len(ev.Images) == 1 && ev.Images[0].Stored {
			found = true
		}
	}
	if !found {
		t.Fatalf("带图 tool_result 未正确落盘: %+v", tt.Events)
	}

	h.Unwire()
}

// TestWireToolCall_ImageResultNoStoreDoesNotSelfDeadlock 未接线附件存储时，
// 带图结果走 saveToolImages 的 WARN 分支 —— 该分支同样先取 RLock，修复前同样自死锁。
func TestWireToolCall_ImageResultNoStoreDoesNotSelfDeadlock(t *testing.T) {
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.SetAttachments(nil, zap.NewNop())

	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "画猫"})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakePermAdapter()
	h := WireToolCall(fa, bus, store, tt.ID)
	emitImageToolCall(fa, "tc-2")

	flushed := make(chan struct{})
	go func() { h.Flush(); close(flushed) }()
	select {
	case <-flushed:
	case <-time.After(3 * time.Second):
		t.Fatal("未接线分支同样自死锁（saveToolImages 的 RLock 在 att==nil 判断之前）")
	}

	tt, _ = store.Get(tt.ID)
	var results int
	for _, ev := range tt.Events {
		if ev.Type == model.EventToolResult {
			results++
			if len(ev.Images) != 0 {
				t.Errorf("未接线时不该有图元数据: %+v", ev.Images)
			}
		}
	}
	if results != 1 {
		t.Fatalf("tool_result 事件数=%d, want 1", results)
	}
	h.Unwire()
}
