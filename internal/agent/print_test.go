package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- 接口编译期断言 ---

// TestPrintInterfaceCompileAssertion 编译期断言 PrintAgent 实现 AgentAdapter。
// （print.go 内已有 var _ AgentAdapter = (*PrintAgent)(nil)，测试侧再断言一次。）
func TestPrintInterfaceCompileAssertion(t *testing.T) {
	var _ AgentAdapter = (*PrintAgent)(nil)
}

// TestNewPrintAgent_ConfigDefaults 校验 NewPrintAgent 对空 Binary/PermissionMode 的兜底。
func TestNewPrintAgent_ConfigDefaults(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	if a.cfg.Binary != "claude" {
		t.Errorf("Binary=%q want claude", a.cfg.Binary)
	}
	if a.cfg.PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode=%q want bypassPermissions", a.cfg.PermissionMode)
	}
	if a.spawn == nil {
		t.Error("spawn func not set")
	}
	if a.sess == nil {
		t.Error("sess map not initialized")
	}
	if a.done == nil {
		t.Error("done channel not initialized")
	}
}

// TestNewPrintAgent_ExplicitConfig 校验显式配置被保留。
func TestNewPrintAgent_ExplicitConfig(t *testing.T) {
	a := NewPrintAgent(PrintConfig{
		Binary:         "/usr/local/bin/claude",
		Model:          "claude-3-7-sonnet",
		SysPrompt:      "be concise",
		PermissionMode: "plan",
	}, nil)
	if a.cfg.Binary != "/usr/local/bin/claude" {
		t.Errorf("Binary=%q want /usr/local/bin/claude", a.cfg.Binary)
	}
	if a.cfg.Model != "claude-3-7-sonnet" {
		t.Errorf("Model=%q want claude-3-7-sonnet", a.cfg.Model)
	}
	if a.cfg.SysPrompt != "be concise" {
		t.Errorf("SysPrompt=%q want 'be concise'", a.cfg.SysPrompt)
	}
	if a.cfg.PermissionMode != "plan" {
		t.Errorf("PermissionMode=%q want plan", a.cfg.PermissionMode)
	}
}

// --- buildArgs 参数构建 ---

// TestPrintBuildArgs_FirstTurn 首轮用 -p <prompt> --session-id <sid>。
func TestPrintBuildArgs_FirstTurn(t *testing.T) {
	a := NewPrintAgent(PrintConfig{
		Model:          "sonnet",
		PermissionMode: "bypassPermissions",
	}, nil)
	args := a.buildArgs(false, "sid-1", "hello")
	want := []string{
		"-p", "hello", "--session-id", "sid-1",
		"--output-format", "stream-json", "--verbose",
		"--model", "sonnet",
		"--permission-mode", "bypassPermissions",
	}
	if !sliceEqual(args, want) {
		t.Errorf("first turn args=%v\nwant %v", args, want)
	}
}

// TestPrintBuildArgs_ResumeTurn 续轮用 --resume <sid> -p <prompt>。
func TestPrintBuildArgs_ResumeTurn(t *testing.T) {
	a := NewPrintAgent(PrintConfig{
		Model:          "sonnet",
		PermissionMode: "bypassPermissions",
	}, nil)
	args := a.buildArgs(true, "sid-1", "follow up")
	want := []string{
		"--resume", "sid-1", "-p", "follow up",
		"--output-format", "stream-json", "--verbose",
		"--model", "sonnet",
		"--permission-mode", "bypassPermissions",
	}
	if !sliceEqual(args, want) {
		t.Errorf("resume turn args=%v\nwant %v", args, want)
	}
}

// TestPrintBuildArgs_SysPrompt 验证 SysPrompt 非空时追加 --append-system-prompt。
func TestPrintBuildArgs_SysPrompt(t *testing.T) {
	a := NewPrintAgent(PrintConfig{SysPrompt: "be helpful"}, nil)
	args := a.buildArgs(false, "sid-1", "hi")
	found := false
	for i, a := range args {
		if a == "--append-system-prompt" && i+1 < len(args) && args[i+1] == "be helpful" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("args=%v missing --append-system-prompt be helpful", args)
	}
}

