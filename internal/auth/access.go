package auth

// Cloudflare Access 前置鉴权（2026-10-02）。
//
// 背景：外链访问原本只靠一个 32 位随机 token（URL 带 ?token=，单因子）。当初
// 这么选的理由是「trycloudflare 域名是动态的，无法预注册为飞书 H5 回调/OAuth
// 域」（见 middleware.go）。固定域名上线后这条约束消失 —— 但这个域名的证书是
// Let's Encrypt 签的，**必然进证书透明日志（CT）**，等于可被公开枚举，所以
// 鉴权只能加强、不能取消。
//
// 这里实现的是 Access 的**源站侧校验**：Cloudflare 在应用策略通过后，会给回源
// 请求带上 Cf-Access-Jwt-Assertion（RS256 签名的 JWT）。源站必须自己验签，
// 否则任何人伪造一个同名头就绕过了 Access —— 那 Access 就只是装饰。
//
// 纪律：
//   - 只用 Cloudflare 团队域公布在 .../cdn-cgi/access/certs 的公钥验签；
//   - 强制 RS256，显式拒绝 none / HS* （防算法混淆攻击）；
//   - 必须核 aud 绑定到本应用（防同团队其他应用的 token 被拿来复用）；
//   - 必须核 iss 绑定到团队域；
//   - 校验 exp/nbf（含小量时钟偏移容忍）。
//
// 仅依赖标准库（crypto/rsa + crypto/sha256 + encoding/json），不引第三方 JWT 库。

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// accessJWTHeader 是 Cloudflare Access 回源时注入的 JWT 头名。
	accessJWTHeader = "Cf-Access-Jwt-Assertion"
	// accessJWTCookie 是 Access 下发给浏览器会话的 cookie（头缺失时的退路）。
	accessJWTCookie = "CF_Authorization"

	accessJWKSDefaultTTL = time.Hour
	// accessJWKSMinRefetchGap 限制「未知 kid 触发重拉」的频率，避免被用
	// 伪造 kid 的请求打成 JWKS 拉取放大器。
	accessJWKSMinRefetchGap = 30 * time.Second
	// accessClockLeeway 时钟偏移容忍（签发方与本机可能有秒级偏差）。
	accessClockLeeway = 60 * time.Second
)

// AccessIdentity 是一次成功校验得到的访问者身份。
type AccessIdentity struct {
	Email string // Access 策略通常按邮箱授权；可能为空（非邮箱型 IdP）
	Sub   string // JWT sub
}

// AccessParams 构造 AccessVerifier 的参数（来自 config）。
type AccessParams struct {
	// TeamDomain 团队域，如 https://myteam.cloudflareaccess.com。必填。
	TeamDomain string
	// Audience Access 应用（Self-hosted / Fixed hostname）的 AUD tag。必填。
	Audience string
	// JWKSURL 公钥地址；留空则用 <TeamDomain>/cdn-cgi/access/certs。
	JWKSURL string
	// HTTPClient 拉取 JWKS 用；nil 则用带 10s 超时的默认客户端。
	HTTPClient *http.Client
	// Now 注入时钟（测试用）；nil 用 time.Now。
	Now func() time.Time
}

