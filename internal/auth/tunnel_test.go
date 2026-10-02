package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
//
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

// fakeCloudflaredChatty 模拟真实 cloudflared 的输出特征：**先**打印
// trycloudflare URL，**随后**持续刷日志（QUIC 抖动时官方会成批刷
// `Failed to dial a quic connection` / `Retrying connection`）。
//
// 它在 URL 之后向 stdout 写 ~200KB，最后落一个 marker 文件。~200KB 远超管道
// 缓冲（Windows/Unix 均 ~64KB），因此：
//   - 父进程持续读管道 → 子进程写完 → marker 出现；
//   - 父进程抓到 URL 后弃读 → 写入在 ~64KB 处永久阻塞 → marker 永不出现。
//
// marker 是"子进程没被卡死"的确定性证据，无需任何超时竞猜。
func fakeCloudflaredChatty(t *testing.T, dir, rawURL, markerPath string) string {
	t.Helper()
	pad := strings.Repeat("0123456789", 20) // 200 字节/行
	const iterations = 1000                 // 1000 × ~202B ≈ 200KB，是 64KB 缓冲的 3 倍
	var path, content string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "cloudflared-chatty.bat")
		content = "@echo off\r\n" +
			"echo INF url=" + rawURL + "\r\n" +
			"set PAD=" + pad + "\r\n" +
			"for /L %%i in (1,1," + strconv.Itoa(iterations) + ") do @echo %PAD%\r\n" +
			"echo done > \"" + markerPath + "\"\r\n" +
			"ping -n 30 127.0.0.1 > nul\r\n"
	} else {
		path = filepath.Join(dir, "cloudflared-chatty.sh")
		content = "#!/bin/sh\n" +
			"echo 'INF url=" + rawURL + "'\n" +
			"PAD='" + pad + "'\n" +
			"i=0\n" +
			"while [ $i -lt " + strconv.Itoa(iterations) + " ]; do echo \"$PAD\"; i=$((i+1)); done\n" +
			"echo done > \"" + markerPath + "\"\n" +
			"sleep 30\n"
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write chatty fake cf: %v", err)
	}
	return path
}

// TestTunnel_StartKeepsDrainingChildPipes 是"cloudflared 卡死"的回归测试。
//
// 缺陷：Start() 抓到 URL 后 scanPipe 直接 return，stdout/stderr 两条管道从此无人
// 读取。子进程只要在 URL 之后继续输出（真实 cloudflared 在 QUIC 抖动时会成批刷
// 重试日志），写满 ~64KB 管道缓冲后 write 就永久阻塞 —— 进程卡死：Go runtime 还
// 活着、metrics 还能抓，但套接字全无、边缘连接再也建不起来，公网访问退化成
// Cloudflare 530 且永不恢复（2026-10-01 线上事故）。
//
// 断言：Start() 返回后子进程仍能把 ~200KB 日志写完（marker 出现）。
// 反向验证：把 scanPipe 改回"命中 URL 即 return"，本用例必须 FAIL。
func TestTunnel_StartKeepsDrainingChildPipes(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-finished")
	binary := fakeCloudflaredChatty(t, dir, "https://chatty-pipe.trycloudflare.com", marker)
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000",
		Tokens: NewTokenStore(),
	})
	res, err := m.Start(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = m.Stop(context.Background()) }()
	if !strings.Contains(res.TunnelURL, "chatty-pipe.trycloudflare.com") {
		t.Fatalf("url = %q, want the fake trycloudflare url", res.TunnelURL)
	}

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return // 子进程把 URL 之后的 ~200KB 全部写完 ⇒ 管道确实被持续读取
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("子进程在 URL 之后写满管道即阻塞：Start() 抓完 URL 就弃读管道，" +
		"真实的 cloudflared 会因此卡死并失去边缘连接（公网 530）")
}

