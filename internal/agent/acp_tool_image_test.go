// 工具结果里的图片（**出站**，面向人）：ACP ToolCall/ToolCallUpdate.Content 的解析。
//
// 与 acp_image_test.go（入站：用户发的图 → prompt）方向相反，刻意分文件放：
// 两条链路此前被混为一谈，而"能不能收图"与"能不能看图"是**两个独立的能力**，
// 前者由对端声明，后者由 pieqi 自己是否解析 Content 决定。
//
// 这组用例守的是一条曾经完全不通的链路（2026-10-23 定性）：
// read_image 的图片块在 dsh 侧一直有，而 SessionUpdate 只读 RawInput/RawOutput，
// Content 从没被读过 —— 表现是"看图片只有文字信封"，而且**一个错都不报**。
package agent

import (
	"context"
	"testing"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

// toolImagePNG 1x1 透明 PNG：类型在白名单里、base64 严格合法。
const toolImagePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func toolImageBlock(data, mime string) acp.ToolCallContent {
	return acp.ToolCallContent{Content: &acp.ToolCallContentContent{
		Type:    "content",
		Content: acp.ImageBlock(data, mime),
	}}
}

func toolTextBlock(text string) acp.ToolCallContent {
	return acp.ToolCallContent{Content: &acp.ToolCallContentContent{
		Type:    "content",
		Content: acp.TextBlock(text),
	}}
}

// TestToolCallImages_WhitelistOnly 只有读图类工具的结果图才被收。
//
// 判据是**工具名白名单**，不是"Content 里有图"。任意 Bash 都能在输出里塞图片块，
// 不设白名单等于给 agent 开一条不经语义审查的对外展示通道。
func TestToolCallImages_WhitelistOnly(t *testing.T) {
	content := []acp.ToolCallContent{toolImageBlock(toolImagePNG, "image/png")}

	if got := toolCallImages("read_image", content); len(got) != 1 {
		t.Fatalf("read_image: got %d images, want 1", len(got))
	}
	if got := toolCallImages("  Read_Image ", content); len(got) != 1 {
		t.Fatalf("Read_Image（混合大小写）: got %d images, want 1", len(got))
	}
	for _, name := range []string{"Bash", "pwsh", "edit", "read", ""} {
		if got := toolCallImages(name, content); len(got) != 0 {
			t.Errorf("%q: got %d images, want 0（非读图工具不得透出图片）", name, len(got))
		}
	}
}

// TestToolCallImages_SkipsInvalid 非法图片被跳过，合法的仍然收（单张失败不连坐）。
func TestToolCallImages_SkipsInvalid(t *testing.T) {
	content := []acp.ToolCallContent{
		toolTextBlock("<type>image</type>"),                                 // 只是文字信封
		toolImageBlock("not-base64!!!", "image/png"),                        // 形状非法
		toolImageBlock(toolImagePNG, "image/tiff"),                          // mime 不在白名单
		toolImageBlock("data:image/png;base64,"+toolImagePNG, "image/png"),  // 带了 data: 前缀
		toolImageBlock(toolImagePNG, "image/png"),                           // 唯一合法的一张
	}
	got := toolCallImages("read_image", content)
	if len(got) != 1 {
		t.Fatalf("got %d images, want 1（只有最后一张合法）", len(got))
	}
	if got[0].MimeType != "image/png" || got[0].Data != toolImagePNG {
		t.Errorf("got %+v, want the png passthrough verbatim", got[0])
	}
}

// TestToolCallImages_EmptyContentIsNil 空 Content 返回 nil（不是空切片）。
//
// 返回 nil 让事件 JSON 里**没有** images 字段（omitempty），与旧数据形状一致。
func TestToolCallImages_EmptyContentIsNil(t *testing.T) {
	if got := toolCallImages("read_image", nil); got != nil {
		t.Errorf("nil content: got %v, want nil", got)
	}
	if got := toolCallImages("read_image", []acp.ToolCallContent{toolTextBlock("x")}); got != nil {
		t.Errorf("text-only content: got %v, want nil", got)
	}
}

// TestSessionUpdate_ToolCallUpdateCarriesImages 图片从 ToolCallUpdate.Content 透传到回调。
//
// ⚠️ 覆盖的是**真实形状**：dsh 的 ToolCallUpdate 常常不再重复 Title，只带 Status + Content。
// 工具名只能靠 tool_call 备忘录回查 —— 少了那一步白名单判空，图片被静默丢掉
//（这正是线上"永远看不到图"的直接成因）。
func TestSessionUpdate_ToolCallUpdateCarriesImages(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	var got []ToolCallUpdateInfo
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = append(got, u) })

	ctx := context.Background()
	// 开工通知：带工具名，不带图。
	_ = a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.StartToolCall("tc-img", "read_image"),
	})
	// 结果通知：没有 Title，只有状态与图片内容。
	completed := acp.ToolCallStatusCompleted
	_ = a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.UpdateToolCall("tc-img",
			acp.WithUpdateStatus(completed),
			acp.WithUpdateContent([]acp.ToolCallContent{
				toolTextBlock("<path>x.png</path>"),
				toolImageBlock(toolImagePNG, "image/png"),
			}),
		),
	})

	if len(got) != 2 {
		t.Fatalf("got %d updates, want 2", len(got))
	}
	if len(got[0].Images) != 0 {
		t.Errorf("开工通知不该带图，got %d", len(got[0].Images))
	}
	if len(got[1].Images) != 1 {
		t.Fatalf("结果通知 Images=%d want 1（标题缺省时未回查工具名？）", len(got[1].Images))
	}
	if got[1].Images[0].MimeType != "image/png" {
		t.Errorf("mime=%q want image/png", got[1].Images[0].MimeType)
	}
}

// TestSessionUpdate_ToolCallStartCarriesImages 开工通知本身就带图时也要透传。
//
// 少数实现会在 ToolCall 里直接给完整结果（不另发 Update）；漏掉这个分支的表现
// 同样是"永远没有图"，且只在部分 agent 上出现，最难归因。
func TestSessionUpdate_ToolCallStartCarriesImages(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	var got []ToolCallUpdateInfo
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = append(got, u) })

	_ = a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.StartToolCall("tc-img2", "read_image",
			acp.WithStartContent([]acp.ToolCallContent{toolImageBlock(toolImagePNG, "image/png")}),
		),
	})
	if len(got) != 1 || len(got[0].Images) != 1 {
		t.Fatalf("got %+v, want 1 update with 1 image", got)
	}
}
