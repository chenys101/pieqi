package core

import (
	"strings"
	"testing"

	"pieqi/internal/model"
)

// fakeBotResolver 实现 AdminBotResolver（真身是 *BotStore）。
type fakeBotResolver struct{ id string }

func (f *fakeBotResolver) AdminBotID() string { return f.id }

// Q2：特权只由**管理员机器人**承载 —— 发送者本人是管理员，但消息由 member
// 机器人收到时，仍须拒绝。
func TestTunnelCommand_NonAdminBotRejected(t *testing.T) {
	tun := &fakeTunnel{}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.EnableBotRouting(&fakeBotResolver{id: "bot_admin"})

	msg := larkMsg("ou_admin")
	msg.BotID = "bot_member"
	b.handlePieqiMessage(msg, "隧道")

	if tun.startCalled {
		t.Fatal("tunnel must NOT start when the message arrived at a non-admin bot")
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "无权操作") {
		t.Errorf("member bot should reject with 无权操作: %v", sender.sent)
	}
}

// 管理员机器人收到的命令正常放行。
func TestTunnelCommand_AdminBotAccepted(t *testing.T) {
	tun := &fakeTunnel{}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.EnableBotRouting(&fakeBotResolver{id: "bot_admin"})

	msg := larkMsg("ou_admin")
	msg.BotID = "bot_admin"
	b.handlePieqiMessage(msg, "隧道")

	if !tun.startCalled {
		t.Fatal("tunnel must start when the message arrived at the admin bot")
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "trycloudflare.com") {
		t.Errorf("admin bot should get the deep link: %v", sender.sent)
	}
}

// BotID 为空 = 投递方尚未标注来源（单机器人 / 旧 receiver 路径）→ 不收紧。
// 这是**过渡语义**：改造 receiver 前不能让既有链路全部失效。
func TestTunnelCommand_UnstampedBotIDNotTightened(t *testing.T) {
	tun := &fakeTunnel{}
	b, _ := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.EnableBotRouting(&fakeBotResolver{id: "bot_admin"})

	b.handlePieqiMessage(larkMsg("ou_admin"), "隧道") // BotID 保持空

	if !tun.startCalled {
		t.Fatal("empty BotID must not be treated as a non-admin bot")
	}
}

// 尚未确立管理员机器人（id 为空）时同样不收紧。
func TestTunnelCommand_NoAdminBotConfigured(t *testing.T) {
	tun := &fakeTunnel{}
	b, _ := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.EnableBotRouting(&fakeBotResolver{id: ""})

	msg := larkMsg("ou_admin")
	msg.BotID = "bot_whatever"
	b.handlePieqiMessage(msg, "隧道")

	if !tun.startCalled {
		t.Fatal("no admin bot configured must not block the bound admin")
	}
}

// 第一道校验（人）优先于第二道（机器人）：非管理员从 member 机器人发命令，
// 应回「仅绑定」而不是「无权操作」——错误信息要指向真正的拒绝原因。
func TestTunnelCommand_PersonCheckTakesPrecedence(t *testing.T) {
	tun := &fakeTunnel{}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.EnableBotRouting(&fakeBotResolver{id: "bot_admin"})

	msg := larkMsg("ou_other")
	msg.BotID = "bot_member"
	b.handlePieqiMessage(msg, "隧道")

	if len(sender.sent) != 1 {
		t.Fatalf("want 1 reply, got %v", sender.sent)
	}
	if !strings.Contains(sender.sent[0], "仅绑定") {
		t.Errorf("person check must run first: %v", sender.sent)
	}
	if strings.Contains(sender.sent[0], "无权操作") {
		t.Errorf("must not report bot-level rejection when the sender is not admin: %v", sender.sent)
	}
}

// 与真身 BotStore 对接：首台自动成为 admin，其 id 即解析结果。
func TestTunnelCommand_WithRealBotStore(t *testing.T) {
	store, _ := newTestBotStore(t)
	first, err := store.Create(model.Bot{Channel: model.ChannelLark})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := store.Create(model.Bot{Channel: model.ChannelLark})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	tun := &fakeTunnel{}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.EnableBotRouting(store)

	msg := larkMsg("ou_admin")
	msg.BotID = second.ID
	b.handlePieqiMessage(msg, "隧道")
	if tun.startCalled {
		t.Fatal("second bot (member) must not be able to start the tunnel")
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "无权操作") {
		t.Errorf("member bot should be rejected: %v", sender.sent)
	}

	tun2 := &fakeTunnel{}
	b2, _ := newTestBridge(t, tun2, &fakeAdmin{openid: "ou_admin"})
	b2.EnableBotRouting(store)
	msg2 := larkMsg("ou_admin")
	msg2.BotID = first.ID
	b2.handlePieqiMessage(msg2, "隧道")
	if !tun2.startCalled {
		t.Fatal("first bot (admin) must be able to start the tunnel")
	}
}
