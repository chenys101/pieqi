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
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// previewRoute ANY /api/tasks/:id/preview/*path：preview 路由统一入口。
// gin 的 catch-all 与同前缀静态路由冲突，无法同时注册 /preview/start 与 /preview/*path，
// 故控制端点（start/stop/status）与 P2 视觉子路径（capture/console/network）
// 在 wildcard 路由内按「路径 + HTTP 方法」分发，其余路径走反向代理。
func (s *Server) previewRoute(c *gin.Context) {
	// c.Param("path") 形如 "/start"（含前导斜杠）
	path := strings.TrimPrefix(c.Param("path"), "/")

	switch path {
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
	case "restart":
		// P1 Preview Refresh（p1-design.md §10）：stop + start
		if c.Request.Method == http.MethodPost {
			s.restartPreview(c)
			return
		}
	case "attach":
		// P1 Preview Attach（p1-design.md §10）：外链 + 二维码
		if c.Request.Method == http.MethodGet {
			s.previewAttach(c)
			return
		}
	case "status":
		if c.Request.Method == http.MethodGet {
			s.previewStatus(c)
			return
		}
	default:
		// ⚠️ 视觉采集子路径（capture / console / network / screenshots）已于 2026-10-15
		// **整体移除**（ADR-0012 移除截图，ADR-0013 移除 console/network）。
		//
		// 这里**显式返回 410 Gone**，而不是让它们落到下面的反向代理 ——
		// 否则 /preview/console 之类会被当作**页面路径**代理给被预览的 dev server，
		// 调用方拿到一个莫名其妙的 HTML/404，**分不清"功能没了"还是"服务坏了"**。
		//
		// ⚠️ `screenshots/<id>.png` 这类**带子路径**的也要挡住（用 HasPrefix）：
		// 它不匹配任何精确 case，会掉进下面的 302 代理 ——
		// 这是实测发现的（测试期望 410、实际拿到 302 跳转）。
		if path == "capture" || path == "console" || path == "network" ||
			path == "screenshots" || strings.HasPrefix(path, "screenshots/") {
			c.JSON(http.StatusGone, gin.H{
				"error": "visual capture feature removed",
				"hint":  "视觉采集（截图/console/network）已于 2026-10-15 移除，见 docs/adr/0012 与 0013",
			})
			return
		}
		// 代理能力只保留在 /preview/:id/*（见 core.PreviewMountPrefix 说明）。
		// 旧路径 /api/tasks/:id/preview/* 一律 302 到新挂载点并带上 query（token），
		// 避免请求落进被预览项目自身的 /api 代理规则而形成回环。
		if strings.HasPrefix(c.FullPath(), "/api/") {
			target := core.PreviewBasePath(c.Param("id")) + path
			if q := c.Request.URL.RawQuery; q != "" {
				target += "?" + q
			}
			c.Redirect(http.StatusFound, target)
			return
		}
		s.previewProxy(c)
		return
	}
	// 控制端点存在但方法不匹配 → 405（避免误把控制路径代理给 dev server）
	c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
}

// restartPreview POST /api/tasks/:id/preview/restart：停止后重启（Rewind→Verify / 非 HMR 改动后）。
func (s *Server) restartPreview(c *gin.Context) {
	if s.preview == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview not enabled"})
		return
	}
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if err := s.preview.Restart(task); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "state": s.preview.Status(task).State})
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
		// discovery 失败 → unavailable（不猜），但**不静默**：把权威目录与"疑似方案页"
		// 一并返回。目录约定是魔法位置，只回一句 "no package.json found" 会让人不知道
		// 下一步该做什么（用户以为功能坏了，或者只能去猜 agent 把文件放哪了）。
		st := s.preview.Status(task)
		c.JSON(http.StatusConflict, gin.H{
			"error":      err.Error(),
			"state":      core.PreviewUnavailable,
			"convention": st.Convention,
			"hint":       st.Hint,
			"suspected":  st.Suspected,
		})
		return
	}
	// static 预览登记即 running（无启动期），dev server 则是 starting 需轮询
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "state": s.preview.Status(task).State})
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
		c.JSON(http.StatusOK, core.PreviewStatus{State: core.PreviewUnavailable, Convention: core.PreviewStaticDir})
		return
	}
	c.JSON(http.StatusOK, s.preview.Status(task))
}

