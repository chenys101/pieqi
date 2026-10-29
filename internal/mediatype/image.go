// Package mediatype 收口图片 MIME ↔ 扩展名这一份知识。
//
// 为什么单独一个包：同一条"哪些图片能用"的规则原本散在三处 —— agent 出站白名单
// （agent/acp.go）、附件落盘（core/attachments.go）、本地图片服务（api/local_image.go）。
// 三份表各自能改，改一处不会让另外两处编译失败，症状是"发得出去却看不回来"
// 或"能展示却发不出去"（ADR-0010 明确要求这两个方向保持同步）。
package mediatype

// ExtByMime 可**出站 / 落盘**的图片类型 → 规范扩展名。
//
// 这四种是 agent 侧（dsh-acp 的 IMAGE_MEDIA_TYPES）与前端 ALLOWED_IMAGE_TYPES
// 的交集：能发给 agent 的类型就是能展示的类型，多一种或少一种都会形成落差。
var ExtByMime = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// MimeByExt 本机图片扩展名 → Content-Type（本地图片服务用）。
//
// 比 ExtByMime 多 .bmp / .avif：它们只出现在"agent 已在磁盘上写了一张图"的读取方向，
// 不作为附件上传，因此不进 ExtByMime。
//
// 刻意**不含 .svg**：SVG 是 XML，可以内嵌 <script>。虽然 <img> 上下文不执行脚本，
// 但同源直接打开这个 URL 就会执行 —— 那是面板上开的一个 XSS 口子。
var MimeByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".avif": "image/avif",
}

// IsImageMime 该 MIME 是否允许作为出站图片 / 附件落盘。
func IsImageMime(mime string) bool {
	_, ok := ExtByMime[mime]
	return ok
}

// ExtForMime 出站/落盘图片的规范扩展名。
func ExtForMime(mime string) (string, bool) {
	ext, ok := ExtByMime[mime]
	return ext, ok
}

// MimeForAttachmentExt 把落盘扩展名还原成 MIME。
//
// 只认 ExtByMime 的四个扩展名（**不含** bmp/avif）：附件目录只可能写入那四种，
// 放宽成 MimeByExt 会让"手工塞进 attachments 的别的图片"也变成合法附件。
func MimeForAttachmentExt(ext string) (string, bool) {
	for mime, e := range ExtByMime {
		if e == ext {
			return mime, true
		}
	}
	return "", false
}
