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
	if cfg.Auth.Cloudflared.HealthCheckInterval != 5*time.Minute {
		t.Fatalf("health_check_interval default = %v, want 5m", cfg.Auth.Cloudflared.HealthCheckInterval)
	}
	if cfg.Auth.Cloudflared.HealthCheckFailures != 3 {
		t.Fatalf("health_check_failures default = %d, want 3", cfg.Auth.Cloudflared.HealthCheckFailures)
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
    health_check_interval: 30s
    health_check_failures: 5
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
	if cfg.Auth.Cloudflared.HealthCheckInterval != 30*time.Second {
		t.Fatalf("health_check_interval = %v, want 30s", cfg.Auth.Cloudflared.HealthCheckInterval)
	}
	if cfg.Auth.Cloudflared.HealthCheckFailures != 5 {
		t.Fatalf("health_check_failures = %d, want 5", cfg.Auth.Cloudflared.HealthCheckFailures)
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

func TestConfig_AutoApproveTools(t *testing.T) {
	// 默认：文件改动类 ACP ToolKind 免审（edit/delete/move）。
	p := writeTestConfig(t, "server:\n  port: 3000\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{"edit", "delete", "move"}
	if len(cfg.Pieqi.AutoApproveTools) != len(want) {
		t.Fatalf("auto_approve_tools default = %v, want %v", cfg.Pieqi.AutoApproveTools, want)
	}
	for i, w := range want {
		if cfg.Pieqi.AutoApproveTools[i] != w {
			t.Fatalf("auto_approve_tools default = %v, want %v", cfg.Pieqi.AutoApproveTools, want)
		}
	}

	// 覆盖：显式配置全量替换默认（如只留 edit，或清空关闭免审）。
	p2 := writeTestConfig(t, "pieqi:\n  auto_approve_tools: [\"edit\"]\n")
	cfg2, err := Load(p2)
	if err != nil {
		t.Fatalf("load override: %v", err)
	}
	if len(cfg2.Pieqi.AutoApproveTools) != 1 || cfg2.Pieqi.AutoApproveTools[0] != "edit" {
		t.Fatalf("auto_approve_tools override = %v, want [edit]", cfg2.Pieqi.AutoApproveTools)
	}

	// 空名单 = 显式关闭免审（所有权限请求都走人工审批）。
	p3 := writeTestConfig(t, "pieqi:\n  auto_approve_tools: []\n")
	cfg3, err := Load(p3)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(cfg3.Pieqi.AutoApproveTools) != 0 {
		t.Fatalf("auto_approve_tools empty = %v, want []", cfg3.Pieqi.AutoApproveTools)
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

// TestConfig_TunnelModeAndTokenFileDefaults verifies the named-tunnel knobs:
// mode defaults to quick (临时域名), the token file defaults to a path OUTSIDE
// the repo (~/.pieqi/cloudflared_token), and an explicitly-empty
// tunnel_token_file falls back to that default (viper's empty-string override).
func TestConfig_TunnelModeAndTokenFileDefaults(t *testing.T) {
	p := writeTestConfig(t, "server:\n  port: 3000\n  auth:\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	c := cfg.Auth.Cloudflared
	if c.Mode != "quick" {
		t.Fatalf("mode default = %q, want quick (临时域名)", c.Mode)
	}
	if c.IsNamed() {
		t.Fatal("IsNamed must be false for the default quick mode")
	}
	wantSuffix := filepath.Join(".pieqi", "cloudflared_token")
	if !strings.HasSuffix(filepath.ToSlash(c.TunnelTokenFile), filepath.ToSlash(wantSuffix)) {
		t.Fatalf("tunnel_token_file default = %q, want ...%s", c.TunnelTokenFile, wantSuffix)
	}

	// 显式空值同样回退默认（同 feishu_binding_file 的既有约定）
	p2 := writeTestConfig(t, "auth:\n  cloudflared:\n    mode: named\n    tunnel_token_file: \"\"\n")
	cfg2, err := Load(p2)
	if err != nil {
		t.Fatalf("load2: %v", err)
	}
	if !cfg2.Auth.Cloudflared.IsNamed() {
		t.Fatal("IsNamed must be true when mode: named")
	}
	if cfg2.Auth.Cloudflared.TunnelTokenFile == "" {
		t.Fatal("empty tunnel_token_file must fall back to the default path")
	}
}

// TestConfig_ResolveTunnelToken documents the credential precedence:
// inline config value (incl. env override) > token file > nothing.
// The token must never be reachable from a git-tracked file by default.
func TestConfig_ResolveTunnelToken(t *testing.T) {
	// 1) 配置值优先
	c := CloudflaredConfig{TunnelToken: "inline-tok", TunnelTokenFile: filepath.Join(t.TempDir(), "nope")}
	tok, src, err := c.ResolveTunnelToken()
	if err != nil || tok != "inline-tok" || src != "config/env" {
		t.Fatalf("inline: tok=%q src=%q err=%v", tok, src, err)
	}

	// 2) 文件次之，且容忍尾随换行/空格
	dir := t.TempDir()
	f := filepath.Join(dir, "cloudflared_token")
	if err := os.WriteFile(f, []byte("file-tok\n"), 0600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	c2 := CloudflaredConfig{TunnelTokenFile: f}
	tok2, src2, err := c2.ResolveTunnelToken()
	if err != nil || tok2 != "file-tok" {
		t.Fatalf("file: tok=%q err=%v (want trimmed file-tok)", tok2, err)
	}
	if !strings.HasPrefix(src2, "file:") {
		t.Fatalf("source = %q, want file:<path>", src2)
	}
	if strings.Contains(src2, "file-tok") {
		t.Fatal("source description must not contain the token itself (日志泄漏)")
	}

	// 3) 两边都没有 → 空串且不报错（缺 token 由调用方给出可操作的报错）
	c3 := CloudflaredConfig{TunnelTokenFile: filepath.Join(dir, "missing")}
	tok3, _, err := c3.ResolveTunnelToken()
	if err != nil || tok3 != "" {
		t.Fatalf("missing file: tok=%q err=%v (want empty, nil)", tok3, err)
	}
}

// exampleConfigPath 指向仓库根的配置模板。相对路径基于本测试文件所在目录
// （internal/config）。
const exampleConfigPath = "../../config.example.yaml"

// TestConfig_ExampleFileIsLoadable 是 config.example.yaml 的守卫测试：
// 模板必须始终能被 Load 解析，且字段落在文档承诺的值上。任何新增/重命名字段
// 而忘了同步模板，都会在这里变红 —— 否则用户 copy 模板起来会直接报错。
func TestConfig_ExampleFileIsLoadable(t *testing.T) {
	cfg, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("config.example.yaml 无法加载（模板已失效，请同步字段）: %v", err)
	}
	if cfg.Server.Port != 3000 || cfg.Server.Mode != "debug" {
		t.Fatalf("server = %+v, want 3000/debug", cfg.Server)
	}
	// 默认走"零配置可用"的临时隧道：named 需要使用者自备域名+token。
	if cfg.Auth.Cloudflared.Mode != "quick" {
		t.Fatalf("example cloudflared.mode = %q, want quick（模板必须是零配置可跑的路径）", cfg.Auth.Cloudflared.Mode)
	}
	if cfg.Auth.Cloudflared.IsNamed() {
		t.Fatal("example 不应是 named 模式")
	}
	if cfg.Pieqi.BaseBranch != "main" {
		t.Fatalf("example base_branch = %q, want main（默认分支，用户按需改）", cfg.Pieqi.BaseBranch)
	}
	if cfg.Agents.Claude.Transport != "sdk-bridge" {
		t.Fatalf("example agents.claude.transport = %q, want sdk-bridge", cfg.Agents.Claude.Transport)
	}
	if cfg.Channels.Lark.EventMode != "longconn" {
		t.Fatalf("example lark.event_mode = %q, want longconn（无需公网，推荐）", cfg.Channels.Lark.EventMode)
	}
	// 弃用字段不应出现在模板里（写了会打弃用告警）。
	if len(cfg.Deprecations) != 0 {
		t.Fatalf("example 触发了弃用告警 %v，模板应展示 agents.* 新写法", cfg.Deprecations)
	}
}

// TestConfig_ExampleContainsNoCredentials 是**防泄露守卫**：模板是可提交、
// 可能公开的文件，任何人往里填了真实凭据（或真实域名）都会在这里变红。
func TestConfig_ExampleContainsNoCredentials(t *testing.T) {
	cfg, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	checks := []struct {
		name string
		val  string
	}{
		{"channels.lark.app_id", cfg.Channels.Lark.AppID},
		{"channels.lark.app_secret", cfg.Channels.Lark.AppSecret},
		{"channels.lark.verify_token", cfg.Channels.Lark.VerifyToken},
		{"channels.lark.encrypt_key", cfg.Channels.Lark.EncryptKey},
		{"api.token", cfg.API.Token},
		{"agents.claude.bridge.token", cfg.Agents.Claude.Bridge.Token},
		{"auth.cloudflared.tunnel_token", cfg.Auth.Cloudflared.TunnelToken},
		{"auth.cloudflared.public_hostname", cfg.Auth.Cloudflared.PublicHostname},
	}
	for _, c := range checks {
		if strings.TrimSpace(c.val) != "" {
			t.Errorf("模板里 %s 非空 (%q)：config.example.yaml 会被提交，凭据/个人域名必须留空", c.name, c.val)
		}
	}

	// 兜底：整份文件里不该出现疑似真实隧道 token 的 base64 凭据串。
	raw, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	if strings.Contains(string(raw), "eyJhIjoi") {
		t.Fatal("模板里出现疑似 Cloudflare 隧道凭据（eyJhIjoi…），必须移除")
	}
}

// TestConfig_ExampleQoderSpawnIsBareName 守卫：模板里的 qoder spawn 命令必须写裸名。
// 裸名由 agent.resolveSpawnName 补「安装落点回退」（见 internal/agent/acp.go），
// 跨机器可复制；一旦有人往模板里写死某台机器的绝对路径（如 C:\Users\<名字>\...），
// 这份模板对别人就失效了 —— 正是本用例要拦住的回归。
func TestConfig_ExampleQoderSpawnIsBareName(t *testing.T) {
	cfg, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	cmd := cfg.Agents.Qoder.ACP.SpawnCommand
	if len(cmd) == 0 {
		t.Fatal("example agents.qoder.acp.spawn_command 不应为空（应显式带 -m Qwen3.8-Flash）")
	}
	if strings.ContainsAny(cmd[0], `/\`) {
		t.Fatalf("模板 spawn_command[0] = %q 含路径分隔符：请写裸名（解析器会补安装落点），不要写死机器路径", cmd[0])
	}
	if cmd[0] != "qodercli" {
		t.Fatalf("模板 spawn_command[0] = %q, want qodercli", cmd[0])
	}
	if joined := strings.Join(cmd, " "); !strings.Contains(joined, "Qwen3.8-Flash") {
		t.Fatalf("模板应显式 -m Qwen3.8-Flash（否则走付费默认模型），实际 %v", cmd)
	}
}
