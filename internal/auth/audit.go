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
	fields := []zap.Field{
		zap.String("op", ev.Op),
		zap.String("ip", ClientIP(r)),
		zap.String("ua", r.Header.Get("User-Agent")),
		zap.String("openid", r.Header.Get("X-Feishu-Openid")),
		zap.Bool("token_ok", ev.TokenOK),
		zap.Bool("identity_ok", ev.IdentityOK),
		zap.Bool("debug", ev.Debug),
	}
	a.log.Info("audit", fields...)
}
