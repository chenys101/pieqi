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

// ExternalAuthMiddleware enforces PRD §2.2: external requests must have a
// matching X-Feishu-Openid header AND a valid tunnel token. Internal IPs
// bypass entirely. Debug switch bypasses everything (highest priority).
//
// Order: debug → internal → rate-limit-blacklist → identity → token.
//
// Why identity BEFORE token: PRD §2.2.3 says "外网普通浏览器 / 工具 /
// 爬虫 / Postman - 无飞书登录身份 → 直接403拦截". A plain browser has no
// X-Feishu-Openid header at all, so we reject at the identity step (403)
// before spending a token lookup on them. A bound Feishu admin whose
// token has merely expired still sends their openid, so they reach the
// token check (401 — re-authenticate). The rate limiter guards the token
// check against attackers who happen to know the bound OpenID string.
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
		// 1. Identity check — rejects curl/Postman/plain browsers (no openid)
		openid := c.GetHeader("X-Feishu-Openid")
		idOK := s.Bindings != nil && s.Bindings.Match(openid)
		if !idOK {
			s.audit(c, "biz.api.identity_fail", false, false, false)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "identity mismatch"})
			return
		}
		// 2. Token check — rejects expired/unknown tokens for legit bound admins
		tok := extractToken(c)
		tokOK := s.Tokens != nil && s.Tokens.Validate(tok)
		if !tokOK {
			if s.Limiter != nil {
				s.Limiter.NoteFailure(ip)
			}
			s.audit(c, "biz.api.token_fail", false, true, false)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		s.audit(c, "biz.api", true, true, false)
		c.Next()
	}
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

// extractToken pulls the tunnel token from Authorization header or ?token=
// query param. The query form is essential for the lark:// deep link to
// work directly without the front-end re-attaching headers.
func extractToken(c *gin.Context) string {
	if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return c.Query("token")
}

// audit is a nil-safe wrapper so middlewares can call it even when Audit
// is unset (e.g. in unit tests that don't care about logs).
func (s *Service) audit(c *gin.Context, op string, tokOK, idOK, debug bool) {
	if s.Audit == nil {
		return
	}
	s.Audit.Log(c.Request, AuditEvent{Op: op, TokenOK: tokOK, IdentityOK: idOK, Debug: debug})
}
