package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestConfig_AuthDefaults(t *testing.T) {
	p := writeTestConfig(t, "server:\n  port: 3000\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Auth.DebugSkipAllAuth {
		t.Fatal("debug_skip_all_auth default should be false")
	}
	if cfg.Auth.FeishuBindingFile == "" {
		t.Fatal("feishu_binding_file should default to ~/.pieqi/feishu_binding.json")
	}
	if cfg.Auth.Cloudflared.BinaryPath == "" {
		t.Fatal("cloudflared.binary_path should default to 'cloudflared'")
	}
	if cfg.Auth.Cloudflared.DefaultTTL != 15*time.Minute {
		t.Fatalf("default ttl = %v, want 15m", cfg.Auth.Cloudflared.DefaultTTL)
	}
	if cfg.Auth.RateLimit.MaxFailuresPerMin != 5 || cfg.Auth.RateLimit.BlacklistDuration != 10*time.Minute {
		t.Fatalf("ratelimit defaults wrong: %+v", cfg.Auth.RateLimit)
	}
}

func TestConfig_AuthOverride(t *testing.T) {
	p := writeTestConfig(t, `
auth:
  debug_skip_all_auth: true
  feishu_binding_file: /tmp/binding.json
  cloudflared:
    binary_path: /usr/local/bin/cloudflared
    default_ttl: 1h
  ratelimit:
    max_failures_per_min: 3
    blacklist_duration: 5m
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Auth.DebugSkipAllAuth {
		t.Fatal("debug should be true")
	}
	if cfg.Auth.Cloudflared.DefaultTTL != time.Hour {
		t.Fatalf("ttl = %v, want 1h", cfg.Auth.Cloudflared.DefaultTTL)
	}
}

// TestConfig_EmptyFeishuBindingFileFallsBackToDefault verifies that an
// explicitly-empty feishu_binding_file (as the production-default
// config.yaml sets) falls back to the default path instead of leaving the
// field as "" (which would make auth.NewBindingStore("") misbehave).
// Viper overrides SetDefault with any explicit file value, including "".
func TestConfig_EmptyFeishuBindingFileFallsBackToDefault(t *testing.T) {
	want := filepath.Join(DefaultDataRoot(), "feishu_binding.json")

	// Case 1: auth block present with feishu_binding_file: "" explicitly.
	t.Run("explicit_empty", func(t *testing.T) {
		p := writeTestConfig(t, "server:\n  port: 3000\nauth:\n  feishu_binding_file: \"\"\n")
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Auth.FeishuBindingFile == "" {
			t.Fatal("feishu_binding_file must not be empty when explicitly set to \"\"")
		}
		if cfg.Auth.FeishuBindingFile != want {
			t.Fatalf("feishu_binding_file = %q, want %q", cfg.Auth.FeishuBindingFile, want)
		}
	})

	// Case 2: no auth block at all — relies on the Viper default + the
	// same normalization (defensive: empty default path stays empty).
	t.Run("no_auth_block", func(t *testing.T) {
		p := writeTestConfig(t, "server:\n  port: 3000\n")
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.Auth.FeishuBindingFile == "" {
			t.Fatal("feishu_binding_file must not be empty when no auth block is present")
		}
		if cfg.Auth.FeishuBindingFile != want {
			t.Fatalf("feishu_binding_file = %q, want %q", cfg.Auth.FeishuBindingFile, want)
		}
	})
}

func TestConfig_LarkDefaults(t *testing.T) {
	p := writeTestConfig(t, "server:\n  port: 3000\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Channels.Lark.EventMode != "webhook" {
		t.Fatalf("EventMode default = %q, want \"webhook\"", cfg.Channels.Lark.EventMode)
	}
	if cfg.Channels.Lark.CredentialsFile == "" {
		t.Fatal("CredentialsFile should default to ~/.pieqi/lark_credentials.json")
	}
	// 验证默认路径形态(与 feishu_binding_file 同目录)
	if !strings.HasSuffix(cfg.Channels.Lark.CredentialsFile, "lark_credentials.json") {
		t.Fatalf("CredentialsFile default = %q, want suffix lark_credentials.json", cfg.Channels.Lark.CredentialsFile)
	}
}

func TestConfig_LarkEmptyCredentialsFileFallsBack(t *testing.T) {
	body := "server:\n  port: 3000\nchannels:\n  lark:\n    credentials_file: \"\"\n"
	p := writeTestConfig(t, body)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Channels.Lark.CredentialsFile == "" {
		t.Fatal("empty credentials_file should fall back to default path")
	}
}

func TestConfig_AgentsDefaults(t *testing.T) {
	p := writeTestConfig(t, "server:\n  port: 3000\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agents.Claude.Transport != "sdk-bridge" {
		t.Fatalf("agents.claude.transport default = %q, want sdk-bridge", cfg.Agents.Claude.Transport)
	}
	if cfg.Agents.Claude.Bridge.BaseURL != "http://127.0.0.1:18790" {
		t.Fatalf("agents.claude.bridge.base_url default = %q, want http://127.0.0.1:18790", cfg.Agents.Claude.Bridge.BaseURL)
	}
	if !cfg.Agents.Claude.Bridge.AutoStart {
		t.Fatal("agents.claude.bridge.auto_start default should be true")
	}
	if cfg.Agents.Claude.Bridge.Token != "" {
		t.Fatal("agents.claude.bridge.token default should be empty")
	}
	if cfg.Agents.Claude.Print.Command != "claude" {
		t.Fatalf("agents.claude.print.command default = %q, want claude", cfg.Agents.Claude.Print.Command)
	}
	if cfg.Agents.Qoder.Transport != "acp" {
		t.Fatalf("agents.qoder.transport default = %q, want acp", cfg.Agents.Qoder.Transport)
	}
	if cfg.Agents.Qoder.ACP.AgentType != "qodercli" {
		t.Fatalf("agents.qoder.acp.agent_type default = %q, want qodercli", cfg.Agents.Qoder.ACP.AgentType)
	}
}

func TestConfig_AgentsOverride(t *testing.T) {
	p := writeTestConfig(t, `
agents:
  claude:
    transport: sdk-bridge
    bridge:
      base_url: "http://127.0.0.1:19999"
      token: "s3cr3t"
      auto_start: false
    print:
      command: "claude"
      permission_mode: "default"
      model: "opus"
      sys_prompt: "be concise"
  qoder:
    transport: acp
    acp:
      agent_type: qodercli
      spawn_command: ["qodercli", "--acp"]
      init_timeout: 10s
      idle_timeout: 5m
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agents.Claude.Bridge.BaseURL != "http://127.0.0.1:19999" || cfg.Agents.Claude.Bridge.Token != "s3cr3t" {
		t.Fatalf("bridge override wrong: %+v", cfg.Agents.Claude.Bridge)
	}
	if cfg.Agents.Claude.Bridge.AutoStart {
		t.Fatal("auto_start override should be false")
	}
	if cfg.Agents.Claude.Print.Model != "opus" || cfg.Agents.Claude.Print.SysPrompt != "be concise" {
		t.Fatalf("print override wrong: %+v", cfg.Agents.Claude.Print)
	}
	if cfg.Agents.Claude.Print.PermissionMode != "default" {
		t.Fatalf("print permission_mode override wrong: %q", cfg.Agents.Claude.Print.PermissionMode)
	}
	acp := cfg.Agents.Qoder.ACPConfig()
	if acp.AgentType != "qodercli" || len(acp.SpawnCommand) != 2 || acp.SpawnCommand[0] != "qodercli" {
		t.Fatalf("qoder ACPConfig() wrong: %+v", acp)
	}
	if acp.InitTimeout != 10*time.Second || acp.IdleTimeout != 5*time.Minute {
		t.Fatalf("qoder timeouts wrong: init=%v idle=%v", acp.InitTimeout, acp.IdleTimeout)
	}
}

func TestConfig_DeprecationWarnings(t *testing.T) {
	p := writeTestConfig(t, `
pieqi:
  acp:
    use_acp: true
    agent_type: claude-code
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Deprecations) != 2 {
		t.Fatalf("deprecations = %d, want 2 (use_acp + agent_type): %+v", len(cfg.Deprecations), cfg.Deprecations)
	}
	joined := strings.Join(cfg.Deprecations, ";")
	if !strings.Contains(joined, "use_acp") || !strings.Contains(joined, "agent_type") {
		t.Fatalf("deprecations missing keys: %+v", cfg.Deprecations)
	}
	if !strings.Contains(joined, "agents.claude") || !strings.Contains(joined, "agents.qoder") {
		t.Fatalf("deprecations missing migration target: %+v", cfg.Deprecations)
	}
	// 旧字段语义不受影响（仍被 AgentManager 消费）
	if !cfg.Pieqi.ACP.UseACP || cfg.Pieqi.ACP.AgentType != "claude-code" {
		t.Fatalf("legacy acp fields changed: %+v", cfg.Pieqi.ACP)
	}
}

func TestConfig_NoDeprecationWithoutLegacy(t *testing.T) {
	p := writeTestConfig(t, "server:\n  port: 3000\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Deprecations) != 0 {
		t.Fatalf("deprecations should be empty without legacy fields, got: %+v", cfg.Deprecations)
	}
}

func TestConfig_QoderLegacyBackfill(t *testing.T) {
	// 老配置（agent_type: qodercli + spawn_command）未配 agents.qoder → 自动回填新节
	p := writeTestConfig(t, `
pieqi:
  acp:
    agent_type: qodercli
    acp_spawn_command: ["qodercli", "--acp", "--port", "1234"]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agents.Qoder.Transport != "acp" {
		t.Fatalf("qoder transport backfill = %q, want acp", cfg.Agents.Qoder.Transport)
	}
	q := cfg.Agents.Qoder.ACP
	if q.AgentType != "qodercli" || len(q.SpawnCommand) != 4 || q.SpawnCommand[0] != "qodercli" {
		t.Fatalf("qoder acp backfill wrong: %+v", q)
	}
	if cfg.Agents.Qoder.ACPConfig().AgentType != "qodercli" {
		t.Fatalf("ACPConfig() after backfill wrong")
	}
	found := false
	for _, d := range cfg.Deprecations {
		if strings.Contains(d, "自动回填") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected qoder backfill deprecation note, got: %+v", cfg.Deprecations)
	}
}

func TestConfig_QoderNoBackfillWhenNewSectionSet(t *testing.T) {
	// agents.qoder 已显式配置 → 不回填旧字段
	p := writeTestConfig(t, `
pieqi:
  acp:
    agent_type: qodercli
    acp_spawn_command: ["qodercli", "--acp"]
agents:
  qoder:
    transport: acp
    acp:
      agent_type: qodercli
      spawn_command: ["qodercli", "--acp", "--port", "9999"]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Agents.Qoder.ACP.SpawnCommand; len(got) != 4 || got[3] != "9999" {
		t.Fatalf("qoder acp should keep explicit config (not backfill), got: %v", got)
	}
}
