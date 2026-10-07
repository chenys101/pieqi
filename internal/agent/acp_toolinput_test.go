package agent

import (
	"context"
	"testing"
	"time"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

// 审批时补齐命令原文（toolInputs 索引）的回归测试。
//
// 背景：dsh 的 RequestPermission **不带** title/kind/rawInput（实测 12/12 条全空），
// 但它在先一步的 session/update 里发过 tool_call 的 RawInput。缺了回填，
// DowngradeReadonlyPermission（ADR-0008）对 dsh 完全失效 —— 每条命令都落 L2
// 必弹卡，而 qoder/claude 没这个问题。
//
// 顺序前提（不需要 sleep 的理由）见 ACPAgent.toolInputs 的注释：dsh 侧
// drainUpdates + acp-go-sdk 的通知屏障共同保证 RequestPermission 回调执行时，
// 对应的 ToolCall 已处理完毕。

// allowOnceOpts 一对标准选项（allow_once + reject_once），审批链路必需。
func allowOnceOpts() []acp.PermissionOption {
	return []acp.PermissionOption{
		{OptionId: "opt-allow", Name: "Allow once", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "opt-deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
	}
}

// sendToolCall 模拟 agent 推送一次 tool_call（带 RawInput）。
func sendToolCall(t *testing.T, a *ACPAgent, toolCallID string, rawInput map[string]any) {
	t.Helper()
	if err := a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.StartToolCall(acp.ToolCallId(toolCallID), "pwsh",
			acp.WithStartRawInput(rawInput),
		),
	}); err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}
}

// runPermission 在 goroutine 里发一次 RequestPermission，返回回调收到的请求。
// 请求体**刻意不带 RawInput、不带 title** —— 这正是 dsh 的形态。
func runPermission(t *testing.T, a *ACPAgent, toolCallID string) PermissionRequest {
	t.Helper()
	return runPermissionWithKind(t, a, toolCallID, "")
}

// runPermissionWithKind 同 runPermission，但可指定 ToolKind（部分用例需要 "execute"
// 才能走到降级分支；空串则复现 dsh 的真实形态）。
func runPermissionWithKind(t *testing.T, a *ACPAgent, toolCallID, kind string) PermissionRequest {
	t.Helper()
	return runPermissionWithTitle(t, a, toolCallID, "", kind)
}

// runPermissionWithTitle 完整形态：可指定 Title（工具名）与 Kind。
//
// Title 是名字修正层的输入（toolkind_map.go 按它查表），所以测修正行为必须能设它。
func runPermissionWithTitle(t *testing.T, a *ACPAgent, toolCallID, title, kind string) PermissionRequest {
	t.Helper()
	gotReq := make(chan PermissionRequest, 1)
	a.OnPermissionRequest(func(r PermissionRequest) { gotReq <- r })

	tc := acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(toolCallID)}
	if kind != "" {
		k := acp.ToolKind(kind)
		tc.Kind = &k
	}
	if title != "" {
		tt := title
		tc.Title = &tt
	}

	go func() {
		_, _ = a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall:  tc,
			Options:   allowOnceOpts(),
		})
	}()

	select {
	case r := <-gotReq:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for permission callback")
		return PermissionRequest{}
	}
}

// TestPermission_RawInputBackfilledFromToolCall 核心用例：
// 请求不带 RawInput，但先前的 tool_call 带过 ⇒ 审批时能拿到命令原文。
func TestPermission_RawInputBackfilledFromToolCall(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	// dsh 的形态：session/update 里先发 tool_call（带 command），
	// 随后的 RequestPermission 什么都不带。
	sendToolCall(t, a, "tc-1", map[string]any{"command": "git status --porcelain"})

	got := runPermission(t, a, "tc-1")

	if got.Command() != "git status --porcelain" {
		t.Fatalf("回填失败：Command()=%q，want \"git status --porcelain\"（RawInput=%s）",
			got.Command(), string(got.RawInput))
	}
}