// TestPrintBuildArgs_NoSysPromptOmitted 验证 SysPrompt 为空时不附加 --append-system-prompt。
func TestPrintBuildArgs_NoSysPromptOmitted(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	args := a.buildArgs(false, "sid-1", "hi")
	for _, a := range args {
		if a == "--append-system-prompt" {
			t.Errorf("args=%v should not contain --append-system-prompt", args)
		}
	}
}

// TestPrintBuildArgs_EmptyModelAndPermissionMode 验证 Model/PermissionMode 为空时不附加。
// 直接构造 PrintAgent（绕过 NewPrintAgent 的默认值兜底）以测 buildArgs 的条件附加逻辑。
func TestPrintBuildArgs_EmptyModelAndPermissionMode(t *testing.T) {
	a := &PrintAgent{cfg: PrintConfig{}} // 不走 NewPrintAgent，保持 Model/PermissionMode 为空
	args := a.buildArgs(false, "sid-1", "hi")
	want := []string{
		"-p", "hi", "--session-id", "sid-1",
		"--output-format", "stream-json", "--verbose",
	}
	if !sliceEqual(args, want) {
		t.Errorf("args=%v\nwant %v", args, want)
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- NewSession ---

// TestPrintNewSession_RegistersSession 验证 NewSession 登记 cwd 并返回 uuid sessionID。
func TestPrintNewSession_RegistersSession(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	sid, err := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sid == "" {
		t.Fatal("sessionID empty")
	}
	a.sessMu.Lock()
	sess, ok := a.sess[sid]
	a.sessMu.Unlock()
	if !ok {
		t.Fatal("session not registered")
	}
	if sess.cwd != "/tmp" {
		t.Errorf("cwd=%q want /tmp", sess.cwd)
	}
	if sess.ran {
		t.Error("ran=true want false (NewSession 不应标记已跑)")
	}
}

// TestPrintNewSession_EmptyCwd 验证 Cwd 空时报错。
func TestPrintNewSession_EmptyCwd(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	if _, err := a.NewSession(context.Background(), SessionConfig{}); err == nil {
		t.Fatal("NewSession with empty Cwd returned nil, want error")
	}
}

// --- dispatchLine 派发 ---

// TestPrintDispatchLine_AssistantText 验证 text 块派发到 OnContentDelta(IsThought=false)。
func TestPrintDispatchLine_AssistantText(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	var got []ContentDelta
	a.OnContentDelta(func(d ContentDelta) { got = append(got, d) })

	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`
	resultEvt, systemInitEvt := a.dispatchLine("sess-1", line)
	if resultEvt != nil {
		t.Errorf("resultEvt=%+v want nil", resultEvt)
	}
	if systemInitEvt != nil {
		t.Errorf("systemInitEvt=%+v want nil", systemInitEvt)
	}
	if len(got) != 1 || got[0].Text != "hi" || got[0].IsThought || got[0].SessionID != "sess-1" {
		t.Errorf("got=%+v want Text=hi IsThought=false SessionID=sess-1", got)
	}
}

// TestPrintDispatchLine_Thinking 验证 thinking 块派发到 OnContentDelta(IsThought=true)。
func TestPrintDispatchLine_Thinking(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	var got []ContentDelta
	a.OnContentDelta(func(d ContentDelta) { got = append(got, d) })

	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning"}]}}`
	a.dispatchLine("sess-1", line)
	if len(got) != 1 || !got[0].IsThought || got[0].Text != "reasoning" {
		t.Errorf("got=%+v want IsThought=true Text=reasoning", got)
	}
}

// TestPrintDispatchLine_ToolUseAndResult 验证 tool_use → OnToolCallUpdate(IsNew=true)，
// tool_result → OnToolCallUpdate(IsNew=false, Status=completed/failed)。
func TestPrintDispatchLine_ToolUseAndResult(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	var got []ToolCallUpdateInfo
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { got = append(got, u) })

	// tool_use 块
	useLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}]}}`
	a.dispatchLine("sess-1", useLine)
	// tool_result 块（成功）
	resLine := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"output","is_error":false}]}}`
	a.dispatchLine("sess-1", resLine)
	// tool_result 块（失败）
	errLine := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_2","content":"oops","is_error":true}]}}`
	a.dispatchLine("sess-1", errLine)

	if len(got) != 3 {
		t.Fatalf("got %d tool updates, want 3", len(got))
	}
	// tool_use
	if !got[0].IsNew || got[0].ToolCallID != "tu_1" || got[0].Title != "Bash" {
		t.Errorf("got[0]=%+v want IsNew=true tu_1/Bash", got[0])
	}
	if string(got[0].RawInput) != `{"command":"ls"}` {
		t.Errorf("got[0].RawInput=%q want {\"command\":\"ls\"}", string(got[0].RawInput))
	}
	// tool_result success
	if got[1].IsNew || got[1].ToolCallID != "tu_1" || got[1].Status != "completed" {
		t.Errorf("got[1]=%+v want IsNew=false tu_1 completed", got[1])
	}
	// RawOutput 应为 JSON 编码的字符串 "output"
	if string(got[1].RawOutput) != `"output"` {
		t.Errorf("got[1].RawOutput=%q want \"output\"", string(got[1].RawOutput))
	}
	// tool_result error
	if got[2].Status != "failed" {
		t.Errorf("got[2].Status=%q want failed", got[2].Status)
	}
}

