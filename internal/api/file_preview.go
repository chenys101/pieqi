// file_preview.go 文件预览端点：markdown / pdf 等文档的原始内容读取。
//
//	GET /api/tasks/:id/file?path=<repo 相对路径>  返回 worktree 内 Agent 触碰文件的原始字节
//
// 安全设计：
//   - 路径经 validRepoPath 校验（拒绝绝对路径与 .. 逃逸；FileChange 路径来自事件流，不可信）
//   - 仅允许 Agent 触碰过的文件（对齐 Rewind 的「file touched」校验，checkpoint.go RewindFileToTurn），
//     防止读 worktree 里 Agent 未触碰的敏感文件（.env / 密钥）
//   - Content-Type 按扩展名探测，文本类补 utf-8；X-Content-Type-Options: nosniff
package api

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

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
	content, exists := core.ReadWorktreeFile(task.WorktreePath, path)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	ct := fileContentType(path, content)
	c.Header("Content-Type", ct)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, ct, content)
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
	changes := core.DeriveFileChanges(task.Events, task.WorktreePath, nil)
	for _, fc := range changes {
		if fc.Path == path {
			return true
		}
	}
	return false
}
