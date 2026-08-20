package auth

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// matrixCase is one row of PRD §5 permission matrix.
type matrixCase struct {
	name       string
	ip         string
	ua         string
	openid     string
	tokenSetup func(*TokenStore) string
	wantStatus int
}

// TestPermissionMatrix walks every external-access row for business + tunnel
// + bind endpoints and asserts the documented status code.
//
// Auth policy (2026-08): external business access = token single-factor
// (no X-Feishu-Openid identity check at HTTP layer). Tunnel ops = Lark
// mobile external only. Bind ops = internal only. Debug ON bypasses all.
//
// Each case is routed to the middleware that owns its section:
//   - "biz/..." rows and the debug-on rows → ExternalAuthMiddleware
//   - "tunnel/..." rows → TunnelOpGateMiddleware
//   - "bind/..." rows → BindOpGateMiddleware
func TestPermissionMatrix(t *testing.T) {
	cases := []matrixCase{
		// --- Debug ON: everything 200 ---
		{"debug-on/external/no-creds", "8.8.8.8", "curl", "",
			func(ts *TokenStore) string { return "" }, 200},
		{"debug-on/internal", "192.168.1.1", "", "",
			func(ts *TokenStore) string { return "" }, 200},

		// --- Debug OFF, business API (token single-factor) ---
		{"biz/internal/no-creds", "192.168.1.1", "", "",
			func(ts *TokenStore) string { return "" }, 200},
		{"biz/external/lark-mobile/valid", "4.4.4.4", "Lark/12 (iPhone)", "ou_admin",
			func(ts *TokenStore) string { tok, _ := ts.Issue(time.Minute); return tok }, 200},
		{"biz/external/pc-feishu/valid", "4.4.4.4", "Mozilla Chrome", "ou_admin",
			func(ts *TokenStore) string { tok, _ := ts.Issue(time.Minute); return tok }, 200},
		{"biz/external/plain-browser/valid-no-openid", "4.4.4.4", "Mozilla Chrome", "",
			func(ts *TokenStore) string { tok, _ := ts.Issue(time.Minute); return tok }, 200},
		{"biz/external/valid-token-openid-ignored", "4.4.4.4", "Lark", "ou_other",
			func(ts *TokenStore) string { tok, _ := ts.Issue(time.Minute); return tok }, 200},
		{"biz/external/no-token", "4.4.4.4", "Lark", "ou_admin",
			func(ts *TokenStore) string { return "" }, 401},
		{"biz/external/plain-browser-no-creds", "4.4.4.4", "curl", "",
			func(ts *TokenStore) string { return "" }, 401},

		// --- Tunnel ops (Lark mobile external only) ---
		{"tunnel/internal-blocked", "192.168.1.1", "Lark", "",
			func(ts *TokenStore) string { return "" }, 403},
		{"tunnel/pc-blocked", "4.4.4.4", "Mozilla Chrome", "",
			func(ts *TokenStore) string { return "" }, 403},
		{"tunnel/lark-mobile-allowed", "4.4.4.4", "Lark/12 (iPhone)", "",
			func(ts *TokenStore) string { return "" }, 200},

		// --- Bind ops: internal only ---
		{"bind/internal-allowed", "192.168.1.1", "", "",
			func(ts *TokenStore) string { return "" }, 200},
		{"bind/external-blocked", "4.4.4.4", "Lark", "ou_admin",
			func(ts *TokenStore) string { return "" }, 403},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bindings, _ := NewBindingStore(filepath.Join(t.TempDir(), "b.json"))
			_, _ = bindings.Bind(Binding{OpenID: "ou_admin"})
			tokens := NewTokenStore()
			svc := &Service{
				Debug:    NewDebugSwitch(false),
				Bindings: bindings,
				Tokens:   tokens,
				Limiter:  NewIPLimiter(5, 10*time.Minute),
			}
			gin.SetMode(gin.TestMode)
			g := gin.New()
			// Route each row through the gate that owns its PRD §5
			// section. The case data (ip/ua/openid/token/wantStatus) is
			// unchanged; only the gate is selected by section prefix.
			switch {
			case strings.HasPrefix(tc.name, "tunnel/"):
				g.Use(svc.TunnelOpGateMiddleware())
			case strings.HasPrefix(tc.name, "bind/"):
				g.Use(svc.BindOpGateMiddleware())
			default: // "biz/..." and "debug-on/..."
				g.Use(svc.ExternalAuthMiddleware())
			}
			g.GET("/biz", func(c *gin.Context) { c.String(200, "ok") })

			req := httptest.NewRequest("GET", "/biz", nil)
			req.RemoteAddr = tc.ip + ":1234"
			req.Header.Set("User-Agent", tc.ua)
			if tc.openid != "" {
				req.Header.Set("X-Feishu-Openid", tc.openid)
			}
			tok := tc.tokenSetup(tokens)
			if tok != "" {
				q := req.URL.Query()
				q.Set("token", tok)
				req.URL.RawQuery = q.Encode()
			}
			// Toggle debug ON for the debug-on cases
			if strings.HasPrefix(tc.name, "debug-on") {
				svc.Debug.Set(true)
			}

			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("%s: got %d, want %d (body=%s)", tc.name, w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

// TestDebugSwitchTransition_TrueToFalseClearsTokens verifies PRD §4.4:
// "Debug模式从开启切换为关闭" must invalidate all tunnel tokens.
func TestDebugSwitchTransition_TrueToFalseClearsTokens(t *testing.T) {
	dbg := NewDebugSwitch(true)
	tokens := NewTokenStore()
	tok, _ := tokens.Issue(time.Hour)
	if !tokens.Validate(tok) {
		t.Fatal("token valid while debug ON")
	}
	// Service hooks the transition: when debug flips false, also wipe tokens.
	// Here we simulate the wiring (main.go is responsible for calling this
	// when the flag is toggled at runtime — for now we test the helper).
	dbg.Set(false)
	// The Service-level helper is what main.go calls:
	wipeTokensOnDebugOff(dbg, tokens)
	if tokens.Validate(tok) {
		t.Fatal("token must be cleared when debug flips ON→OFF")
	}
}
