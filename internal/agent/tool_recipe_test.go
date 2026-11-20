package agent

import (
	"testing"
)

func TestToolRecipe(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"memory_recall", "memory_recall"},
		{"Memory_Recall", "memory_recall"},
		{"mcp__agentmemory__memory_recall", "memory_recall"},
		{"MCP__AgentMemory__Memory_Save", "memory_save"},
		{"memory_recall query=foo", "memory_recall"},
		{"gopls__definition", "definition"},
		{"", ""},
		{"批准计划：foo", ""},
		{"Bash", ""},
		{"pwsh", ""},
		{"CallMcpTool", ""},
		{"call_dynamic_tool", ""},
		{"mcp", ""},
		{"other", ""},
	}
	for _, c := range cases {
		if got := ToolRecipe(c.in); got != c.want {
			t.Errorf("ToolRecipe(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestToolRecipeFromRawInput(t *testing.T) {
	if got := ToolRecipeFromRawInput([]byte(`{"toolName":"memory_sessions","query":"x"}`)); got != "memory_sessions" {
		t.Errorf("toolName field: got %q", got)
	}
	if got := ToolRecipeFromRawInput([]byte(`{"tool_name":"mcp__agentmemory__memory_audit"}`)); got != "memory_audit" {
		t.Errorf("tool_name mcp prefix: got %q", got)
	}
	if got := ToolRecipeFromRawInput([]byte(`{"name":"memory_save"}`)); got != "memory_save" {
		t.Errorf("name field: got %q", got)
	}
	if got := ToolRecipeFromRawInput([]byte(`{"command":"curl x"}`)); got != "" {
		t.Errorf("shell rawInput must not yield tool recipe, got %q", got)
	}
	if got := ToolRecipeFromRawInput([]byte(`{"toolName":"CallMcpTool"}`)); got != "" {
		t.Errorf("broad shell must be rejected, got %q", got)
	}
}

func TestNormalizeTrustedRecipes_MCP(t *testing.T) {
	got, err := NormalizeTrustedRecipes([]string{
		"memory_recall",
		"mcp__agentmemory__memory_smart_search",
		"memory_recall", // 去重
		"curl",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"memory_recall", "memory_smart_search", "curl"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
	if _, err := NormalizeTrustedRecipes([]string{"CallMcpTool"}); err == nil {
		t.Fatal("broad MCP shell must be rejected")
	}
	if _, err := NormalizeTrustedRecipes([]string{"pwsh"}); err == nil {
		t.Fatal("pwsh must be rejected as trusted recipe")
	}
}
