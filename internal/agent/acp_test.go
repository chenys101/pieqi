package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

// --- spawn 参数构建 ---

func TestBuildSpawnCommand_ExplicitOverridesDefault(t *testing.T) {
	cfg := config.ACPConfig{
		AgentType:    "claude-code",
		SpawnCommand: []string{"my-agent", "--acp", "--port", "1234"},
	}
	name, args := buildSpawnCommand(cfg)
	if name != "my-agent" {
		t.Fatalf("name=%q want my-agent", name)
	}
	wantArgs := []string{"--acp", "--port", "1234"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args=%v want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("args[%d]=%q want %q", i, args[i], wantArgs[i])
		}
	}
}

func TestDefaultSpawnCommand(t *testing.T) {
	cases := []struct {
		agentType string
		wantName  string
		wantArgs  []string
	}{
		{"claude-code", "npx", []string{"-y", "@agentclientprotocol/claude-agent-acp@latest"}},
		{"", "npx", []string{"-y", "@agentclientprotocol/claude-agent-acp@latest"}}, // 空=claude-code 默认
		{"qodercli", "qodercli", []string{"--acp"}},
		{"codex", "codex", []string{"--acp"}},
		{"grok", "grok", []string{"--acp"}}, // 兜底：自身 --acp
	}
	for _, c := range cases {
		name, args := defaultSpawnCommand(c.agentType)
		if name != c.wantName {
			t.Errorf("agentType=%q name=%q want %q", c.agentType, name, c.wantName)
			continue
		}
		if len(args) != len(c.wantArgs) {
			t.Errorf("agentType=%q args=%v want %v", c.agentType, args, c.wantArgs)
			continue
		}
		for i := range c.wantArgs {
			if args[i] != c.wantArgs[i] {
				t.Errorf("agentType=%q args[%d]=%q want %q", c.agentType, i, args[i], c.wantArgs[i])
			}
		}
	}
}

// --- M1 核心：SessionUpdate → OnContentDelta 回调分发 ---

// TestSessionUpdate_AgentMessageChunkToDelta 验证 M1 端到端出文本的核心逻辑：
// AgentMessageChunk.Content.Text 经 SessionUpdate 回调落到 OnContentDelta（回答正文）。
func TestSessionUpdate_AgentMessageChunkToDelta(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	var got []ContentDelta
	a.OnContentDelta(func(d ContentDelta) { got = append(got, d) })

	ctx := context.Background()
	err := a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.UpdateAgentMessageText("hello "),
	})
	if err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
	if err := a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.UpdateAgentMessageText("world"),
	}); err != nil {
		t.Fatalf("SessionUpdate 2: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d deltas, want 2", len(got))
	}
	if got[0].Text != "hello " || got[0].IsThought {
		t.Errorf("delta[0]=%+v want Text=hello IsThought=false", got[0])
	}
	if got[1].Text != "world" || got[1].IsThought {
		t.Errorf("delta[1]=%+v want Text=world IsThought=false", got[1])
	}
	if got[0].SessionID != "sess-1" {
		t.Errorf("delta[0].SessionID=%q want sess-1", got[0].SessionID)
	}
}

// TestSessionUpdate_AgentThoughtChunkToDelta 验证思考过程增量走同一回调（IsThought=true）。
func TestSessionUpdate_AgentThoughtChunkToDelta(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	var got []ContentDelta
	a.OnContentDelta(func(d ContentDelta) { got = append(got, d) })

	err := a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.UpdateAgentThoughtText("reasoning..."),
	})
	if err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
	if len(got) != 1 || !got[0].IsThought || got[0].Text != "reasoning..." {
		t.Fatalf("got=%+v want IsThought=true Text=reasoning...", got)
	}
}

// TestSessionUpdate_EmptyTextNoCallback 验证空文本块不触发回调（避免空增量噪声）。
func TestSessionUpdate_EmptyTextNoCallback(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	calls := 0
	a.OnContentDelta(func(d ContentDelta) { calls++ })

	_ = a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.UpdateAgentMessageText(""),
	})
	if calls != 0 {
		t.Fatalf("empty text fired %d callbacks, want 0", calls)
	}
}

