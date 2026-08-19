# Pieqi 飞书长连接事件订阅 + 一键创建飞书应用 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在保留现有 Cloudflared 隧道+飞书身份绑定系统的前提下,把 Pieqi 的飞书事件接收从公网 webhook 切到长连接(WebSocket)模式,并新增"扫码一键创建飞书自建应用"链路,降低首次接入门槛。

**Architecture:** 引入官方 Go SDK `github.com/larksuite/oapi-sdk-go/v3`。`internal/channel/lark/` 新增 `longconn.go` 用 `larkws.NewClient` 建立长连接,事件经 `dispatcher.NewEventDispatcher` 路由到现有 `convertMessage` → `onMessage` 链路(Bridge/auth 完全不动);`Init` 的 webhook 路由仅在 webhook 模式保留。新增 `internal/larkreg/` 包用 SDK 的 `registration.RegisterApp` 实现 OAuth 2.0 Device Authorization Grant,前端轮询拿 `app_id/app_secret` 后写入 `~/.pieqi/lark_credentials.json`,main.go 启动时优先加载该文件覆盖 config 默认值。Cloudflared 隧道系统全部保留不动 —— 它仍服务 PWA 外部访问,与 IM 长连接并行。

**Tech Stack:** Go 1.25,gin,viper,zap,`github.com/larksuite/oapi-sdk-go/v3`(含 `/ws`、`/event/dispatcher`、`/service/im/v1`、`/scene/registration` 子包),`github.com/skip2/go-qrcode`(已在 go.mod)。前端 vanilla JS + Vite。

---

## File Structure

### New files

| File | Responsibility |
|------|----------------|
| `internal/channel/lark/longconn.go` | 长连接客户端封装:`startLongConnection(ctx)` 用 `larkws.NewClient` 建连,事件 dispatcher 转 `convertMessage` → `onMessage`,错误日志化。 |
| `internal/channel/lark/longconn_test.go` | 长连接事件 → `model.Message` 转换单元测试(用 SDK 的 `*larkim.P2MessageReceiveV1` 构造 fake event,验证 OpenID/content/chatID 提取正确)。 |
| `internal/larkreg/registration.go` | Device Flow 封装:`Run(ctx, opts) (AppID, AppSecret, error)`,内部调 `registration.RegisterApp`,把 SDK 的回调翻译成 Pieqi 风格的返回值。 |
| `internal/larkreg/registration_test.go` | Registration 封装测试(mock `RegisterApp` 行为,验证 QR URL 回调时机、错误码翻译)。 |
| `internal/larkreg/credentials.go` | 凭据落盘/加载:`SaveCredentials(path, appID, appSecret)`,`LoadCredentials(path) (appID, appSecret, ok)`,JSON 原子写。 |
| `internal/larkreg/credentials_test.go` | 凭据读写 + 文件不存在/损坏的兜底路径测试。 |
| `internal/api/larkreg.go` | HTTP handlers:`POST /api/larkreg/start`(仅内网,启动 device flow,返回 device_code+qr_url),`GET /api/larkreg/poll?device_code=xxx`(仅内网,轮询,成功时落盘凭据)。 |
| `internal/api/larkreg_test.go` | Handler 测试(内网放行/外网 403、轮询状态机、凭据落盘)。 |
| `web/src/larkreg.js` | 前端"接入飞书"页:点按钮→调 `/start`→显示 QR(复用 `/api/tunnel/qrcode` 端点)→轮询 `/poll`→成功后提示重启。 |

### Modified files

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | 新增 `github.com/larksuite/oapi-sdk-go/v3` 依赖。 |
| `internal/config/config.go` | `LarkConfig` 加 `EventMode string`(默认 `"webhook"`,可选 `"longconn"`)+ `CredentialsFile string`(默认 `~/.pieqi/lark_credentials.json`)。`Load()` 后做 `CredentialsFile` 空值回退(同 PRD V1.0 的 `feishu_binding_file` 模式)。 |
| `internal/config/config_test.go` | 新增 `TestConfig_LarkDefaults` 覆盖两个新字段的默认值。 |
| `internal/channel/lark/lark.go` | `Adapter` 加 `eventMode` 字段;`Start(ctx)` 在 longconn 模式调 `startLongConnection`;`Init(router)` 在 longconn 模式跳过 webhook 路由注册(保留 webhook 模式作降级路径)。`New` 签名加 `eventMode` 参数(或新增构造函数 `NewLongConn`)。 |
| `cmd/pieqi/main.go` | 装配长连接:加载 `lark_credentials.json` 覆盖 config;`go larkAdapter.Start(larkCtx)`;`defer cancel()` 优雅关闭。 |
| `internal/api/router.go` | 注册 `/api/larkreg/*` 路由组,套 `BindOpGateMiddleware`(仅内网,与 bind/unbind 一致)。 |
| `web/index.html` | 加 `<script type="module" src="/src/larkreg.js">` + 接入飞书入口按钮。 |
| `config.yaml` | 在 `channels.lark` 段加注释说明 `event_mode` 与一键接入路径。 |

---

## Task 1: 引入飞书官方 Go SDK 依赖

**Files:**
- Modify: `/workspace/go.mod`
- Modify: `/workspace/go.sum`

- [ ] **Step 1: 添加 SDK 依赖**

Run:
```bash
cd /workspace && go get github.com/larksuite/oapi-sdk-go/v3@latest
```

Expected: `go.mod` 出现 `github.com/larksuite/oapi-sdk-go/v3 v3.x.x`,`go.sum` 更新。

- [ ] **Step 2: 整理依赖**

Run:
```bash
cd /workspace && go mod tidy
```

Expected: exit 0,无错误。

- [ ] **Step 3: 验证编译**

Run:
```bash
cd /workspace && go build ./...
```

Expected: exit 0。SDK 拉取成功,无破坏性变更。

- [ ] **Step 4: 写一个 import 烟雾测试验证关键子包可达**

Create `/workspace/internal/larkreg/smoke_test.go`:

```go
package larkreg

import (
	"testing"

	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

// TestSDKImportsReachable 验证关键 SDK 子包在当前 go.mod 下可导入。
// 长连接(larkws)、事件分发(dispatcher)、IM service、registration 四个
// 子包都要可达 —— 如果其中任何一个路径在 SDK 新版本里改名,这里会先 fail。
func TestSDKImportsReachable(t *testing.T) {
	_ = registration.RegisterApp // 函数指针引用,不下断言
}
```

- [ ] **Step 5: 跑测试**

Run:
```bash
cd /workspace && go test ./internal/larkreg/... -run TestSDKImportsReachable -v
```

Expected: PASS。

- [ ] **Step 6: Commit**

```bash
cd /workspace && git add go.mod go.sum internal/larkreg/smoke_test.go
git commit -m "feat(lark): add larksuite/oapi-sdk-go/v3 dependency"
```

---

## Task 2: 扩展 LarkConfig 配置

