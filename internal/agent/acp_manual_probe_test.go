// 手工探针（默认跳过）：把一个**真实** dsh ACP 通知形状喂给 SessionUpdate，
// 打印 pieqi 侧的解析结果。用 `go test -run TestManualProbe -v -args -probe` 打开。
//
// 为什么留这么一条：dsh 的 read_image 结果形状来自它自己的 data/patch 层，
// 与 acp-go-sdk 的字段名对齐与否**只能实测**。这条用例把那个形状钉在代码里，
// 免得日后 sdk 升级或 dsh 改形状时只能靠"界面没图"去猜。
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

func TestManualProbe_ToolImageShape(t *testing.T) {
	if os.Getenv("PIEQI_PROBE_IMAGE") == "" {
		t.Skip("set PIEQI_PROBE_IMAGE=1 to run the manual probe")
	}
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) {
		t.Logf("update: new=%v status=%s tool=%q images=%d", u.IsNew, u.Status, u.Title, len(u.Images))
	})

	img := base64.StdEncoding.EncodeToString([]byte("not-a-real-png-but-shape-valid"))
	ctx := context.Background()
	_ = a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "s",
		Update:    acp.StartToolCall("call_1", "read_image"),
	})
	completed := acp.ToolCallStatusCompleted
	_ = a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "s",
		Update: acp.UpdateToolCall("call_1",
			acp.WithUpdateStatus(completed),
			acp.WithUpdateContent([]acp.ToolCallContent{
				{Content: &acp.ToolCallContentContent{Type: "content", Content: acp.TextBlock("<path>x.png</path>")}},
				{Content: &acp.ToolCallContentContent{Type: "content", Content: acp.ImageBlock(img, "image/png")}},
			}),
		),
	})
}

// TestToolCallImages_DshRealShape 钉住 dsh 的真实 ToolCallUpdate JSON 形状。
//
// 直接用**线上抓到的 JSON** 反序列化，而不是用 SDK 的构造函数：
// 两者一致时这条用例与构造函数版等价，而一旦 SDK 的 unmarshal 与真实报文
// 出现分歧（字段名/包装层变化），只有这条会红。
func TestToolCallImages_DshRealShape(t *testing.T) {
	const raw = `{"content":[{"type":"content","content":{"type":"image","mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="}}],"toolCallId":"call_x","status":"completed"}`
	var upd acp.ToolCallUpdate
	if err := json.Unmarshal([]byte(raw), &upd); err != nil {
		t.Fatalf("unmarshal real shape: %v", err)
	}
	got := toolCallImages("read_image", upd.Content)
	if len(got) != 1 {
		t.Fatalf("got %d images from the real shape, want 1（SDK 解析与真实报文不一致？）", len(got))
	}
}
