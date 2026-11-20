package agent

import (
	"encoding/json"
	"strings"
	"unicode"
)

// ToolRecipe 从非 shell / MCP 工具标题抽出配方指纹。
//
// 用途与 ShellRecipes 对称：会话记忆与设置页 trusted_recipes 用同一串做匹配。
//
//	memory_recall                         → memory_recall
//	mcp__agentmemory__memory_recall       → memory_recall（取末段）
//	MCP__AgentMemory__Memory_Save         → memory_save
//	批准计划：xxx / Bash / 带空格的散文    → ""（不是工具 id，不记）
//
// 空串表示「这条请求没有可记忆的工具配方」——调用方不得据此免审。
func ToolRecipe(toolTitle string) string {
	t := strings.ToLower(strings.TrimSpace(toolTitle))
	if t == "" {
		return ""
	}
	// 标题偶发带参数（`memory_recall query=…`）：只取首词。
	if i := strings.IndexAny(t, " \t\r\n"); i >= 0 {
		t = t[:i]
	}
	// MCP 命名空间前缀：`mcp__server__tool` / `gopls__definition` → 末段工具名。
	if strings.Contains(t, "__") {
		parts := strings.Split(t, "__")
		for i := len(parts) - 1; i >= 0; i-- {
			if p := strings.TrimSpace(parts[i]); p != "" {
				t = p
				break
			}
		}
	}
	if !isToolRecipeID(t) {
		return ""
	}
	if toolRecipeTooBroad(t) {
		return ""
	}
	return t
}

// ToolRecipeFromRawInput 从 MCP / 动态工具入参里抽工具名（标题是 CallMcpTool 等宽壳时用）。
//
// 认常见字段；取到后仍走 ToolRecipe 规范化（含 mcp__ 剥段与宽名拒绝）。
func ToolRecipeFromRawInput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, k := range []string{"toolName", "tool_name", "tool"} {
		s, _ := m[k].(string)
		if r := ToolRecipe(s); r != "" {
			return r
		}
	}
	// CallDynamicTool 形态：namespace + toolName 已在上面；偶发只有 name。
	if s, _ := m["name"].(string); s != "" {
		if r := ToolRecipe(s); r != "" {
			return r
		}
	}
	return ""
}

// isToolRecipeID 只收 ASCII 标识符形态（字母开头，后接字母数字 _ -）。
//
// 中文计划卡标题、路径、整句摘要一律拒绝 —— 否则 approve_session 会把
// 「批准计划：…」记成配方，下次同标题误放行。
func isToolRecipeID(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r < 'a' || r > 'z' {
				return false
			}
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false // 非 ASCII 字母数字：不收
		}
		return false
	}
	return true
}

// toolRecipeTooBroad 拒绝「一次信任等于任意 MCP / 任意 shell」的宽壳名。
func toolRecipeTooBroad(id string) bool {
	switch id {
	case "mcp", "tool", "tools", "other", "unknown",
		"callmcptool", "call_mcp_tool", "calldynamictool", "call_dynamic_tool",
		"bash", "pwsh", "powershell", "powershell.exe", "shell", "cmd", "cmd.exe",
		"execute", "switch_mode", "switch-mode":
		return true
	default:
		return false
	}
}
