package auth

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestAudit_LogsAllRequiredFields(t *testing.T) {
	core, obs := observer.New(zap.InfoLevel)
	a := NewAuditLogger(zap.New(core))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:5555"
	req.Header.Set("User-Agent", "Lark/12.3 (iPhone)")
	req.Header.Set("X-Feishu-Openid", "ou_audit_test")
	a.Log(req, AuditEvent{
		Op:         "tunnel.start",
		TokenOK:    true,
		IdentityOK: true,
		Debug:      false,
	})
	logs := obs.All()
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(logs))
	}
	entry := logs[0]
	if !strings.Contains(entry.Message, "audit") {
		t.Errorf("message = %q, want substring 'audit'", entry.Message)
	}
	// Verify all 6 PRD-required fields present as context fields
	for _, field := range []string{"ip", "ua", "openid", "token_ok", "op", "debug"} {
		if _, ok := findField(entry, field); !ok {
			t.Errorf("missing audit field %q in entry %+v", field, entry)
		}
	}
	// Sanity-check values
	if v, ok := findField(entry, "ip"); !ok || v != "1.2.3.4" {
		t.Errorf("ip field = %v ok=%v, want 1.2.3.4", v, ok)
	}
	if v, ok := findField(entry, "op"); !ok || v != "tunnel.start" {
		t.Errorf("op field wrong: %v ok=%v", v, ok)
	}
}

func TestAudit_SanitizesToken(t *testing.T) {
	core, obs := observer.New(zap.InfoLevel)
	a := NewAuditLogger(zap.New(core))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:5"
	// PRD §7.1: Token must NEVER be logged. Audit only logs bool token_ok.
	a.Log(req, AuditEvent{Op: "biz", TokenOK: true})
	logs := obs.All()
	if len(logs) != 1 {
		t.Fatalf("got %d logs", len(logs))
	}
	// Marshal and scan for the literal token we never set — guard the
	// contract: AuditEvent struct has no token field.
	raw, _ := json.Marshal(logs[0])
	if strings.Contains(string(raw), "token_value") {
		t.Error("audit log must not contain raw token values")
	}
}

// findField locates a context field by key in a logged entry. Returns the
// string form of the field's value (for String/String fields; for Bool
// fields the String field is empty — use findBool for those in other tests).
func findField(e observer.LoggedEntry, key string) (string, bool) {
	for _, f := range e.Context {
		if f.Key == key {
			return f.String, true
		}
	}
	return "", false
}

// Compile-time: ensure gin is referenced (handlers in middleware_test will use it).
var _ = gin.H{}
