// Package agent: print_stream.go 是 PrintAgent 专用的 stream-json 解析（自包含，不依赖 core）。
//
// Phase 1 的 core/stream_event.go 解析 claude --output-format stream-json 的每行事件，
// 但它在 core 包，agent 包不能 import core（避免循环依赖）。PrintAgent 作为 Phase 1
// claude -p 路径的 AgentAdapter 包装（ACP 适配器不可用时的回退），需要独立实现一份解析。
//
// 解析格式与 core/stream_event.go 对齐（参考该文件），但结构独立：
//   - type:"system" + subtype:"init" → 取 session_id（claude 真实会话 id，可能 ≠ 预生成 uuid）
//   - type:"assistant" → message.content[]：text 块→文本；thinking 块→思考；tool_use 块→{id,name,input}
//   - type:"user" → message.content[]：tool_result 块→{tool_use_id,content,is_error}
//   - type:"result" → {subtype,is_error,result,session_id}
//
// 提供一个解析函数把每行转成 0~N 个枚举式 printStreamEvent，供 PrintAgent 派发回调。
package agent

import (
	"encoding/json"
	"strings"
)

// printStreamEventKind stream-json 行的事件种类（枚举式）。
type printStreamEventKind int

const (
	printEventUnknown           printStreamEventKind = iota
	printEventSystemInit                             // system init 行：含 claude 真实 session_id
	printEventAssistantText                          // assistant message.content 的 text 块
	printEventAssistantThinking                      // assistant message.content 的 thinking 块
	printEventAssistantToolUse                       // assistant message.content 的 tool_use 块
	printEventUserToolResult                         // user message.content 的 tool_result 块
	printEventResult                                 // result 行：终结本轮 prompt turn
)

// printStreamEvent 一行 stream-json 解析后的枚举式结果。
//
// 一行可能产生多个事件（assistant/user 行有多个 content 块时各产一个），
// 故 parsePrintStreamLine 返回切片。字段按 Kind 取用：
//   - printEventSystemInit    → SessionID
//   - printEventAssistantText/Thinking → Text
//   - printEventAssistantToolUse → ToolCallID/ToolName/ToolInput
//   - printEventUserToolResult → ToolCallID/IsError/ToolOutput
//   - printEventResult        → Subtype/IsError/Text(result.result)/SessionID
type printStreamEvent struct {
	Kind printStreamEventKind

	SessionID string // system init / result 行的 session_id

	Text       string          // text/thinking 块内容；result 行的 result 字段
	Subtype    string          // result 行的 subtype（success / error_max_tokens / ...）
	IsError    bool            // result 行的 is_error
	ToolCallID string          // tool_use.id / tool_result.tool_use_id
	ToolName   string          // tool_use.name
	ToolInput  json.RawMessage // tool_use.input 原始 JSON
	ToolOutput string          // tool_result.content 文本化后的结果
}

// printStreamLine stream-json 一行的顶层结构。
type printStreamLine struct {
	Type      string          `json:"type"`       // system / assistant / user / result
	Subtype   string          `json:"subtype"`    // system: init；result: success/...
	SessionID string          `json:"session_id"` // system init 报告 claude 真实会话 id
	Raw       json.RawMessage `json:"-"`          // 整行原始 JSON，供按类型再解析
}

// printContentBlock assistant/user message.content 数组的一个元素。
type printContentBlock struct {
	Type      string          `json:"type"`                  // text / tool_use / tool_result / thinking
	Text      string          `json:"text,omitempty"`        // text 块
	Thinking  string          `json:"thinking,omitempty"`    // thinking 块（内容在 thinking 字段，不在 text）
	ID        string          `json:"id,omitempty"`          // tool_use id
	Name      string          `json:"name,omitempty"`        // tool_use name
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use input
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result 关联的 tool_use id
	Content   json.RawMessage `json:"content,omitempty"`     // tool_result content（string 或数组）
	IsError   bool            `json:"is_error,omitempty"`
}

