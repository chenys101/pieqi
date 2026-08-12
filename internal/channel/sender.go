package channel

import (
	"context"

	"claude-bridge/internal/model"
)

// MessageSender 消息发送接口。
// 与接收解耦——某些场景只需要发送能力（如只输出不接收的回调渠道）。
type MessageSender interface {
	// Send 发送消息到指定目标
	Send(ctx context.Context, target model.ReplyTarget, text string) error
}
