package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 本文件锁定 2026-10-02 的鉴权绕过修复（见 ClientIP 注释）。
//
// 线上实证过的那条路径：无 token 的 GET https://304456.xyz/api/tasks 加一个
// `X-Forwarded-For: 127.0.0.1` 就返回 200 —— 因为旧实现取 XFF 首跳，而 CF 对
// 客户端预置值是「保留在前、真实 IP 追加在后」，首跳正好是攻击者可控段；
// IsInternalRequest 又建立在 ClientIP 上，于是外网请求被判成内网、放行一切。
//
// 表里的 peer 区分两种形态：
//   - 127.0.0.1 / ::1：本机 cloudflared 子进程回源（转发头此时可信）；
//   - 公网 / 局域网地址：直连源站（转发头一律不可信，对端地址即唯一事实）。
//
// 这些用例走的是**真实 gin 中间件链**，与生产装配一致。

// tunnelReq 构造一个带指定 headers 的请求。peer 为对端地址。
func tunnelReq(path, peer string, headers map[string]string) *http.Request {
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// runAt 让请求走一遍中间件链，并把它自己的路径注册成路由 —— 否则"中间件放行"
// 的用例会撞上 404，把 404 误读成"被拦"。
func runAt(mw gin.HandlerFunc, req *http.Request) int {
	gin.SetMode(gin.TestMode)
	g := gin.New()
	g.Use(mw)
	g.Handle(req.Method, req.URL.Path, func(c *gin.Context) { c.String(200, "ok") })
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	return w.Code
}

func newAuthSvc(t *testing.T, tokens *TokenStore) *Service {
	t.Helper()
	bindings, err := NewBindingStore(tempPath(t))
	if err != nil {
		t.Fatalf("binding store: %v", err)
	}
	// 阈值放宽：避免用例之间互相拉黑，干扰断言的失败原因。
	return &Service{
		Debug:    NewDebugSwitch(false),
		Bindings: bindings,
		Tokens:   tokens,
		Limiter:  NewIPLimiter(1000, time.Minute),
	}
}

// TestTunnelPath_ExternalAuth_NoToken 无有效 token 时，任何形态都必须 401 ——
// 尤其是「回环对端 + 伪造转发头」这一组（旧实现全部 200）。
func TestTunnelPath_ExternalAuth_NoToken(t *testing.T) {
	svc := newAuthSvc(t, NewTokenStore())
	mw := svc.ExternalAuthMiddleware()

	cases := []struct {
		name    string
		peer    string
		headers map[string]string
		want    int
	}{
		// —— 隧道回源（回环对端）：转发头可信，必须解析出「外部」——
		{"隧道/CF-Connecting-IP 为公网", "127.0.0.1:5555",
			map[string]string{"CF-Connecting-IP": "4.4.4.4"}, 401},
		{"隧道/IPv6 回环对端", "[::1]:5555",
			map[string]string{"CF-Connecting-IP": "4.4.4.4"}, 401},
		{"隧道/XFF 单跳公网", "127.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "4.4.4.4"}, 401},
		// 核心回归：首跳是攻击者预置的回环，末跳是 CF 追加的真实 IP。
		// 旧实现取首跳 → 判为内网 → 200。
		{"隧道/XFF 首跳伪造回环+末跳真实", "127.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "127.0.0.1, 4.4.4.4"}, 401},
		{"隧道/XFF 首跳伪造私网+末跳真实", "127.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "10.0.0.1, 4.4.4.4"}, 401},
		// 末跳为私网 → 判为内网放行。**这不是漏洞**：末跳是 CF/cloudflared
		// 追加的那一段，客户端控制不了它（线上实测：客户端发
		// `X-Forwarded-For: 10.0.0.1, 1.2.3.4` 时源站解析出的是真实公网出口
		// 116.22.3.237，而非 1.2.3.4）。末跳是私网 ⇒ 该请求真的来自私网。
		{"隧道/XFF 末跳私网（外部无法构造）", "127.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "10.0.0.1, 192.168.1.1"}, 200},
		{"隧道/CF 头优先于伪造 XFF", "127.0.0.1:5555",
			map[string]string{"CF-Connecting-IP": "4.4.4.4", "X-Forwarded-For": "127.0.0.1"}, 401},

		// —— 直连源站（对端不是回环）：转发头一律不可信 ——
		{"直连/公网对端伪造 XFF 回环", "203.0.113.9:5555",
			map[string]string{"X-Forwarded-For": "127.0.0.1"}, 401},
		{"直连/公网对端伪造 CF 头回环", "203.0.113.9:5555",
			map[string]string{"CF-Connecting-IP": "127.0.0.1"}, 401},
		{"直连/公网对端伪造 XFF 私网", "203.0.113.9:5555",
			map[string]string{"X-Forwarded-For": "192.168.1.1, 203.0.113.9"}, 401},

		// —— 本机进程直连 API（回环对端、无转发头）→ 内网放行 ——
		{"本机进程/无转发头", "127.0.0.1:5555", nil, 200},
		{"本机进程/IPv6 回环", "[::1]:5555", nil, 200},

		// —— 真内网直连 → 放行（内网语义不变）——
		{"内网/局域网对端", "192.168.1.50:5555", nil, 200},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runAt(mw, tunnelReq("/api/tasks", c.peer, c.headers)); got != c.want {
				t.Fatalf("status = %d, want %d", got, c.want)
			}
		})
	}
}

// TestTunnelPath_ExternalAuth_ValidTokenStillWorks 合法访问不受修复影响：
// 隧道形态下带有效 token 仍放行。这是「别把正常访问一起修坏」的守护。
func TestTunnelPath_ExternalAuth_ValidTokenStillWorks(t *testing.T) {
	tokens := NewTokenStore()
	tok, err := tokens.Issue(time.Minute)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	svc := newAuthSvc(t, tokens)
	mw := svc.ExternalAuthMiddleware()

	cases := []struct {
		name    string
		peer    string
		headers map[string]string
	}{
		{"隧道/CF-Connecting-IP", "127.0.0.1:5555",
			map[string]string{"CF-Connecting-IP": "4.4.4.4"}},
		{"隧道/XFF 首跳伪造+末跳真实", "127.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "127.0.0.1, 4.4.4.4"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := tunnelReq("/api/tasks", c.peer, c.headers)
			q := req.URL.Query()
			q.Set("token", tok)
			req.URL.RawQuery = q.Encode()
			if got := runAt(mw, req); got != 200 {
				t.Fatalf("带有效 token 应放行，status = %d", got)
			}
		})
	}
}

// TestTunnelPath_BindGate_SpoofedInternalRejected 内网专属闸门同理：
// 隧道形态下伪造内网转发头必须被拒（线上曾返回 400 = 已过闸门）。
func TestTunnelPath_BindGate_SpoofedInternalRejected(t *testing.T) {
	svc := newAuthSvc(t, NewTokenStore())
	mw := svc.BindOpGateMiddleware()

	cases := []struct {
		name    string
		peer    string
		headers map[string]string
		want    int
	}{
		{"隧道/伪造 XFF 回环", "127.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "127.0.0.1, 4.4.4.4"}, 403},
		{"隧道/伪造 XFF 私网", "127.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "192.168.1.1, 4.4.4.4"}, 403},
		{"隧道/真外部", "127.0.0.1:5555",
			map[string]string{"CF-Connecting-IP": "4.4.4.4"}, 403},
		{"直连/公网对端伪造回环", "203.0.113.9:5555",
			map[string]string{"X-Forwarded-For": "127.0.0.1"}, 403},
		{"本机进程/无转发头", "127.0.0.1:5555", nil, 200},
		{"内网/局域网对端", "192.168.1.50:5555", nil, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runAt(mw, tunnelReq("/api/auth/bind", c.peer, c.headers)); got != c.want {
				t.Fatalf("status = %d, want %d", got, c.want)
			}
		})
	}
}

// TestTunnelPath_TunnelOpGate_SpoofedInternalRejected 隧道变更闸门同理：
// 伪造内网转发头不得绕过（旧实现会命中 internal_blocked 分支前的判定）。
func TestTunnelPath_TunnelOpGate_SpoofedInternalRejected(t *testing.T) {
	svc := newAuthSvc(t, NewTokenStore())
	mw := svc.TunnelOpGateMiddleware()

	cases := []struct {
		name    string
		peer    string
		headers map[string]string
		want    int
	}{
		{"隧道/伪造内网+飞书移动端 UA", "127.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "192.168.1.1, 4.4.4.4", "User-Agent": "Lark/12 (iPhone)"}, 200},
		// 该闸门只判「外部 + 飞书移动端 UA」，本身不是鉴权闸门（鉴权由
		// ExternalAuthMiddleware 负责）。直连的外部对端只要带飞书 UA 就能过
		// 这一关 —— 这是既有设计，此处仅锁定行为，不视为放行漏洞。
		{"直连/公网对端+飞书移动端 UA", "203.0.113.9:5555",
			map[string]string{"User-Agent": "Lark/12 (iPhone)"}, 200},
		{"直连/公网对端伪造内网+非飞书 UA", "203.0.113.9:5555",
			map[string]string{"X-Forwarded-For": "192.168.1.1", "User-Agent": "curl"}, 403},
		{"本机进程直连", "127.0.0.1:5555", nil, 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runAt(mw, tunnelReq("/api/tunnel/start", c.peer, c.headers)); got != c.want {
				t.Fatalf("status = %d, want %d", got, c.want)
			}
		})
	}
}

// TestClientIP_VerifiedCloudflareHeaderShape 用线上实测到的头部形态锁定解析契约：
//   - 探针 X-Forwarded-For: 127.0.0.1 时，源站解析出的是真实公网 IP（末跳），
//     而不是客户端预置的 127.0.0.1；
//   - 伪造 CF-Connecting-IP 的请求被 Cloudflare 在边缘 403 拦下、根本不到源站，
//     故本机会话内对它的信任只影响本机（本机本就是可信域），无提权。
func TestClientIP_VerifiedCloudflareHeaderShape(t *testing.T) {
	req := tunnelReq("/", "127.0.0.1:5555", map[string]string{
		"X-Forwarded-For": "127.0.0.1, 240e:3b7:8c4:5140:ede9:a59f:8461:1b3",
	})
	got, src := IPSource(req)
	if got != "240e:3b7:8c4:5140:ede9:a59f:8461:1b3" {
		t.Fatalf("ClientIP = %q, want 真实公网 IPv6（末跳）", got)
	}
	if src == "peer" {
		t.Fatalf("隧道流量的 IP 判定来源不得落到 peer（%q）；否则等于放行", src)
	}
	if IsInternalRequest(req) {
		t.Fatal("解析出真实公网 IP 后不得被判为内网")
	}
}

// TestIPSource_Labels 锁定判定来源标签 —— 审计要靠它回答"这次依据哪个头"。
func TestIPSource_Labels(t *testing.T) {
	cases := []struct {
		name       string
		peer       string
		headers    map[string]string
		wantIP     string
		wantSource string
	}{
		{"CF 头优先", "127.0.0.1:5555",
			map[string]string{"CF-Connecting-IP": "198.51.100.7", "X-Forwarded-For": "1.2.3.4"}, "198.51.100.7", "cf"},
		{"CF 头非法则退化 XFF", "127.0.0.1:5555",
			map[string]string{"CF-Connecting-IP": "not-an-ip", "X-Forwarded-For": "1.2.3.4"}, "1.2.3.4", "xff"},
		{"无转发头走对端", "127.0.0.1:5555", nil, "127.0.0.1", "peer"},
		{"对端不可信不看头", "203.0.113.9:5555",
			map[string]string{"CF-Connecting-IP": "198.51.100.7"}, "203.0.113.9", "peer"},
		{"无法解析", "", nil, "", "none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip, src := IPSource(tunnelReq("/", c.peer, c.headers))
			if ip != c.wantIP || src != c.wantSource {
				t.Fatalf("IPSource = (%q, %q), want (%q, %q)", ip, src, c.wantIP, c.wantSource)
			}
		})
	}
}
