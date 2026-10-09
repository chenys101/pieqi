package agent

import (
	"context"
	"testing"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

// usageNotification 构造一条 usage_update 的 session/update 通知。
//
// 不用 SDK 的 helper：v0.13.5 没有为这个 UNSTABLE 变体生成构造函数
// （helpers_gen.go 里只有 UpdateAgentMessageText 等常用的那几个），
// 只能自己拼结构体。
func usageNotification(sid string, used, size int, cost *acp.Cost) acp.SessionNotification {
	return acp.SessionNotification{
		SessionId: acp.SessionId(sid),
		Update: acp.SessionUpdate{
			UsageUpdate: &acp.SessionUsageUpdate{
				SessionUpdate: "usage_update",
				Used:          used,
				Size:          size,
				Cost:          cost,
			},
		},
	}
}

// TestSessionUpdate_UsageUpdateDispatched 验证 usage_update 落到 OnUsageUpdate：
// 这是 dsh-acp 已经在发、而 pieqi 原先直接丢掉的那条（acp.go 旧注释 "M1 暂不处理"）。
func TestSessionUpdate_UsageUpdateDispatched(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	var got []UsageInfo
	a.OnUsageUpdate(func(u UsageInfo) { got = append(got, u) })

	err := a.SessionUpdate(context.Background(), usageNotification("sess-1", 12345, 200000, nil))
	if err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d usage updates, want 1", len(got))
	}
	if got[0].Used != 12345 || got[0].Size != 200000 {
		t.Errorf("usage=%+v want Used=12345 Size=200000", got[0])
	}
	if got[0].SessionID != "sess-1" {
		t.Errorf("SessionID=%q want sess-1", got[0].SessionID)
	}
	// 没给 Cost 时必须 HasCost=false —— UI 据此隐藏成本，而不是显示 "$0.00"。
	if got[0].HasCost {
		t.Errorf("HasCost=true want false when agent omitted cost: %+v", got[0])
	}
}

// TestSessionUpdate_UsageUpdateCarriesCost 验证 Cost 指针被正确翻译，
// 且"报了 0"与"没报"可区分（HasCost 是唯一判据）。
func TestSessionUpdate_UsageUpdateCarriesCost(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	var got []UsageInfo
	a.OnUsageUpdate(func(u UsageInfo) { got = append(got, u) })

	cost := &acp.Cost{Amount: 1.25, Currency: "USD"}
	if err := a.SessionUpdate(context.Background(), usageNotification("s", 10, 100, cost)); err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
	if len(got) != 1 || !got[0].HasCost || got[0].CostUSD != 1.25 {
		t.Fatalf("got=%+v want HasCost=true CostUSD=1.25", got)
	}
}

// TestSessionUpdate_UsageUpdateZeroCostIsReported 验证 agent 明确报 amount=0 时
// HasCost 仍为 true —— "成本就是 0"与"agent 没报成本"是两件事，
// 混为一谈会让 UI 把未知成本渲染成"免费"。
func TestSessionUpdate_UsageUpdateZeroCostIsReported(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	var got []UsageInfo
	a.OnUsageUpdate(func(u UsageInfo) { got = append(got, u) })

	if err := a.SessionUpdate(context.Background(), usageNotification("s", 10, 100, &acp.Cost{Amount: 0, Currency: "USD"})); err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
	if len(got) != 1 || !got[0].HasCost {
		t.Fatalf("got=%+v want HasCost=true for explicit zero cost", got)
	}
}

// TestSessionUpdate_UsageUpdateZeroSizeDropped 验证 Size<=0 的快照被丢弃。
//
// 判据：协议里 Size 是必填，但实现可能给 0（"不知道窗口多大"）。此时
// "占用了 12345/0" 是个荒谬的显示，宁可整条不发，让前端保持"暂无用量"。
func TestSessionUpdate_UsageUpdateZeroSizeDropped(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	var got []UsageInfo
	a.OnUsageUpdate(func(u UsageInfo) { got = append(got, u) })

	if err := a.SessionUpdate(context.Background(), usageNotification("s", 12345, 0, nil)); err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d updates, want 0 (Size=0 must be dropped): %+v", len(got), got)
	}
}

// TestSessionUpdate_UsageUpdateNilCallbackNoPanic 验证未注册回调时静默丢弃、不 panic。
// 标题生成等非任务场景不会注册该回调，这条路径必须是安全的。
func TestSessionUpdate_UsageUpdateNilCallbackNoPanic(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	if err := a.SessionUpdate(context.Background(), usageNotification("s", 1, 100, nil)); err != nil {
		t.Fatalf("SessionUpdate with nil callback: %v", err)
	}
}

// TestClose_ClearsCallbacks 验证 Close 摘除全部回调。
//
// 判据：进程已结束，迟到的 update 不应再写进已收尾的 task。
// 这里是 usage 的回归位（其余回调同批摘除）。
func TestClose_ClearsCallbacks(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	fired := false
	a.OnUsageUpdate(func(UsageInfo) { fired = true })
	a.OnContentDelta(func(ContentDelta) { fired = true })

	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close 后回调应已摘除，即使再投递也不触发。
	_ = a.SessionUpdate(context.Background(), usageNotification("s", 1, 100, nil))
	if fired {
		t.Error("usage callback fired after Close; callbacks must be cleared")
	}
}

// 编译期断言：ACPAgent 实现 UsageReporter（可选能力接口）。
var _ UsageReporter = (*ACPAgent)(nil)
