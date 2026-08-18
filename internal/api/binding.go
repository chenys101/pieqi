package api

import (
	"net/http"

	"pieqi/internal/auth"

	"github.com/gin-gonic/gin"
)

type bindReq struct {
	OpenID   string `json:"openid" binding:"required"`
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
}

// bind handles POST /api/auth/bind (internal-only, gated by BindOpGateMiddleware).
func (s *Server) bind(c *gin.Context) {
	var req bindReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "openid is required"})
		return
	}
	b, err := s.auth.Bindings.Bind(auth.Binding{
		OpenID:   req.OpenID,
		UserID:   req.UserID,
		Nickname: req.Nickname,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"openid":   b.OpenID,
		"user_id":  b.UserID,
		"nickname": b.Nickname,
		"bound_at": b.BoundAt,
		"active":   b.Active,
	})
}

// unbind handles DELETE /api/auth/bind (internal-only).
func (s *Server) unbind(c *gin.Context) {
	if err := s.auth.Bindings.Unbind(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unbound"})
}

// authStatus handles GET /api/auth/status — public (no auth gate).
// Front-end polls this on boot to know if binding is required + debug state.
func (s *Server) authStatus(c *gin.Context) {
	resp := gin.H{
		"bound": false,
		"debug": s.auth.Debug.BypassAll(),
	}
	if b, ok := s.auth.Bindings.Get(); ok {
		resp["bound"] = true
		resp["openid"] = b.OpenID
		resp["nickname"] = b.Nickname
		resp["bound_at"] = b.BoundAt
	}
	c.JSON(http.StatusOK, resp)
}
