// preview.go Preview 控制端点与反向代理（p0-design.md §7.3）。
//
//	POST /api/tasks/:id/preview/start    启动（discovery → spawn → running）
//	POST /api/tasks/:id/preview/stop      停止（SIGTERM→KILL）
//	GET  /api/tasks/:id/preview/status    状态轮询（P0 不推流）
//	ANY  /api/tasks/:id/preview/*path     反向代理（继承 ExternalAuthMiddleware 隧道鉴权）
//
// 安全设计：
//   - 代理目标端口只来自 PreviewManager 注册表（该 taskID 绑定的 127.0.0.1 端口），
//     绝不接受客户端传入的端口/URL —— 防任意端口代理
//   - HTML 响应注入 <base href> 并重写根绝对资源路径，让子路径形态可用（vite HMR WS 一并转发）
package api

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// previewRoute ANY /api/tasks/:id/preview/*path：preview 路由统一入口。
// gin 的 catch-all 与同前缀静态路由冲突，无法同时注册 /preview/start 与 /preview/*path，
// 故控制端点（start/stop/status）在 wildcard 路由内按「末段 + HTTP 方法」分发，
// 其余路径走反向代理。
func (s *Server) previewRoute(c *gin.Context) {
	// c.Param("path") 形如 "/start"（含前导斜杠）
	switch strings.TrimPrefix(c.Param("path"), "/") {
	case "start":
		if c.Request.Method == http.MethodPost {
			s.startPreview(c)
			return
		}
	case "stop":
		if c.Request.Method == http.MethodPost {
			s.stopPreview(c)
			return
		}
	case "status":
		if c.Request.Method == http.MethodGet {
			s.previewStatus(c)
			return
		}
	default:
		s.previewProxy(c)
		return
	}
	// 控制端点存在但方法不匹配 → 405（避免误把控制路径代理给 dev server）
	c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
}

// startPreview POST /api/tasks/:id/preview/start。
func (s *Server) startPreview(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.preview == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview not enabled"})
		return
	}
	if task.Status == model.TaskRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "Agent 执行中，暂不可启动预览"})
		return
	}
	if err := s.preview.Start(task); err != nil {
		// discovery 失败 → unavailable（不猜）
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "state": core.PreviewUnavailable})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "state": core.PreviewStarting})
}

// stopPreview POST /api/tasks/:id/preview/stop。
func (s *Server) stopPreview(c *gin.Context) {
	if _, ok := s.requireTask(c); !ok {
		return
	}
	if s.preview == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview not enabled"})
		return
	}
	s.preview.Stop(c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"ok": true, "state": core.PreviewStopped})
}

// previewStatus GET /api/tasks/:id/preview/status。
func (s *Server) previewStatus(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.preview == nil {
		c.JSON(http.StatusOK, core.PreviewStatus{State: core.PreviewUnavailable})
		return
	}
	c.JSON(http.StatusOK, s.preview.Status(task))
}

// previewProxy ANY /api/tasks/:id/preview/*path：反向代理到该 task 绑定的 127.0.0.1 端口。
func (s *Server) previewProxy(c *gin.Context) {
	if s.preview == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview not enabled"})
		return
	}
	taskID := c.Param("id")
	port := s.preview.RunningPort(taskID)
	if port == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview not running"})
		return
	}

	// 剥掉 /api/tasks/:id/preview 前缀，余下路径原样转发（默认 "/" → dev server 根）
	prefix := "/api/tasks/" + taskID + "/preview"
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", itoa(port))}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			// 相对路径重建：c.Request.URL.Path 此时是完整路径
			rest := strings.TrimPrefix(req.URL.Path, prefix)
			if rest == "" || !strings.HasPrefix(rest, "/") {
				rest = "/" + rest
			}
			req.URL.Path = rest
			req.Host = "localhost" // dev server 常按 Host 判断，避免 127.0.0.1 导致重定向异常
		},
		ModifyResponse: rewritePreviewHTML(prefix),
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

// absoluteRootAssetRe 匹配 HTML 里根绝对资源引用（src/href="/@vite/client" 等）。
var absoluteRootAssetRe = regexp.MustCompile(`((?:src|href)=["'])/((?:@|assets|node_modules|\.|_next|_nuxt|__next)[^"']*)["']`)

// rewritePreviewHTML HTML 重写中间件（p0-design.md §7.3）：
// 注入 <base href="/api/tasks/:id/preview/">，并把根绝对 src/href 改写成子路径前缀。
// 已知限制：客户端路由写死根绝对路径的复杂 SPA 可能失效（P0 接受）。
func rewritePreviewHTML(prefix string) func(*http.Response) error {
	return func(resp *http.Response) error {
		if resp == nil || resp.Body == nil {
			return nil
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			return nil
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()

		base := "<base href=\"" + prefix + "/\">"
		rewritten := absoluteRootAssetRe.ReplaceAll(data, []byte("${1}"+prefix+"/${2}\""))

		// base 标签注入在 <head> 之后（有 head 时），否则拼在开头
		out := bytes.Buffer{}
		if i := bytes.Index(lower(rewritten), []byte("<head")); i >= 0 {
			end := bytes.IndexByte(rewritten[i:], '>')
			out.Write(rewritten[:i+end+1])
			out.WriteString(base)
			out.Write(rewritten[i+end+1:])
		} else {
			out.WriteString(base)
			out.Write(rewritten)
		}

		resp.Body = io.NopCloser(bytes.NewReader(out.Bytes()))
		resp.Header.Set("Content-Length", itoa(out.Len()))
		resp.Header.Del("Content-Encoding") // 重写后内容已解压，防止 gzip 长度/编码不匹配
		return nil
	}
}

// itoa 局部小工具。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// lower ASCII 小写（避免引入 strings.ToLower 的分配开销；HTML 标签 ASCII）。
func lower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}
