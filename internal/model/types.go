package model

import "time"

// Channel 渠道类型
type Channel string

const (
	ChannelLark   Channel = "lark"
	ChannelWeCom  Channel = "wecom"
	ChannelWeChat Channel = "wechat"
)

// Message 渠道无关的统一消息格式
type Message struct {
	Channel    Channel     `json:"channel"`
	ChatID     string      `json:"chat_id"`
	UserID     string      `json:"user_id"`
	UserName   string      `json:"user_name"`
	Content    string      `json:"content"`
	MentionBot bool        `json:"mention_bot"`
	Raw        interface{} `json:"raw,omitempty"`
}

// ReplyTarget 回复目标
type ReplyTarget struct {
	ChatID  string `json:"chat_id"`
	ReplyTo string `json:"reply_to,omitempty"` // 回复某条消息
}

// Identity 统一用户身份
type Identity struct {
	ID       string              `json:"id"`
	Bindings map[Channel]Binding `json:"bindings"`
}

// Binding 某个渠道的用户绑定
type Binding struct {
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
}

// SessionInfo 会话摘要（供列表命令用）
type SessionInfo struct {
	Index    int       `json:"index"`
	UUID     string    `json:"uuid"`
	Preview  string    `json:"preview"`  // 第一条消息预览
	LastUsed time.Time `json:"last_used"`
	Expired  bool      `json:"expired"`
}

// Result Claude 子进程返回结果
type Result struct {
	Output    string `json:"output"`
	SessionID string `json:"session_id"`
	Duration  int64  `json:"duration_ms"`
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
}

// UserBindings 用户绑定文件格式
type UserBindings map[string]*Identity