// TestSessionUpdate_ToolCallDispatch 验证 ToolCall/ToolCallUpdate 走 OnToolCallUpdate 回调。
func TestSessionUpdate_ToolCallDispatch(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	var got []ToolCallUpdateInfo
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = append(got, u) })

	ctx := context.Background()
	// 新工具调用开始
	_ = a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.StartToolCall("tc-1", "Bash: ls -la"),
	})
	// 工具调用状态变更（completed）
	completed := acp.ToolCallStatusCompleted
	_ = a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.UpdateToolCall("tc-1", acp.WithUpdateStatus(completed)),
	})

	if len(got) != 2 {
		t.Fatalf("got %d tool updates, want 2", len(got))
	}
	if !got[0].IsNew || got[0].ToolCallID != "tc-1" || got[0].Title != "Bash: ls -la" {
		t.Errorf("tool[0]=%+v want IsNew ToolCallID=tc-1", got[0])
	}
	if got[1].IsNew || got[1].Status != "completed" {
		t.Errorf("tool[1]=%+v want IsNew=false Status=completed", got[1])
	}
}

// TestSessionUpdate_ToolCallRawInputOutput 验证 ToolCall/ToolCallUpdate 的 RawInput/RawOutput
// 被填充到 ToolCallUpdateInfo（→ EventToolUse.Input / EventToolResult.Result）。
func TestSessionUpdate_ToolCallRawInputOutput(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	var got []ToolCallUpdateInfo
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = append(got, u) })

	ctx := context.Background()
	// 新工具调用开始：带 RawInput（map）和 RawOutput（map）。
	_ = a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.StartToolCall("tc-1", "Bash: ls -la",
			acp.WithStartRawInput(map[string]any{"command": "ls -la"}),
			acp.WithStartRawOutput(map[string]any{"preview": "init"}),
		),
	})
	// 状态变更（completed）：带 RawOutput（map）。
	completed := acp.ToolCallStatusCompleted
	_ = a.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.UpdateToolCall("tc-1",
			acp.WithUpdateStatus(completed),
			acp.WithUpdateRawOutput(map[string]any{"stdout": "total 0"}),
		),
	})

	if len(got) != 2 {
		t.Fatalf("got %d tool updates, want 2", len(got))
	}
	// ToolCall（开始）：RawInput/RawOutput 应为对应 JSON（单键 map，键名升序确定）。
	if !got[0].IsNew {
		t.Errorf("tool[0] IsNew=%v want true", got[0].IsNew)
	}
	if string(got[0].RawInput) != `{"command":"ls -la"}` {
		t.Errorf("tool[0] RawInput=%q want {\"command\":\"ls -la\"}", string(got[0].RawInput))
	}
	if string(got[0].RawOutput) != `{"preview":"init"}` {
		t.Errorf("tool[0] RawOutput=%q want {\"preview\":\"init\"}", string(got[0].RawOutput))
	}
	// ToolCallUpdate（completed）：RawOutput 应为结果 JSON。
	if got[1].IsNew {
		t.Errorf("tool[1] IsNew=%v want false", got[1].IsNew)
	}
	if string(got[1].RawOutput) != `{"stdout":"total 0"}` {
		t.Errorf("tool[1] RawOutput=%q want {\"stdout\":\"total 0\"}", string(got[1].RawOutput))
	}
}

// TestSessionUpdate_ToolCallRawInputString 验证 string 型 RawInput 走"直接转"路径
// （视作已序列化的 JSON，避免 json.Marshal 二次编码加引号）。
func TestSessionUpdate_ToolCallRawInputString(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	var got ToolCallUpdateInfo
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = u })

	_ = a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.StartToolCall("tc-2", "Bash",
			acp.WithStartRawInput(`{"path":"/tmp/x"}`),
		),
	})
	if string(got.RawInput) != `{"path":"/tmp/x"}` {
		t.Errorf("RawInput=%q want raw JSON {\"path\":\"/tmp/x\"} (no double encoding)", string(got.RawInput))
	}
}

// --- 权限审批（M1 默认放行 + Approve/Deny 投递管线） ---

// TestRequestPermission_AutoApproveNoCallback 无回调时自动放行首个 allow 选项（M1 端到端用）。
func TestRequestPermission_AutoApproveNoCallback(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	// 不注册 OnPermissionRequest → 走自动放行

	title := "Bash: rm -rf /tmp/x"
	resp, err := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		SessionId: "sess-1",
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: "tc-1",
			Title:      &title,
		},
		Options: []acp.PermissionOption{
			{OptionId: "opt-allow", Name: "Allow once", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "opt-deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if resp.Outcome.Selected == nil {
		t.Fatal("auto-approve returned Cancelled, want Selected")
	}
	if string(resp.Outcome.Selected.OptionId) != "opt-allow" {
		t.Fatalf("selected=%q want opt-allow", resp.Outcome.Selected.OptionId)
	}
}