// AccessVerifier 校验 Cloudflare Access 注入的 JWT。并发安全。
type AccessVerifier struct {
	teamDomain string
	audience   string
	jwksURL    string
	client     *http.Client
	now        func() time.Time
	ttl        time.Duration

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

// NewAccessVerifier 构造校验器。TeamDomain / Audience 缺失即报错 —— 宁可启动时
// 明确失败，也不要跑成一个"看起来开了 Access、实际谁都能进"的空壳。
func NewAccessVerifier(p AccessParams) (*AccessVerifier, error) {
	team := strings.TrimSpace(p.TeamDomain)
	if team == "" {
		return nil, errors.New("cloudflare_access: team_domain 必填（如 https://myteam.cloudflareaccess.com）")
	}
	if !strings.Contains(team, "://") {
		team = "https://" + team
	}
	u, err := url.Parse(team)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("cloudflare_access: team_domain %q 不是合法 URL", p.TeamDomain)
	}
	team = strings.TrimSuffix(u.Scheme+"://"+u.Host, "/")

	aud := strings.TrimSpace(p.Audience)
	if aud == "" {
		return nil, errors.New("cloudflare_access: audience 必填（Access 应用的 AUD tag）")
	}

	jwks := strings.TrimSpace(p.JWKSURL)
	if jwks == "" {
		jwks = team + "/cdn-cgi/access/certs"
	}
	if ju, err := url.Parse(jwks); err != nil || ju.Host == "" {
		return nil, fmt.Errorf("cloudflare_access: jwks_url %q 不是合法 URL", jwks)
	}

	now := p.Now
	if now == nil {
		now = time.Now
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &AccessVerifier{
		teamDomain: team,
		audience:   aud,
		jwksURL:    jwks,
		client:     client,
		now:        now,
		ttl:        accessJWKSDefaultTTL,
		keys:       make(map[string]*rsa.PublicKey),
	}, nil
}

// Enabled 报告校验器是否可用（nil 安全）。
func (a *AccessVerifier) Enabled() bool { return a != nil }

// Verify 校验请求携带的 Access JWT，成功返回身份。
//
// 失败一律返回 error，调用方按「未通过 Access」处理并落到下一道鉴权。
// 错误信息含失败原因但不含 token 内容（token 绝不落日志）。
func (a *AccessVerifier) Verify(r *http.Request) (AccessIdentity, error) {
	if a == nil {
		return AccessIdentity{}, errors.New("access verifier not configured")
	}
	raw := strings.TrimSpace(r.Header.Get(accessJWTHeader))
	if raw == "" {
		if c, err := r.Cookie(accessJWTCookie); err == nil {
			raw = strings.TrimSpace(c.Value)
		}
	}
	if raw == "" {
		return AccessIdentity{}, errors.New("no access jwt")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return AccessIdentity{}, errors.New("access jwt: malformed (want 3 segments)")
	}

	hdrRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return AccessIdentity{}, errors.New("access jwt: bad header encoding")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(hdrRaw, &hdr); err != nil {
		return AccessIdentity{}, errors.New("access jwt: bad header json")
	}
	// 防算法混淆：只认 RS256。显式拒绝 none / HS256（HS 会用公钥当 HMAC 密钥）。
	if hdr.Alg != "RS256" {
		return AccessIdentity{}, fmt.Errorf("access jwt: unsupported alg %q (want RS256)", hdr.Alg)
	}
	if hdr.Kid == "" {
		return AccessIdentity{}, errors.New("access jwt: missing kid")
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return AccessIdentity{}, errors.New("access jwt: bad signature encoding")
	}

	key, err := a.key(hdr.Kid)
	if err != nil {
		return AccessIdentity{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return AccessIdentity{}, errors.New("access jwt: signature verification failed")
	}

	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return AccessIdentity{}, errors.New("access jwt: bad payload encoding")
	}
	var claims struct {
		Aud   json.RawMessage `json:"aud"`
		Iss   string          `json:"iss"`
		Exp   int64           `json:"exp"`
		Nbf   int64           `json:"nbf"`
		Email string          `json:"email"`
		Sub   string          `json:"sub"`
	}
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return AccessIdentity{}, errors.New("access jwt: bad payload json")
	}

	// exp 必填：签名有效但没有过期时间的 token 等于永久凭据。
	if claims.Exp == 0 {
		return AccessIdentity{}, errors.New("access jwt: missing exp")
	}
	now := a.now()
	if now.After(time.Unix(claims.Exp, 0).Add(accessClockLeeway)) {
		return AccessIdentity{}, errors.New("access jwt: expired")
	}
	if claims.Nbf != 0 && now.Add(accessClockLeeway).Before(time.Unix(claims.Nbf, 0)) {
		return AccessIdentity{}, errors.New("access jwt: not yet valid (nbf)")
	}
	// iss 绑定团队域：防同一个人手里的其他团队 token 被拿来用。
	if !strings.EqualFold(strings.TrimSuffix(claims.Iss, "/"), a.teamDomain) {
		return AccessIdentity{}, fmt.Errorf("access jwt: issuer mismatch (got %q, want %q)", claims.Iss, a.teamDomain)
	}
	// aud 绑定本应用：防同团队其他 Access 应用的 token 被复用到这里。
	if !audContains(claims.Aud, a.audience) {
		return AccessIdentity{}, errors.New("access jwt: audience mismatch")
	}
	return AccessIdentity{Email: claims.Email, Sub: claims.Sub}, nil
}

