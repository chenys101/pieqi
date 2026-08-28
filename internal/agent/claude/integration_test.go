//go:build integration

// 真实端到端集成测试：Go AgentSession ↔ 真实 node claude-sdk-bridge ↔ 真实 claude CLI。
//
// 默认不跑（build tag integration）；显式运行：
//
//	go test -tags integration ./internal/agent/claude/ -run RealIntegration -v
//
// 依赖：node、claude CLI、~/.claude 已登录、代理可达。
package claude

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/agent/claude/bridge"
)

func deltaText(events []agent.Event) string {
	var sb strings.Builder
	for _, e := range events {
		if e.Kind == agent.EventTextDelta {
			sb.WriteString(e.Text)
		}
	}
	return sb.String()
}

func lastResumeID(events []agent.Event) string {
	var id string
	for _, e := range events {
		if e.Kind == agent.EventTurnEnd && e.Turn != nil && e.Turn.ResumeID != "" {
			id = e.Turn.ResumeID
		}
	}
	return id
}

func waitPermission(t *testing.T, mu *sync.Mutex, events *[]agent.Event, timeout time.Duration) *agent.PermissionRequest {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		for i := range *events {
			if (*events)[i].Kind == agent.EventPermissionNeeded {
				p := (*events)[i].Permission
				mu.Unlock()
				return &p
			}
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func TestRealIntegration(t *testing.T) {
	// 定位 bridge 源码（测试 cwd = internal/agent/claude，上溯 3 层即仓库根）
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

	// 起桥（BRIDGE_PORT=0 → 系统分配，从 stdout 解析实际端口）
	cmd := exec.Command("node", bridgeSrc)
	cmd.Env = append(os.Environ(), "BRIDGE_PORT=0", "BRIDGE_HOST=127.0.0.1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// 最先注册 → 最后执行：给子进程释放 cwd 句柄的时间，避免 t.TempDir 清理失败
	defer func() { time.Sleep(3 * time.Second) }()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

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
	var baseURL string
	select {
	case baseURL = <-baseURLCh:
	case <-time.After(20 * time.Second):
		t.Fatal("bridge did not report listening port")
	}

	Configure(Config{BaseURL: baseURL})
	defer Configure(Config{BaseURL: bridge.DefaultBaseURL})

	cwd := t.TempDir()
	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: cwd})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sess.Close(context.Background())

	var mu sync.Mutex
	var events []agent.Event
	sess.OnEvent(func(ev agent.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	// turn1: 记词
	if err := sess.Prompt(context.Background(), "记住一个秘密词：banana。只回复 ok。"); err != nil {
		t.Fatalf("turn1: %v", err)
	}
	mu.Lock()
	resumeID := lastResumeID(events)
	mu.Unlock()
	if resumeID == "" {
		t.Fatal("no resume id captured from turn1")
	}
	t.Logf("turn1 OK, resumeID=%s", resumeID)

	// turn2: 问词（同进程多轮，上下文保留）
	if err := sess.Prompt(context.Background(), "秘密词是什么？只回复这个词。"); err != nil {
		t.Fatalf("turn2: %v", err)
	}
	mu.Lock()
	text := deltaText(events)
	mu.Unlock()
	if !strings.Contains(text, "banana") {
		t.Fatalf("context not preserved across turns: %q", text)
	}
	t.Log("turn2 OK, context preserved")

	// turn3: 权限挂起 → 审批 → 工具执行
	errCh := make(chan error, 1)
	go func() {
		errCh <- sess.Prompt(context.Background(), "运行 shell 命令 `echo SPIKE_OK` 并告诉我输出。")
	}()
	perm := waitPermission(t, &mu, &events, 90*time.Second)
	if perm == nil {
		t.Fatal("no permission_needed event")
	}
	t.Logf("permission_needed: reqId=%s tool=%s", perm.ReqID, perm.ToolTitle)
	if err := sess.RespondPermission(context.Background(), perm.ReqID, true, ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("turn3: %v", err)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("turn3 timeout")
	}
	mu.Lock()
	text3 := deltaText(events)
	mu.Unlock()
	if !strings.Contains(text3, "SPIKE_OK") {
		t.Fatalf("tool not executed after approval: %q", text3)
	}
	t.Log("turn3 OK, permission flow works")
	t.Log("REAL INTEGRATION PASS")
}

// TestProcAutoStartIntegration 真实 spawn 桥进程：EnsureRunning（spawn + 等健康）→
// 会话可用 → Stop（杀进程 + 端口关闭）。验证 auto_start 的完整生命周期。
func TestProcAutoStartIntegration(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bridgeDir := filepath.Join(repoRoot, "services", "claude-sdk-bridge")
	if _, err := os.Stat(filepath.Join(bridgeDir, "src", "index.js")); err != nil {
		t.Skipf("bridge not found: %v", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}

	// 挑一个空闲端口（listen :0 → 关闭，端口交给 spawn 的桥）
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	logPath := filepath.Join(t.TempDir(), "bridge.log")

	p := NewProc(ProcConfig{
		BaseURL: baseURL,
		Dir:     bridgeDir,
		LogPath: logPath,
	})
	if err := p.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop(context.Background()) })
	if !p.Running() {
		t.Fatal("proc should be marked running after spawn")
	}

	// 健康 → 桥可用（会话创建/多轮 e2e 由 TestRealIntegration 覆盖，这里只验进程生命周期）
	if err := bridge.NewClient(baseURL).Health(context.Background()); err != nil {
		t.Fatalf("health after spawn: %v", err)
	}

	// 停止：进程退出、端口关闭、状态清空
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if p.Running() {
		t.Fatal("proc should not be running after Stop")
	}
	if err := bridge.NewClient(baseURL).Health(context.Background()); err == nil {
		t.Fatal("health should fail after bridge stopped")
	}
	t.Log("PROC AUTO-START INTEGRATION PASS")
}
