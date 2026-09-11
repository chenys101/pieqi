package lark

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"pieqi/internal/channel"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Adapter 飞书渠道适配器，实现 MessageReceiver + MessageSender。
//
// 多机器人（D1）：一个 Adapter 实例 = 一台飞书机器人（一个 app_id）。
// 多台机器人 = 多个 Adapter，各自独立长连接 / 各自独立凭据；
// botID 为空表示渠道级实例（单机器人 / 未接入多机器人）。
type Adapter struct {
	// botID 标识本实例代表哪台机器人（model.Bot.ID）。
	// 不可变：实例集合变化时重建 Adapter，不做原地改写 —— 否则与
	// 已经发出的消息归属会产生歧义。
	botID string

	appID       string
	appSecret   string
	verifyToken string
	encryptKey  string
	eventMode   string // "webhook"（默认）| "longconn"
	logger      *zap.Logger
	onMessage   func(model.Message)
	httpClient  *http.Client

	// tenant_access_token 缓存（有效期 2h）
	tokenMu     sync.RWMutex
	accessToken string
	tokenExpiry time.Time

	// 凭据/接入方式并发读安全：webhook 请求处理与 SetConfig（热更新）
	// 可能同时读写这些字段。
	configMu sync.RWMutex
}

// New 创建飞书适配器（默认 webhook 模式）。
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

