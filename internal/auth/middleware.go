package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Service is the top-level auth facade wired into the gin router. Holds
// all subsystems and exposes the four middleware factories.
type Service struct {
	Debug    *DebugSwitch
	Bindings *BindingStore
	Tokens   *TokenStore
	Limiter  *IPLimiter
	Audit    *AuditLogger
}

// ExternalAuthMiddleware enforces external access control. Order:
// debug → internal → rate-limit-blacklist → token.
//
// Auth policy (adjusted 2026-08): external requests are authorized by the
// tunnel token ALONE (single-factor). The tunnel token is a 32-char random
// value issued only to the tunnel creator (bound admin), so possession of a
// valid token is treated as sufficient identity — this lets plain desktop /
// mobile browsers open the tunnel URL directly without any Feishu identity
// header, which matters because the trycloudflare tunnel domain is dynamic
// and cannot be pre-registered as a Feishu H5 callback / OAuth domain.
// Brute force is guarded by the IP rate limiter.
//
// The Feishu bound-admin identity (X-Feishu-Openid) is no longer checked at
// the HTTP layer; it remains the gate for IM tunnel commands (see
// core.Bridge.handleTunnelCommand).
func (s *Service) ExternalAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.Debug != nil && s.Debug.BypassAll() {
			s.audit(c, "biz.api", true, true, true)
			c.Next()
			return
		}
		if IsInternalRequest(c.Request) {
			s.audit(c, "biz.api", true, true, false)
			c.Next()
			return
		}
		ip := ClientIP(c.Request)
		if s.Limiter != nil && !s.Limiter.Allow(ip) {
			s.audit(c, "biz.api.blacklisted", false, false, false)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "ip blacklisted"})
			return
		}
		// Token check — the sole external credential. Rejects expired /
		// unknown / missing tokens (401) and records a rate-limit failure.
		tok := extractToken(c)
		tokOK := s.Tokens != nil && s.Tokens.Validate(tok)
		if !tokOK {
			// 只对"明显乱猜的错误 token"计入暴力破解限流：空 token（没带）与
			// 格式正确的过期/已失效 token（用户只是不知道过期了，重启/换隧道
			// 后常见）都不拉黑，避免合法用户被误锁 10 分钟。
			if s.Limiter != nil && tok != "" && !looksLikeToken(tok) {
				s.Limiter.NoteFailure(ip)
			}
			s.audit(c, "biz.api.token_fail", false, false, false)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		// 种下同源 cookie：预览外链文档请求带 ?token= 通过后，让后续无法携带
		// header/query 的 preview 子资源请求凭 cookie 鉴权（否则外链空白页）。
		// Secure+HttpOnly；token 在进程内，cookie 随隧道 token 失效而失效。
		c.SetCookie(tunnelTokenCookie, tok, 0, "/", "", true, true)
		s.audit(c, "biz.api", true, false, false)
		c.Next()
	}
}

// looksLikeToken 判断 token 是否符合隧道 token 的格式（32 位小写 base32：
// 字符集 a-z2-7，randomToken 的产物）。用于区分"曾有效的过期 token"
// （不拉黑）与"乱猜的错误 token"（拉黑，防暴力破解）。32 位随机串恰好
// 命中格式的概率可忽略，故格式匹配即可认为曾是合法签发的 token。
func looksLikeToken(tok string) bool {
	if len(tok) != 32 {
		return false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if !((c >= 'a' && c <= 'z') || (c >= '2' && c <= '7')) {
			return false
		}
	}
	return true
}

// TunnelOpGateMiddleware enforces PRD §5.2: tunnel start/stop/reset are
// only allowed from external Feishu mobile webview (Lark/Feishu UA).
// Internal IPs, PC browsers, and debug-off regular browsers all blocked.
// (Debug ON bypasses — same as everywhere else.)
func (s *Service) TunnelOpGateMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.Debug != nil && s.Debug.BypassAll() {
			s.audit(c, "tunnel.op", true, true, true)
			c.Next()
			return
		}
		if IsInternalRequest(c.Request) {
			s.audit(c, "tunnel.op.internal_blocked", false, false, false)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tunnel ops forbidden from internal network"})
			return
		}
		ua := c.GetHeader("User-Agent")
		if !isLarkMobileUA(ua) {
			s.audit(c, "tunnel.op.non_mobile_blocked", false, false, false)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tunnel ops only allowed from Feishu mobile"})
			return
		}
		s.audit(c, "tunnel.op", true, true, false)
		c.Next()
	}
}

// BindOpGateMiddleware enforces PRD §3.2.4 + §5.3: binding/unbinding is
// internal-IP-only. External requests always rejected (even with valid
// token + identity — binding is a privileged local op).
func (s *Service) BindOpGateMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.Debug != nil && s.Debug.BypassAll() {
			s.audit(c, "auth.bind", true, true, true)
			c.Next()
			return
		}
		if !IsInternalRequest(c.Request) {
			s.audit(c, "auth.bind.external_blocked", false, false, false)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "binding only allowed from internal network"})
			return
		}
		s.audit(c, "auth.bind", true, true, false)
		c.Next()
	}
}

// isLarkMobileUA reports whether ua indicates a Feishu/Lark mobile app
// webview. PRD §2.2.3: only this UA may perform tunnel ops externally.
// Matches both "Lark/..." (international) and "Feishu/..." (China).
func isLarkMobileUA(ua string) bool {
	if ua == "" {
		return false
	}
	lower := strings.ToLower(ua)
	// Must look like a mobile app webview — exclude the desktop client.
	// Feishu mobile UA contains "lark" or "feishu" plus a mobile marker.
	if !strings.Contains(lower, "lark") && !strings.Contains(lower, "feishu") {
		return false
	}
	// Reject desktop variants ("LarkClient", "feishu-desktop", etc.)
	if strings.Contains(lower, "desktop") || strings.Contains(lower, "larkclient") {
		return false
	}
	return true
}

// tunnelTokenCookie 子资源鉴权 cookie（同名同源）。
// 外部打开预览外链时，文档请求凭 ?token= 通过鉴权，中间件随即种下该 cookie；
// 此后 preview 子资源（<script src>/@vite/client/依赖/HMR WebSocket）无法携带
// header 或 query，靠同源 cookie 通过 ExternalAuth。token 轮换/过期后 cookie 一并失效。
const tunnelTokenCookie = "pieqi_token"

// extractToken pulls the tunnel token from Authorization header, ?token=
// query, or the same-origin cookie (for preview sub-resource requests that
// cannot attach headers/query).
func extractToken(c *gin.Context) string {
	if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if tok := c.Query("token"); tok != "" {
		return tok
	}
	if tok, err := c.Cookie(tunnelTokenCookie); err == nil && tok != "" {
		return tok
	}
	return ""
}

// audit is a nil-safe wrapper so middlewares can call it even when Audit
// is unset (e.g. in unit tests that don't care about logs).
func (s *Service) audit(c *gin.Context, op string, tokOK, idOK, debug bool) {
	if s.Audit == nil {
		return
	}
	s.Audit.Log(c.Request, AuditEvent{Op: op, TokenOK: tokOK, IdentityOK: idOK, Debug: debug})
}