// TestPrintDispatchLine_SystemInit 验证 system init 行返回 systemInitEvt（不派发回调）。
func TestPrintDispatchLine_SystemInit(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	calls := 0
	a.OnContentDelta(func(d ContentDelta) { calls++ })
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { calls++ })

	line := `{"type":"system","subtype":"init","session_id":"real-sid"}`
	resultEvt, systemInitEvt := a.dispatchLine("sess-1", line)
	if resultEvt != nil {
		t.Errorf("resultEvt=%+v want nil", resultEvt)
	}
	if systemInitEvt == nil || systemInitEvt.SessionID != "real-sid" {
		t.Errorf("systemInitEvt=%+v want SessionID=real-sid", systemInitEvt)
	}
	if calls != 0 {
		t.Errorf("system init fired %d callbacks, want 0", calls)
	}
}

// TestPrintDispatchLine_Result 验证 result 行返回 resultEvt（不派发回调）。
func TestPrintDispatchLine_Result(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	calls := 0
	a.OnContentDelta(func(d ContentDelta) { calls++ })
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { calls++ })

	line := `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sid-1"}`
	resultEvt, systemInitEvt := a.dispatchLine("sess-1", line)
	if systemInitEvt != nil {
		t.Errorf("systemInitEvt=%+v want nil", systemInitEvt)
	}
	if resultEvt == nil || resultEvt.Subtype != "success" || resultEvt.IsError || resultEvt.Text != "done" {
		t.Errorf("resultEvt=%+v want subtype=success is_error=false result=done", resultEvt)
	}
	if calls != 0 {
		t.Errorf("result line fired %d callbacks, want 0", calls)
	}
}

// TestPrintDispatchLine_EmptyAndInvalid 验证空行/无效行不派发、不返回事件。
func TestPrintDispatchLine_EmptyAndInvalid(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	calls := 0
	a.OnContentDelta(func(d ContentDelta) { calls++ })
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { calls++ })

	for _, line := range []string{"", "   ", "not json", `{"type":"stream_event"}`} {
		r, s := a.dispatchLine("sess-1", line)
		if r != nil || s != nil {
			t.Errorf("dispatchLine(%q) result=%+v system=%+v, want nil/nil", line, r, s)
		}
	}
	if calls != 0 {
		t.Errorf("invalid lines fired %d callbacks, want 0", calls)
	}
}

