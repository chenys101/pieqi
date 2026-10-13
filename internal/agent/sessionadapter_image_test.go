// sessionadapter_image_test.go adapter 层的图片能力透传。
//
// ★ 为什么这一层必须单独测：AgentManager 断言的是 **adapter**，不是 AgentSession。
//     RunRich:             sess.adapter.(RichPromptSender)   → SendRichPrompt(ctx, sessionID, text, images)
//     SupportsImagePrompt: sess.adapter.(ImagePromptCapable) → SupportsImagePrompt()
//   而 AgentSession 上的对应方法叫 PromptRich(ctx, text, images) —— **签名不同**。
//   两层名字相近、语义相近，却不可互相替代；漏掉 adapter 层的转发时，
//   各层自己的单测**全都是绿的**，只有线上发图会失败。这正是本文件存在的理由。
//
// 复用既有的 fakeSession4Adapter（它已带 lastImages 与 PromptRich），
// 不另造一个平行替身 —— 两个替身会让"到底哪个代表真实底层"变得模糊。
package agent

import (
	"context"
	"testing"
)

// imgCapableSession4Adapter 在 fakeSession4Adapter 之上声明图片能力。
// 分开一个类型而不是给原替身加字段：原替身被多个用例共享，
// 给它加"能收图"会让那些用例的语义悄悄变化。
type imgCapableSession4Adapter struct {
	*fakeSession4Adapter
	capable bool
}

func (s *imgCapableSession4Adapter) SupportsImagePrompt() bool { return s.capable }

// imageAdapterFor 用给定会话构造 adapter（open 注入，避免真起进程）。
func imageAdapterFor(sess AgentSession) *sessionBackedAdapter {
	return newSessionBackedAdapter("claude", func(context.Context, OpenParams) (AgentSession, error) {
		return sess, nil
	})
}

func newCapableSession4Adapter(capable bool) *imgCapableSession4Adapter {
	return &imgCapableSession4Adapter{fakeSession4Adapter: &fakeSession4Adapter{id: "s1"}, capable: capable}
}

// TestSessionBackedAdapterForwardsRichPrompt 带图轮必须走到底层会话的 PromptRich。
func TestSessionBackedAdapterForwardsRichPrompt(t *testing.T) {
	inner := newCapableSession4Adapter(true)
	a := imageAdapterFor(inner)
	if _, err := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	sender, ok := AgentAdapter(a).(RichPromptSender)
	if !ok {
		t.Fatal("sessionBackedAdapter 未实现 RichPromptSender：RunRich 会判定「当前 agent 传输不支持图片」")
	}
	imgs := []ImageInput{{Data: "AAAA", MimeType: "image/png"}}
	if err := sender.SendRichPrompt(context.Background(), a.sid, "看图", imgs); err != nil {
		t.Fatalf("SendRichPrompt: %v", err)
	}
	// 底层替身记录的是张数：为 1 才算真的透传下去了
	if inner.lastImages != 1 {
		t.Fatalf("底层收到 %d 张图，want 1（图在 adapter 层被丢了）", inner.lastImages)
	}
}

// TestSessionBackedAdapterForwardsImageCapability 能力位必须透传，且随底层变化。
func TestSessionBackedAdapterForwardsImageCapability(t *testing.T) {
	for _, capable := range []bool{true, false} {
		inner := newCapableSession4Adapter(capable)
		a := imageAdapterFor(inner)
		if _, err := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		got := AgentAdapter(a).(ImagePromptCapable).SupportsImagePrompt()
		if got != capable {
			t.Errorf("底层 SupportsImagePrompt=%v，adapter 报 %v（未透传）", capable, got)
		}
	}
}

// TestSessionBackedAdapterImageCapabilityFalseWhenUnimplemented 底层没声明能力时必须报 false。
//
// 这就是 print 回退那条路：PrintAgent 不实现 SupportsImagePrompt（只实现 AgentAdapter），
// 经 sessionBackedAdapter 包起来后必须报 false。若报 true，前端会显示加图入口，
// 而回退后发图必然运行时失败 —— 表现就是"功能时好时坏"。
func TestSessionBackedAdapterImageCapabilityFalseWhenUnimplemented(t *testing.T) {
	// 用**未**声明能力的替身：只实现 AgentSession，不实现 SupportsImagePrompt
	a := imageAdapterFor(&fakeSession4Adapter{id: "s2"})
	if _, err := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if AgentAdapter(a).(ImagePromptCapable).SupportsImagePrompt() {
		t.Error("底层未声明图片能力，adapter 却报 true：回退路径会显示入口但发不出去")
	}
}

// TestSessionBackedAdapterNoSessionImagePath 未建会话时：能力位 false，发图明确报错（不 panic）。
func TestSessionBackedAdapterNoSessionImagePath(t *testing.T) {
	a := imageAdapterFor(newCapableSession4Adapter(true))
	if AgentAdapter(a).(ImagePromptCapable).SupportsImagePrompt() {
		t.Error("未建会话时应为 false（保守侧：还没确认的能力一律当作没有）")
	}
	err := AgentAdapter(a).(RichPromptSender).SendRichPrompt(
		context.Background(), "s", "x", []ImageInput{{Data: "AAAA", MimeType: "image/png"}})
	if err == nil {
		t.Fatal("未建会话时 SendRichPrompt 应报错（而不是静默成功）")
	}
}
