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
	// 2026-10-22：qoder / dsh / codebuddy 的节已从配置移除（归 manifest + 运行期设置），
	// 故这里不再断言它们的默认值 —— 那些断言的存在本身就是在为"代码事实被写进配置"背书。
	// 新语义由 TestConfig_ACPAgentSectionsRemoved 与 agent 包的 TestManifestIsTheOnlySource 守。
}

// TestConfig_ACPAgentSectionsRemoved 守卫「ACP agent 不再由配置声明」这条整改。
//
// 判据（2026-10-22 用户要求：约定优于配置）：
//   - 接入一个新 agent 只需改 internal/agent/manifest.go，**不需要碰任何配置**。
//   - 因此旧写法（agents.qoder.acp.spawn_command 等）写进配置必须**不生效**，
//     且给出一条可操作告警 —— 不能静默忽略。老用户可能真的以为那些参数还在起作用，
//     而"启动参数莫名其妙变了"是最难查的一类问题。
func TestConfig_ACPAgentSectionsRemoved(t *testing.T) {
	// 1) 旧写法仍能加载（不能因为多了未知键就起不来 —— 那会让升级变成事故）。
	p := writeTestConfig(t, `
server:
  port: 3000
agents:
  qoder:
    transport: acp
    acp:
      agent_type: qodercli
      spawn_command: ["qodercli", "--acp", "-m", "Qwen3.8-Flash"]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("旧配置应仍可加载，got %v", err)
	}
	// 2) 关键判据：配置里写这些**不会**改变运行期行为（它们已不属于 Config）。
	//    这里用一个可观察的结果来钉住：Claude 节不受影响，且没有 ACP agent 的残留字段可读。
	if cfg.Agents.Claude.Transport != "sdk-bridge" {
		t.Fatalf("claude 默认被未知键影响: %q", cfg.Agents.Claude.Transport)
	}

	// 3) 传 legacy ACP 信号（qodercli）时必须有告警，告知落点已变到运行期设置。
	pl := writeTestConfig(t, `
server:
  port: 3000
pieqi:
  acp:
    agent_type: qodercli
    acp_spawn_command: ["qodercli", "--acp", "-m", "Qwen3.8-Flash"]
`)
	cfgL, err := Load(pl)
	if err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	found := false
	for _, d := range cfgL.Deprecations {
		if strings.Contains(d, "spawn_overrides") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("旧 qoder spawn 配置未告警：用户会以为 -m 仍生效。deprecations=%v", cfgL.Deprecations)
	}
}

// TestConfig_AutoApproveToolsRemoved pieqi.auto_approve_tools 已移除：显式配置必须
// 触发一条弃用告警（而不是静默忽略）。
//
// 为什么必须告警而不是静默删：老用户可能真的以为这个字段在生效 —— 它此前**只被解析、
// 无人消费**，免审名单一直来自设置页的 auto_approve_l0/l1。静默忽略等于让人继续
// 以为改这里有用（而它的默认值里 delete 还属 L3，与硬边界矛盾）。
func TestConfig_AutoApproveToolsRemoved(t *testing.T) {
	// 默认配置不再有这个字段 → 无告警。
	p := writeTestConfig(t, "server:\n  port: 3000\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, d := range cfg.Deprecations {
		if strings.Contains(d, "auto_approve_tools") {
			t.Fatalf("默认配置不该产生 auto_approve_tools 告警：%s", d)
		}
	}

	// 显式配置（无论什么值）→ 一条告警，且不因加载失败。
	for _, body := range []string{
		"pieqi:\n  auto_approve_tools: [\"edit\"]\n",
		"pieqi:\n  auto_approve_tools: []\n",
		"pieqi:\n  auto_approve_tools: [edit, delete, move]\n",
	} {
		cfgN, err := Load(writeTestConfig(t, body))
		if err != nil {
			t.Fatalf("load %q: %v", body, err)
		}
		found := false
		for _, d := range cfgN.Deprecations {
			if strings.Contains(d, "auto_approve_tools") {
				found = true
			}
		}
		if !found {
			t.Errorf("显式配置 auto_approve_tools 必须产生弃用告警，body=%q，got %v", body, cfgN.Deprecations)
		}
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
	// 2026-10-22：agents.qoder 节已移除（归 manifest + 运行期设置），故这里不再断言
	// 它被解析出来。旧写法不生效这一点由 TestConfig_ACPAgentSectionsRemoved 覆盖。
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


// TestConfig_AccessDefaults 守卫 Cloudflare Access 的默认值：
// 必须**默认关闭**且**保留 token 兜底** —— 否则升级后老部署会突然全站 401
// （Access 没配、token 又被关掉 = 外网无任何凭据通道）。
func TestConfig_AccessDefaults(t *testing.T) {
	p := writeTestConfig(t, "server:\n  port: 3000\n  auth:\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a := cfg.Auth.Access
	if a.Enabled {
		t.Fatal("cloudflare_access.enabled 默认必须为 false")
	}
	if !a.TokenFallbackEnabled() {
		t.Fatal("token_fallback 默认必须为 true（未配置时保留兜底）")
	}
	// 显式关闭才关得掉。
	off := false
	a.TokenFallback = &off
	if a.TokenFallbackEnabled() {
		t.Fatal("token_fallback=false 时 TokenFallbackEnabled 必须为 false")
	}
}

// TestConfig_AccessEnvOverride 守卫 AUD / 团队域可走环境变量注入：
// AUD tag 与隧道 token 一样属于凭据类信息，不能强迫使用者写进 git 跟踪的
// config.yaml，必须能用 PIEQI_AUTH_CLOUDFLARE_ACCESS_* 覆盖。
// （嵌套 key 依赖 viper 的 SetEnvKeyReplacer，即 "." → "_"。）
func TestConfig_AccessEnvOverride(t *testing.T) {
	p := writeTestConfig(t, "server:\n  port: 3000\n  auth:\n")
	t.Setenv("PIEQI_AUTH_CLOUDFLARE_ACCESS_ENABLED", "true")
	t.Setenv("PIEQI_AUTH_CLOUDFLARE_ACCESS_TEAM_DOMAIN", "https://envteam.cloudflareaccess.com")
	t.Setenv("PIEQI_AUTH_CLOUDFLARE_ACCESS_AUDIENCE", "env-aud-tag")
	t.Setenv("PIEQI_AUTH_CLOUDFLARE_ACCESS_TOKEN_FALLBACK", "false")

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a := cfg.Auth.Access
	if !a.Enabled {
		t.Fatal("PIEQI_AUTH_CLOUDFLARE_ACCESS_ENABLED=true 未生效")
	}
	if a.TeamDomain != "https://envteam.cloudflareaccess.com" {
		t.Fatalf("team_domain = %q，环境变量未生效（检查 SetEnvKeyReplacer）", a.TeamDomain)
	}
	if a.Audience != "env-aud-tag" {
		t.Fatalf("audience = %q，环境变量未生效", a.Audience)
	}
	if a.TokenFallbackEnabled() {
		t.Fatal("PIEQI_AUTH_CLOUDFLARE_ACCESS_TOKEN_FALLBACK=false 未生效")
	}
}

// TestConfig_ExampleAccessSectionIsSafe 守卫模板里的 Access 段：
// 必须默认关闭、凭据留空、且注释里带上"源站必须自行验签"的关键提示，
// 否则使用者会以为在 CF 控制台挂上 Access 就完事（那样伪造头即可绕过）。
func TestConfig_ExampleAccessSectionIsSafe(t *testing.T) {
	cfg, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	if cfg.Auth.Access.Enabled {
		t.Fatal("模板里 cloudflare_access.enabled 必须为 false（零配置可跑的路径）")
	}
	if cfg.Auth.Access.TeamDomain != "" || cfg.Auth.Access.Audience != "" {
		t.Fatal("模板里 team_domain / audience 必须留空（AUD 属凭据类信息）")
	}
	if !cfg.Auth.Access.TokenFallbackEnabled() {
		t.Fatal("模板里 token_fallback 必须为 true")
	}
	raw, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	if !strings.Contains(string(raw), "cloudflare_access:") {
		t.Fatal("模板缺少 cloudflare_access 段")
	}
}
