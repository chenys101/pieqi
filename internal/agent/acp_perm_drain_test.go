package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
	"go.uber.org/zap"
)

// 这组测试守的是一条真实事故（2026-11-24）：任务 13:35:42 已判 failed，
// 但 13:34:15 发出的 session/request_permission 一直悬着，直到 44 分钟后会话被
// 空闲回收才被 ctx 连带清掉。期间一个 goroutine 死在 `<-ch` 上，agent 侧干等。
//
// 不变量：**prompt 一返回，本轮就不该再有挂起的权限请求。**

// drainConn 只实现 Prompt 的最小替身：其余方法由嵌入的 nil 接口兜着（本测试不会调到）。
type drainConn struct {
	acpConn
	err error
}

func (d *drainConn) Prompt(context.Context, acp.PromptRequest) (acp.PromptResponse, error) {
	return acp.PromptResponse{}, d.err
}

func TestDrainPendingPerms_WakesEveryWaiterAsCancelled(t *testing.T) {
	a := &ACPAgent{
		logger:       zap.NewNop(),
		pendingPerms: make(map[string]chan PermissionResponse),
	}
	chans := map[string]chan PermissionResponse{}
	for _, id := range []string{"req-1", "req-2", "req-3"} {
		ch := make(chan PermissionResponse, 1)
		a.pendingPerms[id] = ch
		chans[id] = ch
	}

	if n := a.drainPendingPerms("test"); n != 3 {
		t.Fatalf("drained=%d want 3", n)
	}
	if len(a.pendingPerms) != 0 {
		t.Fatalf("pendingPerms 未清空: %d", len(a.pendingPerms))
	}
	for id, ch := range chans {
		select {
		case resp := <-ch:
			// Selected=false → toACPOutcome → NewRequestPermissionOutcomeCancelled。
			// 这里断言的是「回了取消」，不是「回了拒绝」：两者在 ACP 层是不同的 outcome。
			if resp.Selected {
				t.Fatalf("%s 收到 Selected=true，应为取消", id)
			}
			if resp.OptionID != "" {
				t.Fatalf("%s OptionID=%q，取消不该带选项", id, resp.OptionID)
			}
		default:
			t.Fatalf("%s 没收到响应（仍在悬空）", id)
		}
	}
}

func TestDrainPendingPerms_NotifiesPermissionEnded(t *testing.T) {
	a := &ACPAgent{
		logger:       zap.NewNop(),
		pendingPerms: make(map[string]chan PermissionResponse),
	}
	type ended struct {
		id        string
		cancelled bool
	}
	var got []ended
	a.onPermEnded = func(id string, cancelled bool) {
		got = append(got, ended{id, cancelled})
	}
	a.pendingPerms["req-1"] = make(chan PermissionResponse, 1)
	a.pendingPerms["req-2"] = make(chan PermissionResponse, 1)

	a.drainPendingPerms("test")

	if len(got) != 2 {
		t.Fatalf("onPermEnded 调用 %d 次，want 2: %+v", len(got), got)
	}
	for _, e := range got {
		// cancelled=true 是前端收起审批卡的依据；报 false 会让卡片继续显示成"等待中"。
		if !e.cancelled {
			t.Fatalf("%s cancelled=false，前端收不起卡片", e.id)
		}
	}
}

func TestDrainPendingPerms_EmptyIsNoOp(t *testing.T) {
	a := &ACPAgent{
		logger:       zap.NewNop(),
		pendingPerms: make(map[string]chan PermissionResponse),
	}
	if n := a.drainPendingPerms("test"); n != 0 {
		t.Fatalf("drained=%d want 0", n)
	}
	// nil map 也要安全（Close 会把 toolInputs 等置 nil，同类写法在收尾路径上很常见）。
	b := &ACPAgent{logger: zap.NewNop()}
	if n := b.drainPendingPerms("test"); n != 0 {
		t.Fatalf("nil map drained=%d want 0", n)
	}
}

// TestPromptOnce_DrainsPendingPermOnFailure 是这次事故的直接回归：
// prompt 失败返回后，本轮里挂着的权限请求必须当场被清掉，而不是等会话回收。
func TestPromptOnce_DrainsPendingPermOnFailure(t *testing.T) {
	a := &ACPAgent{
		started:      true,
		logger:       zap.NewNop(),
		pendingPerms: make(map[string]chan PermissionResponse),
	}
	a.conn = &drainConn{err: errors.New("boom: 上游额度耗尽")}
	ch := make(chan PermissionResponse, 1)
	a.pendingPerms["call-794cf876"] = ch

	err := a.SendPrompt(context.Background(), "sid-1", "hi")
	if err == nil {
		t.Fatal("prompt 应失败")
	}

	select {
	case resp := <-ch:
		if resp.Selected {
			t.Fatal("悬空的权限请求应被取消")
		}
	default:
		t.Fatal("prompt 失败后权限请求仍悬空（事故重现）")
	}
	if len(a.pendingPerms) != 0 {
		t.Fatalf("pendingPerms 残留 %d 条", len(a.pendingPerms))
	}
}

// TestPromptOnce_DrainsPendingPermOnSuccess 守另一半：成功收尾也不留悬空请求。
func TestPromptOnce_DrainsPendingPermOnSuccess(t *testing.T) {
	a := &ACPAgent{
		started:      true,
		logger:       zap.NewNop(),
		pendingPerms: make(map[string]chan PermissionResponse),
	}
	a.conn = &drainConn{}
	ch := make(chan PermissionResponse, 1)
	a.pendingPerms["stale"] = ch

	if err := a.SendPrompt(context.Background(), "sid-1", "hi"); err != nil {
		t.Fatalf("prompt 不该失败: %v", err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("成功收尾后残留的权限请求未被清理")
	}
}
