//go:build integration

package claude

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/agent/claude/bridge"
)

// TestColdResumeAtSessionLayer 冷续问聚焦验证：会话层（不经 TaskRunner/manager）——
// 首轮记词 → 取 SDK resume id → Close 杀会话 → 新会话 ResumeFrom=resume id → 问词。
// 隔离定位"桥续问是否真能重建上下文"这一环节。
func TestColdResumeAtSessionLayer(t *testing.T) {
	baseURL := startBridgeForResumeTest(t)
	Configure(Config{BaseURL: baseURL})
	defer Configure(Config{BaseURL: bridge.DefaultBaseURL})

	cwd := t.TempDir()
	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: cwd})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := sess.Prompt(context.Background(), "记住一个秘密词：banana。只回复 ok。"); err != nil {
		t.Fatalf("turn1: %v", err)
	}
	rs := sess.(interface{ ResumeID() string }).ResumeID()
	if rs == "" {
		t.Fatal("ResumeID empty after turn1")
	}
	t.Logf("resumeID=%s", rs)
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("close sess1: %v", err)
	}

	sess2, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: cwd, ResumeFrom: rs})
	if err != nil {
		t.Fatalf("Open resume: %v", err)
	}
	defer sess2.Close(context.Background())

	var out string
	sess2.OnEvent(func(ev agent.Event) {
		if ev.Kind == agent.EventTextDelta {
			out += ev.Text
		}
	})
	if err := sess2.Prompt(context.Background(), "秘密词是什么？只回复该词，不要解释。"); err != nil {
		t.Fatalf("turn2: %v", err)
	}
	if !strings.Contains(out, "banana") {
		t.Fatalf("cold resume lost context: output=%q", out)
	}
	t.Log("COLD RESUME AT SESSION LAYER PASS")
}

// startBridgeForResumeTest 起真实 bridge（BRIDGE_PORT=0 → 系统分配端口），返回 baseURL。
func startBridgeForResumeTest(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bridgeSrc := filepath.Join(repoRoot, "services", "claude-sdk-bridge", "src", "index.js")
	if _, err := os.Stat(bridgeSrc); err != nil {
		t.Skipf("bridge not found: %v", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}
	cmd := exec.Command("node", bridgeSrc)
	cmd.Env = append(os.Environ(), "BRIDGE_PORT=0", "BRIDGE_HOST=127.0.0.1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { time.Sleep(3 * time.Second) })
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	baseURLCh := make(chan string, 1)
	go func() {
		re := regexp.MustCompile(`listening on http://[^:]+:(\d+)`)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				baseURLCh <- "http://127.0.0.1:" + m[1]
				return
			}
		}
	}()
	select {
	case u := <-baseURLCh:
		return u
	case <-time.After(20 * time.Second):
		t.Fatal("bridge did not report listening port")
		return ""
	}
}
