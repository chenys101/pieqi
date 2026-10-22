package config

import (
	"os"
	"strings"
	"testing"
)

// TestConfig_ExampleHasNoACPAgentSections 守卫「ACP agent 不再由配置声明」。
//
// 2026-10-22 用户要求：约定优于配置 —— 新接入的 agent 不应放在配置上。
// 于是 qoder / dsh / codebuddy 的节已从模板与代码里整体移除：
//   • 代码事实（agent_type / 默认 spawn / 超时 / 能力）→ internal/agent/manifest.go
//   • 用户意图（开不开、装在哪）→ 运行期设置 settings.json 的 agents 段
//
// 这条用例钉住"模板里不许再出现它们"。若有人把 agents.qoder 加回模板，
// 那就是在重建第二份真相：改配置不会生效，而用户会以为生效了。
func TestConfig_ExampleHasNoACPAgentSections(t *testing.T) {
	raw, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	text := string(raw)
	// 只检查"未被注释掉的"行 —— 说明性注释里出现这些名字是正常的。
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, banned := range []string{"qoder:", "dsh:", "codebuddy:"} {
			if trimmed == banned || strings.HasPrefix(trimmed, banned) {
				t.Fatalf("模板里出现了已移除的 agent 节 %q —— 接入新 agent 只应改 manifest.go，"+
					"开关 agent 请走运行期设置", trimmed)
			}
		}
		if strings.Contains(trimmed, "agent_type") {
			t.Fatalf("模板里仍出现 agent_type（代码事实，应归 manifest.go）：%q", trimmed)
		}
	}
	// 加载仍要成功：模板必须是一份合法配置。
	if _, err := Load(exampleConfigPath); err != nil {
		t.Fatalf("load example: %v", err)
	}
}

// TestConfig_ExampleClaudeSectionKept 守卫：claude 的节**必须保留**。
//
// 与上一条不矛盾：桥监听端口、鉴权 token、claude 二进制名、默认模型是真正的
// 机器相关运行期事实 —— 无法由代码推导，也不该由 manifest 编造。
func TestConfig_ExampleClaudeSectionKept(t *testing.T) {
	cfg, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	if cfg.Agents.Claude.Transport != "sdk-bridge" {
		t.Fatalf("example agents.claude.transport = %q, want sdk-bridge", cfg.Agents.Claude.Transport)
	}
	if cfg.Agents.Claude.Bridge.BaseURL == "" {
		t.Fatal("example 缺 agents.claude.bridge.base_url：桥地址是机器相关事实，必须可配")
	}
}