// TestPermission_BackfillDrivesReadonlyDowngrade 端到端语义：
// 回填之后命令分档才真的生效，这才是"dsh 的命令不再白弹卡"的直接判据。
//
// ⚠️ 前提：`DowngradeReadonlyPermission` 只在 **ToolKind == "execute"** 时工作。
// dsh 的 pwsh 经名字修正后正是 execute（见 toolkind_map.go），所以链路能走通。
//
// 本用例覆盖两条判据的协作：
//   - git 段走 gitKindFor 分档（read/edit/execute/delete 四档）；
//   - 非 git 段走 ADR-0008 的只读白名单（read or execute 二值）。
func TestPermission_BackfillDrivesReadonlyDowngrade(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		wantKind string
	}{
		// 非 git：只读白名单
		{"白名单只读命令降级为 read", "ls -la", "read"},
		{"白名单命令带写标志则不降级", "find . -delete", "execute"},
		{"白名单外的命令维持 execute", "npm run build", "execute"},
		{"复合命令含写操作则不降级", "ls -la; rm -rf build/", "execute"},
		// git 分档（本步新增）
		{"git 只读子命令降级为 read", "git status --porcelain", "read"},
		{"git 本地可逆写归 edit(L1)", "git add -A", "edit"},
		{"git 影响远端维持 execute", "git push origin main", "execute"},
		{"git 破坏性不降级", "git reset --hard HEAD~1", "execute"},
		{"git 只读与只读复合仍是 read", "git log --oneline | head -5", "read"},
		{"git 只读混写则整条不降级", "git status; rm -rf build/", "execute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
			sendToolCall(t, a, "tc-1", map[string]any{"command": tc.command})

			got := runPermissionWithKind(t, a, "tc-1", "execute")

			if got.Command() != tc.command {
				t.Fatalf("Command()=%q want %q", got.Command(), tc.command)
			}
			if got.ToolKind != tc.wantKind {
				t.Fatalf("command=%q ToolKind=%q want %q", tc.command, got.ToolKind, tc.wantKind)
			}
		})
	}
}

// TestPermission_EmptyKindStillBlocksDowngrade 记录"命令原文回填"与"kind 修正"
// 是**两件独立的事**：
//
//   - 回填解决"拿不到命令"（本文件的主角）；
//   - kind 修正（toolkind_map.go）解决"kind 报错"。
//
// 本用例刻意用**未映射的工具名**，排除名字修正的介入，从而单独证明：
// 回填本身**不会**改变 kind —— 降级仍然只认 execute。
func TestPermission_EmptyKindStillBlocksDowngrade(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	sendToolCall(t, a, "tc-1", map[string]any{"command": "git status --porcelain"})

	// 工具名不在 dsh 表里 ⇒ 名字修正不介入
	got := runPermissionWithTitle(t, a, "tc-1", "unknown_tool", "")

	if got.Command() != "git status --porcelain" {
		t.Fatalf("命令回填应仍然有效，got %q", got.Command())
	}
	if got.ToolKind != "" {
		t.Fatalf("回填不该改变 kind（空 kind 也不降级），ToolKind=%q", got.ToolKind)
	}
}

// TestPermission_DshReadToolGetsL0 dsh 的 read —— 名字修正的靶心。
//
// dsh 把 tool_call 一律报成 other(L2)，于是 read/grep/edit 全部要人工点；
// 修正层按工具名把它纠正回 read(L0 免审)。
func TestPermission_DshReadToolGetsL0(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	sendToolCall(t, a, "tc-1", map[string]any{"file_path": "D:/x.go"})

	// dsh 的真实形态：kind 为 other（tool_call 写死），审批请求里 title/kind 都不带；
	// 这里模拟修正层看到的入参（title 已被 ToolCall 填成工具名）。
	got := runPermissionWithTitle(t, a, "tc-1", "read", "other")

	if got.ToolKind != "read" {
		t.Fatalf("dsh read 应被修正为 read(L0 免审)，got %q", got.ToolKind)
	}
}

// TestPermission_DshPwshCommandStillDowngradesByContent dsh 的 pwsh：
// 名字修正给 execute(L2)，再由**命令原文**决定能否降级 —— 两层协作的完整链。
func TestPermission_DshPwshCommandStillDowngradesByContent(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	sendToolCall(t, a, "tc-1", map[string]any{"command": "ls -la"})

	got := runPermissionWithTitle(t, a, "tc-1", "pwsh", "other")

	// pwsh → execute（名字修正），ls 在只读白名单 → read（内容降级）
	if got.ToolKind != "read" {
		t.Fatalf("pwsh 的只读命令应经两层修正落 read，got %q", got.ToolKind)
	}
	if got.Command() != "ls -la" {
		t.Fatalf("Command()=%q", got.Command())
	}
}

