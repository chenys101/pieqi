// Package bridge 提供 claude-sdk-bridge 的 HTTP/SSE 客户端（multi-agent.md §5.3）。
// 这是私有协议：只被 internal/agent/claude 使用，不暴露给业务层。
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL 桥的默认监听地址（与 services/claude-sdk-bridge 默认一致）。
const DefaultBaseURL = "http://127.0.0.1:18790"

// Client claude-sdk-bridge 的 HTTP 客户端。
// 普通请求走带超时的 http.Client；SSE 事件流走独立的无超时 client（长连接）。
type Client struct {
	baseURL      string
	token        string
	http         *http.Client
	httpNoTimout *http.Client
}

// NewClient 创建客户端（不带 token）。baseURL 为空时用默认地址。
func NewClient(baseURL string) *Client {
	return NewClientWithToken(baseURL, "")
}

// NewClientWithToken 创建客户端并带桥鉴权 token（与 bridge 的 BRIDGE_TOKEN 对应）。
// token 非空时所有 /v1 请求附 Authorization: Bearer <token>；health 不需要（桥对 health 开放）。
func NewClientWithToken(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		token:        token,
		http:         &http.Client{Timeout: 15 * time.Second},
		httpNoTimout: &http.Client{},
	}
}

// auth 附加鉴权头（token 为空时 no-op）。
func (c *Client) auth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// CreateSessionRequest 创建会话请求。
type CreateSessionRequest struct {
	Cwd                string `json:"cwd"`
	ResumeSdkSessionID string `json:"resumeSdkSessionId,omitempty"`
}

// CreateSession 创建会话，返回桥会话 id。
func (c *Client) CreateSession(ctx context.Context, req CreateSessionRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("bridge: create marshal: %w", err)
	}
	resp, err := c.post(ctx, "/v1/sessions", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", errorFrom(resp)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("bridge: create decode: %w", err)
	}
	return out.ID, nil
}

// Prompt 追加一轮用户消息（非阻塞：桥返回 202，结果经 SSE turn_end 到达）。
// clientRef 供 turn_end 精确关联本轮。
func (c *Client) Prompt(ctx context.Context, id, text, clientRef string) error {
	body, err := json.Marshal(map[string]string{"text": text, "clientRef": clientRef})
	if err != nil {
		return fmt.Errorf("bridge: prompt marshal: %w", err)
	}
	resp, err := c.post(ctx, "/v1/sessions/"+id+"/prompt", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return errorFrom(resp)
	}
	return nil
}

// OpenEventStream 打开 SSE 事件流，返回响应体（调用方负责 Close）。
// 流长期存活；ctx 取消/关闭时中止。
func (c *Client) OpenEventStream(ctx context.Context, id string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/sessions/"+id+"/events", nil)
	if err != nil {
		return nil, fmt.Errorf("bridge: events new request: %w", err)
	}
	c.auth(req)
	resp, err := c.httpNoTimout.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bridge: events: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, errorFrom(resp)
	}
	return resp.Body, nil
}

// RespondPermission 对权限请求给出审批响应。
func (c *Client) RespondPermission(ctx context.Context, id, rid string, allow bool, optionID string) error {
	body, err := json.Marshal(map[string]any{"allow": allow, "optionID": optionID})
	if err != nil {
		return fmt.Errorf("bridge: perm marshal: %w", err)
	}
	resp, err := c.post(ctx, "/v1/sessions/"+id+"/permissions/"+rid, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errorFrom(resp)
	}
	return nil
}

// Cancel 取消当前轮（interrupt）。
func (c *Client) Cancel(ctx context.Context, id string) error {
	resp, err := c.post(ctx, "/v1/sessions/"+id+"/cancel", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return errorFrom(resp)
	}
	return nil
}

// CloseSession 关闭会话（杀子进程）。幂等。
func (c *Client) CloseSession(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/v1/sessions/"+id, nil)
	if err != nil {
		return fmt.Errorf("bridge: close new request: %w", err)
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bridge: close: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errorFrom(resp)
	}
	return nil
}

// Health 探活。
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/health", nil)
	if err != nil {
		return fmt.Errorf("bridge: health new request: %w", err)
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bridge: health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errorFrom(resp)
	}
	return nil
}

// post 通用 POST 请求。
func (c *Client) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bridge: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bridge: %s: %w", path, err)
	}
	return resp, nil
}

// errorFrom 解析桥的错误响应 {error: "..."}。
func errorFrom(resp *http.Response) error {
	var out struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out)
	if out.Error != "" {
		return fmt.Errorf("bridge: status %d: %s", resp.StatusCode, out.Error)
	}
	return fmt.Errorf("bridge: status %d", resp.StatusCode)
}

// SSEEvent 一条桥事件（data 帧 JSON 的 Go 视图）。
type SSEEvent struct {
	Kind       string          `json:"kind"`
	Seq        int             `json:"seq"`
	SessionID  string          `json:"sessionId"`
	TurnSeq    int             `json:"turnSeq"`
	State      string          `json:"state"`
	Text       string          `json:"text"`
	IsThought  bool            `json:"isThought"`
	ToolCallID string          `json:"toolCallId"`
	ToolTitle  string          `json:"toolTitle"`
	ToolStatus string          `json:"toolStatus"`
	ToolKind   string          `json:"toolKind"`
	RawInput   json.RawMessage `json:"rawInput"`
	RawOutput  json.RawMessage `json:"rawOutput"`
	ReqID      string          `json:"reqId"`
	ToolName   string          `json:"toolName"`
	ToolUseID  string          `json:"toolUseID"`
	Subtype    string          `json:"subtype"`
	IsError    bool            `json:"isError"`
	Message    string          `json:"message"`
	ClientRef  string          `json:"clientRef"`
	Turn       *SSETurn        `json:"turn"`
}

// SSETurn turn_end 载荷。
type SSETurn struct {
	ResumeID string          `json:"resumeId"`
	Usage    json.RawMessage `json:"usage"`
	CostUSD  float64         `json:"costUsd"`
}

// ConsumeSSE 读取 SSE 流，逐帧回调 (kind, data)。ctx 取消时返回。
func ConsumeSSE(ctx context.Context, r io.Reader, fn func(kind string, raw []byte)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	kind := "message"
	data := make([]string, 0, 4)
	flush := func() {
		if len(data) > 0 {
			fn(kind, []byte(strings.Join(data, "\n")))
		}
		kind = "message"
		data = data[:0]
	}
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
}

// ErrStreamClosed 事件流被关闭（会话结束或桥重启）。
var ErrStreamClosed = errors.New("bridge: event stream closed")
