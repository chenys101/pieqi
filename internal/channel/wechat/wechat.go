package wechat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"claude-bridge/internal/channel"
	"claude-bridge/internal/model"

	"github.com/gin-gonic/gin"
	qrcode "github.com/skip2/go-qrcode"
	"go.uber.org/zap"
)

// DefaultBaseURL iLink API 默认地址
const DefaultBaseURL = "https://ilinkai.weixin.qq.com"

// Adapter 微信 iLink 渠道适配器，实现 MessageReceiver + MessageSender。
// 使用 Long Polling 模式，不需要公网地址。
type Adapter struct {
	baseURL   string
	token     string
	tokenPath string // token 持久化路径
	logger    *zap.Logger

	onMessage func(model.Message)
	http      *http.Client

	// 长轮询状态
	pollingCtx    context.Context
	pollingCancel context.CancelFunc
	updatesBuf    string

	// 回复上下文
	mu            sync.Mutex
	contextTokens map[string]replyContext
}

type replyContext struct {
	contextToken string
	clientID     string
}

// New 创建微信 iLink 适配器
func New(logger *zap.Logger, baseURL string) *Adapter {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	// token 持久化到 data 目录
	tokenPath := filepath.Join("data", "wechat_token.json")
	a := &Adapter{
		baseURL:       baseURL,
		tokenPath:     tokenPath,
		logger:        logger,
		http: &http.Client{Timeout: 35 * time.Second},
		contextTokens: make(map[string]replyContext),
	}
	// 尝试加载已保存的 token
	a.loadToken()
	return a
}

// -- MessageReceiver --

func (a *Adapter) Name() string { return "wechat" }

func (a *Adapter) Init(_ gin.IRouter) error { return nil }

// Start 扫码登录 → 长轮询循环。阻塞直到 ctx 取消。
func (a *Adapter) Start(ctx context.Context) error {
	a.pollingCtx, a.pollingCancel = context.WithCancel(ctx)
	defer a.pollingCancel()

	if err := a.login(ctx); err != nil {
		return fmt.Errorf("wechat login: %w", err)
	}

	a.logger.Info("wechat: logged in, polling messages")
	return a.pollLoop()
}

func (a *Adapter) OnMessage(cb func(model.Message)) {
	a.onMessage = cb
}

// -- MessageSender --