// previewAttach GET /api/tasks/:id/preview/attach（P1，p1-design.md §10）：
// 生成外部浏览器可打开的预览深链 + 二维码。
// 前置条件：preview 运行中（否则外链打开也是 503）+ 隧道 active（否则仅内网可达）。
// 返回 {url, qr}：url = <tunnel>/api/tasks/:id/preview/?token=<raw>；
// qr = /api/tunnel/qrcode?text=<url>（公开只读端点，渲染 PNG）。
// 调用方已过 ExternalAuth（或内网），暴露 raw token 无越权（本就能 ResetToken）。
func (s *Server) previewAttach(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	if s.preview == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview not enabled"})
		return
	}
	// 前置条件用 Running 而非 RunningPort：静态预览没有端口（port 恒 0），
	// 按端口判断会让静态预览永远拿不到外链。
	if !s.preview.Running(task.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "preview not running"})
		return
	}
	if s.tunnel == nil || !s.tunnel.IsActive() {
		c.JSON(http.StatusConflict, gin.H{"error": "tunnel not active"})
		return
	}
	attach := attachPreviewURL(s.tunnel.FullURL(), task.ID)
	if attach == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "tunnel not active"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"url": attach,
		"qr":  "/api/tunnel/qrcode?text=" + url.QueryEscape(attach),
	})
}

// attachPreviewURL 由隧道完整 URL（…?token=yyy）拼 preview 外链：
// token 从 query 抽出追加到末尾，保证 preview 子路径干净。
func attachPreviewURL(fullTunnelURL, taskID string) string {
	base, token := splitTunnelToken(fullTunnelURL)
	if base == "" {
		return ""
	}
	u := strings.TrimRight(base, "/") + core.PreviewBasePath(taskID)
	if token != "" {
		u += "?token=" + token
	}
	return u
}

// splitTunnelToken 分离隧道 URL 的 base 与 token query 值（无 token → token=""）。
func splitTunnelToken(u string) (base, token string) {
	i := strings.Index(u, "?")
	if i < 0 {
		return u, ""
	}
	base, query := u[:i], u[i+1:]
	for _, kv := range strings.Split(query, "&") {
		if v, found := strings.CutPrefix(kv, "token="); found {
			token = v
		}
	}
	return base, token
}

// previewProxy ANY /api/tasks/:id/preview/*path：反向代理到该 task 绑定的 127.0.0.1 端口。
// static 预览没有端口：由 serveStaticPreview 在进程内直接读文件（不 spawn、不转发）。
func (s *Server) previewProxy(c *gin.Context) {
	if s.preview == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview not enabled"})
		return
	}
	taskID := c.Param("id")
	if dir := s.preview.StaticDir(taskID); dir != "" {
		s.serveStaticPreview(c, taskID, dir)
		return
	}
	// 静态产物免启动：直接打开 /preview/<id>/（新标签、收藏、分享链接）时不该先要求
	// 用户回面板点「启动」。只有 dev server 需要那个准备动作（spawn + 端口探测）。
	if task, ok := s.store.Get(taskID); ok {
		if dir := s.preview.EnsureStatic(task); dir != "" {
			s.serveStaticPreview(c, taskID, dir)
			return
		}
	}
	port := s.preview.RunningPort(taskID)
	if port == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview not running"})
		return
	}

	// 剥掉 /api/tasks/:id/preview 前缀，余下路径原样转发（默认 "/" → dev server 根）
	// vite 例外：spawn 时以 --base 感知子路径，代理须转发完整路径（剥前缀会让 vite 对 "/" 302 到 base → 重定向循环）
	prefix := strings.TrimSuffix(core.PreviewBasePath(taskID), "/")
	fw := s.preview.RunningFramework(taskID)
	stripPrefix := fw != "vite"
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", itoa(port))}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			if stripPrefix {
				// 相对路径重建：c.Request.URL.Path 此时是完整路径
				rest := strings.TrimPrefix(req.URL.Path, prefix)
				if rest == "" || !strings.HasPrefix(rest, "/") {
					rest = "/" + rest
				}
				req.URL.Path = rest
			}
			req.Host = "localhost" // dev server 常按 Host 判断，避免 127.0.0.1 导致重定向异常
		},
		ModifyResponse: rewritePreviewHTML(prefix),
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

