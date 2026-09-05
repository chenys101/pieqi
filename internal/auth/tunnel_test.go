package auth

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeCloudflaredScript writes a tiny shell/bat script that prints a
// trycloudflare URL to stdout and stays alive until killed — same
// observable behavior as real cloudflared quick-tunnel mode.
func fakeCloudflaredScript(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	var path string
	var content string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "cloudflared.bat")
		content = "@echo off\r\necho INF url=" + url + "\r\nping -n 30 127.0.0.1 > nul\r\n"
	} else {
		path = filepath.Join(dir, "cloudflared.sh")
		content = "#!/bin/sh\necho 'INF url=" + url + "'\nsleep 30\n"
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write fake cf: %v", err)
	}
	return path
}

// fakeCloudflaredScriptLong 与 fakeCloudflaredScript 行为一致，但：
//   - hostname 每次 spawn 都不同 —— 脚本通过同目录 counter.txt 计数（第 N 次
//     spawn 打印 heal-<N>.trycloudflare.com），用于断言自愈重启后域名确实被轮换。
//     （不能用 %RANDOM%：Windows cmd 在同一时间窗口启动的进程会产出相同值。）
//   - 寿命 ~10 分钟（ping -n 600 / sleep 600），避免 ~30s 的短脚本在自愈测试
//     途中提前退出（watch 会清空 m.cmd，让巡检误以为"无活跃隧道"而跳过）。
// 自愈的 stopLocked 与测试收尾的 Stop 会 Kill 掉这些进程，无残留。
func fakeCloudflaredScriptLong(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var path, content string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "cloudflared.bat")
		// 注意 counter 写入必须用 `> file echo %N%`：`echo %N%> file` 在 N=1 时
		// 会被 cmd 解析成 fd-1 重定向（写入空文件），计数器永不递增。
		content = "@echo off\r\n" +
			"set N=0\r\n" +
			"if exist %~dp0counter.txt set /p N=<%~dp0counter.txt\r\n" +
			"set /a N+=1\r\n" +
			"> %~dp0counter.txt echo %N%\r\n" +
			"echo INF url=https://heal-%N%.trycloudflare.com\r\n" +
			"ping -n 600 127.0.0.1 > nul\r\n"
	} else {
		path = filepath.Join(dir, "cloudflared.sh")
		content = "#!/bin/sh\n" +
			"C=\"$(dirname \"$0\")/counter.txt\"\n" +
			"N=0\n" +
			"[ -f \"$C\" ] && N=\"$(cat \"$C\")\"\n" +
			"N=$((N+1))\n" +
			"echo \"$N\" > \"$C\"\n" +
			"echo \"INF url=https://heal-$N.trycloudflare.com\"\n" +
			"sleep 600\n"
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write fake cf: %v", err)
	}
	return path
}

func TestTunnel_StartParsesURL(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://abc-xyz.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary,
		LocalURL:   "http://localhost:3000",
		Tokens:     NewTokenStore(),
		Logger:     nil,
	})
	res, err := m.Start(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(res.TunnelURL, "abc-xyz.trycloudflare.com") {
		t.Fatalf("tunnel url = %q, want trycloudflare URL", res.TunnelURL)
	}
	if res.Token == "" {
		t.Fatal("token must be issued on start")
	}
	if !m.IsActive() {
		t.Fatal("should be active after Start")
	}
	// Token is wired into the URL so the front-end can hand the full link
	// to Lark
	if !strings.Contains(res.TunnelURL, "token=") {
		t.Fatalf("tunnel url must include ?token=, got %q", res.TunnelURL)
	}
	// Cleanup
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if m.IsActive() {
		t.Fatal("should not be active after Stop")
	}
}

func TestTunnel_StopInvalidatesToken(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t1.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	res, _ := m.Start(context.Background(), time.Hour)
	if !m.Tokens.Validate(res.Token) {
		t.Fatal("token valid right after start")
	}
	_ = m.Stop(context.Background())
	if m.Tokens.Validate(res.Token) {
		t.Fatal("Stop must invalidate the tunnel token")
	}
}

