package agent

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

// pngB64 是一段最小的合法 base64（解码出 5 字节 "pieqi"），仅用于形状校验类断言。
// 我们不解析图片内容，所以不必是真的 PNG。
const pngB64 = "cGllcWk="

// --- 能力判定 ---

// TestSupportsImagePrompt_ReadsAgentCapability 验证图片能力只认对端声明。
//
// 这条是"发图入口该不该显示"的唯一判据：读错方向（把客户端能力当对端能力）
// 会导致界面上有入口、发出去必失败。
func TestSupportsImagePrompt_ReadsAgentCapability(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)

	// 未握手：保守取假（还没确认的能力当作没有）。
	if a.SupportsImagePrompt() {
		t.Error("SupportsImagePrompt=true before handshake, want false (conservative)")
	}

	// 对端声明 true。
	a.agentCaps = acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{Image: true}}
	if !a.SupportsImagePrompt() {
		t.Error("SupportsImagePrompt=false with agent declaring image=true")
	}

	// 对端声明 false / 没声明。
	a.agentCaps = acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{Image: false}}
	if a.SupportsImagePrompt() {
		t.Error("SupportsImagePrompt=true with agent declaring image=false")
	}
	a.agentCaps = acp.AgentCapabilities{}
	if a.SupportsImagePrompt() {
		t.Error("SupportsImagePrompt=true with agent declaring nothing")
	}
}

// --- 内容块组装 ---

// TestBuildPromptBlocks_TextThenImages 验证块序：文本在前、图片紧随。
//
// 判据：dsh-acp 按块序重建 content，把图放前面会让模型的语境变成"先看图再读问题"，
// 而人写提示词的习惯就是先说要什么、再给素材。
func TestBuildPromptBlocks_TextThenImages(t *testing.T) {
	blocks, err := buildPromptBlocks("这张图里是什么", []ImageInput{
		{Data: pngB64, MimeType: "image/png"},
		{Data: pngB64, MimeType: "image/jpeg"},
	})
	if err != nil {
		t.Fatalf("buildPromptBlocks: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3 (1 text + 2 images)", len(blocks))
	}
	if blocks[0].Text == nil || blocks[0].Text.Text != "这张图里是什么" {
		t.Errorf("blocks[0] should be the text block, got %+v", blocks[0])
	}
	if blocks[1].Image == nil || blocks[1].Image.MimeType != "image/png" {
		t.Errorf("blocks[1] should be the png image, got %+v", blocks[1])
	}
	if blocks[2].Image == nil || blocks[2].Image.MimeType != "image/jpeg" {
		t.Errorf("blocks[2] should be the jpeg image, got %+v", blocks[2])
	}
}

// TestBuildPromptBlocks_ImageOnly 验证"只发图"是合法的：不塞空 TextBlock。
//
// 判据：某些 agent 对空文本块报 invalid，而"让 agent 描述这张图"是完全正当的用法。
func TestBuildPromptBlocks_ImageOnly(t *testing.T) {
	blocks, err := buildPromptBlocks("   ", []ImageInput{{Data: pngB64, MimeType: "image/png"}})
	if err != nil {
		t.Fatalf("buildPromptBlocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Image == nil {
		t.Fatalf("got %+v, want exactly 1 image block (no empty text block)", blocks)
	}
}

// TestBuildPromptBlocks_EmptyRejected 验证空白+无图被拒（不发一轮空 prompt）。
func TestBuildPromptBlocks_EmptyRejected(t *testing.T) {
	if _, err := buildPromptBlocks("", nil); err == nil {
		t.Fatal("buildPromptBlocks(empty, nil) = nil error, want rejection")
	}
}

// TestBuildPromptBlocks_TooManyImages 验证张数上限。
func TestBuildPromptBlocks_TooManyImages(t *testing.T) {
	imgs := make([]ImageInput, maxPromptImages+1)
	for i := range imgs {
		imgs[i] = ImageInput{Data: pngB64, MimeType: "image/png"}
	}
	_, err := buildPromptBlocks("hi", imgs)
	if err == nil {
		t.Fatalf("buildPromptBlocks with %d images = nil error, want rejection", len(imgs))
	}
	if !strings.Contains(err.Error(), "最多") {
		t.Errorf("err=%q should mention the limit", err)
	}
}

// --- 单图校验 ---

// TestValidateImage_RejectsDataURLPrefix 验证 data URL 前缀被明确拒绝。
//
// 这是最容易犯的错（前端/IM 拿到的往往就是 data URL），而症状（图片损坏或
// 协议级 invalid）离根因很远，所以专门为它写一条错误信息。
func TestValidateImage_RejectsDataURLPrefix(t *testing.T) {
	err := validateImage(ImageInput{
		Data:     "data:image/png;base64," + pngB64,
		MimeType: "image/png",
	})
	if err == nil {
		t.Fatal("validateImage accepted a data URL, want rejection")
	}
	if !strings.Contains(err.Error(), "前缀") {
		t.Errorf("err=%q should name the data-URL prefix problem", err)
	}
}

func TestValidateImage_RejectsUnsupportedMime(t *testing.T) {
	for _, mime := range []string{"image/bmp", "image/tiff", "application/pdf", "", "IMAGE/PNG"} {
		if err := validateImage(ImageInput{Data: pngB64, MimeType: mime}); err == nil {
			t.Errorf("validateImage accepted mime %q, want rejection", mime)
		}
	}
	for _, mime := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		if err := validateImage(ImageInput{Data: pngB64, MimeType: mime}); err != nil {
			t.Errorf("validateImage rejected valid mime %q: %v", mime, err)
		}
	}
}

