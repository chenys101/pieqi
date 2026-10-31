// artifacts.go 产物（Artifact）：agent 产出、可直接打开查看的文件。
//
//	GET /api/tasks/:id/artifacts   列出本任务的可查看产物
//
// 为什么需要这一层（与 preview 的分工）：
// preview 回答的是"把这个项目**跑起来**看"（spawn dev server / 静态目录 serve），
// 而 UI 迭代的产物——方案对比页、HTML 报告、截图、PDF——本身就是可打开的**文件**，
// 不需要跑起来。用 preview 承载它们要多走三步（放进约定目录 → 启动 → 打开），
// 散落在别处的文件甚至进不了清单。
//
// 产物层要给的正是三件事：**列得出来、点得开、默认可访问**。
//   - 事实源是 `TaskEvent → DeriveFileChanges`（agent 触碰过的文件），与 Rewind /
//     文件预览同一口径，不另造一份"产物登记"；
//   - 路径只来自事件派生 ⇒ 不需要用户输入路径，也不会读 agent 没碰过的敏感文件；
//   - HTML 的开放方式见 file_preview.go 的 CSP sandbox：默认可访问，但不给宿主同源权限。
package api

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// artifactDTO 一件产物。Open 是**可直接打开的 URL**（由服务端算好，
// 前端不需要知道 preview 与 file 两条通道的差别）。
type artifactDTO struct {
	Path string `json:"path"`           // repo 相对路径（与 Change 列表同口径）
	Name string `json:"name"`           // 文件名，列表展示用
	Kind string `json:"kind"`           // html | markdown | image | pdf | code | text
	Size int64  `json:"size,omitempty"` // 字节数
	Turn int    `json:"turn,omitempty"` // 最后产生它的 turn（用于"第几轮出的"）
	Open string `json:"open"`           // 打开 URL（html 在同源隔离沙箱里渲染；其余走 /file）
}

// getTaskArtifacts GET /api/tasks/:id/artifacts：列出可查看产物。
func (s *Server) getTaskArtifacts(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	items := s.collectArtifacts(task)
	c.JSON(http.StatusOK, gin.H{"artifacts": items})
}

// collectArtifacts 从事件流派生产物清单。
func (s *Server) collectArtifacts(task *model.Task) []artifactDTO {
	if task == nil || task.WorktreePath == "" {
		return nil
	}
	changes := s.cachedFileChanges(task)
	staticDir := ""
	if s.preview != nil {
		// 走 EnsureStatic 而不是 StaticDir：产物清单可能在"面板还没拉过 status"时被请求，
		// 那时实例尚未登记 —— 直接 StaticDir 会拿到空串，把目录内 HTML 的打开 URL
		// 降级成 /file（相对资源全裂）。产物与"是否点过启动"无关。
		staticDir = s.preview.EnsureStatic(task)
	}

	out := make([]artifactDTO, 0, len(changes))
	seen := map[string]bool{}
	for _, fc := range changes {
		// 删除不算产物；失败的写入不保证文件存在（下面还会 stat 兜一层）
		if fc.Operation == "delete" || fc.Status == "failed" {
			continue
		}
		if seen[fc.Path] {
			continue
		}
		kind := artifactKind(fc.Path)
		if kind == "" {
			continue // 未知/二进制扩展名不进清单（白名单外一律不列）
		}
		full := filepath.Join(task.WorktreePath, filepath.FromSlash(fc.Path))
		if !pathWithin(task.WorktreePath, full) {
			continue // 事件里的路径不可信：逃出 worktree 的一律不认
		}
		fi, err := os.Stat(full)
		if err != nil || fi.IsDir() {
			continue // 已被删除/尚未落盘：清单以**磁盘现状**为准
		}
		seen[fc.Path] = true
		out = append(out, artifactDTO{
			Path: fc.Path,
			Name: filepath.Base(fc.Path),
			Kind: kind,
			Size: fi.Size(),
			Turn: fc.Turn,
			Open: artifactOpenURL(task.ID, staticDir, full, fc.Path, kind),
		})
		if len(out) >= 200 {
			break
		}
	}

	// html 排前面（这轮迭代最常被打开的就是方案页），其余按路径字母序；
	// 用 Stable 保证同 kind 内维持事件顺序语义（turn 升序）。
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := artifactKindRank(out[i].Kind), artifactKindRank(out[j].Kind)
		if ri != rj {
			return ri < rj
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// artifactKind 扩展名 → 产物类型；返回 "" 表示"不是可查看产物"。
//
// 白名单而不是黑名单：这是给**人看**的通道。html 另有 sandbox 头（file_preview.go）；
// code/text 由前端只读拉取 /file 原文展示，不做编辑。
func artifactKind(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".html", ".htm":
		return "html"
	case ".md", ".markdown", ".mdown", ".mkd":
		return "markdown"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp":
		return "image"
	case ".pdf":
		return "pdf"
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".vue", ".svelte", ".py", ".rs", ".java", ".kt", ".kts",
		".c", ".h", ".cpp", ".cc", ".cxx", ".hpp", ".hh",
		".cs", ".rb", ".php", ".swift", ".scala", ".r",
		".sh", ".bash", ".zsh", ".fish", ".ps1", ".bat", ".cmd",
		".sql", ".graphql", ".gql", ".proto",
		".css", ".scss", ".sass", ".less",
		".toml", ".ini", ".cfg", ".conf":
		return "code"
	case ".txt", ".text", ".log", ".csv", ".tsv",
		".yaml", ".yml", ".json", ".jsonc", ".json5",
		".xml", ".svg":
		return "text"
	}
	return ""
}

// artifactKindRank 列表排序权重：可视化产出优先，代码/文本靠后。
func artifactKindRank(kind string) int {
	switch kind {
	case "html":
		return 0
	case "markdown":
		return 1
	case "image":
		return 2
	case "pdf":
		return 3
	case "code":
		return 4
	case "text":
		return 5
	}
	return 9
}

// artifactOpenURL 算出打开 URL。
//
//   - HTML 落在静态预览目录内 → 走 preview 路径：同目录的相对资源（css/js/图片）能一起解析，
//     这是多文件方案页能正常显示的前提；
//   - 其余（HTML 在目录外、markdown、图片、pdf、code、text）→ 走 /file 原始字节：
//     markdown/code/text 由前端 fetch 后渲染或只读展示，图片/pdf 直接作 src。
func artifactOpenURL(taskID, staticDir, full, rel, kind string) string {
	if kind == "html" && staticDir != "" {
		if r, err := filepath.Rel(staticDir, full); err == nil && !strings.HasPrefix(r, "..") {
			return core.PreviewBasePath(taskID) + filepath.ToSlash(r)
		}
	}
	return "/api/tasks/" + url.PathEscape(taskID) + "/file?path=" + url.QueryEscape(rel)
}