// --- SendPrompt（注入 spawn） ---

// fakeProcess 测试用 claudeProcess 实现。
//
// 通过 io.Pipe 模拟真实进程的 stdout：
//   - writeLines 写入 stream-json 行（不关闭 writer，等 finish/Kill）
//   - finish 关闭 writer + done，模拟进程正常退出（stdout EOF + Wait 返回 waitErr）
//   - Kill 关闭 reader + done，模拟进程被杀（reader 报错 + Wait 返回 waitErr）
type fakeProcess struct {
	stdoutR   *io.PipeReader
	stdoutW   *io.PipeWriter
	stderrMu  sync.Mutex
	stderr    strings.Builder
	waitErr   error
	killed    atomic.Bool
	done      chan struct{}
	closeOnce sync.Once
}

func newFakeProcess(waitErr error) *fakeProcess {
	r, w := io.Pipe()
	return &fakeProcess{
		stdoutR: r,
		stdoutW: w,
		done:    make(chan struct{}),
		waitErr: waitErr,
	}
}

func (p *fakeProcess) Stdout() io.Reader { return p.stdoutR }
func (p *fakeProcess) Stderr() string {
	p.stderrMu.Lock()
	defer p.stderrMu.Unlock()
	return p.stderr.String()
}
func (p *fakeProcess) appendStderr(s string) {
	p.stderrMu.Lock()
	defer p.stderrMu.Unlock()
	p.stderr.WriteString(s)
}
func (p *fakeProcess) Wait() error {
	<-p.done
	return p.waitErr
}
func (p *fakeProcess) Kill() error {
	if p.killed.Swap(true) {
		return nil
	}
	// 关 reader 让 parseAndDispatch 退出（read 返回 pipe closed 错误）
	_ = p.stdoutR.Close()
	p.closeOnce.Do(func() { close(p.done) })
	return nil
}

// writeLines 写入 stream-json 行（不关闭 writer）。
func (p *fakeProcess) writeLines(lines ...string) {
	for _, l := range lines {
		_, _ = p.stdoutW.Write([]byte(l + "\n"))
	}
}

// finish 模拟进程正常退出：关闭 writer（stdout EOF）+ done（Wait 返回 waitErr）。
func (p *fakeProcess) finish() {
	_ = p.stdoutW.Close()
	p.closeOnce.Do(func() { close(p.done) })
}

// nopWriteCloser 测试用 io.WriteCloser，写操作丢弃，Close no-op。
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// injectFakeSpawn 把 PrintAgent 的 spawn 替换为返回 fp 的 fake，并返回 fp 供测试驱动。
func injectFakeSpawn(a *PrintAgent, waitErr error) *fakeProcess {
	fp := newFakeProcess(waitErr)
	a.spawn = func(ctx context.Context, name string, args []string, dir string) (claudeProcess, io.WriteCloser, error) {
		return fp, nopWriteCloser{}, nil
	}
	return fp
}

