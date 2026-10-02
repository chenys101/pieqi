package auth

import (
	"net/http"

	"go.uber.org/zap"
)

// AuditEvent is the per-request audit payload. PRD §7.3 requires:
// IP, UA, OpenID, token result, op type, debug state.
// Token VALUE is intentionally absent — never logged (PRD §7.1).
type AuditEvent struct {
	Op         string // e.g. "biz.api", "tunnel.start", "auth.bind"
	TokenOK    bool
	IdentityOK bool
	Debug      bool
	// AuthMethod 说明本次判定依据的通道：debug / internal / access / token /
	// external / none。与 ip_source 搭配，可完整回溯"这次为什么放行/拒绝"。
	AuthMethod string
	// AccessEmail 经 Cloudflare Access 验签后的身份邮箱（未走 Access 时为空）。
	// 这是授权主体，必须可审计 —— 否则"谁进的"无从追查。
	AccessEmail string
}

// AuditLogger emits structured audit records via zap. Designed to be
// readable as JSON in production logs.
type AuditLogger struct {
	log *zap.Logger
}

// NewAuditLogger wraps an existing zap logger.
func NewAuditLogger(log *zap.Logger) *AuditLogger {
	return &AuditLogger{log: log}
}

// Log emits one audit record. Extracts IP and UA from the request and
// OpenID from the X-Feishu-Openid header.
func (a *AuditLogger) Log(r *http.Request, ev AuditEvent) {
	if a == nil || a.log == nil {
		return
	}
	a.log.Info("audit", a.fields(r, ev)...)
}

// Debug 记录一条**判定失败**的明细，用于排查"为什么这次被拒"。
// 与 Log 分开：失败明细量大且通常只是"没带凭据"，不该污染 Info 级审计流。
// 同样只记策略与原因，绝不记凭据值。
func (a *AuditLogger) Debug(r *http.Request, op string) {
	if a == nil || a.log == nil {
		return
	}
	ip, ipSource := IPSource(r)
	a.log.Debug("audit_reject",
		zap.String("op", op),
		zap.String("ip", ip),
		zap.String("ip_source", ipSource),
		zap.String("ua", r.Header.Get("User-Agent")),
		zap.Bool("has_access_jwt", r.Header.Get(accessJWTHeader) != ""),
		zap.Bool("has_token_query", r.URL.Query().Get("token") != ""),
	)
}

func (a *AuditLogger) fields(r *http.Request, ev AuditEvent) []zap.Field {
	// ip_source 记录「IP 是依据哪个头判定的」（cf / xff / peer / none）——
	// 内网判定建立在 ClientIP 之上，审计必须能回答"这次为什么被判成内网"，
	// 否则安全判定不可回溯（见 auth.go IPSource）。
	ip, ipSource := IPSource(r)
	return []zap.Field{
		zap.String("op", ev.Op),
		zap.String("ip", ip),
		zap.String("ip_source", ipSource),
		zap.String("auth_method", ev.AuthMethod),
		zap.String("access_email", ev.AccessEmail),
		zap.String("ua", r.Header.Get("User-Agent")),
		zap.String("openid", r.Header.Get("X-Feishu-Openid")),
		zap.Bool("token_ok", ev.TokenOK),
		zap.Bool("identity_ok", ev.IdentityOK),
		zap.Bool("debug", ev.Debug),
	}
}
