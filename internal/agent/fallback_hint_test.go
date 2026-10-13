// fallback_hint_test.go 「发图失败」提示的可行动性。
//
// 背景：桥不可用时会话回退到 claude -p（纯文本通道），此时发图必然失败。旧文案是
// 「当前 agent 传输不支持图片（*agent.PrintAgent）」—— 类型名对用户毫无意义，
// 且听起来像"这个 agent 天生不支持"，而实际只是**这一次**走了降级、重试就能好。
//
// ★ 本文件同时钉住一个**极易踩空**的实现陷阱：
//   claude 的桥→print 回退发生在 agent.Open **内部**（provider 自己回退），
//   所以 AgentManager 层的 fellBack 恒为 false。任何"用 m.FellBack(taskID) 判断
//   要不要给降级提示"的写法都会**永远不触发**（测试若只断言"有错误"也会全绿）。
//   理由必须由会话自己提供（ImagePromptUnavailableReason）。
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// degradedFakeSession 模拟"降级到纯文本通道、能说清为什么"的会话。
//
// PromptRich 必须像真实 print 路径那样返回 ErrImageNotSupported（见 session.go 的
// sessionAdapter.PromptRich）—— 否则测的就不是线上那条路：
// 真实情况下 manager 的 RichPromptSender 断言是**成功**的，错误从 PromptRich 返回。
type degradedFakeSession struct {
	*fakeSession4Adapter
	reason string
}

func (d *degradedFakeSession) ImagePromptUnavailableReason() string { return d.reason }

func (d *degradedFakeSession) PromptRich(ctx context.Context, text string, images []ImageInput) error {
	if len(images) == 0 {
		return d.fakeSession4Adapter.Prompt(ctx, text)
	}
	return fmt.Errorf("%w: claude 桥传输只支持纯文本 prompt", ErrImageNotSupported)
}

var _ ImagePromptUnavailableReason = (*degradedFakeSession)(nil)

// TestRunRich_DegradedSessionHintMentionsFallback 降级会话的提示必须点明"回退/稍后重试"。
func TestRunRich_DegradedSessionHintMentionsFallback(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	m.primary = func() (AgentAdapter, AgentKind, error) {
		inner := &degradedFakeSession{
			fakeSession4Adapter: &fakeSession4Adapter{id: "s1"},
			reason:              "Claude 桥当前不可用，本次已回退到 claude -p（纯文本通道，无法发送图片）",
		}
		return newSessionBackedAdapter("claude", func(context.Context, OpenParams) (AgentSession, error) {
			return inner, nil
		}), AgentKindSession, nil
	}
	if _, _, err := m.Open(context.Background(), "t1", "p1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close("t1")

	err := m.RunRich(context.Background(), "t1", "看图", "", []ImageInput{{Data: "AAAA", MimeType: "image/png"}})
	if err == nil {
		t.Fatal("降级会话带图应当报错")
	}
	if !errors.Is(err, ErrImageNotSupported) {
		t.Errorf("错误应可被 errors.Is 判为 ErrImageNotSupported（API 据此映射状态码）：%v", err)
	}
	msg := err.Error()
	// 提示必须让用户知道"怎么做"，而不是只知道"失败了"
	for _, want := range []string{"桥", "回退", "重试"} {
		if !strings.Contains(msg, want) {
			t.Errorf("提示 %q 应包含 %q（用户需要知道这是可重试的降级，而不是功能坏了）", msg, want)
		}
	}
}

// TestRunRich_NonDegradedKeepsGenericHint 非降级的"不支持"仍走通用文案（别把两种情况混为一谈）。
func TestRunRich_NonDegradedKeepsGenericHint(t *testing.T) {
	m := NewAgentManager(ManagerConfig{}, nil)
	m.primary = func() (AgentAdapter, AgentKind, error) {
		// 收不了图、且**不说**为什么（没有 ImagePromptUnavailableReason）。
		// PromptRich 必须像真实不支持路径那样返回 ErrImageNotSupported ——
		// 否则 manager 那边会"成功"，测不到文案分支。
		inner := &plainUnsupportedSession{fakeSession4Adapter: &fakeSession4Adapter{id: "s2"}}
		return newSessionBackedAdapter("claude", func(context.Context, OpenParams) (AgentSession, error) {
			return inner, nil
		}), AgentKindSession, nil
	}
	if _, _, err := m.Open(context.Background(), "t2", "p1", SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close("t2")

	err := m.RunRich(context.Background(), "t2", "看图", "", []ImageInput{{Data: "AAAA", MimeType: "image/png"}})
	if err == nil {
		t.Fatal("应当报错")
	}
	msg := err.Error()
	if strings.Contains(msg, "回退") {
		t.Errorf("非降级场景不该提「回退」（会误导用户去重试一个不会变好的事）：%q", msg)
	}
	// 无理由时**原样透传**底层文案（不加工、不覆盖）—— 底层比上层更清楚自己为什么不行。
	// 这里钉的是"没有被我们改写"，而不是某个具体措辞。
	if !strings.Contains(msg, "该传输只支持纯文本 prompt") {
		t.Errorf("无理由时应原样透传底层说明：%q", msg)
	}
	if !errors.Is(err, ErrImageNotSupported) {
		t.Errorf("必须可判为 ErrImageNotSupported（API 据此映射状态码）：%v", err)
	}
}

// plainUnsupportedSession 收不了图、且没有理由可说的会话（无 ImagePromptUnavailableReason）。
type plainUnsupportedSession struct {
	*fakeSession4Adapter
}

func (s *plainUnsupportedSession) PromptRich(ctx context.Context, text string, images []ImageInput) error {
	if len(images) == 0 {
		return s.fakeSession4Adapter.Prompt(ctx, text)
	}
	return fmt.Errorf("%w: 该传输只支持纯文本 prompt", ErrImageNotSupported)
}

// TestSessionBackedAdapterForwardsUnavailableReason 降级理由必须经 adapter 透传。
//
// 与 SupportsImagePrompt 同一模式：manager 拿到的是 adapter（不是底层 AgentSession），
// 不显式转发的话上层永远拿不到理由 —— 提示又退化成类型名。
func TestSessionBackedAdapterForwardsUnavailableReason(t *testing.T) {
	inner := &degradedFakeSession{
		fakeSession4Adapter: &fakeSession4Adapter{id: "s1"},
		reason:              "桥当前不可用",
	}
	a := imageAdapterFor(inner)
	if _, err := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	r, ok := AgentAdapter(a).(ImagePromptUnavailableReason)
	if !ok {
		t.Fatal("sessionBackedAdapter 未实现 ImagePromptUnavailableReason：降级理由传不到上层")
	}
	if got := r.ImagePromptUnavailableReason(); got != "桥当前不可用" {
		t.Errorf("理由=%q，want 原样透传", got)
	}

	// 底层没有理由时必须返回空串（调用方据此回落通用文案），而不是 panic 或瞎编
	plain := imageAdapterFor(&fakeSession4Adapter{id: "s2"})
	if _, err := plain.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := AgentAdapter(plain).(ImagePromptUnavailableReason).ImagePromptUnavailableReason(); got != "" {
		t.Errorf("底层无理由时应返回空串，got %q", got)
	}
}