// TestPrintSendPrompt_Success 验证 SendPrompt 解析 fake stdout 并派发回调，正常退出返回 nil。
func TestPrintSendPrompt_Success(t *testing.T) {
	a := NewPrintAgent(PrintConfig{Model: "sonnet"}, nil)
	fp := injectFakeSpawn(a, nil)

	var gotDelta []ContentDelta
	var gotTool []ToolCallUpdateInfo
	a.OnContentDelta(func(d ContentDelta) { gotDelta = append(gotDelta, d) })
	a.OnToolCallUpdate(func(u ToolCallUpdateInfo) { gotTool = append(gotTool, u) })

	sid, err := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// 在另一 goroutine 写入 stream-json 行后 finish（模拟 claude 输出完毕退出）
	go func() {
		fp.writeLines(
			`{"type":"system","subtype":"init","session_id":"real-claude-sid"}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}]}}`,
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"out","is_error":false}]}}`,
			`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"real-claude-sid"}`,
		)
		fp.finish()
	}()

	if err := a.SendPrompt(context.Background(), sid, "hi"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	// 断言回调被正确触发
	if len(gotDelta) != 1 || gotDelta[0].Text != "hello" {
		t.Errorf("delta=%+v want Text=hello", gotDelta)
	}
	if len(gotTool) != 2 {
		t.Fatalf("tool updates=%d want 2", len(gotTool))
	}
	if !gotTool[0].IsNew || gotTool[0].ToolCallID != "tu_1" {
		t.Errorf("tool[0]=%+v want IsNew tu_1", gotTool[0])
	}
	if gotTool[1].IsNew || gotTool[1].Status != "completed" {
		t.Errorf("tool[1]=%+v want IsNew=false completed", gotTool[1])
	}

	// 验证 system init 报告的真实 sid 被记到 sess
	a.sessMu.Lock()
	sess := a.sess[sid]
	a.sessMu.Unlock()
	if sess.realSessionID != "real-claude-sid" {
		t.Errorf("realSessionID=%q want real-claude-sid", sess.realSessionID)
	}
	if !sess.ran {
		t.Error("ran=false want true（已跑过首轮）")
	}
}

// TestPrintSendPrompt_ResultIsError 验证 result.is_error=true 时 SendPrompt 返回 error。
func TestPrintSendPrompt_ResultIsError(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	fp := injectFakeSpawn(a, nil)

	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})

	go func() {
		fp.writeLines(
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial"}]}}`,
			`{"type":"result","subtype":"error_max_tokens","is_error":true,"result":"hit limit","session_id":"sid-x"}`,
		)
		fp.finish()
	}()

	err := a.SendPrompt(context.Background(), sid, "hi")
	if err == nil {
		t.Fatal("SendPrompt returned nil, want error for result.is_error=true")
	}
	if !strings.Contains(err.Error(), "error_max_tokens") {
		t.Errorf("err=%q want contains 'error_max_tokens'", err.Error())
	}
}

// TestPrintSendPrompt_ResultNonSuccessSubtype 验证 result.subtype 非 success 时返回 error。
func TestPrintSendPrompt_ResultNonSuccessSubtype(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	fp := injectFakeSpawn(a, nil)

	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})

	go func() {
		fp.writeLines(`{"type":"result","subtype":"error_tool_use","is_error":false,"result":"x","session_id":"sid-x"}`)
		fp.finish()
	}()

	err := a.SendPrompt(context.Background(), sid, "hi")
	if err == nil {
		t.Fatal("SendPrompt returned nil, want error for subtype != success")
	}
	if !strings.Contains(err.Error(), "error_tool_use") {
		t.Errorf("err=%q want contains 'error_tool_use'", err.Error())
	}
}

