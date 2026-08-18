package config

import (
	"os"
	"path/filepath"
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