// TestHostAlive_CloudflareTunnelErrorIsDead 钉住"530 = 隧道已死"这条判据。
// 此前 hostAlive 对**任何**状态码都判活（为容忍 token 过期的 401），后果是隧道
// 卡死时巡检永远判活、永不触发自愈 —— 链接失效且系统无任何恢复动作。
func TestHostAlive_CloudflareTunnelErrorIsDead(t *testing.T) {
	cases := []struct {
		status int
		want   bool
		why    string
	}{
		{530, false, "Cloudflare Tunnel error：边缘没找到隧道连接 ⇒ 死"},
		{401, true, "token 过期，但请求确实被隧道转回了源站 ⇒ 活"},
		{403, true, "源站拒绝 ⇒ 隧道活着"},
		{502, true, "源站出错 ⇒ 隧道活着"},
		{302, true, "源站重定向 ⇒ 隧道活着"},
		{200, true, "正常 ⇒ 活"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
		}))
		got := hostAlive(srv.URL + "/?token=probe")
		srv.Close()
		if got != c.want {
			t.Errorf("status %d: hostAlive = %v, want %v (%s)", c.status, got, c.want, c.why)
		}
	}
}

// TestHostAlive_UnreachableIsDead 保证真正的失联（DNS/连接失败）仍判死。
func TestHostAlive_UnreachableIsDead(t *testing.T) {
	// 关闭的端口：连接必然失败
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	u := srv.URL
	srv.Close()
	if hostAlive(u + "/") {
		t.Fatal("closed server must be reported dead")
	}
	if hostAlive("http://no-such-host.invalid/") {
		t.Fatal("unresolvable host must be reported dead")
	}
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
	m.StartHealthCheck(0, 1)           // interval<=0 → no-op
	m.StartHealthCheck(time.Minute, 0) // threshold<1 → no-op
	m.StopHealthCheck()                // healthStop nil → no-op
}

// fakeNamedCloudflared 模拟 named tunnel 模式的 cloudflared：向 stderr 打印
// "Registered tunnel connection"（真实 cloudflared 与边缘建好连接后的固定输出）
// 并保持存活，直到被 Kill。打印行数可控：printed=0 时只存活不打印（用于
// 30s 超时路径的短测版则直接静默存活）。
func fakeNamedCloudflared(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var path, content string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "cloudflared-named.bat")
		content = "@echo off\r\n" +
			"echo INF Registered tunnel connection conn=(x\r\n" +
			"ping -n 30 127.0.0.1 > nul\r\n"
	} else {
		path = filepath.Join(dir, "cloudflared-named.sh")
		content = "#!/bin/sh\necho 'INF Registered tunnel connection conn=(x'\nsleep 30\n"
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write fake named cf: %v", err)
	}
	return path
}

// fakeNamedCloudflaredDead 从不打印 Registered 且立即退出 —— 用于断言
// "进程在注册前退出"的失败路径（bad token / 网络不通时真实 cloudflared 的行为）。
func fakeNamedCloudflaredDead(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var path, content string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "cloudflared-dead.bat")
		content = "@echo off\r\necho INF some unrelated line\r\n"
	} else {
		path = filepath.Join(dir, "cloudflared-dead.sh")
		content = "#!/bin/sh\necho 'INF some unrelated line'\n"
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write fake dead cf: %v", err)
	}
	return path
}