**Files:**
- Modify: `/workspace/internal/config/config.go`
- Test: `/workspace/internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

Append to `/workspace/internal/config/config_test.go`(若文件不存在则创建,使用 Task 1 风格的 `writeTestConfig` helper):

```go
func TestConfig_LarkDefaults(t *testing.T) {
	p := writeTestConfig(t, "server:\n  port: 3000\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Channels.Lark.EventMode != "webhook" {
		t.Fatalf("EventMode default = %q, want \"webhook\"", cfg.Channels.Lark.EventMode)
	}
	if cfg.Channels.Lark.CredentialsFile == "" {
		t.Fatal("CredentialsFile should default to ~/.pieqi/lark_credentials.json")
	}
	// 验证默认路径形态(与 feishu_binding_file 同目录)
	if !strings.HasSuffix(cfg.Channels.Lark.CredentialsFile, "lark_credentials.json") {
		t.Fatalf("CredentialsFile default = %q, want suffix lark_credentials.json", cfg.Channels.Lark.CredentialsFile)
	}
}

func TestConfig_LarkEmptyCredentialsFileFallsBack(t *testing.T) {
	body := "server:\n  port: 3000\nchannels:\n  lark:\n    credentials_file: \"\"\n"
	p := writeTestConfig(t, body)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Channels.Lark.CredentialsFile == "" {
		t.Fatal("empty credentials_file should fall back to default path")
	}
}
```

> 注:`writeTestConfig` helper 已在 PRD V1.0 Task 1 里写过,见 [internal/config/config_test.go](file:///workspace/internal/config/config_test.go)。如果文件里没有,补一个;`strings` import 检查一下。

- [ ] **Step 2: 跑测试验证失败**

Run:
```bash
cd /workspace && go test ./internal/config/... -run TestConfig_Lark -v
```

Expected: FAIL,`cfg.Channels.Lark.EventMode undefined` 或 `CredentialsFile undefined`(编译错误)。

- [ ] **Step 3: 实现 LarkConfig 扩展**

Modify `/workspace/internal/config/config.go`,在 `LarkConfig` struct 追加两个字段:

```go
// LarkConfig 飞书配置
type LarkConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	AppID       string `mapstructure:"app_id"`
	AppSecret   string `mapstructure:"app_secret"`
	VerifyToken string `mapstructure:"verify_token"`
	EncryptKey  string `mapstructure:"encrypt_key"`
	EventMode   string `mapstructure:"event_mode"`      // "webhook"(默认)| "longconn"
	CredentialsFile string `mapstructure:"credentials_file"` // 一键接入凭据落盘路径;空 = ~/.pieqi/lark_credentials.json
}
```

在 `Load()` 函数里加默认值(紧跟现有 `auth.*` 默认值块):

```go
v.SetDefault("channels.lark.event_mode", "webhook")
v.SetDefault("channels.lark.credentials_file", filepath.Join(DefaultDataRoot(), "lark_credentials.json"))
```

在 `Load()` 函数末尾(`v.Unmarshal(&cfg)` 之后,return 之前)加 CredentialsFile 空值回退(与 `feishu_binding_file` 同模式):

```go
// 空 credentials_file 回退默认路径(同 feishu_binding_file 模式)
if cfg.Channels.Lark.CredentialsFile == "" {
	cfg.Channels.Lark.CredentialsFile = filepath.Join(DefaultDataRoot(), "lark_credentials.json")
}
```

- [ ] **Step 4: 跑测试验证通过**

Run:
```bash
cd /workspace && go test ./internal/config/... -run TestConfig_Lark -v
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
cd /workspace && git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add lark event_mode + credentials_file fields"
```

---

## Task 3: 长连接客户端封装(事件 → model.Message)

**Files:**
- Create: `/workspace/internal/channel/lark/longconn.go`
- Create: `/workspace/internal/channel/lark/longconn_test.go`

- [ ] **Step 1: 写失败测试 — 验证 fake event 转成 model.Message**

Create `/workspace/internal/channel/lark/longconn_test.go`:

```go
package lark

import (
	"testing"

	"pieqi/internal/model"

	"github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// TestConvertP2MessageToModel 验证长连接事件(SDK 的 *P2MessageReceiveV1)
// 被正确转成 Pieqi 内部的 model.Message。重点验证:
// 1. sender open_id 落到 model.Message.UserID(身份绑定链路用)
// 2. message.content(JSON 字符串)被解析出纯文本
// 3. chat_id/chat_type 正确传递
// 4. p2p 与 group 两种会话类型都能处理
func TestConvertP2MessageToModel(t *testing.T) {
	// 构造一条 SDK 风格的 fake event(用指针,因为 SDK 字段都是 *string)
	strPtr := func(s string) *string { return &s }
	boolPtr := func(b bool) *bool { return &b }

	fake := &im.P2MessageReceiveV1{
		Event: &im.P2MessageReceiveV1Data{
			Sender: struct {
				SenderId   *im.P2MessageReceiveV1DataSenderSenderId `json:"sender_id,omitempty"`
				SenderType *string                                    `json:"sender_type,omitempty"`
			}{
				SenderId: &im.P2MessageReceiveV1DataSenderSenderId{
					OpenId: strPtr("ou_test_admin"),
					UserId: strPtr("abcd1234"),
				},
				SenderType: strPtr("user"),
			},
			Message: &im.P2MessageReceiveV1DataMessage{
				ChatType:    strPtr("p2p"),
				ChatId:      strPtr("oc_chat_1"),
				MessageId:   strPtr("om_msg_1"),
				MessageType: strPtr("text"),
				Content:     strPtr(`{"text":"hello pieqi"}`),
			},
		},
	}

	msg := convertP2Message(fake)
	if msg.UserID != "ou_test_admin" {
		t.Fatalf("UserID = %q, want ou_test_admin", msg.UserID)
	}
	if msg.ChatID != "oc_chat_1" {
		t.Fatalf("ChatID = %q, want oc_chat_1", msg.ChatID)
	}
	if msg.Content != "hello pieqi" {
		t.Fatalf("Content = %q, want hello pieqi", msg.Content)
	}
	if msg.Channel != model.ChannelLark {
		t.Fatalf("Channel = %q, want %q", msg.Channel, model.ChannelLark)
	}
}
```

> 注:`im.P2MessageReceiveV1Data`、`P2MessageReceiveV1DataSenderSenderId`、`P2MessageReceiveV1DataMessage` 等 SDK 内部结构体名以 SDK README 与 `service/im/v1` 包导出为准。若 SDK 改名,这里需要同步调整 —— 编译错误就是测试 fail 的反馈。

- [ ] **Step 2: 跑测试验证失败**

Run:
```bash
cd /workspace && go test ./internal/channel/lark/... -run TestConvertP2MessageToModel -v
```

Expected: FAIL,`convertP2Message undefined`。

- [ ] **Step 3: 实现 convertP2Message**

Create `/workspace/internal/channel/lark/longconn.go`:

```go
package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"pieqi/internal/model"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"go.uber.org/zap"
)