func TestTunnel_StartTwiceResetsToken(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t2.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	first, _ := m.Start(context.Background(), time.Minute)
	second, _ := m.Start(context.Background(), time.Minute)
	if first.Token == second.Token {
		t.Fatal("second start must issue a fresh token")
	}
	if m.Tokens.Validate(first.Token) {
		t.Fatal("first token must be invalidated when a new tunnel starts")
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_ResetIssuesNewToken(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t3.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	orig, _ := m.Start(context.Background(), time.Minute)
	newTok, err := m.ResetToken(context.Background())
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if newTok == orig.Token {
		t.Fatal("reset must issue a different token")
	}
	if m.Tokens.Validate(orig.Token) {
		t.Fatal("old token must die on reset")
	}
	if !m.Tokens.Validate(newTok) {
		t.Fatal("new token must be valid")
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_StatusReflectsState(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t4.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	if st := m.Status(); st.Active {
		t.Fatal("should be inactive initially")
	}
	res, _ := m.Start(context.Background(), 15*time.Minute)
	st := m.Status()
	if !st.Active {
		t.Fatal("status should report active")
	}
	// Status is the safe (public) snapshot: the raw token must be masked
	// so a polling front-end cannot read it. The Task 10 handler test
	// also expects "token=***" in the status URL.
	if !strings.Contains(st.TunnelURL, "token=***") {
		t.Fatalf("status url must mask the token, got %q (start url %q)", st.TunnelURL, res.TunnelURL)
	}
	if !st.ExpiresAt.IsZero() && st.ExpiresAt.Before(time.Now()) {
		t.Fatal("expiry should be in the future")
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_LarkDeepLinkFormat(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t5.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	res, _ := m.Start(context.Background(), time.Minute)
	if !strings.HasPrefix(res.LarkDeepLink, "lark://open?url=") {
		t.Fatalf("lark link = %q, want lark://open?url= prefix", res.LarkDeepLink)
	}
	if !strings.Contains(res.LarkDeepLink, "token=") {
		t.Fatal("lark link must embed the token")
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_RenewToken_KeepsSameTokenAndExtends(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t-renew.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	orig, err := m.Start(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	res, err := m.RenewToken(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	// token 值不变 → 已分发的链接继续可用（续期 vs 重置的核心区别）
	if res.Token != orig.Token {
		t.Fatalf("renew must keep the same token, got %q want %q", res.Token, orig.Token)
	}
	// 返回结构与 Start 一致：同一 TunnelURL（内嵌 token）、LarkDeepLink 前缀正确
	if res.TunnelURL != orig.TunnelURL {
		t.Fatalf("renew must keep the same tunnel url, got %q want %q", res.TunnelURL, orig.TunnelURL)
	}
	if !strings.HasPrefix(res.LarkDeepLink, "lark://open?url=") || !strings.Contains(res.LarkDeepLink, "token=") {
		t.Fatalf("renew lark link malformed: %q", res.LarkDeepLink)
	}
	// 过期时间延长
	if !res.ExpiresAt.After(orig.ExpiresAt) {
		t.Fatalf("renew must extend expiry: orig %v renew %v", orig.ExpiresAt, res.ExpiresAt)
	}
	if !m.Tokens.Validate(res.Token) {
		t.Fatal("token must remain valid after renew")
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_RenewToken_ExpiredTokenReissuesOnSameTunnel(t *testing.T) {
	// 可控时钟：隧道进程仍在但 token 已过期时，续期应在同一隧道上签发新 token，
	// 而不是报错要求重启（旧 token 已死，轮换零损失，域名保留）。
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	binary := fakeCloudflaredScript(t, "https://t-renew-expired.trycloudflare.com")
	ts := NewTokenStoreWithNow(func() time.Time { return now })
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: ts,
	})
	orig, err := m.Start(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 时钟越过 TTL → 旧 token 过期，但 cloudflared 进程仍被视作存活
	now = now.Add(16 * time.Minute)
	if ts.Validate(orig.Token) {
		t.Fatal("precondition: old token must be expired")
	}
	res, err := m.RenewToken(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("renew on expired token must re-issue, got err: %v", err)
	}
	// 新 token 与旧不同、有效、带全新 TTL
	if res.Token == orig.Token {
		t.Fatal("expired-token renew must issue a fresh token")
	}
	if !ts.Validate(res.Token) {
		t.Fatal("fresh token must be valid")
	}
	// 域名不变，仅 URL 里的 token 参数被替换；返回结构仍与 Start 一致
	if !strings.Contains(res.TunnelURL, "t-renew-expired.trycloudflare.com") {
		t.Fatalf("renew must keep the same tunnel hostname, got %q", res.TunnelURL)
	}
	if !strings.Contains(res.TunnelURL, "token="+res.Token) {
		t.Fatalf("renew url must embed the fresh token, got %q", res.TunnelURL)
	}
	if !strings.HasPrefix(res.LarkDeepLink, "lark://open?url=") {
		t.Fatalf("lark link malformed: %q", res.LarkDeepLink)
	}
	_ = m.Stop(context.Background())
}

func TestTunnel_RenewToken_NoActiveTunnel(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t-norenew.trycloudflare.com")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	if _, err := m.RenewToken(context.Background(), time.Hour); err == nil {
		t.Fatal("renew without active tunnel must error")
	}
}

func TestTunnel_PIDFileLifecycle(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t-pid.trycloudflare.com")
	pidFile := filepath.Join(t.TempDir(), "cf.pid")
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(), PIDFile: pidFile,
	})
	res, err := m.Start(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Start 成功后 PID 文件写入当前子进程 PID
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file after start: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("pid file content invalid: %q", data)
	}
	// Stop 后 PID 文件删除
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("pid file should be removed after stop, err=%v", err)
	}
	_ = res
}

func TestTunnel_CleanupOrphansKillsStalePID(t *testing.T) {
	binary := fakeCloudflaredScript(t, "https://t-orphan.trycloudflare.com")
	pidFile := filepath.Join(t.TempDir(), "cf.pid")
	// 预写"上次实例强杀残留"的 PID 记录
	if err := os.WriteFile(pidFile, []byte("424242"), 0600); err != nil {
		t.Fatalf("write stale pid: %v", err)
	}
	var killed []int
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(), PIDFile: pidFile,
	})
	m.killFunc = func(pid int) error { killed = append(killed, pid); return nil }
	if _, err := m.Start(context.Background(), time.Minute); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Start 前置的 cleanupOrphans 必须杀掉残留的 424242
	if len(killed) != 1 || killed[0] != 424242 {
		t.Fatalf("cleanupOrphans should kill stale pid 424242, got %v", killed)
	}
	_ = m.Stop(context.Background())
}

// --- 自动自愈（Cloudflare 域名回收检测）---

// TestTunnel_AutoHeal_RestartsOnDeadHostname 是核心自愈用例：探测到域名"死亡"
// （healthCheck 对首域名返回 false）后，healthLoop 自动重启隧道换新域名，
// 触发 OnHeal，新 token 有效、旧 token 失效、隧道仍活跃。
func TestTunnel_AutoHeal_RestartsOnDeadHostname(t *testing.T) {
	binary := fakeCloudflaredScriptLong(t)
	healed := make(chan TunnelResult, 1)
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
		OnHeal: func(res TunnelResult) { healed <- res },
	})
	first, err := m.Start(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	firstHost, _ := url.Parse(first.TunnelURL)

	// 探测"存活"判定绑定首域名：只要还是首域名就判死（触发自愈）；换新域名后
	// 判活（不再二次自愈）。healthCheck 须在 StartHealthCheck 之前设置。
	m.healthCheck = func(raw string) bool {
		return !strings.Contains(raw, firstHost.Host)
	}
	m.StartHealthCheck(50*time.Millisecond, 1)
	defer m.StopHealthCheck()

	select {
	case res := <-healed:
		u, _ := url.Parse(res.TunnelURL)
		if u.Host == firstHost.Host {
			t.Fatalf("heal must rotate to a new hostname, still %s", u.Host)
		}
		if res.Token == first.Token {
			t.Fatal("heal must issue a fresh token")
		}
		if m.Tokens.Validate(first.Token) {
			t.Fatal("old token must be invalidated after heal")
		}
		if !m.Tokens.Validate(res.Token) {
			t.Fatal("new token must be valid after heal")
		}
		if !m.IsActive() {
			t.Fatal("tunnel must remain active after heal")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("auto-heal did not fire within timeout")
	}
	_ = m.Stop(context.Background())
}

// TestTunnel_AutoHeal_HealthyHostnameNoRestart 确认健康域名不会被误杀：
// healthCheck 恒 true，多个 tick 后仍无自愈、原 token 保持有效。
func TestTunnel_AutoHeal_HealthyHostnameNoRestart(t *testing.T) {
	binary := fakeCloudflaredScriptLong(t)
	healed := make(chan TunnelResult, 1)
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
		OnHeal: func(res TunnelResult) { healed <- res },
	})
	res, err := m.Start(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	m.healthCheck = func(string) bool { return true }
	m.StartHealthCheck(30*time.Millisecond, 1)
	defer m.StopHealthCheck()

	time.Sleep(300 * time.Millisecond) // ~10 个 tick，全部健康
	select {
	case <-healed:
		t.Fatal("OnHeal must not fire for a healthy hostname")
	default:
	}
	if !m.Tokens.Validate(res.Token) {
		t.Fatal("token must remain valid when no heal happens")
	}
	if !m.IsActive() {
		t.Fatal("tunnel must stay active")
	}
	_ = m.Stop(context.Background())
}

// TestTunnel_AutoHeal_NotTriggeredBelowThreshold 验证防抖：连续失败次数未达阈值
// （前 2 次失败后恢复健康，threshold=3）时绝不触发自愈。
func TestTunnel_AutoHeal_NotTriggeredBelowThreshold(t *testing.T) {
	binary := fakeCloudflaredScriptLong(t)
	healed := make(chan TunnelResult, 1)
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
		OnHeal: func(res TunnelResult) { healed <- res },
	})
	first, err := m.Start(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	calls := 0
	m.healthCheck = func(string) bool { // 前 2 次失败，此后健康
		calls++
		return calls > 2
	}
	m.StartHealthCheck(30*time.Millisecond, 3)
	defer m.StopHealthCheck()

	time.Sleep(300 * time.Millisecond) // ~10 个 tick；streak 最多到 2，永不达 3
	select {
	case <-healed:
		t.Fatal("heal must not fire below the failure threshold")
	default:
	}
	if !m.Tokens.Validate(first.Token) {
		t.Fatal("token must survive when below threshold")
	}
	_ = m.Stop(context.Background())
}

// TestTunnel_AutoHeal_StopHealthCheck 验证巡检 goroutine 可干净停止且幂等。
func TestTunnel_AutoHeal_StopHealthCheck(t *testing.T) {
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: "unused", LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	m.StartHealthCheck(10*time.Millisecond, 1) // 无隧道时空转
	done := make(chan struct{})
	go func() {
		m.StopHealthCheck()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StopHealthCheck must return promptly")
	}
	m.StopHealthCheck() // 幂等：二次调用不 panic
}

// TestTunnel_AutoHeal_DisabledWithoutStart verifies StartHealthCheck with
// non-positive interval / threshold is a no-op (and StopHealthCheck is safe).
func TestTunnel_AutoHeal_DisabledWithoutStart(t *testing.T) {
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: "unused", LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	m.StartHealthCheck(0, 1)    // interval<=0 → no-op
	m.StartHealthCheck(time.Minute, 0) // threshold<1 → no-op
	m.StopHealthCheck()         // healthStop nil → no-op
}
