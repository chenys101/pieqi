package core

import (
	"encoding/json"
	"testing"
)

func TestParseStreamLine_Init(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"abc","tools":["Read","Edit"],"skills":["diagnose"],"model":"claude-opus-4-8[1m]","permissionMode":"bypassPermissions"}`
	sl, err := parseStreamLine(line)
	if err != nil || sl == nil {
		t.Fatalf("parse: %v %v", sl, err)
	}
	if sl.Type != "system" || sl.Subtype != "init" {
		t.Fatalf("got type=%s subtype=%s", sl.Type, sl.Subtype)
	}
}

func TestParseStreamLine_AssistantToolUse(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_d36531e1028b47548ca8719e","name":"Read","input":{"file_path":"C:\\sample.txt"}}]}}`
	sl, _ := parseStreamLine(line)
	if sl.Type != "assistant" {
		t.Fatalf("type=%s", sl.Type)
	}
	msg, err := sl.extractMessage()
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != "tool_use" {
		t.Fatalf("content: %+v", msg.Content)
	}
	b := msg.Content[0]
	if b.ID != "call_d36531e1028b47548ca8719e" || b.Name != "Read" {
		t.Fatalf("tool_use: id=%s name=%s", b.ID, b.Name)
	}
	if s := toolUseSummary(b); s != "Read: C:\\sample.txt" {
		t.Fatalf("summary=%q", s)
	}
}

func TestParseStreamLine_ToolResult(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"call_d36531e1028b47548ca8719e","type":"tool_result","content":"hello world"}]}}`
	sl, _ := parseStreamLine(line)
	msg, _ := sl.extractMessage()
	if len(msg.Content) != 1 || msg.Content[0].Type != "tool_result" {
		t.Fatalf("content: %+v", msg.Content)
	}
	if msg.Content[0].ToolUseID != "call_d36531e1028b47548ca8719e" {
		t.Fatalf("tool_use_id=%s", msg.Content[0].ToolUseID)
	}
}

func TestParseStreamLine_Result(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,"result":"hello world","session_id":"s1","num_turns":2,"duration_ms":13083,"terminal_reason":"completed"}`
	sl, _ := parseStreamLine(line)
	r, err := sl.extractResult()
	if err != nil {
		t.Fatal(err)
	}
	if r.Subtype != "success" || r.IsError || r.Result != "hello world" {
		t.Fatalf("result: %+v", r)
	}
}

func TestParseStreamLine_StreamEventWrapper(t *testing.T) {
	// 形态二：stream_event 包裹原生事件（阶段一忽略，但不报错）
	line := `{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`
	sl, err := parseStreamLine(line)
	if err != nil || sl == nil {
		t.Fatalf("parse: %v %v", sl, err)
	}
	if sl.Type != "stream_event" {
		t.Fatalf("type=%s", sl.Type)
	}
}

func TestParseStreamLine_EmptyAndJunk(t *testing.T) {
	if sl, _ := parseStreamLine(""); sl != nil {
		t.Fatal("empty line should return nil")
	}
	if sl, err := parseStreamLine("not json"); sl != nil || err == nil {
		t.Fatal("junk should return nil,nil error... actually err")
	}
}

func TestBuildStreamUserMessage(t *testing.T) {
	msg := buildStreamUserMessage("补全单元测试")
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(msg), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["type"] != "user" {
		t.Fatalf("type=%v", parsed["type"])
	}
	m := parsed["message"].(map[string]interface{})
	if m["role"] != "user" {
		t.Fatalf("role=%v", m["role"])
	}
	content := m["content"].([]interface{})
	c0 := content[0].(map[string]interface{})
	if c0["text"] != "补全单元测试" {
		t.Fatalf("text=%v", c0["text"])
	}
}
