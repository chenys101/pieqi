// images_test.go claude 桥路径的图片透传（回归：此前恒被判定"不支持收图"）。
//
// 背景：桥的 /prompt 此前只收 text，session.js 的 content 写死成 item.text ——
// 图在 Go→桥这一层就丢了，所以 claude/session.go 的 SupportsImagePrompt 返回 false
// 是"诚实的"。现在整条链路打通，本文件钉住三件事：
//   1. 带图轮的 /prompt 报文里**真的有 images**，且形态正确（纯 base64 + mime_type）；
//   2. 纯文本轮的报文**逐字节不变**（不因支持图片而多带一个空 images）；
//   3. 能力位与 print 回退的关系 —— 桥说支持，print 回退**仍然**不支持
//      （它没实现 PromptRich；若这里改成 true，回退后发图会静默失败）。
package claude

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"pieqi/internal/agent"
)

// promptBody 解析 fakeBridge 捕获的 /prompt 原始报文。
func promptBody(t *testing.T, fb *fakeBridge) map[string]any {
	t.Helper()
	fb.mu.Lock()
	raw := fb.lastPrompt
	fb.mu.Unlock()
	if len(raw) == 0 {
		t.Fatal("桥没收到 /prompt（报文为空）")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解析 /prompt 报文失败: %v raw=%s", err, raw)
	}
	return m
}

// TestSessionPromptRichSendsImages 带图轮的报文里必须有正确形态的 images。
func TestSessionPromptRichSendsImages(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	sess := fb.newSession(t)
	defer sess.Close(context.Background())

	data := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})
	images := []agent.ImageInput{{Data: data, MimeType: "image/png"}}

	// PromptRich 阻塞等 turn_end；fakeBridge 的 autoComplete 会补上，故可直接调用。
	if err := sess.PromptRich(context.Background(), "这张图里是什么", images); err != nil {
		t.Fatalf("PromptRich: %v", err)
	}

	body := promptBody(t, fb)
	if body["text"] != "这张图里是什么" {
		t.Errorf("text=%v，want 原样传递", body["text"])
	}
	raw, ok := body["images"]
	if !ok {
		t.Fatal("报文里没有 images 字段：图在 Go→桥这一层被丢了（这正是本次要修的缺陷）")
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("images=%v，want 长度 1 的数组", raw)
	}
	img, _ := arr[0].(map[string]any)
	if img["data"] != data {
		t.Errorf("images[0].data=%v，want 纯 base64（不带 data: 前缀）", img["data"])
	}
	if img["mime_type"] != "image/png" {
		t.Errorf("images[0].mime_type=%v，want image/png", img["mime_type"])
	}
	// 线上最常见的误用就是带上 data URL 前缀；这里钉死它绝不能出现在报文里
	if s, _ := img["data"].(string); strings.HasPrefix(s, "data:") {
		t.Error("images[0].data 带了 data: 前缀，桥会明确拒绝")
	}
}

// TestSessionPromptRichTextOnlyUnchanged 纯文本轮报文必须与改动前一致（不带 images）。
//
// 这条是防"顺手统一"的回归：如果为了支持图片把所有轮次都改成块数组、
// 或者无条件带一个空 images 数组，纯文本这条占绝大多数的路径就被动了。
func TestSessionPromptRichTextOnlyUnchanged(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	sess := fb.newSession(t)
	defer sess.Close(context.Background())

	// 0 张图走 PromptRich（语义等价于纯文本 Prompt）
	if err := sess.PromptRich(context.Background(), "纯文本", nil); err != nil {
		t.Fatalf("PromptRich(nil images): %v", err)
	}
	body := promptBody(t, fb)
	if _, ok := body["images"]; ok {
		t.Error("纯文本轮不该带 images 字段（无谓扩大回归面）")
	}
	if body["text"] != "纯文本" {
		t.Errorf("text=%v", body["text"])
	}
}

// TestSessionPromptRichOnlyImages 只发图（空文本）必须被接受，不能要求文本非空。
func TestSessionPromptRichOnlyImages(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	sess := fb.newSession(t)
	defer sess.Close(context.Background())

	data := base64.StdEncoding.EncodeToString([]byte{9, 9, 9})
	if err := sess.PromptRich(context.Background(), "   ", []agent.ImageInput{{Data: data, MimeType: "image/jpeg"}}); err != nil {
		t.Fatalf("只发图应当被接受: %v", err)
	}
	body := promptBody(t, fb)
	arr, _ := body["images"].([]any)
	if len(arr) != 1 {
		t.Fatalf("images=%v，want 1 张", body["images"])
	}
}

// TestSessionPromptRichRejectsInvalidImages 非法图片在 Go 侧就被拦，不该发到桥上。
//
// 复用 agent.ValidateImages（与 ACP 侧同一份规则）：两处规则同源，改一处必须改另一处。
func TestSessionPromptRichRejectsInvalidImages(t *testing.T) {
	cases := []struct {
		name    string
		img     agent.ImageInput
		wantSub string
	}{
		{"空 data", agent.ImageInput{Data: "", MimeType: "image/png"}, "图片数据为空"},
		{"非法 base64", agent.ImageInput{Data: "!!not-b64!!", MimeType: "image/png"}, "不是合法的 base64"},
		{"data URL 前缀", agent.ImageInput{Data: "data:image/png;base64,AAAA", MimeType: "image/png"}, "纯 base64"},
		{"不支持的 MIME", agent.ImageInput{Data: "AAAA", MimeType: "image/bmp"}, "不支持的图片类型"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fb := newFakeBridge(t)
			defer fb.srv.Close()
			sess := fb.newSession(t)
			defer sess.Close(context.Background())

			err := sess.PromptRich(context.Background(), "x", []agent.ImageInput{c.img})
			if err == nil {
				t.Fatal("非法图片应当报错")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("err=%q 应含 %q", err.Error(), c.wantSub)
			}
			// 关键：非法图不该被发到桥上（发出去等于让桥再拒一次，错误信息更差）
			fb.mu.Lock()
			sent := fb.lastPrompt
			fb.mu.Unlock()
			if len(sent) != 0 {
				t.Errorf("非法图片被发到了桥：%s", sent)
			}
		})
	}
}