// printMessageEnvelope assistant/user 事件的 message 字段。
type printMessageEnvelope struct {
	Role    string              `json:"role"`
	Content []printContentBlock `json:"content"`
}

// printResultBody result 事件的字段。
type printResultBody struct {
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

// parsePrintStreamLine 解析一行 stream-json，返回 0~N 个 printStreamEvent。
//   - 空行 / 无法 JSON 解析 → nil
//   - system init 行 → 1 个 printEventSystemInit
//   - assistant 行 → 每个 text/thinking/tool_use 块各 1 个 event（空文本块跳过）
//   - user 行 → 每个 tool_result 块各 1 个 event
//   - result 行 → 1 个 printEventResult
//   - 其他类型（stream_event/system status 等）→ nil（PrintAgent 不关心）
func parsePrintStreamLine(line string) []printStreamEvent {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var sl printStreamLine
	if err := json.Unmarshal([]byte(line), &sl); err != nil {
		return nil
	}
	sl.Raw = json.RawMessage(line)

	switch sl.Type {
	case "system":
		if sl.Subtype == "init" && sl.SessionID != "" {
			return []printStreamEvent{{Kind: printEventSystemInit, SessionID: sl.SessionID}}
		}
		return nil
	case "assistant":
		msg, err := extractPrintMessage(sl.Raw)
		if err != nil || msg == nil {
			return nil
		}
		var events []printStreamEvent
		for _, b := range msg.Content {
			switch b.Type {
			case "text":
				// 跳过纯空白文本块（避免空增量噪声）
				if strings.TrimSpace(b.Text) != "" {
					events = append(events, printStreamEvent{Kind: printEventAssistantText, Text: b.Text})
				}
			case "thinking":
				// thinking 块内容在 Thinking 字段（不在 Text）
				if strings.TrimSpace(b.Thinking) != "" {
					events = append(events, printStreamEvent{Kind: printEventAssistantThinking, Text: b.Thinking})
				}
			case "tool_use":
				events = append(events, printStreamEvent{
					Kind:       printEventAssistantToolUse,
					ToolCallID: b.ID,
					ToolName:   b.Name,
					ToolInput:  b.Input,
				})
			}
		}
		return events
	case "user":
		msg, err := extractPrintMessage(sl.Raw)
		if err != nil || msg == nil {
			return nil
		}
		var events []printStreamEvent
		for _, b := range msg.Content {
			if b.Type == "tool_result" {
				events = append(events, printStreamEvent{
					Kind:       printEventUserToolResult,
					ToolCallID: b.ToolUseID,
					IsError:    b.IsError,
					ToolOutput: printToolResultText(b.Content),
				})
			}
		}
		return events
	case "result":
		var r printResultBody
		if err := json.Unmarshal(sl.Raw, &r); err != nil {
			return nil
		}
		return []printStreamEvent{{
			Kind:      printEventResult,
			Subtype:   r.Subtype,
			IsError:   r.IsError,
			Text:      r.Result,
			SessionID: r.SessionID,
		}}
	}
	return nil
}

// extractPrintMessage 从 assistant/user 事件行解析 message.content。
func extractPrintMessage(raw json.RawMessage) (*printMessageEnvelope, error) {
	var env struct {
		Message printMessageEnvelope `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return &env.Message, nil
}

// printToolResultText 把 tool_result.content 文本化。
// content 可能是 string 或 contentBlock 数组（参考 core.toolResultText，独立实现）。
func printToolResultText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	// 先试 string
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	// 再试数组：取每个块的 text 字段拼接
	var blocks []printContentBlock
	if json.Unmarshal(content, &blocks) == nil {
		var parts []string
		for _, blk := range blocks {
			if blk.Text != "" {
				parts = append(parts, blk.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	// 兜底：原样返回
	return string(content)
}
