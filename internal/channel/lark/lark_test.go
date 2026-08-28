package lark

import (
	"context"
	"testing"
	"time"

	"pieqi/internal/model"
)

// TestAdapter_LongConnMode_StartRequiresAppID 验证 NewLongConn 构造的
// Adapter 处于 longconn 模式:Start 在缺少 app_id 时立即返回错误(说明
// Start 真的进入了长连接分支,而不是 webhook 模式的 no-op)。
func TestAdapter_LongConnMode_StartRequiresAppID(t *testing.T) {
	a := NewLongConn("", "") // 空 app_id → Start 应返回错误
	err := a.Start(context.Background())
	if err == nil {
		t.Fatal("Start with empty app_id should error in longconn mode")
	}
}

// TestAdapter_WebhookMode_StartIsNoop 验证 webhook 模式 Start 保持 no-op
// (回归保护:不能因为加 longconn 把 webhook 模式搞坏)。
func TestAdapter_WebhookMode_StartIsNoop(t *testing.T) {
	a := New("app", "secret", "verify", "encrypt")
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("webhook mode Start should be no-op, got err: %v", err)
	}
}

// TestAdapter_OnMessageRegistered 验证 OnMessage 回调可以注册并被调用。
func TestAdapter_OnMessageRegistered(t *testing.T) {
	a := New("app", "secret", "verify", "encrypt")
	called := false
	a.OnMessage(func(msg model.Message) { called = true })
	if a.onMessage == nil {
		t.Fatal("onMessage callback not set")
	}
	a.onMessage(model.Message{})
	if !called {
		t.Fatal("onMessage callback not invoked")
	}
}

// TestAdapter_SetConfig_UpdatesFieldsAndClearsToken 验证 SetConfig 更新凭据/
// 接入方式并清空缓存的 tenant_access_token（强制下次用新凭据刷新）。
func TestAdapter_SetConfig_UpdatesFieldsAndClearsToken(t *testing.T) {
	a := New("old_app", "old_secret", "old_vt", "old_ek")

	// 预置一个假 token 缓存
	a.tokenMu.Lock()
	a.accessToken = "cached_token"
	a.tokenExpiry = time.Now().Add(time.Hour)
	a.tokenMu.Unlock()

	a.SetConfig("new_app", "new_secret", "new_vt", "new_ek", "longconn")

	appID, appSecret, verifyToken, encryptKey, eventMode := a.configSnapshot()
	if appID != "new_app" || appSecret != "new_secret" ||
		verifyToken != "new_vt" || encryptKey != "new_ek" || eventMode != "longconn" {
		t.Fatalf("fields not updated: app=%s sec=%s vt=%s ek=%s mode=%s",
			appID, appSecret, verifyToken, encryptKey, eventMode)
	}

	a.tokenMu.RLock()
	tok := a.accessToken
	exp := a.tokenExpiry
	a.tokenMu.RUnlock()
	if tok != "" {
		t.Fatalf("accessToken not cleared, got %q", tok)
	}
	if !exp.IsZero() {
		t.Fatalf("tokenExpiry not cleared, got %v", exp)
	}
}
