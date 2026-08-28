// 桥进程管理的单元测试：不依赖 node/桥源码（真实 spawn 走 integration tag，见 integration_test.go）。
package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestProcPortStr(t *testing.T) {
	cases := []struct{ base, want string }{
		{"http://127.0.0.1:18790", "18790"},
		{"http://127.0.0.1:19999", "19999"},
		{"http://127.0.0.1", "80"},
		{"https://example.com", "443"},
		{"http://127.0.0.1:abc", "18790"}, // 非法端口 → 回退默认
	}
	for _, c := range cases {
		p := NewProc(ProcConfig{BaseURL: c.base})
		if got := p.portStr(); got != c.want {
			t.Errorf("portStr(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

func TestProcResolveDir(t *testing.T) {
	prev := os.Getenv("PIEQI_BRIDGE_DIR")
	t.Cleanup(func() { os.Setenv("PIEQI_BRIDGE_DIR", prev) })

	// 1. 显式配置优先
	explicit := t.TempDir()
	p := NewProc(ProcConfig{Dir: explicit})
	if got := p.resolveDir(); got != explicit {
		t.Fatalf("explicit dir = %q, want %q", got, explicit)
	}

	// 2. 显式配置指向不存在的目录 → 直接空（不落到 env/candidates）
	p = NewProc(ProcConfig{Dir: filepath.Join(t.TempDir(), "nope")})
	if got := p.resolveDir(); got != "" {
		t.Fatalf("missing explicit dir = %q, want empty", got)
	}

	// 3. 环境变量
	envDir := t.TempDir()
	os.Setenv("PIEQI_BRIDGE_DIR", envDir)
	p = NewProc(ProcConfig{})
	if got := p.resolveDir(); got != envDir {
		t.Fatalf("env dir = %q, want %q", got, envDir)
	}

	// 4. 都没有 → 空
	os.Unsetenv("PIEQI_BRIDGE_DIR")
	p = NewProc(ProcConfig{})
	if got := p.resolveDir(); got != "" {
		t.Fatalf("no-source resolveDir = %q, want empty", got)
	}
}

func TestEnsureRunningReusesHealthyExternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	p := NewProc(ProcConfig{BaseURL: srv.URL, Logger: zap.NewNop()})
	if err := p.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning on healthy external: %v", err)
	}
	if p.Running() {
		t.Fatal("external bridge must not be marked as managed")
	}
	// 幂等
	if err := p.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning second call: %v", err)
	}
}

func TestEnsureRunningDirNotFound(t *testing.T) {
	prev := os.Getenv("PIEQI_BRIDGE_DIR")
	t.Cleanup(func() { os.Setenv("PIEQI_BRIDGE_DIR", prev) })
	os.Unsetenv("PIEQI_BRIDGE_DIR")

	// 用已关闭端口（探活立即失败）→ 进入 spawn 分支 → 找不到桥目录 → 明确报错
	p := NewProc(ProcConfig{BaseURL: "http://127.0.0.1:1", Logger: zap.NewNop()})
	err := p.EnsureRunning(context.Background())
	if err == nil {
		t.Fatal("expected error when bridge dir not found")
	}
	if !strings.Contains(err.Error(), "bridge dir not found") {
		t.Fatalf("err = %q, want contains 'bridge dir not found'", err)
	}
}

func TestProcStopNoop(t *testing.T) {
	p := NewProc(ProcConfig{Logger: zap.NewNop()})
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop on non-started proc: %v", err)
	}
}