// audContains 判断 aud 声明（字符串或字符串数组）是否包含 want。
func audContains(raw json.RawMessage, want string) bool {
	if len(raw) == 0 {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s == want
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, v := range list {
			if v == want {
				return true
			}
		}
	}
	return false
}

// key 返回 kid 对应的公钥。缓存未命中或已过期时拉取 JWKS；
// 未知 kid 触发一次重拉（带最小间隔保护，见 accessJWKSMinRefetchGap）。
func (a *AccessVerifier) key(kid string) (*rsa.PublicKey, error) {
	if k, ok := a.cachedKey(kid); ok {
		return k, nil
	}
	if err := a.refreshJWKS(false); err != nil {
		// 拉取失败时若手里还有任意一把旧公钥，让它去验签失败，
		// 而不是给调用方一个"配置坏了"的误导错误。
		if k, ok := a.cachedKey(kid); ok {
			return k, nil
		}
		return nil, fmt.Errorf("access jwks fetch: %w", err)
	}
	if k, ok := a.cachedKey(kid); ok {
		return k, nil
	}
	return nil, fmt.Errorf("access jwks: unknown kid %q", kid)
}

func (a *AccessVerifier) cachedKey(kid string) (*rsa.PublicKey, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.keys == nil {
		return nil, false
	}
	k, ok := a.keys[kid]
	return k, ok
}

// refreshJWKS 拉取并替换公钥集。force=false 时受 TTL 与最小重拉间隔约束。
func (a *AccessVerifier) refreshJWKS(force bool) error {
	a.mu.Lock()
	now := a.now()
	if !force {
		if !a.fetchedAt.IsZero() && now.Sub(a.fetchedAt) < a.ttl {
			a.mu.Unlock()
			return nil
		}
	}
	if !a.lastAttempt.IsZero() && now.Sub(a.lastAttempt) < accessJWKSMinRefetchGap {
		a.mu.Unlock()
		return errors.New("jwks refetch throttled")
	}
	a.lastAttempt = now
	a.mu.Unlock()

	resp, err := a.client.Get(a.jwksURL)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}
	parsed := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kid == "" || !strings.EqualFold(k.Kty, "RSA") {
			continue
		}
		if k.Alg != "" && !strings.EqualFold(k.Alg, "RS256") {
			continue
		}
		if k.Use != "" && !strings.EqualFold(k.Use, "sig") {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		parsed[k.Kid] = pub
	}
	if len(parsed) == 0 {
		return errors.New("jwks contained no usable RSA signing keys")
	}

	a.mu.Lock()
	a.keys = parsed
	a.fetchedAt = now
	a.mu.Unlock()
	return nil
}

// rsaPublicKeyFromJWK 由 JWK 的 n/e（base64url，大端）还原 RSA 公钥。
func rsaPublicKeyFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil || len(nb) == 0 {
		return nil, errors.New("bad n")
	}
	eb, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil || len(eb) == 0 {
		return nil, errors.New("bad e")
	}
	e := new(big.Int).SetBytes(eb)
	// 指数必须能放进 int 且是合法 RSA 指数（>=3 且为奇数）。
	if !e.IsInt64() || e.Int64() < 3 || e.Int64()%2 == 0 {
		return nil, errors.New("bad exponent")
	}
	mod := new(big.Int).SetBytes(nb)
	if mod.BitLen() < 2048 {
		return nil, errors.New("key too small")
	}
	return &rsa.PublicKey{N: mod, E: int(e.Int64())}, nil
}
