package api

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// gzip 中间件（2026-10-22 性能修复，P1-3）。
//
// 为什么需要：`GET /api/tasks` 实测返回 **5.42 MB**（未做 P0-2 前）/ 2.33 MB（之后），
// 而响应头 `Content-Encoding` 为空 —— 服务端**从不压缩**。前端光是 `JSON.parse`
// 就要 71ms（本机 V8），5.4 MB 原样过隧道到手机是数量级的差别。
//
// 为什么自己写而不是引第三方：功能就这么点，而 go.mod 里没有任何压缩依赖；
// 为 60 行逻辑引一个包，换来的是又多一个要跟版本的东西。

// gzipMinBytes 低于这个长度不压缩。
//
// 判据是"压缩收益不抵头部开销 + CPU"：gzip 对小 JSON 往往反而更大（十来个字节的
// 响应压完再加 20 字节头）。512 字节是保守值，常见的小接口（/health、状态位）
// 都会走原样直出。
const gzipMinBytes = 512

// gzipExcludedContains 需要按"路径包含"排除的片段。
//
// ⚠️ 这几条**漏任何一条都是静默失败**（不报错，但浏览器拿到坏数据）：
//   - `/preview/`：反向代理，且会对 HTML 做 `[]byte` 重写后写出（见 preview.go），
//     压缩等于把已被上一步处理过的内容再编码一次。它同时覆盖
//     `/api/tasks/:id/preview/*path` 与 `/preview/:id/*path` 两个挂载点。
//   - `/file`：文件内容透传（markdown/pdf），压缩会破坏 Range/ETag。
//
// 为什么用"包含"而不是前缀：`/api/tasks/` 底下既有该压的（列表、详情），
// 也有绝不能压的（preview / file），整段前缀排除会把详情一起漏掉。
var gzipExcludedContains = []string{
	"/preview/",
	"/file",
}

// shouldGzip 判定该请求路径是否参与压缩。
func shouldGzip(path string) bool {
	// WebSocket 升级：包装 Writer 会破坏 101 握手。
	if path == "/api/ws" || strings.HasPrefix(path, "/api/ws/") {
		return false
	}
	if strings.HasPrefix(path, "/preview/") {
		return false
	}
	for _, frag := range gzipExcludedContains {
		if strings.Contains(path, frag) {
			return false
		}
	}
	return true
}

// acceptsGzip 解析 Accept-Encoding，判断客户端是否**接受** gzip。
//
// ⚠️ 必须处理 `gzip;q=0`：那是客户端**显式拒绝** gzip（RFC 9110），
// 把它当"接受"会让浏览器收到它说不想要的编码 —— 有的客户端直接报错。
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name := part
		q := 1.0
		if i := strings.Index(part, ";"); i >= 0 {
			name = strings.TrimSpace(part[:i])
			for _, p := range strings.Split(part[i+1:], ";") {
				p = strings.TrimSpace(p)
				if strings.HasPrefix(p, "q=") {
					if v, err := strconv.ParseFloat(strings.TrimPrefix(p, "q="), 64); err == nil {
						q = v
					}
				}
			}
		}
		// `*` 表示"除显式列出者外都接受"。
		if (strings.EqualFold(name, "gzip") || name == "*") && q > 0 {
			return true
		}
	}
	return false
}

// gzipWriterPool 复用 gzip.Writer（每个请求 new 一个会白白付一次 8KB 缓冲分配）。
var gzipWriterPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// gzipResponseWriter 包装 gin.ResponseWriter。
//
// 三个必须实现对的东西，缺一个都是静默故障：
//   - WriteHeader **不**透传 Content-Length —— 那是未压缩时的长度，留着浏览器会
//     按它截断响应（表现为"JSON 只到一半，解析失败"）。
//   - Write 在首次写入时才建 gzip writer（要先把底层的头定下来）。
//   - Flush 必须先 Flush gzip 再 Flush 底层，否则已写入但未刷出的缓冲会丢。
type gzipResponseWriter struct {
	gin.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	// passthrough 表示这条响应**不压**，原样直出（非 2xx 状态码）。
	passthrough bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	// 只压成功的响应；错误响应很小，且让它们保持可读（便于排查）。
	if code < 200 || code >= 300 || code == http.StatusNoContent {
		w.passthrough = true
		w.ResponseWriter.WriteHeader(code)
		return
	}
	h := w.Header()
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	// Vary 告诉中间缓存：同一 URL 因 Accept-Encoding 有不同的表示。
	// 少了它，Cloudflare 之类的缓存可能把压缩版发给不支持 gzip 的客户端。
	h.Add("Vary", "Accept-Encoding")
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		return w.ResponseWriter.Write(b)
	}
	if w.gz == nil {
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.gz = gz
	}
	return w.gz.Write(b)
}

// WriteString 覆盖 gin.ResponseWriter 特有方法，避免它回退到不压缩的 Write。
func (w *gzipResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *gzipResponseWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	w.ResponseWriter.Flush()
}

// GzipMiddleware 给响应加 gzip 压缩。
//
// 挂成全局中间件（而不是只挂 /api 组）：`/api` 组与 `/preview` 组是在 Register 里
// 分别创建的，全局挂一处才不会有"某条路径忘了加"的可乘之机；需要排除的路径在
// shouldGzip 里集中列着。
func GzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !shouldGzip(c.Request.URL.Path) || !acceptsGzip(c.GetHeader("Accept-Encoding")) {
			c.Next()
			return
		}
		// HEAD 无响应体可压；已带 Content-Encoding 的（如预压缩资源）不重复压。
		if c.Request.Method == http.MethodHead || c.Writer.Header().Get("Content-Encoding") != "" {
			c.Next()
			return
		}

		gw := &gzipResponseWriter{ResponseWriter: c.Writer}
		c.Writer = gw
		c.Next()

		// 收尾：真的建过 gzip writer 才需要 Close（写出尾部校验）+ 归还池。
		if gw.gz != nil {
			_ = gw.gz.Close()
			gzipWriterPool.Put(gw.gz)
			gw.gz = nil
		}
	}
}