// TestRequestPermission_AutoApproveFallbackToFirst 无 allow 选项时退而选首个选项。
func TestRequestPermission_AutoApproveFallbackToFirst(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	resp, err := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		SessionId: "sess-1",
		ToolCall:  acp.ToolCallUpdate{ToolCallId: "tc-1"},
		Options: []acp.PermissionOption{
			{OptionId: "opt-only", Name: "Only", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if resp.Outcome.Selected == nil || string(resp.Outcome.Selected.OptionId) != "opt-only" {
		t.Fatalf("selected=%+v want opt-only", resp.Outcome.Selected)
	}
}

// TestRequestPermission_AutoApproveNoOptionsCancelled 无任何选项 → Cancelled。
func TestRequestPermission_AutoApproveNoOptionsCancelled(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	resp, err := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
		SessionId: "sess-1",
		ToolCall:  acp.ToolCallUpdate{ToolCallId: "tc-1"},
	})
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatal("want Cancelled outcome when no options")
	}
}

// TestRequestPermission_ApproveDelivery 注册回调后，Approve 投递响应唤醒 RequestPermission。
// 这是 M3 审批管线的核心；M1 阶段验证管线畅通即可（不接 IM）。
func TestRequestPermission_ApproveDelivery(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	gotReq := make(chan PermissionRequest, 1)
	a.OnPermissionRequest(func(r PermissionRequest) {
		gotReq <- r
	})

	title := "Write: foo.txt"
	respCh := make(chan acp.RequestPermissionResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall: acp.ToolCallUpdate{
				ToolCallId: "tc-42",
				Title:      &title,
			},
			Options: []acp.PermissionOption{
				{OptionId: "opt-allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
				{OptionId: "opt-deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
			},
		})
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	// 等回调通知（含 ReqID）
	select {
	case r := <-gotReq:
		if r.ToolCallID != "tc-42" || r.ToolTitle != "Write: foo.txt" {
			t.Fatalf("callback req=%+v want ToolCallID=tc-42", r)
		}
		if len(r.Options) != 2 || r.Options[0].ID != "opt-allow" {
			t.Fatalf("callback options=%+v", r.Options)
		}
		// 投递批准
		if err := a.Approve(context.Background(), r.ReqID, "opt-allow"); err != nil {
			t.Fatalf("Approve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for permission callback")
	}

	select {
	case resp := <-respCh:
		if resp.Outcome.Selected == nil || string(resp.Outcome.Selected.OptionId) != "opt-allow" {
			t.Fatalf("response=%+v want Selected opt-allow", resp.Outcome)
		}
	case err := <-errCh:
		t.Fatalf("RequestPermission error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for RequestPermission response")
	}
}

// TestRequestPermission_DenyDelivery Deny 投递 → Cancelled outcome。
func TestRequestPermission_DenyDelivery(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	gotReq := make(chan PermissionRequest, 1)
	a.OnPermissionRequest(func(r PermissionRequest) { gotReq <- r })

	respCh := make(chan acp.RequestPermissionResponse, 1)
	go func() {
		resp, _ := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall:  acp.ToolCallUpdate{ToolCallId: "tc-deny"},
			Options: []acp.PermissionOption{
				{OptionId: "opt-allow", Kind: acp.PermissionOptionKindAllowOnce},
			},
		})
		respCh <- resp
	}()

	r := <-gotReq
	if err := a.Deny(context.Background(), r.ReqID); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	select {
	case resp := <-respCh:
		if resp.Outcome.Cancelled == nil {
			t.Fatalf("Deny returned %v, want Cancelled", resp.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Deny response")
	}
}

// TestApprove_NoPending 对不存在的 ReqID 调 Approve 返回错误。
func TestApprove_NoPending(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	err := a.Approve(context.Background(), "nonexistent", "opt")
	if err == nil {
		t.Fatal("Approve on nonexistent req returned nil, want error")
	}
}

// --- ACP 路径不支持的操作 ---

// TestInjectToolResult_NotSupported ACP 路径不支持注入 tool_result。
func TestInjectToolResult_NotSupported(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	err := a.InjectToolResult(context.Background(), "sess-1", "tc-1", "result", false)
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("err=%v want ErrNotSupported", err)
	}
}

// TestSendPrompt_BeforeStart 未启动时 SendPrompt 报错。
func TestSendPrompt_BeforeStart(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	if err := a.SendPrompt(context.Background(), "sess-1", "hi"); err == nil {
		t.Fatal("SendPrompt before Start returned nil, want error")
	}
}

// TestCancel_BeforeStart 未启动时 Cancel 报错。
func TestCancel_BeforeStart(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	if err := a.Cancel(context.Background(), "sess-1"); err == nil {
		t.Fatal("Cancel before Start returned nil, want error")
	}
}

// TestNewSession_EmptyCwd Cwd 空时 NewSession 报错（不会去 Start 真进程）。
func TestNewSession_EmptyCwd(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	_, err := a.NewSession(context.Background(), SessionConfig{})
	if err == nil {
		t.Fatal("NewSession with empty Cwd returned nil, want error")
	}
}

// TestStart_NonexistentCommandFails spawn 命令不存在时 Start 立即失败（不挂起、不泄漏 goroutine）。
// 用一个确定不存在的二进制名，避免触发 npx 拉包（沙箱无网络/无 Claude 凭证）。
func TestStart_NonexistentCommandFails(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{
		SpawnCommand: []string{"acp-test-nonexistent-binary-xyz-12345"},
		InitTimeout:  2 * time.Second,
	}, nil)

	err := a.Start(context.Background())
	if err == nil {
		t.Fatal("Start with nonexistent command returned nil, want error")
	}
	// 失败后 started 仍为 false，且 Start 幂等返回同一错误
	if a.started {
		t.Fatal("started=true after failed Start")
	}
	if err2 := a.Start(context.Background()); err2 != err {
		t.Fatalf("second Start err=%v, want same as first=%v", err2, err)
	}
}

