package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestTunnelStart_NoTokenRequiredForLarkMobile verifies the PRD §5.2 +
// §4.4 bootstrap fix: /api/tunnel/start is the token-issuance trigger, so
// it MUST be gated ONLY by TunnelOpGateMiddleware (Lark-mobile + external).
// Requiring ExternalAuthMiddleware (token check) on /start would deadlock
// because no tunnel token can exist before the first tunnel starts.
//
// This test wires the FULL production router via Server.Register (same
// split-wiring as router.go) and asserts:
//   - a Lark-mobile external POST /api/tunnel/start with NO Authorization
//     header and NO ?token= query returns 200 (the handler mints the token);
//   - the same request with a non-Lark UA returns 403 (TunnelOpGate rejects).
//
// If someone re-adds ExternalAuthMiddleware to the /start group, the first
// case flips to 401 (no token) — exactly the deadlock this test guards
// against.
func TestTunnelStart_NoTokenRequiredForLarkMobile(t *testing.T) {
	// Case 1: Lark-mobile external request with no token → 200.
	t.Run("lark_mobile_no_token_200", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		srv, _ := newServerWithTunnel(t)
		r := gin.New()
		srv.Register(r) // real production wiring (split tunnel groups)

		req, _ := http.NewRequest("POST", "/api/tunnel/start", nil)
		req.Header.Set("User-Agent", "Lark/12 (iPhone)")
		req.RemoteAddr = "4.4.4.4:1234" // external
		// Deliberately NO Authorization header and NO ?token= query.
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("lark-mobile /start with no token → 200, got %d body=%s", w.Code, w.Body.String())
		}
		// Cleanup the (fake) cloudflared process the handler spawned.
		_ = srv.tunnel.Stop(context.Background())
	})

	// Case 2: non-Lark UA → 403 (gate rejects before handler runs).
	t.Run("non_lark_ua_403", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		srv, _ := newServerWithTunnel(t)
		r := gin.New()
		srv.Register(r)

		req, _ := http.NewRequest("POST", "/api/tunnel/start", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 Chrome")
		req.RemoteAddr = "4.4.4.4:1234" // external
		// NO Authorization header and NO ?token= query.
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("non-Lark UA /start → 403, got %d body=%s", w.Code, w.Body.String())
		}
	})
}
