package main

// web.go 通过 embed.FS 把 PWA 前端（web/dist）嵌入二进制，单文件自包含。
// 嵌入由 web/embed.go 完成（路径相对 web/ 包）。

import (
	"io/fs"
	"net/http"
	"strings"

	"pieqi/web"

	"github.com/gin-gonic/gin"
)

// registerStatic 在 gin router 上挂 PWA 静态资源 + SPA fallback。
func registerStatic(r *gin.Engine) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(dist))
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// API / webhook / internal 路径返回 404 JSON，不回退 SPA
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/internal/") || strings.HasPrefix(path, "/webhook/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// 尝试静态文件
		if path != "/" {
			if f, err := dist.Open(strings.TrimPrefix(path, "/")); err == nil {
				f.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}

		// SPA fallback: index.html
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}