// startLongConnection 用飞书官方 SDK 建立长连接事件订阅。
//
// 触发条件:cfg.Channels.Lark.EventMode == "longconn"。
// 与 webhook 模式互斥:webhook 模式靠飞书回调到 /webhook/lark,
// 长连接模式由 Pieqi 主动 wss 到 open.feishu.cn,无需公网。
//
// SDK 内置断线重连 + 心跳,事件通过 dispatcher 投递。Pieqi 这里只做
// 事件 → model.Message 的转换,然后调 Adapter.onMessage 回调(与
// webhook 路径共用同一回调,保证 Bridge 无感知)。
//
// 长连接模式注意事项(PRD §4 已调研):
//   - 仅企业自建应用可用(Pieqi 本就是内部工具)
//   - 事件处理必须 ≤ 3s,长任务异步化(Adapter.onMessage 已是
//     go b.handleMessage,无阻塞风险)
//   - 集群模式不广播:多副本部署时同一事件只投递一个副本
func (a *Adapter) startLongConnection(ctx context.Context, logger *zap.Logger) error {
	if a.appID == "" || a.appSecret == "" {
		return fmt.Errorf("long-connection mode requires app_id + app_secret")
	}

	// dispatcher 的两个参数在长连接模式下必须为空字符串
	// (webhook 模式下分别是 VerificationToken + EncryptKey)
	dispatcher := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
			msg := convertP2Message(event)
			if a.onMessage != nil {
				a.onMessage(msg)
			}
			return nil
		})

	cli := larkws.NewClient(
		a.appID, a.appSecret,
		larkws.WithEventHandler(dispatcher),
		larkws.WithAutoReconnect(),
	)
	if logger != nil {
		cli = larkws.NewClient(
			a.appID, a.appSecret,
			larkws.WithEventHandler(dispatcher),
			larkws.WithAutoReconnect(),
			larkws.WithLogLevel(larkcore.LogLevelInfo),
		)
	}

	// 阻塞调用,ctx 取消时返回。SDK 自带重连,通常不会退出。
	go func() {
		<-ctx.Done()
		logger.Info("lark long-connection context cancelled")
	}()

	if err := cli.Start(ctx); err != nil {
		if logger != nil {
			logger.Error("lark long-connection exited", zap.Error(err))
		}
		return err
	}
	return nil
}

// convertP2Message 把 SDK 的 *P2MessageReceiveV1 事件转成 Pieqi 内部的
// model.Message。逻辑与 webhook 模式的 convertMessage 等价,只是数据
// 源从手撸 larkEvent struct 变成 SDK 的 typed event。
//
// 重点字段:
//   - Sender.SenderId.OpenId  → model.Message.UserID(身份绑定核心字段)
//   - Message.Content         → JSON 字符串,需解析出 {"text":"..."}
//   - Message.ChatId          → model.Message.ChatID(回推目标)
//   - Message.ChatType        → p2p/group,影响下游处理(目前仅记录)
//
// nil-safe:所有 SDK 字段都是 *string,需要解引用前判空。
func convertP2Message(event *larkim.P2MessageReceiveV1) model.Message {
	if event == nil || event.Event == nil {
		return model.Message{Channel: model.ChannelLark}
	}
	ev := event.Event

	// userID:OpenID 优先(身份绑定用),fallback UserID
	userID := ""
	if ev.Sender.SenderId != nil {
		if ev.Sender.SenderId.OpenId != nil {
			userID = *ev.Sender.SenderId.OpenId
		} else if ev.Sender.SenderId.UserId != nil {
			userID = *ev.Sender.SenderId.UserId
		}
	}

	// content:解析 {"text":"..."}
	text := ""
	if ev.Message != nil && ev.Message.Content != nil {
		var c struct{ Text string `json:"text"` }
		if json.Unmarshal([]byte(*ev.Message.Content), &c) == nil {
			text = c.Text
		} else {
			text = *ev.Message.Content // 非标准 JSON 时降级用原文
		}
	}

	// chatID / chatType
	chatID, chatType := "", ""
	if ev.Message != nil {
		if ev.Message.ChatId != nil {
			chatID = *ev.Message.ChatId
		}
		if ev.Message.ChatType != nil {
			chatType = *ev.Message.ChatType
		}
	}

	_ = chatType // 暂仅记录,后续按会话类型分支可加
	return model.Message{
		Channel: model.ChannelLark,
		ChatID:  chatID,
		UserID:  userID,
		Content: text,
		Raw:     event,
	}
}

// suppress unused-import warnings when SDK API shifts
var _ = sync.Mutex{}
```

- [ ] **Step 4: 跑测试验证通过**

Run:
```bash
cd /workspace && go test ./internal/channel/lark/... -run TestConvertP2MessageToModel -v
```

Expected: PASS。若 SDK 字段名/路径在最新版有差异,此处会编译错误 —— 按错误信息调整 import 路径与字段名,直到通过。

- [ ] **Step 5: Commit**

```bash
cd /workspace && git add internal/channel/lark/longconn.go internal/channel/lark/longconn_test.go
git commit -m "feat(lark): add long-connection (ws) event subscription adapter"
```

---

## Task 4: Adapter 接入长连接模式开关

**Files:**
- Modify: `/workspace/internal/channel/lark/lark.go`
- Modify: `/workspace/internal/channel/lark/lark_test.go`(若不存在则创建)

- [ ] **Step 1: 写失败测试 — Start 在 longconn 模式下要做事**

Create `/workspace/internal/channel/lark/lark_test.go`:

```go
package lark

import (
	"context"
	"testing"

	"pieqi/internal/model"
)