// NewLongConn 创建飞书适配器（长连接模式）。verifyToken/encryptKey
// 在长连接模式下不需要（SDK 内置鉴权），传空字符串即可。
func NewLongConn(appID, appSecret string) *Adapter {
	return &Adapter{
		appID:      appID,
		appSecret:  appSecret,
		eventMode:  "longconn",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// WithLogger 注入 logger，用于长连接错误日志。nil = 静默。
func (a *Adapter) WithLogger(l *zap.Logger) *Adapter {
	a.logger = l
	return a
}

// WithBotID 声明本实例代表哪台机器人（model.Bot.ID）。
// 链式调用，便于 main 侧一行构造；空串 = 渠道级实例。
func (a *Adapter) WithBotID(id string) *Adapter {
	a.botID = id
	return a
}

// BotID 实现 channel.BotIdentity：投递的消息据此标注来源机器人。
func (a *Adapter) BotID() string { return a.botID }

// Mode 返回当前接入方式（"webhook" | "longconn"），读锁下取。
// WebhookMux 用它判定实例是否受理 HTTP 回调 —— 长连接实例不应受理。
func (a *Adapter) Mode() string {
	_, _, _, _, mode := a.configSnapshot()
	return mode
}

// configSnapshot 在 configMu 读锁下取当前配置快照。
// 避免 SetConfig（热更新）与 webhook 处理/取 token 并发竞态。
func (a *Adapter) configSnapshot() (appID, appSecret, verifyToken, encryptKey, eventMode string) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.appID, a.appSecret, a.verifyToken, a.encryptKey, a.eventMode
}

// SetConfig 热更新渠道凭据与接入方式（无需重建 adapter）。
// 由 main.go 的渠道控制器在配置保存后调用：
//   - webhook 模式：字段更新即生效（路由每次请求读最新字段）；
//   - longconn 模式：字段更新后由控制器重启长连接 goroutine（client 用新凭据重建）。
// 同时清空 tenant_access_token 缓存，强制用新 app_id/app_secret 刷新。
func (a *Adapter) SetConfig(appID, appSecret, verifyToken, encryptKey, eventMode string) {
	a.configMu.Lock()
	a.appID = appID
	a.appSecret = appSecret
	a.verifyToken = verifyToken
	a.encryptKey = encryptKey
	a.eventMode = eventMode
	a.configMu.Unlock()

	a.tokenMu.Lock()
	a.accessToken = ""
	a.tokenExpiry = time.Time{}
	a.tokenMu.Unlock()
}

// Name 返回渠道名
func (a *Adapter) Name() string { return "lark" }

// Init 不注册路由 —— 飞书是「同一渠道多实例」的渠道（多台机器人同属
// ChannelLark），HTTP 入口必须由 WebhookMux 独占：gin 不允许同一路由
// pattern 被注册多次，而多台机器人天然共享 /webhook/lark 这个前缀。
//
// 保留本方法以满足 channel.MessageReceiver 契约（见该接口的 Init 注释）。
// 路由见 webhook_mux.go，它由渠道控制器在启动时注册一次。
func (a *Adapter) Init(_ gin.IRouter) error { return nil }

// Start 启动渠道。
//   - webhook 模式：no-op（等飞书回调即可）
//   - longconn 模式：启动飞书长连接事件订阅（阻塞直到 ctx 取消）
func (a *Adapter) Start(ctx context.Context) error {
	if a.eventMode != "longconn" {
		return nil
	}
	return a.startLongConnection(ctx, a.logger)
}

// OnMessage 注册消息回调
func (a *Adapter) OnMessage(cb func(model.Message)) {
	a.onMessage = cb
}

// Send 发送消息到飞书
func (a *Adapter) Send(ctx context.Context, target model.ReplyTarget, text string) error {
	token, err := a.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	body := map[string]interface{}{
		"receive_id": target.ChatID,
		"msg_type":   "text",
		"content":    fmt.Sprintf(`{"text":"%s"}`, escapeJSON(text)),
	}

	data, _ := json.Marshal(body)
	// receive_id_type 是 URL query 参数（非 body 字段），缺失时报 99992402
	// "receive_id_type is required"。消息事件里的 chat_id 即会话 ID。
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("lark api error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// -- Webhook 处理 --

// larkEvent 飞书事件回调结构
type larkEvent struct {
	Schema string `json:"schema"`
	Header struct {
		EventType string `json:"event_type"`
		Token     string `json:"token"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
				UserID string `json:"user_id"`
			} `json:"sender_id"`
		} `json:"sender"`
		Message struct {
			MessageID string `json:"message_id"`
			ChatID    string `json:"chat_id"`
			ChatType  string `json:"chat_type"`
			Content   string `json:"content"`
			Mentions  []struct {
				Key string `json:"key"`
				ID  struct {
					OpenID string `json:"open_id"`
				} `json:"id"`
				Name string `json:"name"`
			} `json:"mentions"`
		} `json:"message"`
	} `json:"event"`
}

// handleWebhook 处理飞书事件回调
func (a *Adapter) handleWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": "read body failed"})
		return
	}

	// 1. URL 验证（首次配置时飞书会发 challenge）
	var challenge struct {
		Challenge string `json:"challenge"`
		Type      string `json:"type"`
	}
	if json.Unmarshal(body, &challenge) == nil && challenge.Type == "url_verification" {
		c.JSON(200, gin.H{"challenge": challenge.Challenge})
		return
	}

	// 2. 解密消息（如果配置了加密）。configSnapshot 保证与 SetConfig 并发安全。
	_, _, verifyToken, encryptKey, _ := a.configSnapshot()
	plaintext := body
	if encryptKey != "" {
		var encrypted struct {
			Encrypt string `json:"encrypt"`
		}
		if json.Unmarshal(body, &encrypted) == nil && encrypted.Encrypt != "" {
			decrypted, err := a.decrypt(encryptKey, encrypted.Encrypt)
			if err != nil {
				c.JSON(400, gin.H{"error": "decrypt failed"})
				return
			}
			plaintext = decrypted
		}
	}

	// 3. 解析事件
	var event larkEvent
	if err := json.Unmarshal(plaintext, &event); err != nil {
		c.JSON(200, gin.H{"status": "ok"}) // 飞书要求 200
		return
	}

	// 3b. 校验 verify_token：配置了才校验（未配置保持向后兼容）。
	// 路由现在是常驻的（多机器人需单路由分发，见 webhook_mux.go），
	// 长连接部署也不会少掉这条入口，故必须验来源 —— 否则任何人 POST
	// 一个伪造事件就能驱使机器人往任意会话发消息。
	if verifyToken != "" && event.Header.Token != verifyToken {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid verify token"})
		return
	}

	if event.Header.EventType != "im.message.receive_v1" {
		c.JSON(200, gin.H{"status": "ok"})
		return
	}

	// 4. 转为统一 Message
	msg := a.convertMessage(&event)

	// 5. 回调
	if a.onMessage != nil {
		a.onMessage(msg)
	}

	c.JSON(200, gin.H{"status": "ok"})
}

// convertMessage 将飞书事件转为统一 Message
func (a *Adapter) convertMessage(e *larkEvent) model.Message {
	// 解析消息文本
	text := ""
	var content struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(e.Event.Message.Content), &content) == nil {
		text = content.Text
	}

	// 提取 @ 并清洗
	mentionBot := false
	for _, m := range e.Event.Message.Mentions {
		if m.Name == "" {
			mentionBot = true // 飞书 @ 机器人时 name 为空
		}
	}
	text = strings.TrimSpace(text)

	// 确定 userID
	userID := e.Event.Sender.SenderID.OpenID
	if userID == "" {
		userID = e.Event.Sender.SenderID.UserID
	}

	return model.Message{
		Channel:    model.ChannelLark,
		BotID:      a.botID,
		ChatID:     e.Event.Message.ChatID,
		UserID:     userID,
		Content:    text,
		MentionBot: mentionBot,
		Raw:        e,
	}
}

// -- Access Token --

func (a *Adapter) getAccessToken(ctx context.Context) (string, error) {
	a.tokenMu.RLock()
	if a.accessToken != "" && time.Now().Before(a.tokenExpiry) {
		token := a.accessToken
		a.tokenMu.RUnlock()
		return token, nil
	}
	a.tokenMu.RUnlock()

	return a.refreshToken(ctx)
}

func (a *Adapter) refreshToken(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	// 双重检查
	if a.accessToken != "" && time.Now().Before(a.tokenExpiry) {
		return a.accessToken, nil
	}

	appID, appSecret, _, _, _ := a.configSnapshot()
	body := map[string]string{
		"app_id":     appID,
		"app_secret": appSecret,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"` // 秒
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("lark auth error %d: %s", result.Code, result.Msg)
	}

	a.accessToken = result.TenantAccessToken
	a.tokenExpiry = time.Now().Add(time.Duration(result.Expire-60) * time.Second) // 提前 1min 刷新
	return a.accessToken, nil
}

// -- 消息加解密 --

func (a *Adapter) decrypt(encryptKey, encrypted string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}

	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	if len(data) < aes.BlockSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	iv := data[:aes.BlockSize]
	ciphertext := data[aes.BlockSize:]

	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(ciphertext, ciphertext)

	return pkcs7Unpad(ciphertext), nil
}

func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	pad := int(data[len(data)-1])
	if pad > len(data) {
		return data
	}
	return data[:len(data)-pad]
}

// -- 工具 --

// escapeJSON 转义 JSON 字符串中的特殊字符
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

// 编译检查：确保实现了接口
var (
	_ channel.MessageReceiver = (*Adapter)(nil)
	_ channel.MessageSender   = (*Adapter)(nil)
	_ channel.BotIdentity     = (*Adapter)(nil)
)
