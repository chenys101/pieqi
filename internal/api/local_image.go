// local_image.go 任务工作区内的本地图片读取端点。
//
//	GET /api/tasks/:id/local-image?path=<绝对路径或相对项目根的路径>  返回图片原始字节
//
// 为什么需要它：Agent 在结论里写 `![总览](D:\code\go\pieqi\.tmp-avatar\final\_contact-6up.png)`
// 是很自然的表达（它刚把这些文件写到磁盘上），但浏览器没有 D 盘 —— 这个 src 会被当成
// 同源相对路径，请求 /sessions/D:%5Ccode... 拿回 SPA 兜底页，于是**结论里永远是一枚破图**。
// 前端把这类本地路径改写成本端点（见 web/src/features/timeline/localPath.ts）。
//
// 安全设计（与 file_preview.go / attachments.go 同一套思路，但闸门不同）：
//   - **只放行任务自己的项目目录/工作树内的文件**：路径先 Clean，再逐 root 求相对路径，
//     任何 `..` 逃逸或落在 root 之外一律拒绝。这里的 root 来自任务记录，不接受客户端传；
//   - **只放行图片扩展名**（白名单 + 固定 Content-Type）：本端点不读文本/配置，
//     避免"Agent 把 config.yaml 的路径写进 markdown"就把 token 送到页面上；
//   - 体积上限 32MiB：再大的图浏览器也解不动，读进内存只会拖垮服务；
//   - 不开长缓存（与 attachments 相反）：路径是**可复用**的 —— Agent 重新生成头像会覆盖
//     同一个文件名。这里缓存住就会一直显示旧图。
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"pieqi/internal/mediatype"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// maxLocalImageBytes 单张本地图片的体积上限。
// 32MiB 覆盖 1024~4096 的 PNG 绰绰有余，同时把"误指到某个巨大文件"挡在内存之外。
const maxLocalImageBytes = 32 << 20

// 扩展名白名单与 Content-Type 的唯一真相在 internal/mediatype（与附件落盘共用）。
// 其中**不含 .svg**：SVG 是 XML，可以内嵌 <script>。虽然 <img> 上下文不执行脚本，
// 但同源直接打开这个 URL 就会执行 —— 那是我们在面板上开的一个 XSS 口子。

// getTaskLocalImage GET /api/tasks/:id/local-image?path=...：返回任务目录内某张图片的字节。
func (s *Server) getTaskLocalImage(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	raw := strings.TrimSpace(c.Query("path"))
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	abs, err := resolveTaskImagePath(task, raw)
	if err != nil {
		// 相对路径找不到 = 404（Agent 写的路径没生成）；越界 = 400（有人在拼路径）。
		if err == errImageNotFoundInWorkspace {
			c.JSON(http.StatusNotFound, gin.H{"error": "image not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 扩展名先于 stat 判断：非图片路径不必走到文件系统，也就不暴露"这个文件存不存在"。
	ct, isImage := mediatype.MimeByExt[strings.ToLower(filepath.Ext(abs))]
	if !isImage {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only image files can be previewed"})
		return
	}

	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		// 不存在与不可读同码：本端点的典型 404 就是"Agent 说的路径其实没生成"。
		c.JSON(http.StatusNotFound, gin.H{"error": "image not found"})
		return
	}
	if info.Size() > maxLocalImageBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "image too large"})
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "image not found"})
		return
	}

	c.Header("Content-Type", ct)
	c.Header("X-Content-Type-Options", "nosniff")
	// 文件名可复用（重新生成会覆盖同路径文件）⇒ 不允许缓存，否则永远看到旧图。
	c.Header("Cache-Control", "private, no-cache")
	c.Data(http.StatusOK, ct, data)
}

// resolveTaskImagePath 把请求里的路径解析成**绝对路径**，并校验它落在该任务的项目目录或工作树内。
//
// 接受两种形态（Agent 两种都会写）：
//   - 绝对路径：`D:\code\go\pieqi\.tmp-avatar\final\_contact-6up.png`
//   - 项目相对路径：`.tmp-avatar/final/_contact-6up.png` / `./a.png`
//
// 返回的 error 是**给调用方看的原因**，不含服务端绝对路径（避免回显目录结构）。
func resolveTaskImagePath(task *model.Task, raw string) (string, error) {
	// 反斜杠统一成正斜杠：Windows 路径与 URL 混着来，且 filepath 在 Linux 上不认反斜杠。
	p := strings.ReplaceAll(raw, "\\", "/")

	var roots []string
	for _, r := range []string{task.WorktreePath, task.ProjectPath} {
		if strings.TrimSpace(r) != "" {
			roots = append(roots, r)
		}
	}
	if len(roots) == 0 {
		return "", errImageNoWorkspace
	}

	// 绝对路径：必须落在某个 root 下。
	if filepath.IsAbs(p) || (len(p) > 1 && p[1] == ':') {
		abs := filepath.Clean(p)
		for _, root := range roots {
			if _, ok := relUnder(root, abs); ok {
				return abs, nil
			}
		}
		return "", errImageOutsideWorkspace
	}

	// 相对路径：按 root 依次拼，取第一个真实存在的（多 root 时 worktree 优先）。
	// 注意把"逃出 root"与"root 内没这个文件"分开：前者是有人在拼路径（400），
	// 后者只是 Agent 写的路径没生成（404）—— 混成同一个码会让排查时要靠猜。
	trimmed := strings.TrimPrefix(p, "./")
	escaped := false
	for _, root := range roots {
		abs, err := filepath.Abs(filepath.Join(root, trimmed))
		if err != nil {
			continue
		}
		if _, ok := relUnder(root, abs); !ok {
			escaped = true
			continue
		}
		if info, err := os.Stat(abs); err == nil && info.Mode().IsRegular() {
			return abs, nil
		}
	}
	if escaped {
		return "", errImageOutsideWorkspace
	}
	return "", errImageNotFoundInWorkspace
}

// relUnder 判断 target 是否在 root 之内，并返回 root 相对路径（分隔符已统一为 /）。
//
// 只做字符串层面的包含判断，但有三处必须精确：
//   - 大小写：Windows 路径不区分大小写，`d:` 与 `D:` 是同一个盘（EqualFold）；
//   - 前缀必须落在**路径分隔符**上：`...\pieqi2` 不算在 `...\pieqi` 下；
//   - `..` 段一律拒绝（normalize 之后的残留只可能来自拼进来的恶意串）。
func relUnder(root, target string) (string, bool) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	r := strings.ReplaceAll(filepath.Clean(rootAbs), "\\", "/")
	t := strings.ReplaceAll(filepath.Clean(target), "\\", "/")
	if len(t) <= len(r) || !strings.EqualFold(t[:len(r)], r) {
		return "", false
	}
	rest := t[len(r):]
	if !strings.HasPrefix(rest, "/") {
		return "", false
	}
	rest = strings.TrimPrefix(rest, "/")
	for _, seg := range strings.Split(rest, "/") {
		if seg == ".." {
			return "", false
		}
	}
	return rest, rest != ""
}

// 三类失败原因（不暴露服务端路径，只说清了哪一类）。
var (
	errImageNoWorkspace         = errImageReason("task has no workspace directory")
	errImageOutsideWorkspace    = errImageReason("path is outside the task workspace")
	errImageNotFoundInWorkspace = errImageReason("image not found")
)

type errImageReason string

func (e errImageReason) Error() string { return string(e) }
