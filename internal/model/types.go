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
	Channel Channel `json:"channel"`
	// BotID 是接收该消息的机器人 id（model.Bot.ID），由各 receiver 在投递时填充。
	// 多机器人下同一渠道（lark）可有多个实例，仅凭 Channel 无法区分；
	// 特权命令按机器人收紧时依赖它（见 core.Bridge.handleTunnelCommand）。
	// 空串 = 投递方未标注来源（单机器人 / 旧路径），调用方不应据此收紧。
	BotID      string      `json:"bot_id,omitempty"`
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