// TestPermission_RequestRawInputWins 请求自带 RawInput 时用它，
// 不被索引里的旧值覆盖（qoder/claude 路径：请求里就是权威答案）。
func TestPermission_RequestRawInputWins(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "qoder"}, nil)

	// 索引里是一条无关的旧命令（模拟同 id 曾被别的内容占过）
	sendToolCall(t, a, "tc-1", map[string]any{"command": "ls -la"})

	gotReq := make(chan PermissionRequest, 1)
	a.OnPermissionRequest(func(r PermissionRequest) { gotReq <- r })
	go func() {
		_, _ = a.RequestPermission(context.Background(), acp.RequestPermissionRequest{
			SessionId: "sess-1",
			ToolCall: acp.ToolCallUpdate{
				ToolCallId: "tc-1",
				RawInput:   map[string]any{"command": "cat /etc/hosts"},
			},
			Options: allowOnceOpts(),
		})
	}()

	select {
	case got := <-gotReq:
		if got.Command() != "cat /etc/hosts" {
			t.Fatalf("请求自带 RawInput 应优先，got %q", got.Command())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for permission callback")
	}
}

// TestPermission_NoToolCallStaysConservative 查不到入参时：
// 不猜测、不降级，维持保守判级（execute = L2 必弹卡）。
func TestPermission_NoToolCallStaysConservative(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	// 刻意不发 tool_call

	// 这里显式给 execute：本用例要证的是"没入参 ⇒ 即便 kind 本来可降级也不降"，
	// 给空 kind 会让它退化成测另一件事。
	got := runPermissionWithKind(t, a, "tc-unknown", "execute")

	if got.Command() != "" {
		t.Fatalf("无 tool_call 时不该凭空得到命令，got %q", got.Command())
	}
	if got.ToolKind != "execute" {
		t.Fatalf("查不到入参应维持保守判级，ToolKind=%q want execute", got.ToolKind)
	}
}

// TestRememberToolInput_EmptyDoesNotOverwrite 空入参不覆盖已有记录。
//
// 这是真实序列：ToolCall（带完整入参）→ ToolCallUpdate（只带状态、RawInput 为空）。
// 若空值覆盖，审批排在 update 之后就再也拿不到命令原文。
func TestRememberToolInput_EmptyDoesNotOverwrite(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	sendToolCall(t, a, "tc-1", map[string]any{"command": "git status"})

	// 后续状态变更不带 RawInput（dsh 的 completed 形态）
	completed := acp.ToolCallStatusCompleted
	if err := a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.UpdateToolCall("tc-1", acp.WithUpdateStatus(completed)),
	}); err != nil {
		t.Fatalf("SessionUpdate(update): %v", err)
	}

	if _, ok := a.toolInput("tc-1"); !ok {
		t.Fatal("空 RawInput 的 update 抹掉了先前的记录")
	}
	got := runPermission(t, a, "tc-1")
	if got.Command() != "git status" {
		t.Fatalf("Command()=%q want \"git status\"", got.Command())
	}
}

// TestRememberToolInput_UpdateCarriesInput 部分 agent 只在 update 里给入参，
// 那条路径同样要记下来。
func TestRememberToolInput_UpdateCarriesInput(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	if err := a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.UpdateToolCall("tc-1",
			acp.WithUpdateRawInput(map[string]any{"command": "go test ./..."}),
		),
	}); err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}

	got := runPermission(t, a, "tc-1")
	if got.Command() != "go test ./..." {
		t.Fatalf("Command()=%q want \"go test ./...\"", got.Command())
	}
}

// TestRememberToolInput_EmptyIDIgnored 空 toolCallId 不记录
// （没有键就无法关联，存了也永远查不到，只会让 map 无界增长）。
func TestRememberToolInput_EmptyIDIgnored(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	a.rememberToolInput("", "", []byte(`{"command":"x"}`))

	if _, ok := a.toolInput(""); ok {
		t.Fatal("空 toolCallId 不该被记录")
	}
	a.toolInputsMu.Lock()
	n := len(a.toolInputs)
	a.toolInputsMu.Unlock()
	if n != 0 {
		t.Fatalf("toolInputs 应保持为空，got %d 条", n)
	}
}

// TestClose_ReleasesToolInputs Close 释放记忆表（一个会话可达上千条）。
func TestClose_ReleasesToolInputs(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	sendToolCall(t, a, "tc-1", map[string]any{"command": "git status"})

	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := a.toolInput("tc-1"); ok {
		t.Fatal("Close 后应释放 toolInputs")
	}
}
