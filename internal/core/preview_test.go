package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverPreview_Vite(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^5.0.0"}}`), 0644)
	_ = os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte(`export default { server: { port: 5188 } }`), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != "vite" {
		t.Errorf("framework = %s", p.Framework)
	}
	if p.Port != 5188 {
		t.Errorf("port = %d, want 5188 (from vite.config.ts)", p.Port)
	}
	if len(p.Command) != 3 || p.Command[0] != "npm" {
		t.Errorf("command = %v", p.Command)
	}
}

func TestDiscoverPreview_DefaultPortAndRunner(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"dev":"next dev"},"dependencies":{"next":"14"}}`), 0644)
	_ = os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Port != 3000 {
		t.Errorf("next default port = %d", p.Port)
	}
	if p.Command[0] != "pnpm" {
		t.Errorf("runner = %s, want pnpm (lockfile)", p.Command[0])
	}
}

func TestDiscoverPreview_FrontendSubdir(t *testing.T) {
	dir := t.TempDir()
	fe := filepath.Join(dir, "frontend")
	_ = os.MkdirAll(fe, 0755)
	_ = os.WriteFile(filepath.Join(fe, "package.json"),
		[]byte(`{"scripts":{"dev":"nuxt dev"},"devDependencies":{"nuxt":"3"}}`), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != "nuxt" || p.Cwd != "frontend" {
		t.Errorf("profile wrong: %+v", p)
	}
}

func TestDiscoverPreview_NoDevScript(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"build":"x"}}`), 0644)
	if _, err := DiscoverPreview(dir); err == nil {
		t.Error("should be unavailable without dev script")
	}
}

func TestDiscoverPreview_OverrideFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0644)
	_ = os.MkdirAll(filepath.Join(dir, ".pieqi"), 0755)
	_ = os.WriteFile(filepath.Join(dir, ".pieqi", "preview.json"),
		[]byte(`{"framework":"vite","command":["bun","run","dev"],"port":9999,"cwd":"."}`), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Command[0] != "bun" || p.Port != 9999 {
		t.Errorf("override not respected: %+v", p)
	}
}

func TestPreviewEnv_StripsSecrets(t *testing.T) {
	t.Setenv("TUNNEL_TOKEN", "secret")
	t.Setenv("PIEQI_HOME", "/x")
	t.Setenv("BRIDGE_TOKEN", "y")
	env := previewEnv()
	for _, kv := range env {
		if kv == "TUNNEL_TOKEN=secret" || kv == "PIEQI_HOME=/x" || kv == "BRIDGE_TOKEN=y" {
			t.Errorf("secret leaked into preview env: %s", kv)
		}
	}
}
