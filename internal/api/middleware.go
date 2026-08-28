package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// authMiddleware 校验 Bearer token；token 空则放行（仅本地 dev）。
func authMiddleware(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// corsMiddleware 本地 dev 放行所有来源。
func corsMiddleware(allowAll bool, origins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allow := false
		if allowAll || origin == "" {
			allow = true
		} else {
			for _, o := range origins {
				if o == origin || o == "*" {
					allow = true
					break
				}
			}
		}
		if allow {
			c.Header("Access-Control-Allow-Origin", func() string {
				if origin == "" {
					return "*"
				}
				return origin
			}())
			c.Header("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Feishu-Openid")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
