package agent

import (
	"context"
	"testing"
	"time"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

// toolInputWait 的回归测试。
//
// 背景（2026-10-07 实测）：SessionUpdate（写 toolInputs）与 RequestPermission
// （读 toolInputs）在 pieqi 内部**并发**。协议层的顺序保证（dsh 的 drainUpdates、
// acp-go-sdk 的通知屏障）只覆盖"线上先发完 update 再发请求"，
// **不覆盖 pieqi 两个回调的处理完成顺序**。
// 实测本会话 12:39:37 那次 push 的审批就跑赢了自己的 tool_call，
// 结果审批卡上没有命令原文 —— 用户无法判断批的是什么。

// TestToolInputWait_LateArrivalIsPickedUp ★ 核心回归：
// 审批先到、tool_call 稍后到 ⇒ 等待能把命令原文捞回来。
func TestToolInputWait_LateArrivalIsPickedUp(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	const want = `{"command":"git push origin main"}`
	// 100ms 后才写入（模拟 tool_call 晚于审批到达），远小于 300ms 上限。
	go func() {
		time.Sleep(100 * time.Millisecond)
		a.rememberToolInput("tc-late", []byte(want))
	}()

	start := time.Now()
	raw, ok := a.toolInputWait("tc-late")
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("tool_call 在等待窗口内到达，toolInputWait 应拿到它（这正是本次修复的靶心）")
	}
	if string(raw) != want {
		t.Fatalf("raw=%s want %s", raw, want)
	}
	// 必须是"被唤醒"而不是"睡满 300ms 再撞上" —— 否则等于每次都白等。
	if elapsed > 250*time.Millisecond {
		t.Errorf("耗时 %v，像是睡满超时才拿到；应被 Broadcast 立刻唤醒", elapsed)
	}
}

// TestToolInputWait_FastPathNoDelay 已在索引里时零延迟返回。
//
// 这是绝大多数情况（含 qoder/claude），必须不引入任何可感知延迟。
func TestToolInputWait_FastPathNoDelay(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	a.rememberToolInput("tc-1", []byte(`{"command":"ls"}`))

	start := time.Now()
	raw, ok := a.toolInputWait("tc-1")
	elapsed := time.Since(start)

	if !ok || string(raw) != `{"command":"ls"}` {
		t.Fatalf("ok=%v raw=%s", ok, raw)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("快路径耗时 %v，应接近 0", elapsed)
	}
}

// TestToolInputWait_TimesOutReturnsFalse 始终不来 ⇒ 超时返回 false（保守判级）。
//
// 不 panic、不永久阻塞、不猜。
func TestToolInputWait_TimesOutReturnsFalse(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	start := time.Now()
	raw, ok := a.toolInputWait("tc-never")
	elapsed := time.Since(start)

	if ok {
		t.Fatalf("不该拿到入参，got %s", raw)
	}
	if elapsed < toolInputWaitTimeout {
		t.Errorf("耗时 %v 短于超时 %v —— 应等满窗口再放弃", elapsed, toolInputWaitTimeout)
	}
	// 上限容差：不应显著超过设定值（否则审批会被拖慢）。
	if elapsed > toolInputWaitTimeout+300*time.Millisecond {
		t.Errorf("耗时 %v 远超上限 %v", elapsed, toolInputWaitTimeout)
	}
}

// TestToolInputWait_EmptyIDNoWait 空 toolCallId 立刻返回，不等待。
func TestToolInputWait_EmptyIDNoWait(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	start := time.Now()
	if _, ok := a.toolInputWait(""); ok {
		t.Fatal("空 id 不该拿到入参")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("空 id 耗时 %v，应立即返回", elapsed)
	}
}

// TestToolInputWait_CloseWakesWaiters Close 要唤醒等待方，不让它们干等超时。
func TestToolInputWait_CloseWakesWaiters(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	done := make(chan bool, 1)
	go func() {
		_, ok := a.toolInputWait("tc-1")
		done <- ok
	}()

	time.Sleep(50 * time.Millisecond) // 让等待方进入 Wait
	start := time.Now()
	_ = a.Close(context.Background())

	select {
	case ok := <-done:
		if ok {
			t.Fatal("Close 后不该拿到入参")
		}
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Errorf("Close 后等待方耗时 %v，应被立刻唤醒而非等满超时", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待方未被唤醒（Close 没有 Broadcast）")
	}
}

// TestRequestPermission_LateToolCallStillBackfills ★ 端到端：
// 审批请求先发生，tool_call 随后（带命令）到达 ⇒ 回调拿到的请求里**必须有命令原文**。
//
// 这是用户能感知的最终判据：审批卡上要显示得出"批的是什么"。
func TestRequestPermission_LateToolCallStillBackfills(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	gotReq := make(chan PermissionRequest, 1)
	a.OnPermissionRequest(func(r PermissionRequest) { gotReq <- r })

	// 审批先到：请求体不带任何入参（dsh 的真实形态）
	go func() {
		_, _ = a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall:  acp.ToolCallUpdate{ToolCallId: "tc-race"},
			Options:   allowOnceOpts(),
		})
	}()

	// 80ms 后 tool_call 才到（模拟 pieqi 内部并发导致的乱序）
	go func() {
		time.Sleep(80 * time.Millisecond)
		sendToolCall(t, a, "tc-race", map[string]any{"command": "git push origin main"})
	}()

	select {
	case got := <-gotReq:
		if got.Command() != "git push origin main" {
			t.Fatalf("★ 审批卡上必须有命令原文，got %q（RawInput=%s）—— "+
				"没有它用户无法判断批的是什么", got.Command(), string(got.RawInput))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for permission callback")
	}
}

// TestRequestPermission_NoCommandStillManual 等不到命令时仍走人工审批（不猜、不放行）。
//
// 这是安全底线：取不到命令 ⇒ kind 不被降级 ⇒ 维持弹卡。
//
// ⚠️ 注意 kind 的取值取决于**工具名**：本用例给了 title="pwsh"，
// 经 applyToolKindFix 修正为 execute；随后因无命令原文，DowngradeReadonlyPermission
// 无从判断，维持 execute（L2 弹卡）。若不给 title，kind 会保持 agent 原报的值
// （dsh 下是空串）—— 那也同样是弹卡，但测的就不是同一条链路了。
func TestRequestPermission_NoCommandStillManual(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	gotReq := make(chan PermissionRequest, 1)
	a.OnPermissionRequest(func(r PermissionRequest) { gotReq <- r })

	title := "pwsh"
	go func() {
		_, _ = a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall:  acp.ToolCallUpdate{ToolCallId: "tc-none", Title: &title},
			Options:   allowOnceOpts(),
		})
	}()

	select {
	case got := <-gotReq:
		if got.Command() != "" {
			t.Fatalf("不该凭空得到命令，got %q", got.Command())
		}
		// pwsh 经名字修正为 execute；无命令 ⇒ 无法降级 ⇒ 维持 L2。
		if got.ToolKind != "execute" {
			t.Fatalf("无命令时应维持 execute(L2 弹卡)，got %q", got.ToolKind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for permission callback")
	}
}