func TestValidateImage_RejectsMalformedBase64(t *testing.T) {
	cases := map[string]string{
		"长度不是 4 的倍数": "cGllcW",       // 6 字符
		"含非法字符":       "cGllcWk!!",    // ! 不在字母表
		"padding 在中间":  "cGll=Wk=",    // = 之后还有数据
		"过多 padding":   "cG==",        // 2 个 padding 但长度 4 → 合法，见下条说明
		"空数据":          "",            // 单独由 Data=="" 分支拦
		"纯 padding":     "====",        // 全 padding
	}
	for name, data := range cases {
		err := validateImage(ImageInput{Data: data, MimeType: "image/png"})
		// "cG==" 实际是合法 base64（解码出 1 字节），不该在拒绝列表里。
		if name == "过多 padding" {
			if err != nil {
				t.Errorf("validateImage(%s) rejected a legal base64: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("validateImage(%s, data=%q) = nil, want rejection", name, data)
		}
	}
}

// TestValidateImage_SizeLimit 验证按**解码后**字节数判上限。
//
// 为什么按解码后算：base64 会把体积撑大约 1/3，拿 base64 长度当判据会在边界上
// 放进过大的图；而这条限制的真实意图是控制上下文开销 —— 那取决于原始字节数。
func TestValidateImage_SizeLimit(t *testing.T) {
	// 构造略超上限的合法 base64：解码后 maxImageBytes+3 字节。
	over := base64.StdEncoding.EncodeToString(make([]byte, maxImageBytes+3))
	err := validateImage(ImageInput{Data: over, MimeType: "image/png"})
	if err == nil {
		t.Fatal("validateImage accepted an oversized image, want rejection")
	}
	if !strings.Contains(err.Error(), "过大") {
		t.Errorf("err=%q should say the image is too large", err)
	}
	// base64 长度确实比解码后大 —— 证明确实按解码后判，而不是按字符串长度。
	if len(over) <= maxImageBytes {
		t.Fatal("test setup invalid: base64 should be longer than raw bytes")
	}

	// 刚好在上限内：通过。
	ok := base64.StdEncoding.EncodeToString(make([]byte, maxImageBytes))
	if err := validateImage(ImageInput{Data: ok, MimeType: "image/png"}); err != nil {
		t.Errorf("validateImage rejected an image exactly at the limit: %v", err)
	}
}

// TestDecodedBase64Len 直接钉住长度推算（大小判据全靠它）。
func TestDecodedBase64Len(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"cGllcWk=", 5, true},  // "pieqi"（5 字节 + 1 padding）
		{"cG==", 1, true},      // 1 字节 + 2 padding
		{"cGk=", 2, true},      // 2 字节 + 1 padding
		{"cGll", 3, true},      // 无 padding
		{"", 0, true},          // 空：长度 0 是 4 的倍数
		{"cGllcW", 0, false},   // 长度非 4 的倍数
		{"cGll=Wk", 0, false},  // padding 在中间
		{"cGllcWk!", 0, false}, // 非法字符
		{"=cGllcWk", 0, false}, // padding 在前
	}
	for _, c := range cases {
		got, ok := decodedBase64Len(c.in)
		if ok != c.ok {
			t.Errorf("decodedBase64Len(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("decodedBase64Len(%q)=%d, want %d", c.in, got, c.want)
		}
	}
}

// --- 发送路径 ---

// TestSendRichPrompt_RejectsWhenAgentCannotReceive 验证对端不支持时**明确报错**。
//
// 这是本设计最重要的一条：静默丢图让用户以为 agent 看过图了，而它会对着
// 没图的上下文说些不相干的话 —— 那种失败最难归因。
func TestSendRichPrompt_RejectsWhenAgentCannotReceive(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "claude-code"}, nil)
	a.started = true // 绕过握手，只测能力闸门

	err := a.SendRichPrompt(context.Background(), "s", "hi", []ImageInput{{Data: pngB64, MimeType: "image/png"}})
	if err == nil {
		t.Fatal("SendRichPrompt succeeded without agent image capability, want error")
	}
	if !strings.Contains(err.Error(), "未声明图片输入能力") {
		t.Errorf("err=%q should explain the agent does not accept images", err)
	}
}

// TestSendRichPrompt_NoImagesFallsBackToText 验证 0 张图走纯文本路径。
//
// 有活连接时它会真的发出去；这里只断言"没图时不因能力缺失而报错"这一条
// （agent 未声明图片能力，但没图就该照常发）。
func TestSendRichPrompt_ValidatesBeforeDialing(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	a.started = true
	a.agentCaps = acp.AgentCapabilities{PromptCapabilities: acp.PromptCapabilities{Image: true}}

	// 非法图片应在连上 agent **之前**就被拦下：这里 conn 是 nil，
	// 若校验没生效就会 panic 在 nil 解引用上。
	err := a.SendRichPrompt(context.Background(), "s", "hi", []ImageInput{{Data: "not base64!", MimeType: "image/png"}})
	if err == nil {
		t.Fatal("SendRichPrompt accepted malformed base64, want error")
	}
	if !strings.Contains(err.Error(), "base64") {
		t.Errorf("err=%q should mention base64", err)
	}
}

// TestSendRichPrompt_RejectsBeforeStart 验证未启动时明确报错（与 SendPrompt 一致）。
func TestSendRichPrompt_RejectsBeforeStart(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "dsh"}, nil)
	if err := a.SendRichPrompt(context.Background(), "s", "hi", nil); err == nil {
		t.Fatal("SendRichPrompt before Start = nil, want error")
	}
}
