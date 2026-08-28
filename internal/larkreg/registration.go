package larkreg

import (
	"context"
	"fmt"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

// Result 是 Device Flow 成功后的返回值。
type Result struct {
	AppID     string
	AppSecret string
}

// Options 控制 Registration.Run 的行为。
type Options struct {
	// OnQRCode 在 SDK 返回验证 URL 时被调用(通常一次)。
	// url 是用户需在飞书里打开的链接;expireIn 是过期秒数。
	OnQRCode func(url string, expireIn int)

	// Addons 可选,预填权限/事件/回调(默认 nil = 用平台基础模板)。
	// 长连接事件订阅需要订阅 im.message.receive_v1,这里建议预填。
	Addons *registration.AppAddons

	// CreateOnly true = 只允许新建应用,不允许选已有应用(默认 true)。
	CreateOnly bool
}

// Registration 封装飞书 Device Flow。sdkFunc 字段是为了测试注入;
// 默认值(由 NewRegistration 设置)调真实的 registration.RegisterApp。
type Registration struct {
	sdkFunc func(ctx context.Context, onQR func(url string, expireIn int)) (appID, appSecret string, err error)
}

// NewRegistration 构造一个使用真实 SDK 的 Registration。
// 测试用例通过直接给 Registration.sdkFunc 赋值注入 fake。
func NewRegistration() *Registration {
	return &Registration{sdkFunc: realSDKRegisterApp}
}

// realSDKRegisterApp 是 registration.RegisterApp 的薄封装,把 SDK 回调
// 翻译成 Pieqi 风格的回调签名(只暴露 URL + 过期秒数,屏蔽 SDK 内部细节)。
func realSDKRegisterApp(ctx context.Context, onQR func(url string, expireIn int)) (string, string, error) {
	// 10 分钟超时兜底:用户扫码可能慢,但无限阻塞也不合理
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	result, err := registration.RegisterApp(ctx, &registration.Options{
		OnQRCode: func(info *registration.QRCodeInfo) {
			if onQR != nil && info != nil {
				onQR(info.URL, info.ExpireIn)
			}
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("register app: %w", err)
	}
	if result == nil {
		return "", "", fmt.Errorf("register app: nil result")
	}
	return result.ClientID, result.ClientSecret, nil
}

// Run 启动 Device Flow,阻塞直到成功/失败/ctx 取消。
// 成功时返回 AppID + AppSecret,err=nil。
func (r *Registration) Run(ctx context.Context, opts Options) (Result, error) {
	if r.sdkFunc == nil {
		return Result{}, fmt.Errorf("sdkFunc not configured")
	}
	id, sec, err := r.sdkFunc(ctx, opts.OnQRCode)
	if err != nil {
		return Result{}, err
	}
	return Result{AppID: id, AppSecret: sec}, nil
}