// TestPrintSendPrompt_NonZeroExit 验证 Wait 返回错误时 SendPrompt 返回 error（带 stderr）。
// 用一个普通 error 模拟（exec.ExitError 路径需要真进程，这里测 generic 路径）。
func TestPrintSendPrompt_NonZeroExit(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	waitErr := errors.New("claude crashed")
	fp := injectFakeSpawn(a, waitErr)
	fp.appendStderr("boom from claude")

	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})

	go func() {
		fp.writeLines(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"x"}]}}`)
		fp.finish()
	}()

	err := a.SendPrompt(context.Background(), sid, "hi")
	if err == nil {
		t.Fatal("SendPrompt returned nil, want error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "claude crashed") {
		t.Errorf("err=%q want contains 'claude crashed'", err.Error())
	}
	// stderr 摘要应包含
	if !strings.Contains(err.Error(), "boom from claude") {
		t.Errorf("err=%q want contains 'boom from claude'", err.Error())
	}
}

// TestPrintSendPrompt_ExitErrorPath 验证 Wait 返回 *exec.ExitError 时走 ExitCode 分支。
// 用一个真实的失败命令构造 ExitError。
func TestPrintSendPrompt_ExitErrorPath(t *testing.T) {
	// 用真实失败命令拿到一个 *exec.ExitError
	cmd := exec.Command("sh", "-c", "exit 7")
	waitErr := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("无法构造 ExitError: %v", waitErr)
	}

	a := NewPrintAgent(PrintConfig{}, nil)
	fp := injectFakeSpawn(a, waitErr) // 直接复用真实 ExitError
	fp.appendStderr("claude stderr line")

	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})

	go func() {
		fp.writeLines(`{"type":"result","subtype":"success","is_error":false,"result":"x","session_id":"sid-x"}`)
		fp.finish()
	}()

	sendErr := a.SendPrompt(context.Background(), sid, "hi")
	if sendErr == nil {
		t.Fatal("SendPrompt returned nil, want error for ExitError")
	}
	// ExitError 路径的 error 信息含 "claude exit"
	if !strings.Contains(sendErr.Error(), "claude exit") {
		t.Errorf("err=%q want contains 'claude exit'", sendErr.Error())
	}
}

// TestPrintSendPrompt_UnknownSession 验证 SendPrompt 对未登记的 session 报错。
func TestPrintSendPrompt_UnknownSession(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	if err := a.SendPrompt(context.Background(), "nonexistent", "hi"); err == nil {
		t.Fatal("SendPrompt on unknown session returned nil, want error")
	}
}

// TestPrintSendPrompt_EmptyPrompt 验证空 prompt 报错（防尾部 \n 空输出语义）。
func TestPrintSendPrompt_EmptyPrompt(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})
	if err := a.SendPrompt(context.Background(), sid, "  \n  "); err == nil {
		t.Fatal("SendPrompt with whitespace-only prompt returned nil, want error")
	}
}

// TestPrintSendPrompt_TrimspacesPrompt 验证 prompt 被去首尾空白。
func TestPrintSendPrompt_TrimspacesPrompt(t *testing.T) {
	a := NewPrintAgent(PrintConfig{Model: "sonnet"}, nil)

	var capturedArgs []string
	fp := newFakeProcess(nil)
	a.spawn = func(ctx context.Context, name string, args []string, dir string) (claudeProcess, io.WriteCloser, error) {
		capturedArgs = args
		return fp, nopWriteCloser{}, nil
	}

	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})

	go func() {
		fp.writeLines(`{"type":"result","subtype":"success","is_error":false,"result":"x","session_id":"sid-x"}`)
		fp.finish()
	}()

	if err := a.SendPrompt(context.Background(), sid, "  hello\n  "); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	// 找到 -p 后的 prompt 值
	for i, a := range capturedArgs {
		if a == "-p" && i+1 < len(capturedArgs) {
			if capturedArgs[i+1] != "hello" {
				t.Errorf("prompt=%q want 'hello' (TrimSpace)", capturedArgs[i+1])
			}
			return
		}
	}
	t.Errorf("args=%v missing -p", capturedArgs)
}

// TestPrintSendPrompt_ResumeAfterFirstTurn 验证首轮跑过后，续轮用 --resume + 真实 sid。
func TestPrintSendPrompt_ResumeAfterFirstTurn(t *testing.T) {
	a := NewPrintAgent(PrintConfig{Model: "sonnet"}, nil)

	var capturedArgs []string
	var argsMu sync.Mutex
	fp1 := newFakeProcess(nil)
	fp2 := newFakeProcess(nil)
	current := &fp1
	a.spawn = func(ctx context.Context, name string, args []string, dir string) (claudeProcess, io.WriteCloser, error) {
		argsMu.Lock()
		capturedArgs = append(capturedArgs, strings.Join(args, " "))
		argsMu.Unlock()
		return *current, nopWriteCloser{}, nil
	}

	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})

	// 首轮
	go func() {
		fp1.writeLines(
			`{"type":"system","subtype":"init","session_id":"real-sid"}`,
			`{"type":"result","subtype":"success","is_error":false,"result":"x","session_id":"real-sid"}`,
		)
		fp1.finish()
	}()
	if err := a.SendPrompt(context.Background(), sid, "first"); err != nil {
		t.Fatalf("first SendPrompt: %v", err)
	}

	// 续轮
	*current = fp2
	go func() {
		fp2.writeLines(`{"type":"result","subtype":"success","is_error":false,"result":"y","session_id":"real-sid"}`)
		fp2.finish()
	}()
	if err := a.SendPrompt(context.Background(), sid, "second"); err != nil {
		t.Fatalf("second SendPrompt: %v", err)
	}

	argsMu.Lock()
	defer argsMu.Unlock()
	if len(capturedArgs) != 2 {
		t.Fatalf("captured %d spawn calls, want 2", len(capturedArgs))
	}
	// 首轮应含 --session-id
	if !strings.Contains(capturedArgs[0], "--session-id") {
		t.Errorf("first args=%q missing --session-id", capturedArgs[0])
	}
	if strings.Contains(capturedArgs[0], "--resume") {
		t.Errorf("first args=%q should not contain --resume", capturedArgs[0])
	}
	// 续轮应含 --resume real-sid（用 system init 报告的真实 sid）
	if !strings.Contains(capturedArgs[1], "--resume real-sid") {
		t.Errorf("second args=%q missing --resume real-sid", capturedArgs[1])
	}
	if strings.Contains(capturedArgs[1], "--session-id") {
		t.Errorf("second args=%q should not contain --session-id", capturedArgs[1])
	}
}

// --- Cancel/Close/Done 生命周期 ---

// TestPrintCancel_KillsRunning 验证 Cancel 杀当前 running 进程。
func TestPrintCancel_KillsRunning(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	fp := injectFakeSpawn(a, nil)

	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})

	sendDone := make(chan error, 1)
	go func() {
		// 不写任何行，SendPrompt 会阻塞在 parseAndDispatch 读 stdout
		sendDone <- a.SendPrompt(context.Background(), sid, "hi")
	}()

	// 等 running 已登记
	waitForRunning(a, 1*time.Second)

	if err := a.Cancel(context.Background(), sid); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !fp.killed.Load() {
		t.Error("fake process not killed after Cancel")
	}

	select {
	case <-sendDone:
		// SendPrompt 应返回（Kill 关了 stdout reader，parseAndDispatch 退出，Wait 返回）
	case <-time.After(2 * time.Second):
		t.Fatal("SendPrompt did not return after Cancel")
	}
}

// TestPrintCancel_NoRunning 验证无 running 进程时 Cancel no-op（不报错）。
func TestPrintCancel_NoRunning(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	if err := a.Cancel(context.Background(), "sess-1"); err != nil {
		t.Errorf("Cancel with no running returned %v, want nil", err)
	}
}

// TestPrintClose_IdempotentAndDone 验证 Close 幂等且关闭 Done channel。
func TestPrintClose_IdempotentAndDone(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)

	// Close 前 Done 未关闭
	select {
	case <-a.Done():
		t.Fatal("Done() closed before Close")
	default:
	}

	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	select {
	case <-a.Done():
	default:
		t.Fatal("Done() not closed after Close")
	}
	// 幂等：再次 Close 不 panic
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close 2 (idempotent): %v", err)
	}
}

// TestPrintClose_KillsRunning 验证 Close 杀当前 running 进程。
func TestPrintClose_KillsRunning(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	fp := injectFakeSpawn(a, nil)

	sid, _ := a.NewSession(context.Background(), SessionConfig{Cwd: "/tmp"})

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- a.SendPrompt(context.Background(), sid, "hi")
	}()

	waitForRunning(a, 1*time.Second)

	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fp.killed.Load() {
		t.Error("fake process not killed after Close")
	}

	select {
	case <-sendDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SendPrompt did not return after Close")
	}
}

// TestPrintDone_NotClosedBeforeClose 验证 Done 在 Close 前未关闭。
func TestPrintDone_NotClosedBeforeClose(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	select {
	case <-a.Done():
		t.Fatal("Done() closed before any Close")
	default:
	}
}

// waitForRunning 等待 PrintAgent.running 被设置（带超时）。
func waitForRunning(a *PrintAgent, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.procMu.Lock()
		rp := a.running
		a.procMu.Unlock()
		if rp != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// --- 不支持的操作（审批/InjectToolResult） ---

// TestPrintOnPermissionRequest_NoOp 验证 OnPermissionRequest 为 no-op（不存储回调）。
// 注册后内部 onPerm 仍为 nil（PrintAgent 审批不经 adapter）。
func TestPrintOnPermissionRequest_NoOp(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	called := false
	a.OnPermissionRequest(func(r PermissionRequest) { called = true })

	a.cbMu.RLock()
	stored := a.onPerm
	a.cbMu.RUnlock()
	if stored != nil {
		t.Errorf("OnPermissionRequest stored callback %p, want nil (no-op)", stored)
	}
	// 即使手动触发也不应调用（因为没存）
	if called {
		t.Error("OnPermissionRequest callback was invoked, want never")
	}
}

// TestPrintApprove_NotSupported 验证 Approve 返回 ErrNotSupported。
func TestPrintApprove_NotSupported(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	err := a.Approve(context.Background(), "req-1", "opt-1")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("Approve err=%v want ErrNotSupported", err)
	}
}

// TestPrintDeny_NotSupported 验证 Deny 返回 ErrNotSupported。
func TestPrintDeny_NotSupported(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	err := a.Deny(context.Background(), "req-1")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("Deny err=%v want ErrNotSupported", err)
	}
}

// TestPrintInjectToolResult_NotSupported 验证 InjectToolResult 返回 ErrNotSupported。
func TestPrintInjectToolResult_NotSupported(t *testing.T) {
	a := NewPrintAgent(PrintConfig{}, nil)
	err := a.InjectToolResult(context.Background(), "sess-1", "tc-1", "result", false)
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("InjectToolResult err=%v want ErrNotSupported", err)
	}
}

// --- 辅助函数 ---

// TestJSONString 验证 jsonString 的行为。
func TestJSONString(t *testing.T) {
	if got := jsonString(""); got != nil {
		t.Errorf("jsonString(\"\")=%v want nil", got)
	}
	got := jsonString("hello")
	if string(got) != `"hello"` {
		t.Errorf("jsonString(hello)=%q want \"hello\"", string(got))
	}
	// 含特殊字符也应正确编码为合法 JSON
	got = jsonString("a\"b\n")
	var s string
	if err := json.Unmarshal(got, &s); err != nil {
		t.Errorf("jsonString result %q not valid JSON: %v", string(got), err)
	}
	if s != "a\"b\n" {
		t.Errorf("decoded=%q want a\"b\\n", s)
	}
}

// TestTruncateStderr 验证 truncateStderr 截断行为。
func TestTruncateStderr(t *testing.T) {
	// 短文本不截断
	short := "error message"
	if got := truncateStderr(short); got != short {
		t.Errorf("truncateStderr(short)=%q want %q", got, short)
	}
	// 长文本截断
	long := strings.Repeat("x", 3000)
	got := truncateStderr(long)
	if !strings.Contains(got, "已截断") {
		t.Errorf("truncateStderr(long) missing 截断 marker")
	}
	if len([]rune(got)) > 2000+10 { // 2000 + 截断后缀
		t.Errorf("truncateStderr(long) len=%d too long", len([]rune(got)))
	}
}
