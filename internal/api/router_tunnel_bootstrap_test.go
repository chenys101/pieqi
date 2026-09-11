package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestTunnelStart_RequiresToken 守着 D3-c 定案（IMPLEMENTATION-PLAN §4.2.1）：
// **匿名 bootstrap 已关闭**，/api/tunnel/start 与 /stop /reset /renew 同组 ——
// 需要 UA 门（仅飞书移动端 + 外网）**加** ExternalAuth（有效 token）。
//
// 这条断言在 2026-09-11 被**显式反转**。它此前断言的是相反的结论：
//
//	"a Lark-mobile external POST /api/tunnel/start with NO Authorization
//	 header and NO ?token= query returns 200 (the handler mints the token)"
//
// 那个结论建立在「首启还没有 token，要求 token 会死锁」这个前提上。前提是错的：
// **IM 命令才是 bootstrap** —— Bridge.handleTunnelCommand 直接调
// TunnelManager.Start()，根本不经过 HTTP。于是"只挂 UA 门"实际得到的是一个
// 任何人声明自己是飞书移动端就能自取**管理员代理凭据**的入口，而 token 的
// 可信度完全依赖"只经 IM 交付给管理员"这条有 adminBinding.Match 把关的通道。
//
// 设计变了，断言跟着改（而不是让它失效）—— 见 SPEC §6.3。
//
// 本测试通过 Server.Register 走**完整生产路由**（与 router.go 同一套分组），
// 断言三件：
//   - 飞书移动端 + 外网 + **无 token** → 401（曾经的 200 正是那个洞）；
//   - 飞书移动端 + 外网 + **有效 token** → 200（功能无损：已有凭据仍可重启 / 续期）；
//   - 非飞书 UA → 403（UA 门本身没被削弱）。
func TestTunnelStart_RequiresToken(t *testing.T) {
	// Case 1: Lark-mobile external request WITHOUT a token → 401.
	// (This is the inverted assertion: it used to be 200.)
	t.Run("lark_mobile_no_token_401", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		srv, _ := newServerWithTunnel(t)
		r := gin.New()
		srv.Register(r) // real production wiring

		req, _ := http.NewRequest("POST", "/api/tunnel/start", nil)
		req.Header.Set("User-Agent", "Lark/12 (iPhone)")
		req.RemoteAddr = "4.4.4.4:1234" // external
		// Deliberately NO Authorization header and NO ?token= query.
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous lark-mobile /start must be rejected → 401, got %d body=%s",
				w.Code, w.Body.String())
		}
	})

	// Case 2: Lark-mobile external request WITH a valid token → 200.
	t.Run("lark_mobile_with_token_200", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		srv, _ := newServerWithTunnel(t)
		tok, err := srv.auth.Tokens.Issue(time.Hour)
		if err != nil {
			t.Fatalf("issue token: %v", err)
		}
		r := gin.New()
		srv.Register(r)

		req, _ := http.NewRequest("POST", "/api/tunnel/start?token="+tok, nil)
		req.Header.Set("User-Agent", "Lark/12 (iPhone)")
		req.RemoteAddr = "4.4.4.4:1234" // external
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("token-bearing lark-mobile /start → 200, got %d body=%s", w.Code, w.Body.String())
		}
		// Cleanup the (fake) cloudflared process the handler spawned.
		_ = srv.tunnel.Stop(context.Background())
	})

	// Case 3: non-Lark UA → 403 (the UA gate is untouched; rejected before auth).
	t.Run("non_lark_ua_403", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		srv, _ := newServerWithTunnel(t)
		// Even WITH a valid token, a PC browser is rejected by the UA gate.
		tok, _ := srv.auth.Tokens.Issue(time.Hour)
		r := gin.New()
		srv.Register(r)

		req, _ := http.NewRequest("POST", "/api/tunnel/start?token="+tok, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 Chrome")
		req.RemoteAddr = "4.4.4.4:1234" // external
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("non-Lark UA /start → 403, got %d body=%s", w.Code, w.Body.String())
		}
	})
}
