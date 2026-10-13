// fallback_hint_real_test.go 用**真实装配链**验证降级提示（不打桩）。
//
// 为什么要有这一条：fallback_hint_test.go（在 agent 包）用的是测试替身，而替身很容易
// "恰好"满足被测分支 —— 我第一版就把理由判断写在了**永远走不到**的分支上，替身照样绿。
// 这里改成真配置 + 真 Open：
//   - 真 Configure 把桥地址指向一个**没人监听**的端口 → openSession 真的探活失败 → 真的回退 print；
//   - 真 agent.Open("claude") → 拿到真实的降级会话，直接问它的降级理由与图片能力。
//
// ⚠️ 本测试必须在 claude 包内：agent 包 import 了 claude（provider 注册），反过来会成环。
package claude

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pieqi/internal/agent"
)

// 关掉的端口（探活必然失败）：高位端口，且不监听。
const deadBridgeURL = "http://127.0.0.1:19997"

func TestBridgeDownImageRejectionHasActionableHint(t *testing.T) {
	prev := defaultConfig
	// 桥地址指向死端口 → Health 失败 → 回退 print（线上桥挂掉时的真实路径）
	Configure(Config{Transport: "sdk-bridge", BaseURL: deadBridgeURL, Print: agent.PrintConfig{Binary: "claude"}})
	defer func() { defaultConfig = prev }()

	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("agent.Open（应回退 print 并成功）: %v", err)
	}
	defer sess.Close(context.Background())

	// 降级理由必须挂在**会话本身**上：调用方（agent 包的 manager）拿到的就是这个对象，
	// 它若说不出理由，最终提示就只能退回无用的类型名。
	r, ok := sess.(interface{ ImagePromptUnavailableReason() string })
	if !ok {
		t.Fatal("回退到 print 的会话未提供 ImagePromptUnavailableReason：上层只能给出无用的类型名")
	}
	reason := r.ImagePromptUnavailableReason()
	if reason == "" {
		t.Fatal("理由为空：桥不可用的降级态没被记录下来")
	}
	if !strings.Contains(reason, "桥") {
		t.Errorf("理由 %q 应点明是桥的问题（否则用户不知道该等什么恢复）", reason)
	}
	if !strings.Contains(reason, "claude -p") {
		t.Errorf("理由 %q 应点明退到了哪条通道（用户才知道为什么图发不了）", reason)
	}

	// 真实 print 回退会话：带图必须**明确报错**（绝不静默丢图）
	err = sess.PromptRich(context.Background(), "看图", []agent.ImageInput{{Data: "AAAA", MimeType: "image/png"}})
	if err == nil {
		t.Fatal("print 回退下带图应当报错（不能静默丢图）")
	}
	if !errors.Is(err, agent.ErrImageNotSupported) {
		t.Errorf("错误应可判为 ErrImageNotSupported（API 据此映射 400）：%v", err)
	}
	// 能力位必须仍为否（否则前端会显示一个必然失败的入口）
	if capable, ok := sess.(interface{ SupportsImagePrompt() bool }); ok && capable.SupportsImagePrompt() {
		t.Error("print 回退会话声称支持收图：前端会显示入口，用户选了图才失败")
	}
}

// TestExplicitPrintTransportHasNoDegradedHint 显式配成 print **不算降级**，不该给"重试"提示。
//
// 判据：用户自己配的 print 是稳定状态，提示「稍后重试（桥恢复后自动可用）」是**误导** ——
// 桥根本没被使用过。两种情况必须能区分开，否则用户会一直白等一个不会发生的变化。
func TestExplicitPrintTransportHasNoDegradedHint(t *testing.T) {
	prev := defaultConfig
	Configure(Config{Transport: "print", Print: agent.PrintConfig{Binary: "claude"}})
	defer func() { defaultConfig = prev }()

	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("agent.Open(print): %v", err)
	}
	defer sess.Close(context.Background())

	if r, ok := sess.(interface{ ImagePromptUnavailableReason() string }); ok {
		if got := r.ImagePromptUnavailableReason(); got != "" {
			t.Errorf("显式 print 不是降级，不该有理由（got %q）—— 否则会提示用户「重试等桥恢复」，而桥压根没参与", got)
		}
	}
	// 但带图仍然要明确报错（能力确实没有）
	if err := sess.PromptRich(context.Background(), "看图", []agent.ImageInput{{Data: "AAAA", MimeType: "image/png"}}); err == nil {
		t.Fatal("print 传输带图应当报错")
	}
}
