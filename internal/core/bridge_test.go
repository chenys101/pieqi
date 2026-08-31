package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"pieqi/internal/auth"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// --- fakes ---

type fakeSender struct {
	sent    []string
	chatIDs []string
}

func (f *fakeSender) Send(_ context.Context, target model.ReplyTarget, text string) error {
	f.sent = append(f.sent, text)
	f.chatIDs = append(f.chatIDs, target.ChatID)
	return nil
}

type fakeTunnel struct {
	startCalled bool
	stopCalled  bool
	renewCalled bool
	result      auth.TunnelResult
	startErr    error
	stopErr     error
	renewErr    error
}

func (f *fakeTunnel) Start(_ context.Context, ttl time.Duration) (auth.TunnelResult, error) {
	f.startCalled = true
	if f.startErr != nil {
		return auth.TunnelResult{}, f.startErr
	}
	return auth.TunnelResult{
		TunnelURL:    "https://abc.trycloudflare.com?token=t1",
		LarkDeepLink: "lark://open?url=https%3A%2F%2Fabc.trycloudflare.com%3Ftoken%3Dt1",
		Token:        "t1",
		ExpiresAt:    time.Now().Add(ttl),
	}, nil
}

func (f *fakeTunnel) Stop(_ context.Context) error {
	f.stopCalled = true
	return f.stopErr
}

func (f *fakeTunnel) RenewToken(_ context.Context, ttl time.Duration) (auth.TunnelResult, error) {
	f.renewCalled = true
	if f.renewErr != nil {
		return auth.TunnelResult{}, f.renewErr
	}
	return auth.TunnelResult{
		TunnelURL:    "https://abc.trycloudflare.com?token=t1", // 续期不换 token/URL
		LarkDeepLink: "lark://open?url=https%3A%2F%2Fabc.trycloudflare.com%3Ftoken%3Dt1",
		Token:        "t1",
		ExpiresAt:    time.Now().Add(ttl),
	}, nil
}

type fakeAdmin struct{ openid string }

func (f *fakeAdmin) Match(openid string) bool { return f.openid == openid }

// --- helpers ---

func newTestBridge(t *testing.T, tunnel TunnelOps, admin AdminBinding) (*Bridge, *fakeSender) {
	t.Helper()
	b := NewBridge(zap.NewNop())
	sender := &fakeSender{}
	b.RegisterSender("lark", sender)
	b.EnableTunnelOps(tunnel, admin)
	return b, sender
}

func larkMsg(userID string) model.Message {
	return model.Message{Channel: model.ChannelLark, UserID: userID, ChatID: "oc_test"}
}

// --- parseTunnelCommand ---

func TestParseTunnelCommand(t *testing.T) {
	cases := []struct {
		in     string
		wantOp string
		wantTTL time.Duration
	}{
		{"隧道", "start", 15 * time.Minute},
		{"tunnel", "start", 15 * time.Minute},
		{"/tunnel", "start", 15 * time.Minute},
		{"隧道 1h", "start", time.Hour},
		{"/tunnel 4h", "start", 4 * time.Hour},
		{"tunnel 60m", "start", time.Hour},
		{"隧道 2h", "start", 15 * time.Minute}, // 非法 TTL → 回退默认
		{"关隧道", "stop", 0},
		{"停隧道", "stop", 0},
		{"tunnel stop", "stop", 0},
		{"/tunnel stop", "stop", 0},
		{"续期", "renew", 15 * time.Minute},
		{"延期", "renew", 15 * time.Minute},
		{"续隧道", "renew", 15 * time.Minute},
		{"tunnel renew", "renew", 15 * time.Minute},
		{"/tunnel renew", "renew", 15 * time.Minute},
		{"续期 1h", "renew", time.Hour},
		{"续期 4h", "renew", 4 * time.Hour},
		{"你好", "", 0},
		{"帮我开个隧道", "", 0}, // 前缀不含空格，不算命令
		{"", "", 0},
	}
	for _, c := range cases {
		op, ttl := parseTunnelCommand(c.in)
		if op != c.wantOp || ttl != c.wantTTL {
			t.Errorf("parseTunnelCommand(%q) = (%q, %v), want (%q, %v)", c.in, op, ttl, c.wantOp, c.wantTTL)
		}
	}
}

// --- handleTunnelCommand ---

func TestTunnelCommand_AdminStartRepliesDeepLink(t *testing.T) {
	tun := &fakeTunnel{}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.handlePieqiMessage(larkMsg("ou_admin"), "隧道")

	if !tun.startCalled {
		t.Fatal("Start must be called")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("got %d replies, want 1: %v", len(sender.sent), sender.sent)
	}
	reply := sender.sent[0]
	if !strings.Contains(reply, "lark://open?url=") {
		t.Errorf("reply should contain lark deep link: %s", reply)
	}
	if !strings.Contains(reply, "trycloudflare.com") {
		t.Errorf("reply should contain tunnel url: %s", reply)
	}
}

