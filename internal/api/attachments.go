// attachments.go 工具结果图片的读取端点。
//
//	GET /api/tasks/:id/attachments/:hash  返回该任务某张工具结果图片的原始字节
//
// 为什么按 hash 取而不是"按事件 seq + 第 N 张"：
//   - hash 就是内容本身，前端从事件的 images[].hash 直接拼 URL，不需要额外记住
//     "这张图属于哪条事件"；
//   - 同一张图被重复读到只存一份，两个事件引用同一个 URL，浏览器缓存自然命中。
//
// 安全设计（与 file_preview.go 同一套思路）：
//   - hash 必须是 64 位 hex，否则 400 —— 它是唯一直拼进文件路径的参数，
//     放行 `..` 或分隔符等于用 URL 读任意文件（见 core.validAttachmentHash）；
//   - **只放行本任务事件流里真的引用过的 hash**（404）—— 附件是全局按内容寻址的，
//     不加这一道的话，知道别的任务图片 hash 的人（看过链接即可）就能跨任务读图。
//   - 不走 gzip：图片已经是压缩格式，再压一遍只是白花 CPU。
package api

import (
	"errors"
	"net/http"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// getTaskAttachment GET /api/tasks/:id/attachments/:hash：返回图片原始字节。
func (s *Server) getTaskAttachment(c *gin.Context) {
	task, ok := s.requireTask(c)
	if !ok {
		return
	}
	hash := core.NormalizeAttachmentHash(c.Param("hash"))
	// 先校验形状：非法 hash 不该被当成"这张图不属于该任务"混进 404（理由见上）。
	if !core.ValidAttachmentHash(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid attachment hash"})
		return
	}
	if err := validateAttachmentRef(task, hash); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}
	store := s.store.Attachments()
	if store == nil {
		// 未接线：与"文件不存在"同码。这是部署配置问题，不该把内部状态透露给调用方。
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}
	raw, mime, err := store.Read(hash)
	if errors.Is(err, core.ErrInvalidAttachmentHash) {
		// 形状不合法只在"引用校验"漏掉时才会走到（见上），仍然单独回 400：
		// 把"有人在拼非法路径"与"图没了"分开，排查时不必再猜。
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid attachment hash"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read attachment failed"})
		return
	}
	if raw == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}
	c.Header("Content-Type", mime)
	c.Header("X-Content-Type-Options", "nosniff")
	// 内容寻址 ⇒ 同一 hash 的字节永远不变，可以放心长缓存（换图必然换 hash）。
	c.Header("Cache-Control", "private, max-age=86400, immutable")
	c.Data(http.StatusOK, mime, raw)
}

// validateAttachmentRef 判断某张图是否真的属于这个任务（事件流里引用过它）。
//
// 判据是**事件里的引用**而不是磁盘：附件目录是全局共享的，磁盘上有不代表
// 该任务可以看它。这个函数就是跨任务读图的唯一闸门。
//
// 只看 Images[].Hash：Stored 为假（旧数据）说明本来就没有可读的字节，
// 就算_hash 撞上别的任务存过的图，也不该借它显示出来。
func validateAttachmentRef(task *model.Task, hash string) error {
	for i := range task.Events {
		for _, img := range task.Events[i].Images {
			if img.Stored && img.Hash == hash {
				return nil
			}
		}
	}
	return errAttachmentNotReferenced
}

// errAttachmentNotReferenced 该任务的事件流里没有引用这张图。
var errAttachmentNotReferenced = errors.New("attachment not referenced by task")
