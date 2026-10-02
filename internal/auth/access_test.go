package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 本文件覆盖 Cloudflare Access 的源站侧验签（internal/auth/access.go）。
// 这条通道无法在本地端到端实测（需要在 Cloudflare 控制台建 Access 应用），
// 因此正反两面都必须用确定性用例锁死 —— 尤其是"伪造头必须被拒"这一组：
// 如果漏了验签，Access 就只是装饰品。

const (
	testTeamDomain = "https://team.example.com"
	testAudience   = "aud-tag-abc123"
	testKID        = "kid-primary"
)

// 生成一次 RSA 密钥（2048 位生成较慢，全包共用）。
var (
	testKeyOnce sync.Once
	testKey     *rsa.PrivateKey
	testKeyErr  error
)

func sharedTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	testKeyOnce.Do(func() {
		testKey, testKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if testKeyErr != nil {
		t.Fatalf("generate rsa key: %v", testKeyErr)
	}
	return testKey
}

// jwksDoc 把公钥渲染成 JWKS JSON（n/e 为 base64url 大端）。
func jwksDoc(t *testing.T, pub *rsa.PublicKey, kid string) string {
	t.Helper()
	doc := map[string]any{
		"keys": []map[string]any{{
			"kid": kid,
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return string(b)
}

// b64 是 JWT 各段的编码（无 padding 的 base64url）。
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// signJWT 用 alg/kid 指定的头部与给定 claims 签一个 JWT。
// key 为 nil 时不签名（用于构造 alg=none / 只剩两段等畸形样本）。
func signJWT(t *testing.T, key *rsa.PrivateKey, alg, kid string, claims map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]any{"alg": alg, "kid": kid, "typ": "JWT"})
	cb, _ := json.Marshal(claims)
	signing := b64(hb) + "." + b64(cb)
	if key == nil {
		return signing + "."
	}
	d := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + b64(sig)
}

func validClaims() map[string]any {
	return map[string]any{
		"aud":   testAudience,
		"iss":   testTeamDomain,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"email": "zhangsan@example.com",
		"sub":   "user-1",
	}
}

// newTestVerifier 起一个假 JWKS 服务并返回校验器与命中计数器。
func newTestVerifier(t *testing.T, pub *rsa.PublicKey, doc string) (*AccessVerifier, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(srv.Close)

	v, err := NewAccessVerifier(AccessParams{
		TeamDomain: testTeamDomain,
		Audience:   testAudience,
		JWKSURL:    srv.URL + "/cdn-cgi/access/certs",
	})
	if err != nil {
		t.Fatalf("NewAccessVerifier: %v", err)
	}
	return v, &hits
}

func reqWithJWT(t *testing.T, raw string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	if raw != "" {
		req.Header.Set(accessJWTHeader, raw)
	}
	return req
}

func TestAccess_Verify_Valid(t *testing.T) {
	key := sharedTestKey(t)
	v, _ := newTestVerifier(t, &key.PublicKey, jwksDoc(t, &key.PublicKey, testKID))

	id, err := v.Verify(reqWithJWT(t, signJWT(t, key, "RS256", testKID, validClaims())))
	if err != nil {
		t.Fatalf("valid jwt rejected: %v", err)
	}
	if id.Email != "zhangsan@example.com" {
		t.Fatalf("email = %q, want zhangsan@example.com", id.Email)
	}
	if id.Sub != "user-1" {
		t.Fatalf("sub = %q, want user-1", id.Sub)
	}
}

func TestAccess_Verify_Rejects(t *testing.T) {
	key := sharedTestKey(t)
	v, _ := newTestVerifier(t, &key.PublicKey, jwksDoc(t, &key.PublicKey, testKID))

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen other key: %v", err)
	}

	// aud 为数组形态也应通过 —— 单独一条正向用例。
	t.Run("aud 数组形态通过", func(t *testing.T) {
		c := validClaims()
		c["aud"] = []string{"other-app", testAudience}
		if _, err := v.Verify(reqWithJWT(t, signJWT(t, key, "RS256", testKID, c))); err != nil {
			t.Fatalf("aud 数组应通过，got %v", err)
		}
	})

	// cookie 退路（头缺失时）。
	t.Run("cookie 退路通过", func(t *testing.T) {
		raw := signJWT(t, key, "RS256", testKID, validClaims())
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: accessJWTCookie, Value: raw})
		if _, err := v.Verify(req); err != nil {
			t.Fatalf("cookie 应通过，got %v", err)
		}
	})

	cases := []struct {
		name string
		req  func() *http.Request
	}{
		{"没带 JWT", func() *http.Request { return reqWithJWT(t, "") }},
		{"cookie 里是垃圾值", func() *http.Request {
			req := httptest.NewRequest("GET", "/", nil)
			req.AddCookie(&http.Cookie{Name: accessJWTCookie, Value: "not-a-jwt"})
			return req
		}},
		{"段数不足（畸形）", func() *http.Request {
			return reqWithJWT(t, b64([]byte(`{"alg":"RS256"}`))+".abc")
		}},
		{"alg=none 必须拒", func() *http.Request {
			// 手工构造：none 算法不带签名。
			return reqWithJWT(t, signJWT(t, nil, "none", testKID, validClaims()))
		}},
		{"alg=HS256 必须拒（防算法混淆）", func() *http.Request {
			return reqWithJWT(t, signJWT(t, nil, "HS256", testKID, validClaims()))
		}},
		{"kid 缺失", func() *http.Request {
			return reqWithJWT(t, signJWT(t, key, "RS256", "", validClaims()))
		}},
		{"kid 未知", func() *http.Request {
			return reqWithJWT(t, signJWT(t, key, "RS256", "kid-nope", validClaims()))
		}},
		{"签名由别的密钥产生", func() *http.Request {
			return reqWithJWT(t, signJWT(t, otherKey, "RS256", testKID, validClaims()))
		}},
		{"签名被截断", func() *http.Request {
			raw := signJWT(t, key, "RS256", testKID, validClaims())
			return reqWithJWT(t, raw[:len(raw)-6])
		}},
		{"已过期", func() *http.Request {
			c := validClaims()
			c["exp"] = time.Now().Add(-2 * time.Hour).Unix()
			return reqWithJWT(t, signJWT(t, key, "RS256", testKID, c))
		}},
		{"exp 缺失（等价永久凭据）", func() *http.Request {
			c := validClaims()
			delete(c, "exp")
			return reqWithJWT(t, signJWT(t, key, "RS256", testKID, c))
		}},
		{"尚未生效 nbf", func() *http.Request {
			c := validClaims()
			c["nbf"] = time.Now().Add(time.Hour).Unix()
			return reqWithJWT(t, signJWT(t, key, "RS256", testKID, c))
		}},
		{"aud 不匹配（同团队别的应用）", func() *http.Request {
			c := validClaims()
			c["aud"] = "another-app-aud"
			return reqWithJWT(t, signJWT(t, key, "RS256", testKID, c))
		}},
		{"aud 缺失", func() *http.Request {
			c := validClaims()
			delete(c, "aud")
			return reqWithJWT(t, signJWT(t, key, "RS256", testKID, c))
		}},
		{"iss 不匹配（别人的团队域）", func() *http.Request {
			c := validClaims()
			c["iss"] = "https://evil-team.cloudflareaccess.com"
			return reqWithJWT(t, signJWT(t, key, "RS256", testKID, c))
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if id, err := v.Verify(c.req()); err == nil {
				t.Fatalf("必须拒绝，却通过了（identity=%+v）", id)
			}
		})
	}
}