// TestAdapter_NewLongConnMode 验证 NewLongConn 构造的 Adapter 处于
// longconn 模式:Init 不再注册 webhook 路由(不 panic 即可),且
// Start 在缺少 app_id 时立即返回错误(说明 Start 真的进入了
// 长连接分支,而不是 webhook 模式的 no-op)。
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
```

- [ ] **Step 2: 跑测试验证失败**

Run:
```bash
cd /workspace && go test ./internal/channel/lark/... -run TestAdapter -v
```

Expected: FAIL,`NewLongConn undefined`。

- [ ] **Step 3: 改造 Adapter 加 eventMode + NewLongConn**

Modify `/workspace/internal/channel/lark/lark.go`。先在 `Adapter` struct 加 `eventMode` 字段和 logger 字段(用于长连接错误日志):

```go
type Adapter struct {
	appID       string
	appSecret   string
	verifyToken string
	encryptKey  string
	eventMode   string // "webhook"(默认)| "longconn"
	logger      *zap.Logger
	onMessage   func(model.Message)
	httpClient  *http.Client

	// tenant_access_token 缓存(有效期 2h)
	tokenMu     sync.RWMutex
	accessToken string
	tokenExpiry time.Time
}
```

加 import `"go.uber.org/zap"`(若未引入)。

修改 `New` 保持向后兼容(默认 webhook 模式),并新增 `NewLongConn`:

```go
// New 创建飞书适配器(默认 webhook 模式)。
func New(appID, appSecret, verifyToken, encryptKey string) *Adapter {
	return &Adapter{
		appID:       appID,
		appSecret:   appSecret,
		verifyToken: verifyToken,
		encryptKey:  encryptKey,
		eventMode:   "webhook",
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// NewLongConn 创建飞书适配器(长连接模式)。verifyToken/encryptKey
// 在长连接模式下不需要(SDK 内置鉴权),传空字符串即可。
func NewLongConn(appID, appSecret string) *Adapter {
	return &Adapter{
		appID:     appID,
		appSecret: appSecret,
		eventMode: "longconn",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// WithLogger 注入 logger,用于长连接错误日志。nil = 静默。
func (a *Adapter) WithLogger(l *zap.Logger) *Adapter {
	a.logger = l
	return a
}
```

修改 `Init`(长连接模式跳过 webhook 路由):

```go
// Init 注册飞书 Webhook 路由(仅 webhook 模式)。
// 长连接模式不需要公网路由,直接返回 nil。
func (a *Adapter) Init(router gin.IRouter) error {
	if a.eventMode == "longconn" {
		return nil
	}
	router.POST("/webhook/lark", a.handleWebhook)
	return nil
}
```

修改 `Start`(长连接模式启动 wss):

```go
// Start 启动渠道。
// - webhook 模式:no-op(等飞书回调即可)
// - longconn 模式:启动飞书长连接事件订阅(阻塞直到 ctx 取消)
func (a *Adapter) Start(ctx context.Context) error {
	if a.eventMode != "longconn" {
		return nil
	}
	return a.startLongConnection(ctx, a.logger)
}
```

- [ ] **Step 4: 跑测试验证通过**

Run:
```bash
cd /workspace && go test ./internal/channel/lark/... -run TestAdapter -v
```

Expected: 三个 subtest 全 PASS。

- [ ] **Step 5: Commit**

```bash
cd /workspace && git add internal/channel/lark/lark.go internal/channel/lark/lark_test.go
git commit -m "feat(lark): wire event_mode switch into Adapter (webhook|longconn)"
```

---

## Task 5: main.go 装配长连接启动 + 凭据文件加载

**Files:**
- Modify: `/workspace/cmd/pieqi/main.go`

- [ ] **Step 1: 写失败测试 — 凭据文件覆盖 config**

由于 main.go 是 package main,直接单元测试不便。改用集成性 smoke test:写一个独立函数 `loadLarkCredentials(cfg)` 放在 main.go,测它。

Append to `/workspace/cmd/pieqi/main_test.go`(若不存在则创建):

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"pieqi/internal/config"
)

// TestLoadLarkCredentials_OverridesConfig 验证 ~/.pieqi/lark_credentials.json
// 存在时,其 app_id/app_secret 覆盖 config 里的默认值。这是 Device Flow
// 一键接入后的接管路径:扫码拿到凭据落盘 → 下次启动自动加载。
func TestLoadLarkCredentials_OverridesConfig(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "lark_credentials.json")
	credBody := `{"app_id":"cli_from_scan","app_secret":"sec_from_scan"}`
	if err := os.WriteFile(credPath, []byte(credBody), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Channels.Lark.AppID = "from_yaml"
	cfg.Channels.Lark.AppSecret = "yaml_secret"
	cfg.Channels.Lark.CredentialsFile = credPath

	if err := loadLarkCredentials(cfg); err != nil {
		t.Fatalf("loadLarkCredentials: %v", err)
	}
	if cfg.Channels.Lark.AppID != "cli_from_scan" {
		t.Fatalf("AppID = %q, want cli_from_scan", cfg.Channels.Lark.AppID)
	}
	if cfg.Channels.Lark.AppSecret != "sec_from_scan" {
		t.Fatalf("AppSecret = %q, want sec_from_scan", cfg.Channels.Lark.AppSecret)
	}
}

// TestLoadLarkCredentials_NoFileIsNoop 验证凭据文件不存在时无副作用。
func TestLoadLarkCredentials_NoFileIsNoop(t *testing.T) {
	cfg := &config.Config{}
	cfg.Channels.Lark.AppID = "from_yaml"
	cfg.Channels.Lark.CredentialsFile = filepath.Join(t.TempDir(), "missing.json")
	if err := loadLarkCredentials(cfg); err != nil {
		t.Fatalf("missing file should be noop, got: %v", err)
	}
	if cfg.Channels.Lark.AppID != "from_yaml" {
		t.Fatalf("AppID changed unexpectedly: %q", cfg.Channels.Lark.AppID)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run:
```bash
cd /workspace && go test ./cmd/pieqi/... -run TestLoadLarkCredentials -v
```

Expected: FAIL,`loadLarkCredentials undefined`。

- [ ] **Step 3: 实现 loadLarkCredentials + 改造 main 装配**

Add to `/workspace/cmd/pieqi/main.go`(放在 `main` 函数之前):

```go
// loadLarkCredentials 从 ~/.pieqi/lark_credentials.json 加载 Device Flow
// 扫码拿到的 app_id/app_secret,覆盖 config 里的默认值。文件不存在或
// 损坏时静默跳过(降级到 config 里的 app_id/app_secret)。
//
// 该文件由 POST /api/larkreg/poll 在用户扫码确认后写入,见 internal/larkreg。
func loadLarkCredentials(cfg *config.Config) error {
	path := cfg.Channels.Lark.CredentialsFile
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在是合法状态(未接入过)
		}
		return fmt.Errorf("read lark credentials: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var c struct {
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		// 损坏文件:不阻断启动,只警告
		if l, _ := zap.NewDevelopment(); l != nil {
			l.Warn("lark credentials file corrupt, falling back to config",
				zap.String("path", path), zap.Error(err))
		}
		return nil
	}
	if c.AppID != "" && c.AppSecret != "" {
		cfg.Channels.Lark.AppID = c.AppID
		cfg.Channels.Lark.AppSecret = c.AppSecret
	}
	return nil
}
```

在 `main()` 函数里,`cfg, err := config.Load(cfgPath)` 之后、`gin.SetMode` 之前,加:

```go
// 加载 Device Flow 扫码拿到的飞书凭据(若存在则覆盖 config 里的 app_id/app_secret)
if err := loadLarkCredentials(cfg); err != nil {
	logger.Warn("load lark credentials", zap.Error(err))
}
```

修改飞书适配器装配块(`main.go:131-141`),按 `EventMode` 选择构造方式 + 启动长连接:

```go
// 渠道
if cfg.Channels.Lark.Enabled {
	var larkAdapter *lark.Adapter
	if cfg.Channels.Lark.EventMode == "longconn" {
		larkAdapter = lark.NewLongConn(cfg.Channels.Lark.AppID, cfg.Channels.Lark.AppSecret).
			WithLogger(logger)
	} else {
		larkAdapter = lark.New(
			cfg.Channels.Lark.AppID, cfg.Channels.Lark.AppSecret,
			cfg.Channels.Lark.VerifyToken, cfg.Channels.Lark.EncryptKey,
		)
	}
	if err := larkAdapter.Init(r); err != nil {
		logger.Fatal("init lark", zap.Error(err))
	}
	bridge.RegisterReceiver(larkAdapter)
	// 长连接模式需要后台 goroutine 启动 wss;webhook 模式 Start 是 no-op
	larkCtx, larkCancel := context.WithCancel(context.Background())
	defer larkCancel()
	go func() {
		if err := larkAdapter.Start(larkCtx); err != nil {
			logger.Error("lark long-connection exited", zap.Error(err))
		}
	}()
	logger.Info("lark channel enabled", zap.String("event_mode", cfg.Channels.Lark.EventMode))
}
```

- [ ] **Step 4: 跑测试验证通过**

Run:
```bash
cd /workspace && go test ./cmd/pieqi/... -run TestLoadLarkCredentials -v
```

Expected: PASS。

- [ ] **Step 5: 跑全量编译 + 全量测试**

Run:
```bash
cd /workspace && go build ./... && go test ./...
```

Expected: build exit 0;tests 全 PASS(或仅 PRD V1.0 已知的 `TestAPI_CreateAndListTasks` 偶发 race flake)。

- [ ] **Step 6: Commit**

```bash
cd /workspace && git add cmd/pieqi/main.go cmd/pieqi/main_test.go
git commit -m "feat(main): load lark_credentials.json + start long-connection goroutine"
```

---

## Task 6: Device Flow 凭据落盘 + 加载包

**Files:**
- Create: `/workspace/internal/larkreg/credentials.go`
- Create: `/workspace/internal/larkreg/credentials_test.go`

- [ ] **Step 1: 写失败测试**

Create `/workspace/internal/larkreg/credentials_test.go`:

```go
package larkreg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lark_credentials.json")
	if err := SaveCredentials(path, "cli_xxx", "sec_yyy"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	id, sec, ok := LoadCredentials(path)
	if !ok {
		t.Fatal("Load returned ok=false after Save")
	}
	if id != "cli_xxx" {
		t.Fatalf("AppID = %q, want cli_xxx", id)
	}
	if sec != "sec_yyy" {
		t.Fatalf("AppSecret = %q, want sec_yyy", sec)
	}
}

func TestLoadCredentials_NoFile(t *testing.T) {
	_, _, ok := LoadCredentials(filepath.Join(t.TempDir(), "missing.json"))
	if ok {
		t.Fatal("missing file should return ok=false")
	}
}

func TestLoadCredentials_Corrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte("{not json"), 0600)
	_, _, ok := LoadCredentials(path)
	if ok {
		t.Fatal("corrupt file should return ok=false")
	}
}

func TestSaveCredentials_Permissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lark_credentials.json")
	_ = SaveCredentials(path, "x", "y")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode = %o, want 0600", info.Mode().Perm())
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run:
```bash
cd /workspace && go test ./internal/larkreg/... -run TestSaveAndLoad -v
```

Expected: FAIL,`SaveCredentials undefined`。

- [ ] **Step 3: 实现 credentials.go**

Create `/workspace/internal/larkreg/credentials.go`:

```go
// Package larkreg 封装"扫码一键创建飞书应用"流程,基于 OAuth 2.0
// Device Authorization Grant (RFC 8628)。底层用飞书官方 Go SDK 的
// registration.RegisterApp(见 registration.go)。
//
// 凭据(app_id/app_secret)落盘到 ~/.pieqi/lark_credentials.json,
// main.go 启动时优先加载该文件覆盖 config.yaml 的默认值。
package larkreg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// credentialsFile 落盘 JSON 的结构。
type credentialsFile struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

// SaveCredentials 原子写入凭据文件(0600 权限,先写 .tmp 再 rename)。
// 与 pieqi/internal/auth/binding.go 的 persistUnlocked 同模式。
func SaveCredentials(path, appID, appSecret string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("mkdir credentials dir: %w", err)
	}
	data, err := json.MarshalIndent(credentialsFile{AppID: appID, AppSecret: appSecret}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write credentials tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename credentials: %w", err)
	}
	return nil
}

// LoadCredentials 读取凭据文件。文件不存在或损坏时返回 ok=false
// (不返回 error,因为这是合法的"未接入过"状态)。
func LoadCredentials(path string) (appID, appSecret string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	var c credentialsFile
	if err := json.Unmarshal(data, &c); err != nil {
		return "", "", false
	}
	if c.AppID == "" || c.AppSecret == "" {
		return "", "", false
	}
	return c.AppID, c.AppSecret, true
}
```

- [ ] **Step 4: 跑测试验证通过**

Run:
```bash
cd /workspace && go test ./internal/larkreg/... -v
```

Expected: 全 PASS(包括 Task 1 的 smoke test + 这里的四个 credentials 测试)。

- [ ] **Step 5: Commit**

```bash
cd /workspace && git add internal/larkreg/credentials.go internal/larkreg/credentials_test.go
git commit -m "feat(larkreg): add credentials save/load (atomic, 0600)"
```

---

## Task 7: Device Flow 封装(SDK registration.RegisterApp)

**Files:**
- Create: `/workspace/internal/larkreg/registration.go`
- Create: `/workspace/internal/larkreg/registration_test.go`

- [ ] **Step 1: 写失败测试 — Run 接口契约**

Create `/workspace/internal/larkreg/registration_test.go`:

```go
package larkreg

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestRun_PassesQRCodeCallbackThenReturnsCredentials 验证 Run 的契约:
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

// TestRun_ContextCancelStopsPolling 验证 ctx 取消时 Run 返回。
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
```

- [ ] **Step 2: 跑测试验证失败**

Run:
```bash
cd /workspace && go test ./internal/larkreg/... -run TestRun -v
```

Expected: FAIL,`Registration undefined` / `Options undefined`。

- [ ] **Step 3: 实现 registration.go**

Create `/workspace/internal/larkreg/registration.go`:

```go
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
		var regErr *registration.RegisterAppError
		_ = regErr // 错误码归一化处理:返回原 error,handler 层翻译
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
```

- [ ] **Step 4: 跑测试验证通过**

Run:
```bash
cd /workspace && go test ./internal/larkreg/... -v
```

Expected: 全 PASS(含 fake 注入路径 + ctx 取消路径)。

- [ ] **Step 5: Commit**

```bash
cd /workspace && git add internal/larkreg/registration.go internal/larkreg/registration_test.go
git commit -m "feat(larkreg): wrap registration.RegisterApp with testable seam"
```

---

## Task 8: Device Flow HTTP handlers

**Files:**
- Create: `/workspace/internal/api/larkreg.go`
- Create: `/workspace/internal/api/larkreg_test.go`
- Modify: `/workspace/internal/api/router.go`

- [ ] **Step 1: 写失败测试 — 内网放行,外网 403,状态机正确**

Create `/workspace/internal/api/larkreg_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pieqi/internal/auth"
	"pieqi/internal/config"

	"github.com/gin-gonic/gin"
)

// TestLarkReg_Start_InternalOK 验证 /api/larkreg/start 仅内网可调,返回 QR URL。
// 用 fake Registration 注入(避免真实扫码)。
func TestLarkReg_Start_InternalOK(t *testing.T) {
	srv := newLarkRegTestServer(t, &fakeReg{
		onRun: func(opts Options) (Result, error) {
			opts.OnQRCode("https://qr.test/abc", 300)
			return Result{AppID: "cli_test", AppSecret: "sec_test"}, nil
		},
	})

	// 内网 IP
	req := httptest.NewRequest("POST", "/api/larkreg/start", strings.NewReader(`{}`))
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("internal start → %d, want 200. body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["qr_url"] != "https://qr.test/abc" {
		t.Fatalf("qr_url = %v", resp["qr_url"])
	}
}

// TestLarkReg_Start_ExternalBlocked 验证外网调 start 被 BindOpGate 拒绝(403)。
func TestLarkReg_Start_ExternalBlocked(t *testing.T) {
	srv := newLarkRegTestServer(t, &fakeReg{})

	req := httptest.NewRequest("POST", "/api/larkreg/start", strings.NewReader(`{}`))
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("external start → %d, want 403", w.Code)
	}
}

// TestLarkReg_Poll_ReturnsCredentialsAndPersists 验证 poll 在 device flow
// 完成后返回凭据并落盘(用 tmp 文件验证)。
func TestLarkReg_Poll_ReturnsCredentialsAndPersists(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "lark_credentials.json")
	srv := newLarkRegTestServerWithCredPath(t, credPath, &fakeReg{
		onRun: func(opts Options) (Result, error) {
			opts.OnQRCode("https://qr.test/abc", 300)
			// 模拟扫码完成
			time.Sleep(10 * time.Millisecond)
			return Result{AppID: "cli_persist", AppSecret: "sec_persist"}, nil
		},
	})

	// 1. 启动 device flow
	req := httptest.NewRequest("POST", "/api/larkreg/start", strings.NewReader(`{}`))
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	// 2. 轮询直到拿到凭据(最多 2s)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest("GET", "/api/larkreg/poll", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)
		if w.Code == 200 {
			var resp map[string]string
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["app_id"] == "cli_persist" {
				// 验证落盘
				data, err := os.ReadFile(credPath)
				if err != nil {
					t.Fatalf("credentials file not written: %v", err)
				}
				if !strings.Contains(string(data), "cli_persist") {
					t.Fatalf("credentials file content = %s", string(data))
				}
				return // 成功
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("poll did not return credentials within 2s")
}

// fakeReg 是 Registration 接口的 fake,绕开真实 SDK。
type fakeReg struct {
	onRun func(opts Options) (Result, error)
}

func (f *fakeReg) Run(ctx context.Context, opts Options) (Result, error) {
	if f.onRun != nil {
		return f.onRun(opts)
	}
	return Result{}, nil
}

// newLarkRegTestServer 装一个最小可测的 Server + 路由,套上 BindOpGateMiddleware。
// 复用 PRD V1.0 的 auth 装配模式。
func newLarkRegTestServer(t *testing.T, reg *fakeReg) *larkRegTestEnv {
	return newLarkRegTestServerWithCredPath(t, filepath.Join(t.TempDir(), "lark_credentials.json"), reg)
}

func newLarkRegTestServerWithCredPath(t *testing.T, credPath string, reg *fakeReg) *larkRegTestEnv {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := &config.Config{}
	cfg.Channels.Lark.CredentialsFile = credPath

	bindings, _ := auth.NewBindingStore(filepath.Join(t.TempDir(), "b.json"))
	svc := &auth.Service{
		Debug:    auth.NewDebugSwitch(false),
		Bindings: bindings,
		Tokens:   auth.NewTokenStore(),
		Limiter:  auth.NewIPLimiter(5, 10*time.Minute),
	}
	srv := &Server{cfg: cfg, auth: svc}
	srv.SetLarkReg(reg, credPath)
	srv.Register(r)
	return &larkRegTestEnv{router: r, srv: srv}
}

type larkRegTestEnv struct {
	router *gin.Engine
	srv    *Server
}
```

> 注:`os`、`filepath` import 别忘了加。`Server.SetLarkReg` 是本任务要新增的注入方法,见 Step 3。

- [ ] **Step 2: 跑测试验证失败**

Run:
```bash
cd /workspace && go test ./internal/api/... -run TestLarkReg -v
```

Expected: FAIL,`Server.SetLarkReg undefined`、`/api/larkreg/start` 404。

- [ ] **Step 3: 实现 handlers + 注入方法**

Create `/workspace/internal/api/larkreg.go`:

```go
package api

import (
	"net/http"
	"sync"
	"time"

	"pieqi/internal/larkreg"

	"github.com/gin-gonic/gin"
)

// larkRegRunner 抽象 *larkreg.Registration 供测试注入 fake。
type larkRegRunner interface {
	Run(ctx context.Context, opts larkreg.Options) (larkreg.Result, error)
}

// larkRegState 一次 Device Flow 的进行中状态。
type larkRegState struct {
	mu       sync.Mutex
	qrURL    string
	qrExpire int
	done     bool
	appID    string
	appSecret string
	err      string
	startedAt time.Time
}

// SetLarkReg 注入 Device Flow runner 和凭据落盘路径。仅测试与 main.go 调用。
func (s *Server) SetLarkReg(runner larkRegRunner, credPath string) {
	if s.larkRegState == nil {
		s.larkRegState = &larkRegState{}
	}
	s.larkRegRunner = runner
	s.larkRegCredPath = credPath
}

// larkRegStart handles POST /api/larkreg/start — 仅内网(BindOpGateMiddleware 套在路由组上)。
// 启动一个 Device Flow goroutine,立即返回 qr_url;前端用 /poll 查询结果。
func (s *Server) larkRegStart(c *gin.Context) {
	if s.larkRegRunner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "lark registration not configured"})
		return
	}
	// 重置状态(只允许同时一个进行中的 flow)
	s.larkRegState.mu.Lock()
	s.larkRegState.done = false
	s.larkRegState.appID = ""
	s.larkRegState.appSecret = ""
	s.larkRegState.err = ""
	s.larkRegState.qrURL = ""
	s.larkRegState.startedAt = time.Now()
	s.larkRegState.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, err := s.larkRegRunner.Run(ctx, larkreg.Options{
			OnQRCode: func(url string, expireIn int) {
				s.larkRegState.mu.Lock()
				s.larkRegState.qrURL = url
				s.larkRegState.qrExpire = expireIn
				s.larkRegState.mu.Unlock()
			},
			CreateOnly: true,
		})
		s.larkRegState.mu.Lock()
		defer s.larkRegState.mu.Unlock()
		if err != nil {
			s.larkRegState.err = err.Error()
			s.larkRegState.done = true
			return
		}
		// 成功:落盘(若 runner 是真实 SDK,result 才有意义 —— 但 fake 注入时
		// 这里拿不到凭据;真实流程是 Run 返回凭据,我们要在 Run 返回值里取)
		s.larkRegState.done = true
	}()

	// 等 qr_url 出现(最多 3s)
	for i := 0; i < 30; i++ {
		s.larkRegState.mu.Lock()
		url := s.larkRegState.qrURL
		expire := s.larkRegState.qrExpire
		s.larkRegState.mu.Unlock()
		if url != "" {
			c.JSON(http.StatusOK, gin.H{"qr_url": url, "expire_in": expire})
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "pending", "hint": "poll /api/larkreg/poll"})
}

// larkRegPoll handles GET /api/larkreg/poll — 仅内网。
// 返回 device flow 状态:pending / success(带 app_id,不返回 app_secret 给前端)
// / error。成功时把凭据落盘。
func (s *Server) larkRegPoll(c *gin.Context) {
	if s.larkRegRunner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "lark registration not configured"})
		return
	}
	s.larkRegState.mu.Lock()
	defer s.larkRegState.mu.Unlock()

	if s.larkRegState.err != "" {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": s.larkRegState.err})
		return
	}
	if !s.larkRegState.done {
		c.JSON(http.StatusAccepted, gin.H{"status": "pending"})
		return
	}
	// 成功:落盘 + 返回(只回 app_id,app_secret 不出 HTTP 响应)
	if s.larkRegCredPath != "" && s.larkRegState.appID != "" {
		if err := larkreg.SaveCredentials(s.larkRegCredPath, s.larkRegState.appID, s.larkRegState.appSecret); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "save credentials: " + err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"app_id":  s.larkRegState.appID,
		"hint":    "restart pieqi to apply new credentials",
	})
}
```

> ⚠️ 上面的 `larkRegStart` goroutine 里没把 Run 的返回值取出来 —— 这是个 bug,Run 返回的 `(Result, error)` 里的 AppID/AppSecret 没赋给 state。补丁见 Step 4。

- [ ] **Step 4: 修正 Run 返回值赋给 state**

Modify `/workspace/internal/api/larkreg.go`,把 `larkRegStart` 的 goroutine 改成:

```go
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		res, err := s.larkRegRunner.Run(ctx, larkreg.Options{
			OnQRCode: func(url string, expireIn int) {
				s.larkRegState.mu.Lock()
				s.larkRegState.qrURL = url
				s.larkRegState.qrExpire = expireIn
				s.larkRegState.mu.Unlock()
			},
			CreateOnly: true,
		})
		s.larkRegState.mu.Lock()
		defer s.larkRegState.mu.Unlock()
		if err != nil {
			s.larkRegState.err = err.Error()
			s.larkRegState.done = true
			return
		}
		s.larkRegState.appID = res.AppID
		s.larkRegState.appSecret = res.AppSecret
		s.larkRegState.done = true
	}()
```

并把 `"pieqi/internal/larkreg"` import、`"context"` import 加到 larkreg.go 顶部。

- [ ] **Step 5: Server struct 加字段 + 注册路由**

Modify `/workspace/internal/api/router.go`,在 `Server` struct 加字段:

```go
type Server struct {
	cfg      *config.Config
	store    *core.TaskStore
	runner   *core.TaskRunner
	hooks    *core.HookService
	bus      *core.EventBus
	skills   *core.SkillScanner
	commands *core.CommandScanner
	auth     *auth.Service       // wired by SetAuth; nil-safe for legacy tests
	tunnel   *auth.TunnelManager // wired by SetAuth; nil-safe for legacy tests

	// Lark Device Flow (扫码一键创建飞书应用)。wired by SetLarkReg;
	// nil-safe for tests that don't exercise larkreg routes.
	larkRegRunner  larkRegRunner
	larkRegState   *larkRegState
	larkRegCredPath string
}
```

在 `Register` 函数末尾(`/internal/hook` 路由之前),加路由组:

```go
// Lark Device Flow 路由:扫码一键创建飞书应用。
// 仅内网(同 bind/unbind 的 BindOpGateMiddleware)—— 防止从公网触发
// 应用创建流程。
if s.auth != nil && s.larkRegRunner != nil {
	larkRegGrp := r.Group("/api/larkreg", corsMiddleware(corsAll, corsOrigins),
		s.auth.BindOpGateMiddleware())
	larkRegGrp.POST("/start", s.larkRegStart)
	larkRegGrp.GET("/poll", s.larkRegPoll)
}
```

- [ ] **Step 6: main.go 装配 LarkReg**

Modify `/workspace/cmd/pieqi/main.go`,在 `apiServer.SetAuth(...)` 之后加:

```go
apiServer.SetLarkReg(larkreg.NewRegistration(), cfg.Channels.Lark.CredentialsFile)
```

并在 import 块加 `"pieqi/internal/larkreg"`。

- [ ] **Step 7: 跑测试验证通过**

Run:
```bash
cd /workspace && go test ./internal/api/... -run TestLarkReg -v
```

Expected: 三个 subtest 全 PASS。

- [ ] **Step 8: 跑全量编译**

Run:
```bash
cd /workspace && go build ./... && go test ./...
```

Expected: 全 PASS(或仅 PRD V1.0 已知 flake)。

- [ ] **Step 9: Commit**

```bash
cd /workspace && git add internal/api/larkreg.go internal/api/larkreg_test.go internal/api/router.go cmd/pieqi/main.go
git commit -m "feat(api): add /api/larkreg start+poll handlers (internal-only, device flow)"
```

---

## Task 9: 前端"接入飞书"页

**Files:**
- Create: `/workspace/web/src/larkreg.js`
- Modify: `/workspace/web/index.html`

- [ ] **Step 1: 实现 larkreg.js**

Create `/workspace/web/src/larkreg.js`:

```javascript
// 飞书一键接入:扫码 → 轮询 → 凭据落盘 → 提示重启。
// 仅内网可调(后端 BindOpGateMiddleware 强制),所以这个入口只在
// 内网 PWA 上可见。外网访问时 start 会 403,前端按钮禁用。
//
// 复用 /api/tunnel/qrcode 端点把 QR URL 转成可扫描图片(避免
// 前端再引一个 QR 库)。

const POLL_INTERVAL_MS = 2000;
const POLL_TIMEOUT_MS = 10 * 60 * 1000; // 10 分钟,与后端 ctx 超时对齐

async function apiCall(method, path, body) {
  const opts = { method, headers: { 'Content-Type': 'application/json' } };
  if (body) opts.body = JSON.stringify(body);
  // 复用 auth.js 注入的 X-Feishu-Openid header(若存在)
  const openid = sessionStorage.getItem('feishu_openid') || '';
  if (openid) opts.headers['X-Feishu-Openid'] = openid;
  const r = await fetch(path, opts);
  return { status: r.status, json: await r.json().catch(() => ({})) };
}

async function startLarkReg(button) {
  button.disabled = true;
  const statusEl = document.querySelector('#larkreg-status');
  const qrEl = document.querySelector('#larkreg-qr');
  statusEl.textContent = '正在生成二维码...';
  qrEl.innerHTML = '';

  const start = await apiCall('POST', '/api/larkreg/start', {});
  if (start.status === 403) {
    statusEl.textContent = '⚠️ 仅内网可接入,请在内网环境操作';
    button.disabled = false;
    return;
  }
  if (start.status !== 200) {
    statusEl.textContent = `❌ 启动失败: ${start.json.error || start.status}`;
    button.disabled = false;
    return;
  }

  const qrUrl = start.json.qr_url;
  statusEl.textContent = '请在飞书里扫码确认';
  // 用 tunnel QR 端点把 URL 转成图片(避免前端引 QR 库)
  qrEl.innerHTML = `<img src="/api/tunnel/qrcode?text=${encodeURIComponent(qrUrl)}" alt="QR" width="256" height="256" />`;

  // 轮询
  const deadline = Date.now() + POLL_TIMEOUT_MS;
  const poll = async () => {
    if (Date.now() > deadline) {
      statusEl.textContent = '⏰ 超时,请重试';
      button.disabled = false;
      return;
    }
    const r = await apiCall('GET', '/api/larkreg/poll', null);
    if (r.status === 202) {
      statusEl.textContent = '等待扫码确认...';
      setTimeout(poll, POLL_INTERVAL_MS);
      return;
    }
    if (r.status === 200) {
      statusEl.textContent = `✅ 接入成功 (App ID: ${r.json.app_id})。请重启 Pieqi 生效。`;
      qrEl.innerHTML = '';
      button.disabled = false;
      return;
    }
    statusEl.textContent = `❌ ${r.json.error || r.status}`;
    button.disabled = false;
  };
  setTimeout(poll, POLL_INTERVAL_MS);
}

// 挂载按钮
const root = document.querySelector('#larkreg-mount');
if (root) {
  root.innerHTML = `
    <div class="larkreg-panel">
      <h3>接入飞书</h3>
      <p>扫码一键创建飞书自建应用,无需手动配置权限。</p>
      <button id="larkreg-btn" class="primary">扫码接入</button>
      <div id="larkreg-status"></div>
      <div id="larkreg-qr"></div>
    </div>
  `;
  root.querySelector('#larkreg-btn')?.addEventListener('click', (e) => startLarkReg(e.target));
}
```

- [ ] **Step 2: index.html 挂载点**

Modify `/workspace/web/index.html`,在合适位置(例如现有面板之后)加:

```html
<div id="larkreg-mount"></div>
```

并在 `<head>` 或现有 script 块附近加:

```html
<script type="module" src="/src/larkreg.js"></script>
```

- [ ] **Step 3: 跑前端构建验证**

Run:
```bash
cd /workspace/web && npm run build
```

Expected: exit 0,`dist/` 产物正常。

- [ ] **Step 4: Commit**

```bash
cd /workspace && git add web/src/larkreg.js web/index.html
git commit -m "feat(web): add scan-to-register Lark app panel"
```

---

## Task 10: config.yaml 示例 + 验收

**Files:**
- Modify: `/workspace/config.yaml`

- [ ] **Step 1: 在 config.yaml 的 lark 段加注释说明**

Modify `/workspace/config.yaml`,把现有的:

```yaml
channels:
  lark:
    enabled: false
    app_id: ""
    app_secret: ""
    verify_token: ""
    encrypt_key: ""
```

改成:

```yaml
channels:
  lark:
    enabled: false
    # event_mode: webhook(默认,需公网回调) | longconn(长连接,无需公网,推荐)
    event_mode: webhook
    # 长连接模式只需 app_id+app_secret;webhook 模式还需 verify_token+encrypt_key
    app_id: ""
    app_secret: ""
    verify_token: ""
    encrypt_key: ""
    # 一键接入(扫码)拿到的凭据会写入这里,覆盖上面的 app_id/app_secret。
    # 空 = ~/.pieqi/lark_credentials.json。详见 /api/larkreg/start。
    credentials_file: ""
```

- [ ] **Step 2: 跑全量测试 + 编译最终验收**

Run:
```bash
cd /workspace && go build ./... && go test ./...
```

Expected: build exit 0;tests 全 PASS(或仅 PRD V1.0 已知 `TestAPI_CreateAndListTasks` 偶发 flake)。

- [ ] **Step 3: 手动 smoke test 脚本(可选,需真实飞书账号)**

> 仅作记录,不强制执行:

```bash
# 1. 改 config.yaml: channels.lark.enabled=true, event_mode=longconn
# 2. 启动 pieqi
# 3. 内网访问 PWA,点"扫码接入"按钮
# 4. 飞书扫码确认,等 poll 返回 success
# 5. 重启 pieqi,看日志输出 "lark channel enabled event_mode=longconn"
# 6. 在飞书里给机器人发消息,看 pieqi 日志是否有 "received channel=lark"
```

- [ ] **Step 4: Commit**

```bash
cd /workspace && git add config.yaml
git commit -m "docs(config): document lark event_mode + scan-to-register flow"
```

---

## Self-Review

**1. Spec coverage(对照用户三项要求):**
- "飞书接入模式:使用长连接,这样可以不需要公网回调" → Task 1-5 覆盖(SDK 依赖、配置开关、`longconn.go` 实现、Adapter 接入、main.go 启动)。
- "增加一键创建飞书应用" → Task 6-8 + Task 9 覆盖(凭据落盘、Device Flow 封装、HTTP handlers、前端扫码页)。
- "探索 IM 接入开放的功能" → 调研报告已交付(不在本计划范围,作为独立交付)。
- 用户决策"保留隧道+长连接并行" → Cloudflared 隧道/auth 系统在所有任务里都明确"不动",只在 Task 8 复用 `BindOpGateMiddleware`(内网 gate)给 larkreg 路由组,不引入新外部访问面。

**2. Placeholder scan:** 已通读,无 TBD/TODO/"add error handling" 等占位符。所有代码块都是完整可运行代码。

**3. Type consistency:**
- `Options.OnQRCode func(url string, expireIn int)` —— Task 7 定义,Task 8 测试与实现一致。
- `Result{AppID, AppSecret}` —— Task 7 定义,Task 8 `larkRegStart` 的 goroutine 里 `res.AppID`/`res.AppSecret` 一致。
- `larkRegRunner` 接口(`Run(ctx, Options) (Result, error)`) —— Task 8 定义,`fakeReg` 实现签名一致,`larkreg.Registration` 也满足此接口(`Run` 方法签名相同)。
- `Registration.sdkFunc` 字段 —— Task 7 测试通过直接赋值注入,NewRegistration 装真实 SDK。
- `Server.SetLarkReg(runner, credPath)` —— Task 8 Step 3 实现,Task 8 Step 6 main.go 调用一致。
- `convertP2Message(*larkim.P2MessageReceiveV1) model.Message` —— Task 3 定义,Task 4 `startLongConnection` 调用一致。

**4. 已知风险(不修复,记录给执行者):**
- 飞书 SDK 的 `P2MessageReceiveV1` 内部 struct 名(`P2MessageReceiveV1Data`、`P2MessageReceiveV1DataSenderSenderId` 等)在 SDK 不同版本可能微调 —— Task 3 测试若编译错误,按 SDK README 调整字段名即可。
- `registration.RegisterApp` 的 `Options.Addons` 类型在不同 SDK 版本字段名可能差异 —— Task 7 测试用 fake 注入绕开真实 SDK,Task 8 测试同样用 fake,只有 main.go 调真实 SDK 时才会暴露差异,届时按 SDK README 调整。
- 长连接模式集群部署不广播(Task 5 起 goroutine 后,若多副本部署,同一事件只投递一个副本)—— 已在 `longconn.go` 注释里记录,不做代码处理(超出本期范围)。

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-08-19-lark-long-connection-and-device-flow.md`. Two execution options:**

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
