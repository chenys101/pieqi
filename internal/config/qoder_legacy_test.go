package config

import (
	"strings"
	"testing"
)

// TestConfig_QoderLegacyNowWarnsInsteadOfBackfill 旧 pieqi.acp 的 qoder 配置不再"回填到
// agents.qoder.acp"，而是**告警并指出新落点**（运行期设置 spawn_overrides）。
//
// 2026-10-22 整改：agents.qoder 整节已删除（spawn 等归 internal/agent/manifest.go）。
// 这里刻意不静默丢弃：用户写了 acp_spawn_command（例如带 -m Qwen3.8-Flash）而它悄悄
// 失效，表现是"模型莫名其妙变了"，属于最难查的一类问题。
func TestConfig_QoderLegacyNowWarnsInsteadOfBackfill(t *testing.T) {
	p := writeTestConfig(t, "pieqi:\n"+
		"  acp:\n"+
		"    agent_type: qodercli\n"+
		"    acp_spawn_command: [\"qodercli\", \"--acp\", \"--port\", \"1234\"]\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	found := false
	for _, d := range cfg.Deprecations {
		if strings.Contains(d, "spawn_overrides") {
			found = true
		}
	}
	if !found {
		t.Fatalf("旧 qoder spawn 配置未告警到运行期新落点, got: %+v", cfg.Deprecations)
	}
}