func TestTunnelCommand_NonAdminRejected(t *testing.T) {
	tun := &fakeTunnel{}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.handlePieqiMessage(larkMsg("ou_other"), "隧道")

	if tun.startCalled {
		t.Fatal("Start must NOT be called for non-admin")
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "仅绑定") {
		t.Errorf("non-admin should be rejected: %v", sender.sent)
	}
}

func TestTunnelCommand_UnboundRejected(t *testing.T) {
	// 未绑定任何管理员（adminBinding 无匹配）
	b, sender := newTestBridge(t, &fakeTunnel{}, &fakeAdmin{openid: ""})
	b.handlePieqiMessage(larkMsg("ou_any"), "隧道")
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "仅绑定") {
		t.Errorf("unbound should be rejected: %v", sender.sent)
	}
}

func TestTunnelCommand_NonLarkChannelRejected(t *testing.T) {
	tun := &fakeTunnel{}
	b := NewBridge(zap.NewNop())
	wxSender := &fakeSender{}
	b.RegisterSender("wechat", wxSender) // 微信渠道有 sender，但隧道命令仍须拒绝
	b.EnableTunnelOps(tun, &fakeAdmin{openid: "ou_admin"})
	// 微信渠道消息（UserID 不是飞书 open_id）
	b.handlePieqiMessage(model.Message{Channel: model.ChannelWeChat, UserID: "wx_user", ChatID: "wx_1"}, "隧道")
	if tun.startCalled {
		t.Fatal("tunnel must not start from non-lark channel")
	}
	if len(wxSender.sent) != 1 || !strings.Contains(wxSender.sent[0], "仅绑定") {
		t.Errorf("non-lark channel should be rejected: %v", wxSender.sent)
	}
}

func TestTunnelCommand_Stop(t *testing.T) {
	tun := &fakeTunnel{}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.handlePieqiMessage(larkMsg("ou_admin"), "关隧道")

	if !tun.stopCalled {
		t.Fatal("Stop must be called")
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "已关闭") {
		t.Errorf("stop reply wrong: %v", sender.sent)
	}
}

func TestTunnelCommand_Renew(t *testing.T) {
	tun := &fakeTunnel{}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.handlePieqiMessage(larkMsg("ou_admin"), "续期 1h")

	if !tun.renewCalled {
		t.Fatal("RenewToken must be called")
	}
	if tun.startCalled {
		t.Fatal("renew must NOT trigger start")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("got %d replies, want 1: %v", len(sender.sent), sender.sent)
	}
	reply := sender.sent[0]
	// 与启动同构：包含深链 + 隧道 URL + 到期时间
	for _, want := range []string{"续期", "lark://open?url=", "trycloudflare.com", "新到期"} {
		if !strings.Contains(reply, want) {
			t.Errorf("renew reply missing %q: %s", want, reply)
		}
	}
}

func TestTunnelCommand_RenewErrorRepliesError(t *testing.T) {
	tun := &fakeTunnel{renewErr: context.DeadlineExceeded}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.handlePieqiMessage(larkMsg("ou_admin"), "续期")

	if !tun.renewCalled {
		t.Fatal("RenewToken must be called")
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "续期失败") {
		t.Errorf("renew error reply wrong: %v", sender.sent)
	}
}

func TestTunnelCommand_StartErrorRepliesError(t *testing.T) {
	tun := &fakeTunnel{startErr: context.DeadlineExceeded}
	b, sender := newTestBridge(t, tun, &fakeAdmin{openid: "ou_admin"})
	b.handlePieqiMessage(larkMsg("ou_admin"), "隧道")

	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "启动失败") {
		t.Errorf("start error reply wrong: %v", sender.sent)
	}
}

func TestTunnelCommand_NotEnabled(t *testing.T) {
	// tunnel 未注入（nil）→ 提示未启用
	b, sender := newTestBridge(t, nil, &fakeAdmin{openid: "ou_admin"})
	b.handlePieqiMessage(larkMsg("ou_admin"), "隧道")
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "未启用") {
		t.Errorf("disabled tunnel reply wrong: %v", sender.sent)
	}
}

func TestHandlePieqiMessage_NonCommandGuide(t *testing.T) {
	b, sender := newTestBridge(t, &fakeTunnel{}, &fakeAdmin{openid: "ou_admin"})
	b.handlePieqiMessage(larkMsg("ou_admin"), "帮我看下任务")
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "PWA") {
		t.Errorf("non-command message should get PWA guide: %v", sender.sent)
	}
}
