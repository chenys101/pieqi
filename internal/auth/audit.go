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
	// ip_source 记录「IP 是依据哪个头判定的」（cf / xff / peer / none）——
	// 内网判定建立在 ClientIP 之上，审计必须能回答"这次为什么被判成内网"，
	// 否则安全判定不可回溯（见 auth.go IPSource）。
	ip, ipSource := IPSource(r)
	fields := []zap.Field{
		zap.String("op", ev.Op),
		zap.String("ip", ip),
		zap.String("ip_source", ipSource),
		zap.String("ua", r.Header.Get("User-Agent")),
		zap.String("openid", r.Header.Get("X-Feishu-Openid")),
		zap.Bool("token_ok", ev.TokenOK),
		zap.Bool("identity_ok", ev.IdentityOK),
		zap.Bool("debug", ev.Debug),
	}
	a.log.Info("audit", fields...)
}
