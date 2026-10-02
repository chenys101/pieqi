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
	// Access 是 Cloudflare Access 的源站侧 JWT 校验器；nil = 未启用。
	Access *AccessVerifier
	// TokenDisabled 关闭「外链 ?token=」这道凭据（Access-only 模式）。
	//
	// 取反命名是为了让零值语义正确：Service 到处以结构体字面量构造，
	// 零值必须保持"token 可用"以兼容既有行为与测试。只有显式置 true
	// （配置 cloudflare_access.token_fallback=false）才关掉它。
	//
	// ⚠️ Access 与 token 同时关闭 = 外网无任何凭据通道 → 中间件一律 401
	// （deny-by-default），启动时另打 WARN。
	TokenDisabled bool
}

// accessEmailContextKey 保存 Access 校验通过后的身份邮箱，供下游 handler 审计。
const accessEmailContextKey = "auth.access_email"

// AccessEmailFrom 取当前请求经 Cloudflare Access 验证出的身份邮箱（可能为空）。
func AccessEmailFrom(c *gin.Context) string {
	if v, ok := c.Get(accessEmailContextKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ExternalAuthMiddleware enforces external access control. Order:
// debug → internal → rate-limit-blacklist → Cloudflare Access → tunnel token.
//
// Auth policy:
//   - 内网直连 / 本机进程 → 全放行（见 IsInternalRequest，其可信性依赖
//     ClientIP 的转发头信任纪律）。
//   - 外网 → 两道凭据通道，任一通过即可：
//     ① Cloudflare Access：源站自行验签 Cf-Access-Jwt-Assertion（RS256 +
//     aud/iss/exp）。这是**密码学**判定，与来源 IP 无关，故不受转发头
//     影响。域名固定后证书会进 CT 日志（可被公开枚举），Access 是把
//     "谁发现域名谁能进"变成"按人授权、可随时撤销"的正确姿势。
//     ② 外链 ?token=（32 位随机、内存态、随隧道重启轮换）：作为兜底保留，
//     可用 cloudflare_access.token_fallback=false 关掉走 Access-only。
//
// 两道都关掉时外网一律 401（deny-by-default），不会出现"裸奔"。
//
// 历史上 external 曾要求 X-Feishu-Openid 身份，2026-08 起身份判定已移出
// HTTP 层（现在只用于 IM 隧道命令，见 core.Bridge.handleTunnelCommand）。
func (s *Service) ExternalAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.Debug != nil && s.Debug.BypassAll() {
			s.audit(c, "biz.api", AuditEvent{TokenOK: true, IdentityOK: true, Debug: true, AuthMethod: "debug"})
			c.Next()
			return
		}
		if IsInternalRequest(c.Request) {
			s.audit(c, "biz.api", AuditEvent{TokenOK: true, IdentityOK: true, AuthMethod: "internal"})
			c.Next()
			return
		}
		ip := ClientIP(c.Request)
		if s.Limiter != nil && !s.Limiter.Allow(ip) {
			s.audit(c, "biz.api.blacklisted", AuditEvent{AuthMethod: "none"})
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "ip blacklisted"})
			return
		}

		// ① Cloudflare Access（主通道）：验签通过即放行。
		if s.Access.Enabled() {
			if id, err := s.Access.Verify(c.Request); err == nil {
				c.Set(accessEmailContextKey, id.Email)
				// 这里不需要像 token 通道那样补种同源 cookie：Access 的
				// JWT 是 Cloudflare 对**该应用的每个请求**注入的头（会话
				// 由 CF 侧的 CF_Authorization cookie 维持），预览子资源
				// 请求同样会带上，故不存在"子资源空白页"问题。
				s.audit(c, "biz.api", AuditEvent{TokenOK: true, IdentityOK: true,
					AuthMethod: "access", AccessEmail: id.Email})
				c.Next()
				return
			}
			// 验签失败不立即拒绝：可能只是没走 Access（直连域名），
			// 交给下面的 token 通道兜底。失败原因只记 debug，且不含 token。
			if s.Audit != nil {
				s.Audit.Debug(c.Request, "biz.api.access_fail")
			}
		}

		// ② 外链 token（兜底）：可被显式关闭。
		if !s.TokenDisabled {
			tok := extractToken(c)
			if s.Tokens != nil && s.Tokens.Validate(tok) {
				// 种下同源 cookie：预览外链文档请求带 ?token= 通过后，让后续无法携带
				// header/query 的 preview 子资源请求凭 cookie 鉴权（否则外链空白页）。
				// Secure+HttpOnly；token 在进程内，cookie 随隧道 token 失效而失效。
				c.SetCookie(tunnelTokenCookie, tok, 0, "/", "", true, true)
				s.audit(c, "biz.api", AuditEvent{TokenOK: true, IdentityOK: true, AuthMethod: "token"})
				c.Next()
				return
			}
			// 只对"明显乱猜的错误 token"计入暴力破解限流：空 token（没带）与
			// 格式正确的过期/已失效 token（用户只是不知道过期了，重启/换隧道
			// 后常见）都不拉黑，避免合法用户被误锁 10 分钟。
			if s.Limiter != nil && tok != "" && !looksLikeToken(tok) {
				s.Limiter.NoteFailure(ip)
			}
		}

		s.audit(c, "biz.api.token_fail", AuditEvent{AuthMethod: "none"})
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
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
			s.audit(c, "tunnel.op", AuditEvent{TokenOK: true, IdentityOK: true, Debug: true, AuthMethod: "debug"})
			c.Next()
			return
		}
		if IsInternalRequest(c.Request) {
			s.audit(c, "tunnel.op.internal_blocked", AuditEvent{AuthMethod: "internal"})
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tunnel ops forbidden from internal network"})
			return
		}
		ua := c.GetHeader("User-Agent")
		if !isLarkMobileUA(ua) {
			s.audit(c, "tunnel.op.non_mobile_blocked", AuditEvent{AuthMethod: "none"})
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tunnel ops only allowed from Feishu mobile"})
			return
		}
		s.audit(c, "tunnel.op", AuditEvent{TokenOK: true, IdentityOK: true, AuthMethod: "external-lark-mobile"})
		c.Next()
	}
}

// BindOpGateMiddleware enforces PRD §3.2.4 + §5.3: binding/unbinding is
// internal-IP-only. External requests always rejected (even with valid
// token + identity — binding is a privileged local op).
func (s *Service) BindOpGateMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.Debug != nil && s.Debug.BypassAll() {
			s.audit(c, "auth.bind", AuditEvent{TokenOK: true, IdentityOK: true, Debug: true, AuthMethod: "debug"})
			c.Next()
			return
		}
		if !IsInternalRequest(c.Request) {
			s.audit(c, "auth.bind.external_blocked", AuditEvent{AuthMethod: "external"})
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "binding only allowed from internal network"})
			return
		}
		s.audit(c, "auth.bind", AuditEvent{TokenOK: true, IdentityOK: true, AuthMethod: "internal"})
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

// audit 是 nil-safe 包装：Ev.Op 由 op 参数统一填充，避免调用点重复写。
func (s *Service) audit(c *gin.Context, op string, ev AuditEvent) {
	if s.Audit == nil {
		return
	}
	ev.Op = op
	s.Audit.Log(c.Request, ev)
}
