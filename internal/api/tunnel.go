package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
)

type tunnelStartReq struct {
	TTL string `json:"ttl"` // "15m" | "1h" | "4h"; empty = default
}

// ttlFromString maps user input to a duration. Defaults to 15m.
func ttlFromString(s string) time.Duration {
	switch strings.TrimSpace(s) {
	case "1h", "60m":
		return time.Hour
	case "4h":
		return 4 * time.Hour
	default:
		return 15 * time.Minute
	}
}

// tunnelStart handles POST /api/tunnel/start (Lark-mobile-only, gated by TunnelOpGateMiddleware).
func (s *Server) tunnelStart(c *gin.Context) {
	if s.tunnel == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "tunnel manager not configured"})
		return
	}
	var req tunnelStartReq
	_ = c.ShouldBindJSON(&req) // optional body
	res, err := s.tunnel.Start(c.Request.Context(), ttlFromString(req.TTL))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tunnel_url":     res.TunnelURL,
		"lark_deep_link": res.LarkDeepLink,
		"token":          res.Token,
		"expires_at":     res.ExpiresAt,
	})
}

// tunnelStop handles POST /api/tunnel/stop (Lark-mobile-only).
func (s *Server) tunnelStop(c *gin.Context) {
	if s.tunnel == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "tunnel manager not configured"})
		return
	}
	if err := s.tunnel.Stop(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// tunnelReset handles POST /api/tunnel/reset (Lark-mobile-only).
// Issues a new token for the running tunnel, killing the old one.
func (s *Server) tunnelReset(c *gin.Context) {
	if s.tunnel == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "tunnel manager not configured"})
		return
	}
	tok, err := s.tunnel.ResetToken(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tok})
}

// tunnelStatus handles GET /api/tunnel/status — public (read-only, no token leak).
func (s *Server) tunnelStatus(c *gin.Context) {
	if s.tunnel == nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}
	st := s.tunnel.Status()
	c.JSON(http.StatusOK, st)
}

// tunnelQRCode handles GET /api/tunnel/qrcode?text=<...> — returns a PNG
// QR of the given text. Used by the front-end to render the lark:// deep
// link as a scannable code. Read-only (no token leak beyond what the
// caller already has — the URL passed in is decided by the frontend).
func (s *Server) tunnelQRCode(c *gin.Context) {
	text := c.Query("text")
	if text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text is required"})
		return
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "image/png", png)
}