// TestTunnel_NamedMode_Start verifies named mode: no stdout URL scraping —
// the returned URL is always PublicHostname?token=..., success gated on the
// "Registered tunnel connection" line, and the process keeps draining pipes.
func TestTunnel_NamedMode_Start(t *testing.T) {
	binary := fakeNamedCloudflared(t)
	m := NewTunnelManager(TunnelConfig{
		BinaryPath:     binary,
		LocalURL:       "http://localhost:3000",
		Tokens:         NewTokenStore(),
		Mode:           "named",
		TunnelToken:    "eyJhIjoiYiJ9",
		PublicHostname: "304456.xyz",
	})
	res, err := m.Start(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("Start named: %v", err)
	}
	defer m.Stop(context.Background())

	if !strings.HasPrefix(res.TunnelURL, "https://304456.xyz?token=") {
		t.Fatalf("url = %q, want https://304456.xyz?token=...", res.TunnelURL)
	}
	if res.Token == "" || res.ExpiresAt.IsZero() {
		t.Fatalf("token/expiry missing: %+v", res)
	}
	if !m.IsActive() {
		t.Fatal("tunnel should be active after named start")
	}
	if st := m.Status(); !st.Active || !strings.HasPrefix(st.TunnelURL, "https://304456.xyz?token=***") {
		t.Fatalf("status = %+v, want masked named url", st)
	}
	// PublicHostname 已带 scheme 的写法同样可用。
	m2 := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000", Tokens: NewTokenStore(),
		Mode: "named", TunnelToken: "tok", PublicHostname: "https://fixed.example.com/",
	})
	res2, err := m2.Start(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Start named (scheme'd host): %v", err)
	}
	defer m2.Stop(context.Background())
	if !strings.HasPrefix(res2.TunnelURL, "https://fixed.example.com?token=") {
		t.Fatalf("url = %q, want trailing slash trimmed", res2.TunnelURL)
	}
}

// TestTunnel_NamedMode_MissingConfig verifies the fail-fast guards: named
// mode without public_hostname / tunnel_token must error before spawning.
func TestTunnel_NamedMode_MissingConfig(t *testing.T) {
	binary := fakeNamedCloudflared(t)
	for _, tc := range []struct{ name, host, tok string }{
		{"no hostname", "", "tok"},
		{"no token", "304456.xyz", ""},
	} {
		m := NewTunnelManager(TunnelConfig{
			BinaryPath: binary, LocalURL: "http://localhost:3000", Tokens: NewTokenStore(),
			Mode: "named", PublicHostname: tc.host, TunnelToken: tc.tok,
		})
		if _, err := m.Start(context.Background(), time.Minute); err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
		if m.IsActive() {
			t.Fatalf("%s: must not spawn a process", tc.name)
		}
	}
}

// TestTunnel_NamedMode_ProcessDiesBeforeRegister verifies that a cloudflared
// that exits without ever registering (bad token, blocked network) fails the
// Start with a clear error and leaves no active tunnel / stale tokens.
func TestTunnel_NamedMode_ProcessDiesBeforeRegister(t *testing.T) {
	binary := fakeNamedCloudflaredDead(t)
	ts := NewTokenStore()
	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000", Tokens: ts,
		Mode: "named", TunnelToken: "bad", PublicHostname: "304456.xyz",
	})
	if _, err := m.Start(context.Background(), time.Minute); err == nil {
		t.Fatal("expected error for process dying before register")
	}
	if m.IsActive() {
		t.Fatal("no tunnel should be active after failed start")
	}
}

// fakeNamedCloudflaredSpy 记录**自己的命令行参数**与 TUNNEL_TOKEN 环境变量是否可见，
// 落盘到同目录 args.txt，然后照常打印 Registered 建连行。
// 用途：证明隧道凭据走环境变量而非 argv —— argv 对同机任何进程可见
// （tasklist / wmic / /proc/<pid>/cmdline），等于把凭据写在明处。
func fakeNamedCloudflaredSpy(t *testing.T, spyPath string) string {
	t.Helper()
	dir := t.TempDir()
	var path, content string
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "cloudflared-spy.bat")
		content = "@echo off\r\n" +
			"> \"" + spyPath + "\" echo ARGS=%*\r\n" +
			"if defined TUNNEL_TOKEN (>> \"" + spyPath + "\" echo ENV=set) else (>> \"" + spyPath + "\" echo ENV=missing)\r\n" +
			"echo INF Registered tunnel connection connIndex=0\r\n" +
			"ping -n 30 127.0.0.1 > nul\r\n"
	} else {
		path = filepath.Join(dir, "cloudflared-spy.sh")
		content = "#!/bin/sh\n" +
			"D=\"" + spyPath + "\"\n" +
			"printf 'ARGS=%s\n' \"$*\" > \"$D\"\n" +
			"if [ -n \"$TUNNEL_TOKEN\" ]; then echo 'ENV=set' >> \"$D\"; else echo 'ENV=missing' >> \"$D\"; fi\n" +
			"echo 'INF Registered tunnel connection connIndex=0'\n" +
			"sleep 30\n"
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write spy cf: %v", err)
	}
	return path
}

