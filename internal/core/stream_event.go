package core

import (
	"encoding/json"
	"strings"
)

// stream_event.go 解析 claude --output-format stream-json 的每行事件。
//
// 实测两种顶层形态（见 plan 附录 A.1）：
//  1. Claude Code 自有事件：{type:"system"|"assistant"|"user"|"result", ...}
//  2. Anthropic 原生流事件：{type:"stream_event", event:{type:"message_start"|...}}
//
// 阶段一只解析形态一（buffered）；形态二留给阶段二 partial 逐字。

// streamLine stream-json 的一行原始解析结果。
type streamLine struct {
	Type      string          `json:"type"`      // system / assistant / user / result / stream_event
	Subtype   string          `json:"subtype"`   // system: init/status/thinking_tokens；result: success/...
	SessionID string          `json:"session_id"` // system init 报告 claude 真实会话 id（可能 ≠ 预生成 uuid）
	Raw       json.RawMessage `json:"-"`         // 整行原始 JSON
	Body      json.RawMessage `json:"-"`         // 按类型再解析的子体
}

// contentBlock assistant/user message.content 数组的一个元素。
type contentBlock struct {
	Type       string          `json:"type"`         // text / tool_use / tool_result / thinking
	Text       string          `json:"text,omitempty"`
	Thinking   string          `json:"thinking,omitempty"` // thinking 块的推理草稿（内容在 thinking 字段，不在 text）
	ID         string          `json:"id,omitempty"`          // tool_use id
	Name       string          `json:"name,omitempty"`        // tool_use name
	Input      json.RawMessage `json:"input,omitempty"`       // tool_use input
	ToolUseID  string          `json:"tool_use_id,omitempty"` // tool_result 关联的 tool_use id
	Content    json.RawMessage `json:"content,omitempty"`     // tool_result content（可能是 string 或数组）
	IsError    bool            `json:"is_error,omitempty"`
}

// messageEnvelope assistant/user 事件的 message 字段。
type messageEnvelope struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// resultBody result 事件的字段。
type resultBody struct {
	Subtype           string  `json:"subtype"`
	IsError           bool    `json:"is_error"`
	Result            string  `json:"result"`
	SessionID         string  `json:"session_id"`
	NumTurns          int     `json:"num_turns"`
	DurationMS        int64   `json:"duration_ms"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TerminalReason    string  `json:"terminal_reason"`
	StopReason        string  `json:"stop_reason"`
}

// parseStreamLine 解析一行 stream-json。
func parseStreamLine(line string) (*streamLine, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	var sl streamLine
	if err := json.Unmarshal([]byte(line), &sl); err != nil {
		return nil, err
	}
	sl.Raw = json.RawMessage(line)
	return &sl, nil
}

// extractMessage 从 assistant/user 事件行解析 message.content。
func (sl *streamLine) extractMessage() (*messageEnvelope, error) {
	var env struct {
		Message messageEnvelope `json:"message"`
	}
	if err := json.Unmarshal(sl.Raw, &env); err != nil {
		return nil, err
	}
	return &env.Message, nil
}

// extractResult 从 result 事件行解析结果体。
func (sl *streamLine) extractResult() (*resultBody, error) {
	var r resultBody
	if err := json.Unmarshal(sl.Raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// toolUseSummary 由 tool_use 块合成人读摘要，如 "Bash: rm -rf node_modules"。
func toolUseSummary(b contentBlock) string {
	input := strings.TrimSpace(string(b.Input))
	// 取 input 的第一个字符串字段值做摘要，截断到 80 字符
	var m map[string]json.RawMessage
	if json.Unmarshal(b.Input, &m) == nil {
		for _, key := range []string{"command", "file_path", "pattern", "path"} {
			if v, ok := m[key]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil {
					input = s
					break
				}
			}
		}
	}
	if len(input) > 80 {
		input = input[:80] + "..."
	}
	return b.Name + ": " + input
}

// toolResultText 把 tool_result 的 content 文本化。content 可能是 string 或 contentBlock 数组。
func toolResultText(b contentBlock) string {
	if len(b.Content) == 0 {
		return ""
	}
	// 先试 string
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return s
	}
	// 再试数组:取每个块的 text 字段拼接
	var blocks []contentBlock
	if json.Unmarshal(b.Content, &blocks) == nil {
		var parts []string
		for _, blk := range blocks {
			if blk.Text != "" {
				parts = append(parts, blk.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(b.Content)
}