// TestStart_EmptyCommandFails 空命令（agent_type 兜底后仍空）时 Start 报错。
func TestStart_EmptyCommandFails(t *testing.T) {
	// 用显式空 SpawnCommand + 让 buildSpawnCommand 走默认；
	// 这里直接验证 cmdName 为空时 Start 报错：构造一个 cmdName 被清空的 agent。
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	a.cmdName = "" // 模拟异常配置
	if err := a.Start(context.Background()); err == nil {
		t.Fatal("Start with empty cmdName returned nil, want error")
	}
}

// --- 生命周期清理 ---

// TestClose_IdempotentAndDoneSignal Close 幂等且关闭 Done channel。
// 对应 Phase 1 liveProc.done / cancel 语义：Close 后 Done() 必须可读。
func TestClose_IdempotentAndDoneSignal(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	// Done 应已关闭
	select {
	case <-a.Done():
	default:
		t.Fatal("Done() not closed after Close")
	}
	// 幂等：再次 Close 不 panic 不报错
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close 2 (idempotent): %v", err)
	}
}

// TestClose_CancelsPendingPermission Close 时取消挂起的权限请求（不让 RequestPermission 死等）。
func TestClose_CancelsPendingPermission(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	gotReq := make(chan PermissionRequest, 1)
	a.OnPermissionRequest(func(r PermissionRequest) { gotReq <- r })

	respCh := make(chan acp.RequestPermissionResponse, 1)
	go func() {
		resp, _ := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall:  acp.ToolCallUpdate{ToolCallId: "tc-pending"},
			Options: []acp.PermissionOption{
				{OptionId: "opt-allow", Kind: acp.PermissionOptionKindAllowOnce},
			},
		})
		respCh <- resp
	}()

	<-gotReq // 等回调注册 pending
	// Close 应取消挂起请求
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case resp := <-respCh:
		if resp.Outcome.Cancelled == nil {
			t.Fatalf("pending permission after Close=%+v, want Cancelled", resp.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: Close did not cancel pending permission")
	}
}

// TestRequestPermission_DoneCancels agent 退出（Done 关闭）时挂起的权限请求被取消。
func TestRequestPermission_DoneCancels(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)

	gotReq := make(chan PermissionRequest, 1)
	a.OnPermissionRequest(func(r PermissionRequest) { gotReq <- r })

	respCh := make(chan acp.RequestPermissionResponse, 1)
	go func() {
		resp, _ := a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall:  acp.ToolCallUpdate{ToolCallId: "tc-done"},
			Options: []acp.PermissionOption{
				{OptionId: "opt-allow", Kind: acp.PermissionOptionKindAllowOnce},
			},
		})
		respCh <- resp
	}()

	<-gotReq
	// 模拟 agent 进程退出：直接关闭 done（绕过 Close，因为 Close 还会 Kill nil 进程——安全）
	a.markDone()
	select {
	case resp := <-respCh:
		if resp.Outcome.Cancelled == nil {
			t.Fatalf("permission after Done=%+v, want Cancelled", resp.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: Done did not cancel pending permission")
	}
}