// absoluteRootAssetRe 匹配 HTML 里根绝对资源引用（src/href="/@vite/client" 等）。
// 白名单覆盖各框架常见资源段：vite(@/node_modules)、构建产物(assets)、源码入口(src)、
// next/nuxt(_next/_nuxt)。vite 另有 --base 前缀化兜底（见 core/preview.go spawn），此处为非 vite 框架兜底。
var absoluteRootAssetRe = regexp.MustCompile(`((?:src|href)=["'])/((?:@|assets|node_modules|src|\.|_next|_nuxt|__next)[^"']*)["']`)

// staticRootAbsRefRe 静态页的根绝对引用：src="/x"、href='/x'。
// 比 absoluteRootAssetRe 更宽（不限定资源段），因为静态产物（方案对比页、HTML 报告）
// 的文件名不可预知；[^/"'] 排除协议相对地址（//cdn...）与已带前缀的路径。
var staticRootAbsRefRe = regexp.MustCompile(`((?:src|href)=["'])/([^/"'][^"']*)["']`)

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

		out := injectBaseAndRewrite(data, prefix, absoluteRootAssetRe)

		resp.Body = io.NopCloser(bytes.NewReader(out))
		resp.Header.Set("Content-Length", itoa(len(out)))
		resp.Header.Del("Content-Encoding") // 重写后内容已解压，防止 gzip 长度/编码不匹配
		return nil
	}
}

// injectBaseAndRewrite 注入 <base href="<prefix>/"> 并按 re 前缀化根绝对引用。
// reverse proxy（注入到被代理响应）与静态 serve（直接读盘）共用这一份改写逻辑，
// 避免两处 base 规则漂移（漂移的表现是「静态页能开、图片全裂」这类难查问题）。
func injectBaseAndRewrite(data []byte, prefix string, re *regexp.Regexp) []byte {
	rewritten := data
	if re != nil {
		rewritten = re.ReplaceAll(data, []byte("${1}"+prefix+"/${2}\""))
	}

	base := "<base href=\"" + prefix + "/\">"
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
	return out.Bytes()
}

// serveStaticPreview 进程内 serve 静态预览目录（PreviewStatic，见 core.PreviewStatic）。
//
// 与 reverse proxy 的差异（有意为之）：
//   - 不 spawn 进程 ⇒ 零内存；任务终态后仍可访问（core 的 stopIfProcess 不回收 static）
//   - 仍注入 <base href> + 前缀化根绝对引用 ⇒ 页面里的相对链接可用
//   - HTML 一律 no-store：方案页会被反复重写，缓存住旧版本会让人误以为 agent 没改
func (s *Server) serveStaticPreview(c *gin.Context, taskID, root string) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
		return
	}
	prefix := strings.TrimSuffix(core.PreviewBasePath(taskID), "/")
	rel := strings.TrimPrefix(c.Request.URL.Path, prefix)
	// path.Clean 先行折叠 ".."；pathWithin 二次校验，防拼接意外把根目录之外的文件读出去
	clean := path.Clean("/" + strings.TrimPrefix(rel, "/"))
	fsPath := filepath.Join(root, filepath.FromSlash(clean))
	if !pathWithin(root, fsPath) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	fi, err := os.Stat(fsPath)
	isDir := err == nil && fi.IsDir()
	if isDir {
		// 目录 → index.html（约定俗成）
		if idx := filepath.Join(fsPath, "index.html"); fileExistsAsFile(idx) {
			if st, serr := os.Stat(idx); serr == nil {
				fsPath, fi = idx, st
			}
			isDir = false
		}
	}
	if isDir {
		s.serveStaticDirFallback(c, prefix, fsPath, clean)
		return
	}
	if err != nil || fi.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found", "path": clean})
		return
	}

	c.Header("Cache-Control", "no-store")
	if strings.EqualFold(filepath.Ext(fsPath), ".html") {
		data, readErr := os.ReadFile(fsPath)
		if readErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": readErr.Error()})
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", injectBaseAndRewrite(data, prefix, staticRootAbsRefRe))
		return
	}

	f, openErr := os.Open(fsPath)
	if openErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": openErr.Error()})
		return
	}
	defer func() { _ = f.Close() }()
	// ServeContent 负责 Content-Type（按扩展名）、Content-Length、Range、Last-Modified
	http.ServeContent(c.Writer, c.Request, filepath.Base(fsPath), fi.ModTime(), f)
}

