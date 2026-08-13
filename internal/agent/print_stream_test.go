package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestParsePrintStreamLine_EmptyAndInvalid 验证空行与无法 JSON 解析的行返回 nil。
func TestParsePrintStreamLine_EmptyAndInvalid(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"\n",
		"not a json",
		"{", // 不完整 JSON
	}
	for _, c := range cases {
		if got := parsePrintStreamLine(c); got != nil {
			t.Errorf("parsePrintStreamLine(%q)=%v, want nil", c, got)
		}
	}
}

// TestParsePrintStreamLine_SystemInit 验证 system init 行提取 session_id。
func TestParsePrintStreamLine_SystemInit(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"claude-real-sid-123","cwd":"/tmp","tools":[]}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	ev := got[0]
	if ev.Kind != printEventSystemInit {
		t.Errorf("Kind=%v want printEventSystemInit", ev.Kind)
	}
	if ev.SessionID != "claude-real-sid-123" {
		t.Errorf("SessionID=%q want claude-real-sid-123", ev.SessionID)
	}
}

// TestParsePrintStreamLine_SystemNonInit 验证 system 非 init 行（如 status）被忽略。
func TestParsePrintStreamLine_SystemNonInit(t *testing.T) {
	line := `{"type":"system","subtype":"status","session_id":"x"}`
	if got := parsePrintStreamLine(line); got != nil {
		t.Errorf("system status line returned %v, want nil", got)
	}
}

// TestParsePrintStreamLine_AssistantText 验证 assistant text 块提取文本。
func TestParsePrintStreamLine_AssistantText(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello world"}]}}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Kind != printEventAssistantText || got[0].Text != "hello world" {
		t.Errorf("ev=%+v want text=hello world", got[0])
	}
}

// TestParsePrintStreamLine_AssistantThinking 验证 thinking 块内容在 Thinking 字段（非 Text）。
func TestParsePrintStreamLine_AssistantThinking(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning about the task"}]}}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Kind != printEventAssistantThinking || got[0].Text != "reasoning about the task" {
		t.Errorf("ev=%+v want thinking=reasoning about the task", got[0])
	}
}

// TestParsePrintStreamLine_AssistantToolUse 验证 tool_use 块提取 id/name/input。
func TestParsePrintStreamLine_AssistantToolUse(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls -la"}}]}}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	ev := got[0]
	if ev.Kind != printEventAssistantToolUse {
		t.Errorf("Kind=%v want printEventAssistantToolUse", ev.Kind)
	}
	if ev.ToolCallID != "tu_1" || ev.ToolName != "Bash" {
		t.Errorf("id=%q name=%q want tu_1/Bash", ev.ToolCallID, ev.ToolName)
	}
	// RawInput 应为原始 JSON（map 单键，键名确定）
	if string(ev.ToolInput) != `{"command":"ls -la"}` {
		t.Errorf("ToolInput=%q want {\"command\":\"ls -la\"}", string(ev.ToolInput))
	}
}

// TestParsePrintStreamLine_UserToolResult 验证 user tool_result 块提取字段。
func TestParsePrintStreamLine_UserToolResult(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"total 0\ndrwxr-xr-x","is_error":false}]}}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	ev := got[0]
	if ev.Kind != printEventUserToolResult {
		t.Errorf("Kind=%v want printEventUserToolResult", ev.Kind)
	}
	if ev.ToolCallID != "tu_1" {
		t.Errorf("ToolCallID=%q want tu_1", ev.ToolCallID)
	}
	if ev.IsError {
		t.Errorf("IsError=true want false")
	}
	if ev.ToolOutput != "total 0\ndrwxr-xr-x" {
		t.Errorf("ToolOutput=%q want 'total 0\\ndrwxr-xr-x'", ev.ToolOutput)
	}
}

// TestParsePrintStreamLine_UserToolResultIsError 验证 tool_result 的 is_error=true 被捕获。
func TestParsePrintStreamLine_UserToolResultIsError(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_2","content":"command not found","is_error":true}]}}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if !got[0].IsError {
		t.Errorf("IsError=false want true")
	}
}

// TestParsePrintStreamLine_ToolResultContentArray 验证 tool_result.content 为 contentBlock 数组时拼接 text。
func TestParsePrintStreamLine_ToolResultContentArray(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_3","content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}]}}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].ToolOutput != "line1\nline2" {
		t.Errorf("ToolOutput=%q want 'line1\\nline2'", got[0].ToolOutput)
	}
}

// TestParsePrintStreamLine_ResultSuccess 验证 result 行提取字段（成功）。
func TestParsePrintStreamLine_ResultSuccess(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sid-1","num_turns":3,"duration_ms":1234}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	ev := got[0]
	if ev.Kind != printEventResult {
		t.Errorf("Kind=%v want printEventResult", ev.Kind)
	}
	if ev.Subtype != "success" || ev.IsError || ev.Text != "done" || ev.SessionID != "sid-1" {
		t.Errorf("ev=%+v want subtype=success is_error=false result=done session_id=sid-1", ev)
	}
}

