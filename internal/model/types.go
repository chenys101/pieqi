package model

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
