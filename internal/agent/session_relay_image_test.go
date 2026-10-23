// 图片穿过**两层中转**时不能被丢掉。
//
// 2026-10-23 线上复盘：图片在 ToolCallUpdateInfo 里解析正确（日志 `images=1`），
// 但任务 JSON 里没有 images。根因不是解析、也不是落盘，而是中间两层**中转结构**
// 各自都没列这个字段：
//
//	ACP SessionUpdate → ToolCallUpdateInfo.Images
//	                    ↓  Event（中性事件，session.go）        ← 第一处丢失
//	                    ↓  sessionBackedAdapter.onEvent          ← 第二处丢失
//	                    ↓  core.applyToolCallUpdate → TaskEvent.Images
//
// 这类丢失**不会有任何报错**：工具结果照常有、只是没有图。所以这里逐跳钉住，
// 而不是只测两端。
package agent

import (
	"testing"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

// TestEventRelay_ImagesSurviveNeutralEvent 第一跳：Event（中性事件）→ adapter 回调。
//
// 底层会话 fire 一条带图的中性事件，sessionBackedAdapter 翻译成 ToolCallUpdateInfo 时
// 必须仍带图 —— 漏了就是"适配器解析到了、事件层却永远收不到"。
func TestEventRelay_ImagesSurviveNeutralEvent(t *testing.T) {
	fake := &fakeSession4Adapter{id: "sess-1"}
	adapter := newSessionBackedAdapter("dsh", openFake("dsh", fake))
	// ⚠️ 不用 t.TempDir()：它建在系统临时目录，而本仓库的会话沙箱只允许写工作区内，
	// 表现是"用例报 Access is denied"、看起来像代码坏了（2026-10-23 实际踩过）。
	// 这里给一个空串 cwd —— 本用例不碰文件系统，只需要一个能建会话的最小参数。
	if _, err := adapter.NewSession(t.Context(), SessionConfig{}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var got []ToolCallUpdateInfo
	adapter.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = append(got, u) })

	fake.fire(Event{
		Kind: EventToolEnd, ToolCallID: "tc-img", ToolTitle: "read_image", ToolStatus: "completed",
		Images: []ImageInput{{Data: toolImagePNG, MimeType: "image/png"}},
	})

	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1", len(got))
	}
	if len(got[0].Images) != 1 {
		t.Fatalf("经过 Event 中性事件后图片丢了（Event.Images 没透传？）got %d", len(got[0].Images))
	}
	if got[0].Images[0].MimeType != "image/png" {
		t.Errorf("mime=%q want image/png", got[0].Images[0].MimeType)
	}
}

// TestEventRelay_ACPUpdateReachesAdapter 第二跳：ACP SessionUpdate → Event → adapter 回调。
//
// 用真实 ACPAgent 发一条带图的 ToolCallUpdate，断言**最终**到达 OnToolCallUpdate 的
// 信息仍带图。这条链路经过 session.go 的 OnToolCallUpdate 桥接与 sessionBackedAdapter.onEvent
// 两次翻译，任何一层漏字段都会红。
//
// 注意这里不走 AgentSession：ACPAgent 本身就是 AgentAdapter，直接注册回调即可。
// 两个中转层的字段覆盖由上面那条用例（Event → adapter）与 core 层的
// TestApplyToolCallUpdate_AttachesImagesToToolResult 共同守住。
func TestEventRelay_ACPUpdateCarriesImages(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	var last ToolCallUpdateInfo
	var n int
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) {
		n++
		last = u
	})

	_ = a.SessionUpdate(t.Context(), acp.SessionNotification{
		SessionId: "s",
		Update:    acp.StartToolCall("tc-e2e", "read_image"),
	})
	completed := acp.ToolCallStatusCompleted
	_ = a.SessionUpdate(t.Context(), acp.SessionNotification{
		SessionId: "s",
		Update: acp.UpdateToolCall("tc-e2e",
			acp.WithUpdateStatus(completed),
			acp.WithUpdateContent([]acp.ToolCallContent{toolImageBlock(toolImagePNG, "image/png")}),
		),
	})

	if n == 0 {
		t.Fatal("没有收到任何工具更新")
	}
	if len(last.Images) != 1 {
		t.Fatalf("ACP 更新里图片丢失（最后一跳 Images=%d）", len(last.Images))
	}
}