// TestParsePrintStreamLine_ResultIsError 验证 result 行的 is_error=true 被捕获。
func TestParsePrintStreamLine_ResultIsError(t *testing.T) {
	line := `{"type":"result","subtype":"error_max_tokens","is_error":true,"result":"hit token limit","session_id":"sid-2"}`
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if !got[0].IsError {
		t.Errorf("IsError=false want true")
	}
	if got[0].Subtype != "error_max_tokens" {
		t.Errorf("Subtype=%q want error_max_tokens", got[0].Subtype)
	}
}

// TestParsePrintStreamLine_MultiBlocks 验证一行多 content 块拆成多事件。
func TestParsePrintStreamLine_MultiBlocks(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"let me think"},{"type":"text","text":"answer"},{"type":"tool_use","id":"tu_9","name":"Read","input":{"file_path":"/tmp/x"}}]}}`
	got := parsePrintStreamLine(line)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Kind != printEventAssistantThinking || got[0].Text != "let me think" {
		t.Errorf("ev[0]=%+v want thinking=let me think", got[0])
	}
	if got[1].Kind != printEventAssistantText || got[1].Text != "answer" {
		t.Errorf("ev[1]=%+v want text=answer", got[1])
	}
	if got[2].Kind != printEventAssistantToolUse || got[2].ToolCallID != "tu_9" || got[2].ToolName != "Read" {
		t.Errorf("ev[2]=%+v want tool_use tu_9/Read", got[2])
	}
}

// TestParsePrintStreamLine_EmptyTextSkipped 验证纯空白 text/thinking 块被跳过（避免空增量噪声）。
func TestParsePrintStreamLine_EmptyTextSkipped(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"   "},{"type":"thinking","thinking":""}]}}`
	if got := parsePrintStreamLine(line); got != nil {
		t.Errorf("empty blocks returned %v, want nil", got)
	}
}

// TestParsePrintStreamLine_UnknownType 验证未识别的 type（如 stream_event）返回 nil。
func TestParsePrintStreamLine_UnknownType(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"message_start"}}`
	if got := parsePrintStreamLine(line); got != nil {
		t.Errorf("stream_event returned %v, want nil", got)
	}
}

// TestPrintToolResultText 验证 tool_result content 文本化的各分支。
func TestPrintToolResultText(t *testing.T) {
	cases := []struct {
		name string
		in   json.RawMessage
		want string
	}{
		{"empty", nil, ""},
		{"string", json.RawMessage(`"hi"`), "hi"},
		{"array", json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`), "a\nb"},
		{"array_empty_text_skipped", json.RawMessage(`[{"type":"text","text":""},{"type":"text","text":"x"}]`), "x"},
		{"fallback_raw", json.RawMessage(`123`), "123"}, // 非 string/数组 → 原样
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := printToolResultText(c.in); got != c.want {
				t.Errorf("printToolResultText(%s)=%q want %q", string(c.in), got, c.want)
			}
		})
	}
}

// TestPrintStreamEventKind_NonZero 防御性断言枚举常量定义合理（避免后续重构打乱顺序）。
func TestPrintStreamEventKind_NonZero(t *testing.T) {
	kinds := []printStreamEventKind{
		printEventUnknown,
		printEventSystemInit,
		printEventAssistantText,
		printEventAssistantThinking,
		printEventAssistantToolUse,
		printEventUserToolResult,
		printEventResult,
	}
	// 所有 Kind 应互不相同
	seen := map[printStreamEventKind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("duplicate Kind %d", k)
		}
		seen[k] = true
	}
	// printEventUnknown 应为 0（零值）
	if printEventUnknown != 0 {
		t.Errorf("printEventUnknown=%d want 0", printEventUnknown)
	}
}

// TestParsePrintStreamLine_AssistantMalformed 验证 assistant 行 message 字段缺失时不 panic 返回 nil。
func TestParsePrintStreamLine_AssistantMalformed(t *testing.T) {
	line := `{"type":"assistant"}` // 无 message 字段
	if got := parsePrintStreamLine(line); got != nil {
		t.Errorf("malformed assistant line returned %v, want nil", got)
	}
}

// TestParsePrintStreamLine_ResultMalformed 验证 result 行字段缺失时不 panic。
func TestParsePrintStreamLine_ResultMalformed(t *testing.T) {
	line := `{"type":"result"}` // 无字段
	got := parsePrintStreamLine(line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 (empty result)", len(got))
	}
	if got[0].Kind != printEventResult {
		t.Errorf("Kind=%v want printEventResult", got[0].Kind)
	}
	// 各字段应为零值
	if !reflect.DeepEqual(got[0], printStreamEvent{Kind: printEventResult}) {
		t.Errorf("ev=%+v want zero-value result event", got[0])
	}
}
