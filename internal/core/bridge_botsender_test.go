package core

import (
	"context"
	"testing"

	"pieqi/internal/channel"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// newRoutingBridge 建一个只关心发送路由的 Bridge。
func newRoutingBridge() *Bridge { return NewBridge(zap.NewNop()) }

// fakeBotReceiver 同时实现 MessageReceiver + MessageSender，用于验证
// 「绑定回调」与「注册进渠道发送表」是两件事。
type fakeBotReceiver struct {
	name string
	cb   func(model.Message)
	sent []string
}

func (f *fakeBotReceiver) Name() string                       { return f.name }
func (f *fakeBotReceiver) Init(gin.IRouter) error             { return nil }
func (f *fakeBotReceiver) Start(ctx context.Context) error    { return nil }
func (f *fakeBotReceiver) OnMessage(cb func(model.Message))   { f.cb = cb }
func (f *fakeBotReceiver) Send(_ context.Context, _ model.ReplyTarget, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

var (
	_ channel.MessageReceiver = (*fakeBotReceiver)(nil)
	_ channel.MessageSender   = (*fakeBotReceiver)(nil)
)

// 多机器人回执不串台：A 台收到的消息必须由 A 台回。
func TestReplyRoutesToOwningBot(t *testing.T) {
	b := newRoutingBridge()
	a, other := &fakeSender{}, &fakeSender{}
	b.SyncBotSenders([]BotSenderRef{
		{BotID: "bot_a", Channel: "lark", Sender: a},
		{BotID: "bot_b", Channel: "lark", Sender: other},
	})

	b.reply(model.Message{Channel: model.ChannelLark, BotID: "bot_b", ChatID: "oc_1"}, "hi")

	if len(other.sent) != 1 {
		t.Fatalf("bot_b should send, got %v", other.sent)
	}
	if len(a.sent) != 0 {
		t.Fatalf("bot_a must not send bot_b's reply, got %v", a.sent)
	}
}

// 未知 bot id 回退渠道级 sender（实例刚被删 / 重启竞态也别把回执丢了）。
func TestReplyFallsBackToChannelSender(t *testing.T) {
	b := newRoutingBridge()
	chSender := &fakeSender{}
	b.RegisterSender("lark", chSender)
	botA := &fakeSender{}
	b.SyncBotSenders([]BotSenderRef{{BotID: "bot_a", Channel: "lark", Sender: botA}})

	b.reply(model.Message{Channel: model.ChannelLark, BotID: "bot_gone", ChatID: "oc_1"}, "hi")

	if len(chSender.sent) != 1 {
		t.Fatalf("should fall back to channel sender, got %v", chSender.sent)
	}
	if len(botA.sent) != 0 {
		t.Fatalf("bot_a must not be used for another bot's id, got %v", botA.sent)
	}
}

// 无渠道级实例时，渠道名寻址回退到该渠道的管理员机器人 ——
// Sender("lark") 拿到 nil 会让 outcome 推送静默失效。
func TestSenderFallsBackToAdminBot(t *testing.T) {
	b := newRoutingBridge()
	admin, member := &fakeSender{}, &fakeSender{}
	b.EnableBotRouting(&fakeBotResolver{id: "bot_admin"})
	b.SyncBotSenders([]BotSenderRef{
		{BotID: "bot_member", Channel: "lark", Sender: member},
		{BotID: "bot_admin", Channel: "lark", Sender: admin},
	})

	got, ok := b.Sender("lark")
	if !ok {
		t.Fatal("Sender(lark) must resolve in multi-bot mode")
	}
	if got != channel.MessageSender(admin) {
		t.Fatal("Sender(lark) should fall back to the admin bot")
	}
}

// 没有管理员时的确定回退：按注册顺序取该渠道第一台（不依赖 map 遍历顺序）。
func TestSenderFallsBackToFirstBotOfChannel(t *testing.T) {
	b := newRoutingBridge()
	first, second := &fakeSender{}, &fakeSender{}
	b.SyncBotSenders([]BotSenderRef{
		{BotID: "bot_1", Channel: "lark", Sender: first},
		{BotID: "bot_2", Channel: "lark", Sender: second},
	})

	got, ok := b.Sender("lark")
	if !ok || got != channel.MessageSender(first) {
		t.Fatal("without an admin, Sender(channel) must fall back to the first registered instance")
	}
}

// 回退不得跨渠道：管理员是另一渠道的机器人时，lark 仍取 lark 自己的实例。
func TestSenderFallbackDoesNotCrossChannels(t *testing.T) {
	b := newRoutingBridge()
	larkBot, wxBot := &fakeSender{}, &fakeSender{}
	b.EnableBotRouting(&fakeBotResolver{id: "bot_wx"})
	b.SyncBotSenders([]BotSenderRef{
		{BotID: "bot_wx", Channel: "wechat", Sender: wxBot},
		{BotID: "bot_lark", Channel: "lark", Sender: larkBot},
	})

	got, ok := b.Sender("lark")
	if !ok || got != channel.MessageSender(larkBot) {
		t.Fatal("fallback must stay within the requested channel")
	}
}

// SyncBotSenders 是整体替换：旧实例立刻从路由表消失。
func TestSyncBotSendersReplaces(t *testing.T) {
	b := newRoutingBridge()
	old, fresh := &fakeSender{}, &fakeSender{}
	b.SyncBotSenders([]BotSenderRef{{BotID: "bot_a", Channel: "lark", Sender: old}})
	b.SyncBotSenders([]BotSenderRef{{BotID: "bot_a", Channel: "lark", Sender: fresh}})

	b.reply(model.Message{Channel: model.ChannelLark, BotID: "bot_a", ChatID: "oc_1"}, "hi")
	if len(old.sent) != 0 {
		t.Fatalf("replaced instance must not send, got %v", old.sent)
	}
	if len(fresh.sent) != 1 {
		t.Fatalf("new instance should send, got %v", fresh.sent)
	}
}

// 空 BotID / nil sender 不入口路由表（渠道级实例走 senders）。
func TestSyncBotSendersSkipsInvalid(t *testing.T) {
	b := newRoutingBridge()
	b.SyncBotSenders([]BotSenderRef{
		{BotID: "", Channel: "lark", Sender: &fakeSender{}},
		{BotID: "bot_a", Channel: "lark", Sender: nil},
	})
	if _, ok := b.Sender("lark"); ok {
		t.Fatal("invalid refs must not enter the bot routing table")
	}
}

// BindReceiver 只接管回调，不写入按渠道名的发送表 ——
// 多机器人下每台都注册进渠道名会互相挤掉，回执全串到最后一台。
func TestBindReceiverDoesNotRegisterChannelSender(t *testing.T) {
	b := newRoutingBridge()
	r := &fakeBotReceiver{name: "lark"}
	b.BindReceiver(r)

	if _, ok := b.Sender("lark"); ok {
		t.Fatal("BindReceiver must not register a channel-level sender")
	}
	if r.cb == nil {
		t.Fatal("BindReceiver must wire the message callback")
	}
}

// RegisterReceiver 仍保持渠道级语义（渠道名可寻址），回归保护。
func TestRegisterReceiverStillRegistersChannelSender(t *testing.T) {
	b := newRoutingBridge()
	r := &fakeBotReceiver{name: "lark"}
	b.RegisterReceiver(r)

	got, ok := b.Sender("lark")
	if !ok || got != channel.MessageSender(r) {
		t.Fatal("RegisterReceiver must keep registering the channel-level sender")
	}
}