// TestAccess_Verify_JWKSIsCached 公钥集必须缓存，不能每个请求都回拉 ——
// 否则每个外网请求都会打一次 Cloudflare，且给了被当放大器用的空间。
func TestAccess_Verify_JWKSIsCached(t *testing.T) {
	key := sharedTestKey(t)
	v, hits := newTestVerifier(t, &key.PublicKey, jwksDoc(t, &key.PublicKey, testKID))

	for i := 0; i < 5; i++ {
		if _, err := v.Verify(reqWithJWT(t, signJWT(t, key, "RS256", testKID, validClaims()))); err != nil {
			t.Fatalf("第 %d 次校验失败: %v", i+1, err)
		}
	}
	if got := atomic.LoadInt64(hits); got != 1 {
		t.Fatalf("JWKS 拉取次数 = %d, want 1（应命中缓存）", got)
	}
}

func TestNewAccessVerifier_ConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		p    AccessParams
	}{
		{"缺 team_domain", AccessParams{Audience: testAudience}},
		{"缺 audience", AccessParams{TeamDomain: testTeamDomain}},
		{"team_domain 不是 URL", AccessParams{TeamDomain: "http://", Audience: testAudience}},
		{"全空", AccessParams{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewAccessVerifier(c.p); err == nil {
				t.Fatal("配置不全必须报错，否则会跑成'看起来开了 Access、实际谁都能进'的空壳")
			}
		})
	}
}

// newAccessMW 构造走真实中间件链的 Auth Service。
func newAccessMW(t *testing.T, access *AccessVerifier, tokenDisabled bool, tokens *TokenStore) *Service {
	t.Helper()
	bindings, err := NewBindingStore(tempPath(t))
	if err != nil {
		t.Fatalf("binding store: %v", err)
	}
	if tokens == nil {
		tokens = NewTokenStore()
	}
	return &Service{
		Debug:         NewDebugSwitch(false),
		Bindings:      bindings,
		Tokens:        tokens,
		Limiter:       NewIPLimiter(1000, time.Minute),
		Access:        access,
		TokenDisabled: tokenDisabled,
	}
}

