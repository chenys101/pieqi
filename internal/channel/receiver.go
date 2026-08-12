package channel

import (
	"context"

	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// MessageReceiver 消息接收接口。
// 每个渠道实现自己的 webhook 处理器。
type MessageReceiver interface {
	// Name 返回渠道名称
	Name() string

	// Init 注册 webhook 路由到 gin router
	Init(router gin.IRouter) error

	// Start 启动通道（长连接、轮询等）
	Start(ctx context.Context) error

	// OnMessage 注册消息回调
	OnMessage(func(model.Message))
}
