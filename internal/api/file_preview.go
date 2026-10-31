// file_preview.go 文件预览端点：markdown / pdf 等文档的原始内容读取。
//
//	GET /api/tasks/:id/file?path=<repo 相对路径>  返回 worktree 内 Agent 触碰文件的原始字节
//
// 安全设计：
//   - 路径经 validRepoPath 校验（拒绝绝对路径与 .. 逃逸；FileChange 路径来自事件流，不可信）
//   - 仅允许 Agent 触碰过的文件（对齐 Rewind 的「file touched」校验，checkpoint.go RewindFileToTurn），
//     防止读 worktree 里 Agent 未触碰的敏感文件（.env / 密钥）
//   - Content-Type 按扩展名探测，文本类补 utf-8；X-Content-Type-Options: nosniff
//
// 性能边界（2026-10 反馈面板文件查看链路）：
//   - 文本（code/md/text）最多返回 maxFileTextBytes，超出截断并标 X-Pieqi-Truncated:1
//   - 二进制（pdf/image/html）超过 maxFileBinaryBytes 直接 413（截断会损坏格式）
//   - 响应头始终带 X-Pieqi-File-Size（磁盘真值），前端用来提示「仅前 N / 全文过大」
package api

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// 文本预览硬上限：超过则截断返回（应用内查看器 / marked 都扛不住整文件进 DOM）。
const maxFileTextBytes = 2 << 20 // 2 MiB

// 二进制硬上限：超过则 413（截断会让 PDF/图片/HTML 直接坏掉）。
const maxFileBinaryBytes = 32 << 20 // 32 MiB

// getTaskFile GET /api/tasks/:id/file?path=<repo 相对路径>：返回原始文件字节。
// markdown 前端 fetch 文本后渲染；pdf 直接作 iframe src 由浏览器内嵌渲染。
func (s *Server) getTaskFile(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	path := strings.TrimPrefix(strings.TrimSpace(c.Query("path")), "./")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	if !validRepoPath(path) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	if !s.isTouchedPath(task, path) {
		// 未触碰文件与不存在同码，避免暴露 worktree 内其他文件的存在性
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	binary := isBinaryFilePath(path)
	maxBytes := int64(maxFileTextBytes)
	if binary {
		maxBytes = maxFileBinaryBytes
		// 二进制先看全尺寸：超限不读盘、不截断（截断 = 损坏格式）。
		full := filepath.Join(task.WorktreePath, filepath.FromSlash(path))
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() && fi.Size() > maxFileBinaryBytes {
			c.Header("X-Pieqi-File-Size", strconv.FormatInt(fi.Size(), 10))
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": "file too large",
				"size":  fi.Size(),
				"limit": maxFileBinaryBytes,
			})
			return
		}
	}

	content, size, truncated, exists := core.ReadWorktreeFileLimited(task.WorktreePath, path, maxBytes)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	ct := fileContentType(path, content)
	c.Header("Content-Type", ct)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Pieqi-File-Size", strconv.FormatInt(size, 10))
	if truncated {
		c.Header("X-Pieqi-Truncated", "1")
	} else {
		c.Header("X-Pieqi-Truncated", "0")
	}
	// agent 生成的 HTML/SVG 是**不可信内容**：直接以 pieqi 的源打开，等于把它塞进
	// 宿主页面同一个 origin —— 它能读 localStorage 里的 token、能以用户身份打 /api
	// （驱动 agent 跑命令）。产物"默认可访问"的前提是**不给宿主权限**：
	// CSP sandbox（**刻意不写 allow-same-origin**）把文档关进 opaque origin，
	// 脚本照跑、页面照看，但拿不到宿主的存储与凭据，fetch 同源 API 也会被 CORS 挡下。
	if isSandboxedDocType(ct) {
		c.Header("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups allow-modals")
		c.Header("Referrer-Policy", "no-referrer")
	}
	c.Data(http.StatusOK, ct, content)
}

// isBinaryFilePath 走 /file 时需要"整文件或拒绝"的扩展名（截断会坏格式）。
func isBinaryFilePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp",
		".html", ".htm":
		return true
	}
	return false
}

// isSandboxedDocType 哪些 Content-Type 会被浏览器**当文档执行**（含脚本能力）。
// 只认这两类：图片/PDF/markdown 是纯展示，不需要额外限制。
func isSandboxedDocType(ct string) bool {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	return base == "text/html" || base == "image/svg+xml"
}

// fileContentType 按扩展名返回 Content-Type；markdown 显式标 text/markdown，
// 其余走 mime 表，未知类型按内容嗅探（http.DetectContentType 已带 charset）。
func fileContentType(path string, content []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return "text/markdown; charset=utf-8"
	}
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return http.DetectContentType(content)
}

// validRepoPath 校验 repo 相对路径：非空、非绝对、无 .. 段。
func validRepoPath(p string) bool {
	if p == "" || p == "." {
		return false
	}
	// 统一分隔符（Windows 事件路径可能带反斜杠）
	slashed := strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(slashed, "/") || filepath.IsAbs(p) {
		return false
	}
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// isTouchedPath 判断 path 是否在 Agent 触碰过的文件集合内（事实源 TaskEvent，现场派生）。
func (s *Server) isTouchedPath(task *model.Task, path string) bool {
	for _, fc := range s.cachedFileChanges(task) {
		if fc.Path == path {
			return true
		}
	}
	return false
}
