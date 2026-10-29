package mediatype

import "testing"

// 两张表必须互为逆映射（对同一批 MIME）：否则"存下来的扩展名"在本地图片服务上
// 认不出 Content-Type，表现就是图存进了磁盘、打开时 400。
func TestImageMapsAreInverse(t *testing.T) {
	for mime, ext := range ExtByMime {
		if got := MimeByExt[ext]; got != mime {
			t.Errorf("ExtByMime[%q]=%q，但 MimeByExt[%q]=%q", mime, ext, ext, got)
		}
		if !IsImageMime(mime) {
			t.Errorf("IsImageMime(%q) = false，它在 ExtByMime 里", mime)
		}
	}
	// .jpeg 是 .jpg 的别名：只加在 MimeByExt 一侧，不能反过来多出一个 MIME。
	if MimeByExt[".jpeg"] != "image/jpeg" {
		t.Errorf("MimeByExt[.jpeg] = %q", MimeByExt[".jpeg"])
	}
	if IsImageMime("image/bmp") || IsImageMime("image/avif") {
		t.Error("bmp/avif 只该可读（MimeByExt），不该被当成可上传类型")
	}
}

// SVG 永远不进白名单：同源直开即可执行脚本（见 MimeByExt 注释）。
func TestNoSVG(t *testing.T) {
	if _, ok := MimeByExt[".svg"]; ok {
		t.Error("MimeByExt 不得包含 .svg")
	}
	if IsImageMime("image/svg+xml") {
		t.Error("ExtByMime 不得接受 image/svg+xml")
	}
}