func (a *Adapter) Send(ctx context.Context, target model.ReplyTarget, text string) error {
	a.mu.Lock()
	ctxInfo, ok := a.contextTokens[target.ChatID]
	a.mu.Unlock()

	contextToken := ""
	clientID := ""
	if ok {
		contextToken = ctxInfo.contextToken
		clientID = ctxInfo.clientID
	}

	payload := map[string]interface{}{
		"msg": map[string]interface{}{
			"to_user_id":    target.ChatID,
			"client_id":     clientID,
			"message_type":  2, // BOT
			"message_state": 2, // FINISH
			"item_list": []map[string]interface{}{
				{
					"type":      1, // TEXT
					"text_item": map[string]string{"text": text},
				},
			},
			"context_token": contextToken,
		},
	}

	data, _ := json.Marshal(payload)
	a.logger.Info("wechat: sending reply", zap.String("payload", string(data)))
	req, err := http.NewRequestWithContext(ctx, "POST",
		a.baseURL+"/ilink/bot/sendmessage", bytes.NewReader(data))
	if err != nil {
		return err
	}
	a.setAuthHeaders(req)

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("sendmessage: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	a.logger.Info("wechat: sendmessage response",
		zap.Int("status", resp.StatusCode),
		zap.String("body", string(body)),
	)

	if resp.StatusCode != 200 {
		return fmt.Errorf("sendmessage %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// -- 登录流程 --

func (a *Adapter) login(ctx context.Context) error {
	// 如果已有有效 token，直接跳过扫码
	if a.token != "" {
		a.logger.Info("wechat: using saved token, skip QR login")
		return nil
	}

	// 1. 获取二维码
	qrcodeID, qrcodeURL, err := a.getQRCode(ctx)
	if err != nil {
		return fmt.Errorf("get qrcode: %w", err)
	}

	a.logger.Info("wechat: 请扫码登录",
		zap.String("qrcode_url", qrcodeURL),
		zap.String("qrcode_id", qrcodeID),
	)

	// 生成终端二维码
	a.renderQRCode(qrcodeURL)
	a.logger.Info("wechat: waiting for scan, polling every 1s")

	// 2. 轮询扫码状态
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		time.Sleep(1 * time.Second)

		token, status, err := a.pollQRCodeStatus(ctx, qrcodeID)
		if err != nil {
			a.logger.Warn("wechat: qrcode status poll error", zap.Error(err))
			continue
		}
		switch status {
		case "":
			continue
		case "scanned":
			a.logger.Info("wechat: 已扫码，请在手机上确认")
		case "confirmed":
			a.token = token
			a.saveToken()
			a.logger.Info("wechat: 登录成功")
			return nil
		case "expired":
			return fmt.Errorf("二维码已过期")
		case "cancelled":
			return fmt.Errorf("用户取消登录")
		}
	}
}

// qrcodeResp 获取二维码响应
type qrcodeResp struct {
	Ret              int    `json:"ret"`
	Qrcode           string `json:"qrcode"`
	QrcodeImgContent string `json:"qrcode_img_content"`
	ErrMsg           string `json:"err_msg"`
}

func (a *Adapter) getQRCode(ctx context.Context) (qrcodeID, qrcodeURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		a.baseURL+"/ilink/bot/get_bot_qrcode?bot_type=3", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("SKRouteTag", "1001")

	resp, err := a.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var result qrcodeResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}
	if result.Ret != 0 {
		return "", "", fmt.Errorf("get qrcode failed: %s (ret=%d)", result.ErrMsg, result.Ret)
	}
	return result.Qrcode, result.QrcodeImgContent, nil
}

// qrcodeStatusResp 扫码状态响应
type qrcodeStatusResp struct {
	Ret      int    `json:"ret"`
	BotToken string `json:"bot_token"`
	Status   string `json:"status"` // "confirmed" 时返回 bot_token
}

func (a *Adapter) pollQRCodeStatus(_ context.Context, qrcodeID string) (token, status string, err error) {
	url := fmt.Sprintf("%s/ilink/bot/get_qrcode_status?bot_type=3&qrcode=%s", a.baseURL, qrcodeID)
	// Windows 下 Go HTTP 客户端偶发卡死，用 curl
	cmd := exec.Command("curl", "-s", "-m", "2", "--connect-timeout", "2", "-H", "SKRouteTag: 1001", url)
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("curl qrcode status: %w", err)
	}
	var result qrcodeStatusResp
	a.logger.Info("wechat: raw qrcode status response", zap.String("body", string(out)))
	if err := json.Unmarshal(out, &result); err != nil {
		return "", "", fmt.Errorf("parse qrcode status: %w, body=%s", err, string(out))
	}
	return result.BotToken, result.Status, nil
}

func (a *Adapter) renderQRCode(url string) {
	qr, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		a.logger.Warn("wechat: failed to render QR code", zap.Error(err))
		return
	}
	// 终端 ASCII 输出
	fmt.Print("\n" + qr.ToString(false))
	fmt.Println("\n用微信扫上方二维码登录")
	a.logger.Info("wechat: QR code rendered, polling for scan...")
}

// -- Token 持久化 --

type tokenFile struct {
	Token string `json:"token"`
}

func (a *Adapter) saveToken() {
	data, _ := json.Marshal(tokenFile{Token: a.token})
	if err := os.WriteFile(a.tokenPath, data, 0600); err != nil {
		a.logger.Warn("wechat: failed to save token", zap.Error(err))
	}
}

