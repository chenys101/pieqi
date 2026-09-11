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

	// Init 注册 webhook 路由到 gin router。
	// 单实例渠道（如未接入多机器人的 wechat）照常在实现里注册；
	// 而「同一渠道多实例」的实现（如 lark 机器人）会把它做成空实现 ——
	// 多实例需要**单路由按 id 分发**，且新实例在运行期产生而 gin 不允许
	// 服务启动后安全追加路由，故路由改由渠道自己的分发器独占（见
	// internal/channel/lark/webhook_mux.go）。
	Init(router gin.IRouter) error

	// Start 启动通道（长连接、轮询等）
	Start(ctx context.Context) error

	// OnMessage 注册消息回调
	OnMessage(func(model.Message))
}

// BotIdentity 是可选能力：由「每台机器人一个实例」的 receiver 实现，
// 用于区分同一渠道下的多台机器人（如两台飞书机器人同属 ChannelLark）。
//
// 未实现 / 返回空串 = 渠道级实例（单机器人 / 未接入多机器人），
// 调用方**不应**据此收紧权限 —— 那会把尚未标注来源的链路误伤成"全部无权"。
type BotIdentity interface {
	BotID() string
}