// serveStaticDirFallback 目录请求没有 index.html 时的兜底。
//
// 场景：agent 把多方案写成 <dir>/variant-a/index.html、<dir>/variant-b/index.html
// （或只丢一个 proposal.html 在子目录），根路径没有 index.html。旧行为是裸 404 JSON，
// 手机上看到一坨 {"error":"not found"}，用户只会以为"功能坏了"。
// 现在：唯一入口 → 302 过去；多个 → 生成一个极简索引页（纯链接），点哪个看哪个。
func (s *Server) serveStaticDirFallback(c *gin.Context, prefix, dir, clean string) {
	files := staticDirHTMLs(dir)
	base := strings.TrimSuffix(clean, "/") // "" 或 "/sub"
	switch len(files) {
	case 0:
		c.JSON(http.StatusNotFound, gin.H{
			"error": "not found",
			"path":  clean,
			"hint":  "该目录里没有 .html",
		})
		return
	case 1:
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusFound, prefix+base+"/"+files[0])
		return
	}

	c.Header("Cache-Control", "no-store")
	title := "预览目录"
	var b strings.Builder
	b.WriteString("<!doctype html><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	b.WriteString("<title>" + title + "</title>")
	b.WriteString("<style>body{font:16px/1.9 system-ui,-apple-system,\"Segoe UI\",sans-serif;margin:24px;color:#111}" +
		"a{color:#2563eb;text-decoration:none}li{margin:6px 0}" +
		"p{color:#666;font-size:13px}</style>")
	b.WriteString("<h3>" + title + "</h3>")
	b.WriteString("<p>" + html.EscapeString(base + "/") + "</p><ul>")
	for _, f := range files {
		href := prefix + base + "/" + f
		b.WriteString("<li><a href=\"" + html.EscapeString(href) + "\">" + html.EscapeString(f) + "</a></li>")
	}
	b.WriteString("</ul>")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(b.String()))
}

// staticDirHTMLs 列出 dir 下（递归两层）的 HTML，返回相对 dir 的 "/" 分隔路径（上限 30 条）。
// 复用与 discovery 相同的深度语义：agent 的目录结构深浅不一，但两层够覆盖
// <dir>/variant-a/index.html 这类常见写法。
func staticDirHTMLs(dir string) []string {
	const maxDepth = 3
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != dir {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.Count(rel, "/")+1 >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".html") {
			return nil
		}
		out = append(out, rel)
		if len(out) >= 30 {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// fileExistsAsFile 存在且不是目录。
func fileExistsAsFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// pathWithin p 是否落在 root 之内（Clean 后按分隔符比较，防目录穿越）。
func pathWithin(root, p string) bool {
	ar, err1 := filepath.Abs(root)
	ap, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return false
	}
	ar, ap = filepath.Clean(ar), filepath.Clean(ap)
	if ar == ap {
		return true
	}
	return strings.HasPrefix(ap, ar+string(os.PathSeparator))
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
