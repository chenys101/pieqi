package core

import (
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// 审批生命周期日志的回归测试。
//
// 为什么专门为"日志"写测试：本次加的三条日志**本身就是要交付的可观测面** ——
// 排查"审批点了没反应/等太久"全靠它们。而日志代码最容易的失效方式不是编译错误，
// 而是**静默不触发**（打在了不执行的分支上、或用 Debug 打在 release 里丢掉）。
// 那种情况下看起来"加了日志"，实际排查时一条都搜不到。
//
// 所以这里断言三件事：
//  1. 发起（approval shown）确实打出，且带 shown_at / timeout / decision_id；
//  2. 确认成功（approval resolved by human）带 waited（= 人等了多久）；
//  3. 失败两条分支（no pending / already resolved）都能打出 —— 这是"点不动"的直接证据。
//
// ⚠️ 三条都用 Info/Warn（不是 Debug）：release 模式只落 Info 及以上
// （见 logging.NewLogger），Debug 会在生产环境丢失。
func TestPermissionLifecycleLogs(t *testing.T) {
	t.Run("发起与确认成功：都留证且 waited 可算", func(t *testing.T) {
		core, logs := observer.New(zap.InfoLevel)
		pw, taskID := newObservedPermWire(t, core, time.Minute)
		fa := pw.adapter.(*fakePermAdapter)

		// 发起
		fa.onPerm(permReq("req-1", "Write", "edit", standardOptions()))
		waitForStatus(t, pw.store, taskID, model.TaskWaitingInput, 2*time.Second)

		shown := logs.FilterMessage("approval shown (waiting for human decision)").All()
		if len(shown) != 1 {
			t.Fatalf("approval shown 日志应有 1 条，实际 %d 条（全部消息：%v）",
				len(shown), logMessages(logs))
		}
		ctx := shown[0].ContextMap()
		for _, k := range []string{"shown_at", "timeout", "decision_id", "tool_kind"} {
			if _, ok := ctx[k]; !ok {
				t.Fatalf("approval shown 缺少字段 %q：%v", k, ctx)
			}
		}
		// decision_id 必须非空 —— 它是前端回投时用的 id，空了就必然 no pending。
		if ctx["decision_id"] == "" {
			t.Fatalf("decision_id 为空，前端将无法回投：%v", ctx)
		}

		// 确认成功
		decisionID := ctx["decision_id"].(string)
		if err := pw.Resolve(decisionID, "approve"); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		// 2026-10-23 之后确认日志改名为 "approval settled"：它现在同时是"人批的"
		// 与"重放/免审结清的"唯一事实条目（waited 仍可算，见下）。
		resolved := logs.FilterMessage("approval settled").All()
		if len(resolved) != 1 {
			t.Fatalf("approval settled 日志应有 1 条，实际 %d 条（%v）",
				len(resolved), logMessages(logs))
		}
		rctx := resolved[0].ContextMap()
		if _, ok := rctx["waited"]; !ok {
			t.Fatalf("approval settled 缺少 waited（排查'等了多久'的关键字段）：%v", rctx)
		}
		if rctx["choice"] != "approve" {
			t.Fatalf("choice = %v, want approve", rctx["choice"])
		}
	})

	t.Run("失败：未知 decisionID 必须留证（串任务/陈旧 id 的唯一真信号）", func(t *testing.T) {
		core, logs := observer.New(zap.InfoLevel)
		pw, _ := newObservedPermWire(t, core, time.Minute)

		// 对一个从未展示过的 decisionID 投递 —— 真正该 409 的形态
		err := pw.Resolve("never-existed", "approve")
		if err == nil {
			t.Fatal("对未知 decisionID 投递应返回错误")
		}
		if !strings.Contains(err.Error(), "no pending permission") {
			t.Fatalf("未知 id 应报 no pending permission，实际: %v", err)
		}

		failed := logs.FilterMessage("approval resolve FAILED: unknown decision id").All()
		if len(failed) != 1 {
			t.Fatalf("unknown-id 失败日志应有 1 条，实际 %d（%v）", len(failed), logMessages(logs))
		}
		ctx := failed[0].ContextMap()
		// 这三个字段是定位用的关键：pending 表现状、当前展示的是谁、wire 是否已关
		for _, k := range []string{"pending_now", "displayed_now", "wire_closed", "decision_id"} {
			if _, ok := ctx[k]; !ok {
				t.Fatalf("失败日志缺少字段 %q：%v", k, ctx)
			}
		}
		if ctx["wire_closed"] != false {
			t.Fatalf("wire_closed 应为 false（未关闭），实际 %v", ctx["wire_closed"])
		}
	})

	t.Run("重复确认：必须留证为 re-delivered，且**不得**再报 no pending", func(t *testing.T) {
		core, logs := observer.New(zap.InfoLevel)
		pw, taskID := newObservedPermWire(t, core, time.Minute)
		fa := pw.adapter.(*fakePermAdapter)

		fa.onPerm(permReq("req-2", "Bash", "execute", standardOptions()))
		waitForStatus(t, pw.store, taskID, model.TaskWaitingInput, 2*time.Second)
		shown := logs.FilterMessage("approval shown (waiting for human decision)").All()
		decisionID, _ := shown[0].ContextMap()["decision_id"].(string)

		if err := pw.Resolve(decisionID, "approve"); err != nil {
			t.Fatalf("first resolve: %v", err)
		}
		// 第二次点同一张卡（陈旧前端 / 连点）。
		//
		// ★ 2026-10-23 起语义是**幂等**：Resolve 成功时会把 entry 从 pending 里 delete
		// 并写进 settled，第二次找不到 entry 但能在 settled 里认出"已经批过了"。
		// 旧行为是回 409 no pending —— f74bcb09 那次会话后期 16 条刷屏就是这么来的，
		// 而用户可能批的那条命令**已经执行了**，"no pending"是误导性的答复。
		err := pw.Resolve(decisionID, "deny")
		if err != nil {
			t.Fatalf("重复投递不应失败（幂等），实际: %v", err)
		}
		dup := logs.FilterMessage("approval resolve re-delivered (already settled)").All()
		if len(dup) != 1 {
			t.Fatalf("重复投递的留证应有 1 条，实际 %d（%v）", len(dup), logMessages(logs))
		}
		if ctx := dup[0].ContextMap(); ctx["settled_as"] != PermResolveApproved {
			t.Fatalf("settled_as = %v, want %v", ctx["settled_as"], PermResolveApproved)
		}
		if n := len(logs.FilterMessage("approval resolve FAILED: unknown decision id").All()); n != 0 {
			t.Fatalf("重复投递不该被记成「unknown id」（那是串任务的信号），实际 %d 条", n)
		}
	})

	t.Run("失败：超时必须留证（并把 waited 与 timeout 一起记下）", func(t *testing.T) {
		core, logs := observer.New(zap.InfoLevel)
		// 极短超时以便测试快速触发
		pw, taskID := newObservedPermWire(t, core, 30*time.Millisecond)
		fa := pw.adapter.(*fakePermAdapter)

		fa.onPerm(permReq("req-3", "Bash", "execute", standardOptions()))
		waitForStatus(t, pw.store, taskID, model.TaskWaitingInput, 2*time.Second)

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if len(logs.FilterMessage("approval TIMED OUT (no human decision within timeout)").All()) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		timedOut := logs.FilterMessage("approval TIMED OUT (no human decision within timeout)").All()
		if len(timedOut) != 1 {
			t.Fatalf("超时日志应有 1 条，实际 %d（%v）", len(timedOut), logMessages(logs))
		}
		ctx := timedOut[0].ContextMap()
		for _, k := range []string{"shown_at", "timed_out_at", "waited", "timeout"} {
			if _, ok := ctx[k]; !ok {
				t.Fatalf("超时日志缺少字段 %q：%v", k, ctx)
			}
		}
	})
}

// newObservedPermWire 造一个把日志写进 observer 的 PermissionWire。
//
// 与 setupPermWireAuto 的区别只有 logger（那里传 nil ⇒ Nop，日志被丢弃）。
// 单独写而不是改公共 helper：那些测试不关心日志，注入 observer 只会让它们变慢。
func newObservedPermWire(t *testing.T, core zapcore.Core, timeout time.Duration) (*PermissionWire, string) {
	t.Helper()
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(&model.Task{
		ProjectID:      "p",
		Prompt:         "hi",
		Status:         model.TaskRunning,
		OriginChannel:  "im",
		OriginChatID:   "c1",
		OriginIdentity: "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	fa := newFakePermAdapter()
	pw := WirePermission(fa, bus, store, task.ID, func(*model.Task, string) {}, timeout, nil, zap.New(core))
	return pw, task.ID
}

func logMessages(logs *observer.ObservedLogs) []string {
	all := logs.All()
	out := make([]string, 0, len(all))
	for _, e := range all {
		out = append(out, e.Message)
	}
	return out
}

// 保持 agent 包被引用（permReq/standardOptions 用到其类型）。
var _ = agent.PermissionOptionAllowOnce