// TestSessionSupportsImagePrompt 桥路径声明支持收图（决定前端要不要显示加图入口）。
func TestSessionSupportsImagePrompt(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	sess := fb.newSession(t)
	defer sess.Close(context.Background())

	if !sess.SupportsImagePrompt() {
		t.Error("桥路径 SupportsImagePrompt=false：前端会永远隐藏加图入口，真能收图的能力被锁死")
	}
}

// TestPrintFallbackStillRejectsImages print 回退**不**支持收图（回归保护）。
//
// 为什么必须钉住：桥不可达时会自动回退 print。若 print 也宣称支持收图，
// 用户在回退后发图会得到一个运行时报错（PrintAgent 没有 PromptRich），
// 而前端还显示着入口 —— 表现就是"功能时好时坏"。
// 判据是接口实现：sessionBackedAdapter 只在底层实现了 SupportsImagePrompt 时才为真。
func TestPrintFallbackStillRejectsImages(t *testing.T) {
	pa := agent.NewPrintAgent(agent.PrintConfig{Binary: "claude"}, nil)
	sid, err := pa.NewSession(context.Background(), agent.SessionConfig{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	adapter := agent.NewSessionAdapter(pa, sid, agent.Caps{})
	// NewSessionAdapter 的静态返回类型是 AgentSession（不含该能力方法），
	// 上层（AgentManager.SupportsImagePrompt）正是用类型断言问的 —— 这里照抄同一判据，
	// 否则测的就不是线上真正走的那条路。
	capable, ok := adapter.(agent.ImagePromptCapable)
	if !ok {
		return // 没声明该能力 = 不支持，正是期望
	}
	if capable.SupportsImagePrompt() {
		t.Error("print 回退声称支持收图：回退后发图会运行时失败，而 UI 仍显示入口")
	}
}

// TestSessionAdapterForwardsImageCapability 组合层必须把桥会话的"能收图"透上来。
//
// 分层（容易搞混，这里写清楚）：
//   - 本包的 *session（桥路径）实现 AgentSession，并直接实现
//     SupportsImagePrompt / PromptRich；
//   - RichPromptSender / ImagePromptCapable 这两个**可选 adapter 能力**是给
//     sessionAdapter / ACPAgent 这类 adapter 实现的（manager.go 用类型断言问的就是它们）；
//   - 中间任何一层漏掉转发，表现都是"底层明明支持、pieqi 却说它不支持"，
//     前端加图入口永远不出现。
//
// 这里同时钉住两件事：底层会话确实声明了能力，且它确实落在可选能力接口上。
func TestSessionAdapterForwardsImageCapability(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	sess := fb.newSession(t)
	defer sess.Close(context.Background())

	if !sess.SupportsImagePrompt() {
		t.Error("底层 session 应声明支持收图（false 会让前端永远隐藏入口）")
	}
	// 编译期断言：桥会话必须满足上层用来判定的那个可选能力接口
	var _ agent.ImagePromptCapable = sess
}

// TestBridgeSessionExposesImageCapabilityThroughProvider 走**真实装配链**验证能力位可达。
//
// 这是本条特性最关键的一条断言：前面几个用例都只测到 `*session` 这一层，而线上
// AgentManager.SupportsImagePrompt 问的是 `sess.adapter`（sessionBackedAdapter），
// 它再往下问 AgentSession。中间任何一环没接上，前端加图入口就永远不出现 ——
// 而各层单测**都会是绿的**。所以这里从 agent.Open 出发，一路问到最上层。
func TestBridgeSessionExposesImageCapabilityThroughProvider(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()

	// 把 claude 的 provider 临时指向这个 fake 桥（同包，可直接存取 defaultConfig；
	// 用例结束必须还原，否则会污染同包其他用例）。
	prevCfg := defaultConfig
	Configure(Config{Transport: "sdk-bridge", BaseURL: fb.srv.URL})
	defer func() { defaultConfig = prevCfg }()

	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("agent.Open(claude): %v", err)
	}
	defer sess.Close(context.Background())

	// agent.Open 返回的是 AgentSession（桥路径下就是 *session）。它必须声明图片能力。
	capable, ok := sess.(agent.ImagePromptCapable)
	if !ok {
		t.Fatal("agent.Open 返回的会话未实现 ImagePromptCapable：上层恒判为不支持收图")
	}
	if !capable.SupportsImagePrompt() {
		t.Error("装配链上图片能力丢失：走 agent.Open 拿到的会话说不支持收图")
	}

	// ★ 真正被 AgentManager 断言的是 **adapter 层**（不是 AgentSession 层）：
	//   RunRich 里是 sess.adapter.(RichPromptSender) 然后调 SendRichPrompt(ctx, sessionID, ...)；
	//   SupportsImagePrompt 里是 sess.adapter.(ImagePromptCapable)。
	//   AgentSession 上的方法叫 PromptRich(ctx, text, images)，签名不同，**不能互相替代** ——
	//   这正是最容易"各层单测都绿、线上却发不出图"的地方。
	//   adapter 层的透传由 internal/agent 的 sessionadapter_image_test.go 覆盖
	//   （那里能构造 sessionBackedAdapter；本包看不到未导出的构造器）。
}
