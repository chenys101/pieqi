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
)

// Adapter 飞书渠道适配器，实现 MessageReceiver + MessageSender。
type Adapter struct {
	appID       string
	appSecret   string
	verifyToken string
	encryptKey  string
	onMessage   func(model.Message)
	httpClient  *http.Client

	// tenant_access_token 缓存（有效期 2h）
	tokenMu     sync.RWMutex
	accessToken string
	tokenExpiry time.Time
}

// New 创建飞书适配器
func New(appID, appSecret, verifyToken, encryptKey string) *Adapter {
	return &Adapter{
		appID:       appID,
		appSecret:   appSecret,
		verifyToken: verifyToken,
		encryptKey:  encryptKey,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Name 返回渠道名
func (a *Adapter) Name() string { return "lark" }

// Init 注册飞书 Webhook 路由
func (a *Adapter) Init(router gin.IRouter) error {
	router.POST("/webhook/lark", a.handleWebhook)
	return nil
}

// Start 启动（webhook 模式无需额外启动）
func (a *Adapter) Start(ctx context.Context) error { return nil }

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
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/im/v1/messages", bytes.NewReader(data))
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

	// 2. 解密消息（如果配置了加密）
	plaintext := body
	if a.encryptKey != "" {
		var encrypted struct {
			Encrypt string `json:"encrypt"`
		}
		if json.Unmarshal(body, &encrypted) == nil && encrypted.Encrypt != "" {
			decrypted, err := a.decrypt(encrypted.Encrypt)
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

	body := map[string]string{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
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

func (a *Adapter) decrypt(encrypted string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}

	key := sha256.Sum256([]byte(a.encryptKey))
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
)
