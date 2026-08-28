package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setupMW(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return gin.New()
}

// runWithMW runs a request through a middleware-wrapped endpoint and
// returns the resulting status code.
func runWithMW(mw gin.HandlerFunc, r *http.Request) int {
	gin.SetMode(gin.TestMode)
	g := gin.New()
	g.Use(mw)
	g.GET("/x", func(c *gin.Context) { c.String(200, "ok") })
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w.Code
}

func newReq(ip, ua, openid string) *http.Request {
	req := httptest.NewRequest("GET", "/x", nil)
	if ip != "" {
		req.RemoteAddr = ip + ":1234"
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if openid != "" {
		req.Header.Set("X-Feishu-Openid", openid)
	}
	return req
}

func TestMW_DebugBypass_AllowsEverything(t *testing.T) {
	dbg := NewDebugSwitch(true)
	svc := &Service{Debug: dbg, Tokens: NewTokenStore()}
	mw := svc.ExternalAuthMiddleware()
	// External IP, no token, no openid, weird UA → still 200
	if c := runWithMW(mw, newReq("8.8.8.8", "curl", "")); c != 200 {
		t.Fatalf("debug bypass should allow all, got %d", c)
	}
}

func TestMW_InternalIPBypassesExternalCheck(t *testing.T) {
	dbg := NewDebugSwitch(false)
	bindings, _ := NewBindingStore(tempPath(t))
	svc := &Service{Debug: dbg, Bindings: bindings, Tokens: NewTokenStore()}
	mw := svc.ExternalAuthMiddleware()
	if c := runWithMW(mw, newReq("192.168.1.10", "", "")); c != 200 {
		t.Fatalf("internal IP should bypass, got %d", c)
	}
	if c := runWithMW(mw, newReq("127.0.0.1", "", "")); c != 200 {
		t.Fatalf("loopback should bypass, got %d", c)
	}
}

func TestMW_External_NoToken_401(t *testing.T) {
	dbg := NewDebugSwitch(false)
	bindings, _ := NewBindingStore(tempPath(t))
	_, _ = bindings.Bind(Binding{OpenID: "ou_x"})
	svc := &Service{Debug: dbg, Bindings: bindings, Tokens: NewTokenStore(), Limiter: NewIPLimiter(5, 10*time.Minute)}
	mw := svc.ExternalAuthMiddleware()
	// External IP, no token query param, correct openid → 401
	if c := runWithMW(mw, newReq("4.4.4.4", "Lark/12", "ou_x")); c != 401 {
		t.Fatalf("no token → 401, got %d", c)
	}
}

func TestMW_External_InvalidToken_401AndRateLimited(t *testing.T) {
	dbg := NewDebugSwitch(false)
	bindings, _ := NewBindingStore(tempPath(t))
	_, _ = bindings.Bind(Binding{OpenID: "ou_x"})
	limiter := NewIPLimiter(5, 10*time.Minute)
	tokens := NewTokenStore()
	svc := &Service{Debug: dbg, Bindings: bindings, Tokens: tokens, Limiter: limiter}
	mw := svc.ExternalAuthMiddleware()
	// 5 invalid attempts → still 401; 6th should be 403 (blacklisted)
	for i := 0; i < 5; i++ {
		if c := runWithMW(mw, reqWithToken("4.4.4.4", "Lark", "ou_x", "bad-token")); c != 401 {
			t.Fatalf("attempt %d: want 401, got %d", i+1, c)
		}
	}
	if c := runWithMW(mw, reqWithToken("4.4.4.4", "Lark", "ou_x", "bad-token")); c != 403 {
		t.Fatalf("6th attempt: want 403 (blacklisted), got %d", c)
	}
}

func TestMW_External_ValidToken_OpenIDIgnored_200(t *testing.T) {
	// Token single-factor policy: a valid token is sufficient regardless of
	// X-Feishu-Openid (wrong/missing openid no longer rejects).
	dbg := NewDebugSwitch(false)
	bindings, _ := NewBindingStore(tempPath(t))
	_, _ = bindings.Bind(Binding{OpenID: "ou_admin"})
	tokens := NewTokenStore()
	tok, _ := tokens.Issue(time.Minute)
	svc := &Service{Debug: dbg, Bindings: bindings, Tokens: tokens, Limiter: NewIPLimiter(5, 10*time.Minute)}
	mw := svc.ExternalAuthMiddleware()
	// Valid token + wrong openid → 200
	if c := runWithMW(mw, reqWithToken("4.4.4.4", "Lark", "ou_spoof", tok)); c != 200 {
		t.Fatalf("valid token, wrong openid → 200, got %d", c)
	}
	// Valid token + no openid header at all (plain browser) → 200
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "4.4.4.4:1234"
	q := req.URL.Query()
	q.Set("token", tok)
	req.URL.RawQuery = q.Encode()
	if c := runWithMW(mw, req); c != 200 {
		t.Fatalf("valid token, no openid → 200, got %d", c)
	}
}

func TestMW_External_ValidTokenValidOpenID_200(t *testing.T) {
	dbg := NewDebugSwitch(false)
	bindings, _ := NewBindingStore(tempPath(t))
	_, _ = bindings.Bind(Binding{OpenID: "ou_admin"})
	tokens := NewTokenStore()
	tok, _ := tokens.Issue(time.Minute)
	svc := &Service{Debug: dbg, Bindings: bindings, Tokens: tokens, Limiter: NewIPLimiter(5, 10*time.Minute)}
	mw := svc.ExternalAuthMiddleware()
	if c := runWithMW(mw, reqWithToken("4.4.4.4", "Lark", "ou_admin", tok)); c != 200 {
		t.Fatalf("valid token + valid openid → 200, got %d", c)
	}
}

func TestMW_TunnelOpGate_PCForbidden(t *testing.T) {
	dbg := NewDebugSwitch(false)
	svc := &Service{Debug: dbg}
	mw := svc.TunnelOpGateMiddleware()
	// PC browser UA (Chrome, not Lark/Feishu) → 403
	if c := runWithMW(mw, newReq("4.4.4.4", "Mozilla/5.0 Chrome", "")); c != 403 {
		t.Fatalf("PC UA → 403, got %d", c)
	}
	// Lark mobile UA → 200
	if c := runWithMW(mw, newReq("4.4.4.4", "Lark/12.3 (iPhone)", "")); c != 200 {
		t.Fatalf("Lark mobile → 200, got %d", c)
	}
	// Feishu mobile UA also accepted
	if c := runWithMW(mw, newReq("4.4.4.4", "Feishu/12.3 Android", "")); c != 200 {
		t.Fatalf("Feishu mobile → 200, got %d", c)
	}
}

func TestMW_TunnelOpGate_DebugBypass(t *testing.T) {
	svc := &Service{Debug: NewDebugSwitch(true)}
	mw := svc.TunnelOpGateMiddleware()
	if c := runWithMW(mw, newReq("4.4.4.4", "curl", "")); c != 200 {
		t.Fatalf("debug bypass → 200, got %d", c)
	}
}

func TestMW_TunnelOpGate_InternalBlocked(t *testing.T) {
	// PRD §5.2: tunnel ops are forbidden from internal too. Only Lark
	// mobile external may operate tunnels.
	svc := &Service{Debug: NewDebugSwitch(false)}
	mw := svc.TunnelOpGateMiddleware()
	if c := runWithMW(mw, newReq("192.168.1.5", "Lark/12", "")); c != 403 {
		t.Fatalf("internal IP tunnel op → 403, got %d", c)
	}
}

func TestMW_BindOpGate_InternalAllowedExternalBlocked(t *testing.T) {
	svc := &Service{Debug: NewDebugSwitch(false)}
	mw := svc.BindOpGateMiddleware()
	if c := runWithMW(mw, newReq("192.168.1.5", "", "")); c != 200 {
		t.Fatalf("internal bind → 200, got %d", c)
	}
	if c := runWithMW(mw, newReq("4.4.4.4", "Lark", "ou_admin")); c != 403 {
		t.Fatalf("external bind → 403, got %d", c)
	}
}

// --- helpers ---

func reqWithToken(ip, ua, openid, token string) *http.Request {
	req := newReq(ip, ua, openid)
	// Token travels in Authorization: Bearer <token> OR ?token= for
	// convenience (so lark://open?url=...?token=... works directly).
	q := req.URL.Query()
	q.Set("token", token)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestLooksLikeToken(t *testing.T) {
	valid := "abcdefghijklmnopqrstuvwxyz234567" // 32 chars, all in base32 a-z2-7
	if !looksLikeToken(valid) {
		t.Fatal("valid-format token should pass")
	}
	cases := []struct {
		tok  string
		want bool
	}{
		{"", false},
		{"short", false},
		{"abcdefghijklmnopqrstuvwxyz2345678", false}, // 33 chars
		{"abcdefghijklmnopqrstuvwxyz23456!", false},  // 非法字符
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", false},  // 大写不合格式
		{"abcdefghijklmnopqrstuvwxyz234567", true},   // 精确 32 位合法
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true},   // 全 a 合法
		{"22222222222222222222222222222222", true},   // 全 2 合法
	}
	for _, c := range cases {
		if got := looksLikeToken(c.tok); got != c.want {
			t.Errorf("looksLikeToken(%q) = %v, want %v", c.tok, got, c.want)
		}
	}
}

func TestMW_External_ExpiredTokenNotRateLimited(t *testing.T) {
	// 格式正确的"曾有效、已失效"token（重启/换隧道后旧链接的常见形态）：
	// 连续访问返回 401 但绝不触发 IP 拉黑。
	limiter := NewIPLimiter(5, 10*time.Minute)
	tokens := NewTokenStore()
	tok, _ := tokens.Issue(time.Minute)
	tokens.InvalidateAll() // 模拟重启/换新隧道后旧 token 失效
	svc := &Service{Debug: NewDebugSwitch(false), Tokens: tokens, Limiter: limiter}
	mw := svc.ExternalAuthMiddleware()

	for i := 0; i < 20; i++ {
		if c := runWithMW(mw, reqWithToken("5.5.5.5", "Mozilla", "", tok)); c != 401 {
			t.Fatalf("attempt %d: expired valid-format token → 401, got %d", i+1, c)
		}
	}
	// 20 次后仍未拉黑（若被拉黑会是 403 blacklisted）
	if c := runWithMW(mw, reqWithToken("5.5.5.5", "Mozilla", "", tok)); c != 401 {
		t.Fatalf("expired tokens must not blacklist the IP, got %d", c)
	}
}

func TestMW_External_GarbageTokenRateLimited(t *testing.T) {
	// 乱猜的错误 token（格式不对）→ 5 次后拉黑，保留暴力破解防护。
	limiter := NewIPLimiter(5, 10*time.Minute)
	tokens := NewTokenStore()
	svc := &Service{Debug: NewDebugSwitch(false), Tokens: tokens, Limiter: limiter}
	mw := svc.ExternalAuthMiddleware()

	for i := 0; i < 5; i++ {
		if c := runWithMW(mw, reqWithToken("6.6.6.6", "Mozilla", "", "bad-token")); c != 401 {
			t.Fatalf("attempt %d: garbage token → 401, got %d", i+1, c)
		}
	}
	if c := runWithMW(mw, reqWithToken("6.6.6.6", "Mozilla", "", "bad-token")); c != 403 {
		t.Fatalf("6th garbage attempt → 403 blacklisted, got %d", c)
	}
}