// TestMW_Access_ReplacesToken 是本次改造的主目标：外网请求只需 Access JWT，
// URL 里不再需要 ?token=。对端用非回环地址，确保走的是"外网"分支。
func TestMW_Access_ReplacesToken(t *testing.T) {
	key := sharedTestKey(t)
	v, _ := newTestVerifier(t, &key.PublicKey, jwksDoc(t, &key.PublicKey, testKID))
	jwt := signJWT(t, key, "RS256", testKID, validClaims())

	t.Run("有 Access JWT、无 token、token 已关 → 放行", func(t *testing.T) {
		svc := newAccessMW(t, v, true, nil)
		req := tunnelReq("/api/tasks", "203.0.113.9:5555", map[string]string{accessJWTHeader: jwt})
		if got := runAt(svc.ExternalAuthMiddleware(), req); got != 200 {
			t.Fatalf("status = %d, want 200（Access 通过即放行，URL 不带 token）", got)
		}
	})

	t.Run("有 Access JWT、token 仍开 → 放行", func(t *testing.T) {
		svc := newAccessMW(t, v, false, nil)
		req := tunnelReq("/api/tasks", "203.0.113.9:5555", map[string]string{accessJWTHeader: jwt})
		if got := runAt(svc.ExternalAuthMiddleware(), req); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
	})

	t.Run("Access JWT 伪造、token 已关 → 拒绝", func(t *testing.T) {
		svc := newAccessMW(t, v, true, nil)
		otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		bad := signJWT(t, otherKey, "RS256", testKID, validClaims())
		req := tunnelReq("/api/tasks", "203.0.113.9:5555", map[string]string{accessJWTHeader: bad})
		if got := runAt(svc.ExternalAuthMiddleware(), req); got != 401 {
			t.Fatalf("status = %d, want 401（伪造 JWT 必须拒）", got)
		}
	})

	t.Run("Access JWT 伪造、token 兜底开启且有合法 token → 放行", func(t *testing.T) {
		tokens := NewTokenStore()
		tok, _ := tokens.Issue(time.Minute)
		svc := newAccessMW(t, v, false, tokens)
		otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		bad := signJWT(t, otherKey, "RS256", testKID, validClaims())
		req := tunnelReq("/api/tasks", "203.0.113.9:5555", map[string]string{accessJWTHeader: bad})
		q := req.URL.Query()
		q.Set("token", tok)
		req.URL.RawQuery = q.Encode()
		if got := runAt(svc.ExternalAuthMiddleware(), req); got != 200 {
			t.Fatalf("status = %d, want 200（兜底通道应生效）", got)
		}
	})

	t.Run("两道都没有凭据 → 401", func(t *testing.T) {
		svc := newAccessMW(t, v, true, nil)
		req := tunnelReq("/api/tasks", "203.0.113.9:5555", nil)
		if got := runAt(svc.ExternalAuthMiddleware(), req); got != 401 {
			t.Fatalf("status = %d, want 401", got)
		}
	})

	t.Run("两道凭据全关（Access 未启用 + token 关闭）→ 401，不得裸奔", func(t *testing.T) {
		svc := newAccessMW(t, nil, true, nil)
		req := tunnelReq("/api/tasks", "203.0.113.9:5555", nil)
		if got := runAt(svc.ExternalAuthMiddleware(), req); got != 401 {
			t.Fatalf("status = %d, want 401（deny-by-default）", got)
		}
	})

	t.Run("Access 启用时内网仍放行", func(t *testing.T) {
		svc := newAccessMW(t, v, true, nil)
		req := tunnelReq("/api/tasks", "192.168.1.50:5555", nil)
		if got := runAt(svc.ExternalAuthMiddleware(), req); got != 200 {
			t.Fatalf("status = %d, want 200", got)
		}
	})
}

// TestMW_Access_DefaultKeepsTokenPath 零值 Service 必须保持"token 可用"，
// 否则既有构造点（测试与老装配）会集体改变语义。
func TestMW_Access_DefaultKeepsTokenPath(t *testing.T) {
	tokens := NewTokenStore()
	tok, _ := tokens.Issue(time.Minute)
	svc := &Service{Debug: NewDebugSwitch(false), Tokens: tokens}
	if svc.TokenDisabled {
		t.Fatal("零值 TokenDisabled 必须为 false（token 通道默认可用）")
	}
	req := tunnelReq("/api/tasks", "203.0.113.9:5555", nil)
	q := req.URL.Query()
	q.Set("token", tok)
	req.URL.RawQuery = q.Encode()
	if got := runAt(svc.ExternalAuthMiddleware(), req); got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
}

// TestAccess_Verify_ErrorMessagesDoNotLeakToken 错误信息会进日志，
// 必须只说明"哪一类失败"，绝不带出 token 本体。
func TestAccess_Verify_ErrorMessagesDoNotLeakToken(t *testing.T) {
	key := sharedTestKey(t)
	v, _ := newTestVerifier(t, &key.PublicKey, jwksDoc(t, &key.PublicKey, testKID))

	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	raw := signJWT(t, otherKey, "RS256", testKID, validClaims())
	_, err := v.Verify(reqWithJWT(t, raw))
	if err == nil {
		t.Fatal("应拒绝")
	}
	// 错误信息会进日志，绝不能把 token 本体带出去。
	secret := strings.SplitN(raw, ".", 3)[2]
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), raw) {
		t.Fatalf("错误信息泄露了 token：%v", err)
	}
}

// 编译期确认 gin 被引用（本文件通过 runAt 间接使用）。
var _ = gin.H{}