// TestTunnel_NamedMode_TokenNotInArgv 是凭据泄漏的回归测试。
// 断言：token 必须经 TUNNEL_TOKEN 环境变量传给 cloudflared，**不得**出现在 argv。
// 反向验证：把 startNamed 改回 `--token <tok>`，本用例必须 FAIL。
func TestTunnel_NamedMode_TokenNotInArgv(t *testing.T) {
	const secret = "SECRET_TUNNEL_TOKEN_abc123"
	spy := filepath.Join(t.TempDir(), "args.txt")
	binary := fakeNamedCloudflaredSpy(t, spy)

	m := NewTunnelManager(TunnelConfig{
		BinaryPath: binary, LocalURL: "http://localhost:3000", Tokens: NewTokenStore(),
		Mode: "named", TunnelToken: secret, PublicHostname: "304456.xyz",
	})
	if _, err := m.Start(context.Background(), time.Minute); err != nil {
		t.Fatalf("Start named: %v", err)
	}
	defer m.Stop(context.Background())

	var rec string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(spy); err == nil && len(b) > 0 {
			rec = string(b)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec == "" {
		t.Fatal("spy file never appeared; cloudflared did not run")
	}
	if strings.Contains(rec, secret) {
		t.Fatalf("凭据泄漏：token 出现在命令行参数中 → %q", rec)
	}
	if !strings.Contains(rec, "ENV=set") {
		t.Fatalf("token 未通过 TUNNEL_TOKEN 环境变量传入 → %q", rec)
	}
	if !strings.Contains(rec, "tunnel") || !strings.Contains(rec, "--no-autoupdate") {
		t.Fatalf("argv 形态异常（应含 tunnel --no-autoupdate run）→ %q", rec)
	}
}

// TestHostAlive_TunnelDeadSignatures 固化"边缘判死"的两种签名。
//
// 背景：quick 隧道无连接 → 530；**named 隧道无连接 → 502 + 兜底页
// "error code: 502"**（2026-10-02 实测）。两者都必须判死，否则自愈永不触发。
// 反向验证：删掉 hostAlive 里的 502 body 判断，本用例的 named 子例必 FAIL。
func TestHostAlive_TunnelDeadSignatures(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantLive bool
	}{
		{"quick 隧道无连接：530 判死", 530, "Cloudflare Tunnel error", false},
		{"named 隧道无连接：502 + 兜底页判死", 502, "error code: 502\n", false},
		{"源站自己的 502 仍算活（隧道通、源站出错）", 502, "<html><body>Origin bad gateway</body></html>", true},
		{"token 过期 401 算活（域名与隧道都在）", 401, "unauthorized", true},
		{"正常 200 算活", 200, "<html>ok</html>", true},
		{"504 算活（请求已被转发到源站）", 504, "gateway timeout", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			// hostAlive 直接取 rawURL 的 scheme+host，故用 http 的 httptest 服务。
			if got := hostAlive(srv.URL + "/?token=x"); got != tc.wantLive {
				t.Fatalf("hostAlive = %v, want %v (status=%d body=%q)", got, tc.wantLive, tc.status, tc.body)
			}
		})
	}
	// 连接失败（服务没起）必须判死
	if hostAlive("http://127.0.0.1:1/") {
		t.Fatal("connection refused must be judged dead")
	}
}