func (a *Adapter) loadToken() {
	data, err := os.ReadFile(a.tokenPath)
	if err != nil {
		return
	}
	var tf tokenFile
	if json.Unmarshal(data, &tf) == nil && tf.Token != "" {
		a.token = tf.Token
		a.logger.Info("wechat: loaded saved token")
	}
}

// -- 长轮询 --

func (a *Adapter) pollLoop() error {
	for {
		if a.pollingCtx.Err() != nil {
			return a.pollingCtx.Err()
		}

		msgs, err := a.getUpdates()
		if err != nil {
			a.logger.Warn("wechat: getupdates error", zap.Error(err))
			select {
			case <-a.pollingCtx.Done():
				return a.pollingCtx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}

		a.logger.Info("wechat: getupdates ok", zap.Int("msgs", len(msgs)))
		for _, msg := range msgs {
			a.handleMessage(msg)
		}
		if len(msgs) == 0 {
			select {
			case <-a.pollingCtx.Done():
				return a.pollingCtx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (a *Adapter) getUpdates() ([]ilinkIncomingMsg, error) {
	payload := map[string]interface{}{
		"get_updates_buf":        a.updatesBuf,
		"longpolling_timeout_ms": 35000,
	}

	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(a.pollingCtx, "POST",
		a.baseURL+"/ilink/bot/getupdates", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	a.setAuthHeaders(req)

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result getUpdatesResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Ret != 0 {
		return nil, fmt.Errorf("getupdates ret=%d", result.Ret)
	}

	a.updatesBuf = result.Buf
	return result.Messages, nil
}

func (a *Adapter) handleMessage(msg ilinkIncomingMsg) {
	tokenPreview := msg.ContextToken
	if len(tokenPreview) > 20 {
		tokenPreview = tokenPreview[:20]
	}
	a.logger.Info("wechat: raw incoming",
		zap.String("from", msg.FromUserID),
		zap.String("to", msg.ToUserID),
		zap.String("msg_id", msg.MsgID),
		zap.String("ctx_preview", tokenPreview),
	)
	var textParts []string
	for _, item := range msg.ItemList {
		if item.Type == 1 { // TEXT
			textParts = append(textParts, item.TextItem.Text)
		}
	}
	if len(textParts) == 0 {
		return
	}

	message := model.Message{
		Channel:  model.ChannelWeChat,
		ChatID:   msg.FromUserID,
		UserID:   msg.FromUserID,
		UserName: msg.FromUserID,
		Content:  strings.Join(textParts, ""),
	}

	// 缓存回复上下文
	a.mu.Lock()
	a.contextTokens[msg.FromUserID] = replyContext{
		contextToken: msg.ContextToken,
		clientID:     msg.ClientID,
	}
	a.mu.Unlock()

	if a.onMessage != nil {
		a.onMessage(message)
	}
}

// -- 数据结构 --

type ilinkIncomingMsg struct {
	MsgID        string `json:"msg_id"`
	FromUserID   string `json:"from_user_id"`
	ToUserID     string `json:"to_user_id"`
	ClientID     string `json:"client_id"`
	ContextToken string `json:"context_token"`
	ItemList     []struct {
		Type     int `json:"type"`
		TextItem struct {
			Text string `json:"text"`
		} `json:"text_item"`
	} `json:"item_list"`
}

type getUpdatesResp struct {
	Ret      int               `json:"ret"`
	Messages []ilinkIncomingMsg `json:"msgs"`
	Buf      string            `json:"get_updates_buf"`
}

// -- 工具 --

func (a *Adapter) setAuthHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("SKRouteTag", "1001")
	if a.token != "" {
		req.Header.Set("AuthorizationType", "ilink_bot_token")
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	req.Header.Set("X-WECHAT-UIN", randomWeChatUin())
}

func randomWeChatUin() string {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, rand.Uint32())
	return base64.StdEncoding.EncodeToString(b)
}

func randomClientID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// 编译检查
var (
	_ channel.MessageReceiver = (*Adapter)(nil)
	_ channel.MessageSender   = (*Adapter)(nil)
)
