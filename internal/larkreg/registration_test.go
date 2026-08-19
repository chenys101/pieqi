package larkreg

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestRun_FakeSDK_ReturnsCredentials 验证 Run 的契约:
// 1. 先回调 OnQRCode(带 URL + 过期秒数)
// 2. 阻塞直到 ctx 取消或底层 SDK 返回
// 3. 返回 AppID + AppSecret + nil error(成功路径)
//
// 底层 SDK 函数 registration.RegisterApp 是阻塞调用,无法 mock。
// 我们用 Run 的 input 结构体 + 一个可注入的 sdkFunc 字段做依赖注入,
// 测试时替换 sdkFunc 为 fake。
func TestRun_FakeSDK_ReturnsCredentials(t *testing.T) {
	var qrCalled int32
	fake := func(ctx context.Context, onQR func(url string, expireIn int)) (appID, appSecret string, err error) {
		onQR("https://accounts.feishu.cn/qr/abc", 300)
		atomic.StoreInt32(&qrCalled, 1)
		return "cli_fake", "sec_fake", nil
	}

	r := &Registration{sdkFunc: fake}
	var gotURL string
	var gotExpire int
	res, err := r.Run(context.Background(), Options{
		OnQRCode: func(url string, expireIn int) {
			gotURL = url
			gotExpire = expireIn
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.AppID != "cli_fake" {
		t.Fatalf("AppID = %q, want cli_fake", res.AppID)
	}
	if res.AppSecret != "sec_fake" {
		t.Fatalf("AppSecret = %q, want sec_fake", res.AppSecret)
	}
	if gotURL != "https://accounts.feishu.cn/qr/abc" {
		t.Fatalf("QR URL callback = %q", gotURL)
	}
	if gotExpire != 300 {
		t.Fatalf("QR expire = %d, want 300", gotExpire)
	}
	if atomic.LoadInt32(&qrCalled) != 1 {
		t.Fatal("sdkFunc not invoked")
	}
}

// TestRun_ContextCancelStopsPolling 验证 ctx 取消时 Run 返回 error。
func TestRun_ContextCancelStopsPolling(t *testing.T) {
	fake := func(ctx context.Context, onQR func(url string, expireIn int)) (appID, appSecret string, err error) {
		onQR("u", 1)
		<-ctx.Done() // 阻塞直到 ctx 取消
		return "", "", ctx.Err()
	}
	r := &Registration{sdkFunc: fake}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := r.Run(ctx, Options{OnQRCode: func(string, int) {}})
	if err == nil {
		t.Fatal("Run should return error on ctx cancel")
	}
}
