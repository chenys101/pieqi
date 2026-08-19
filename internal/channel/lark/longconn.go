package lark

import (
	"context"
	"encoding/json"
	"fmt"

	"pieqi/internal/model"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"go.uber.org/zap"
)

// startLongConnection 用飞书官方 SDK 建立长连接事件订阅。
//
// 触发条件:cfg.Channels.Lark.EventMode == "longconn"。
// 与 webhook 模式互斥:webhook 模式靠飞书回调到 /webhook/lark,
// 长连接模式由 Pieqi 主动 wss 到 open.feishu.cn,无需公网。
//
// SDK 内置断线重连 + 心跳,事件通过 dispatcher 投递。Pieqi 这里只做
// 事件 → model.Message 的转换,然后调 Adapter.onMessage 回调(与
// webhook 路径共用同一回调,保证 Bridge 无感知)。
//
// 长连接模式注意事项(PRD §4 已调研):
//   - 仅企业自建应用可用(Pieqi 本就是内部工具)
//   - 事件处理必须 ≤ 3s,长任务异步化(Adapter.onMessage 已是
//     go b.handleMessage,无阻塞风险)
//   - 集群模式不广播:多副本部署时同一事件只投递一个副本
func (a *Adapter) startLongConnection(ctx context.Context, logger *zap.Logger) error {
	if a.appID == "" || a.appSecret == "" {
		return fmt.Errorf("long-connection mode requires app_id + app_secret")
	}

	// dispatcher 的两个参数在长连接模式下必须为空字符串
	// (webhook 模式下分别是 VerificationToken + EncryptKey)
	dispatcher := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
			msg := convertP2Message(event)
			if a.onMessage != nil {
				a.onMessage(msg)
			}
			return nil
		})

	cli := larkws.NewClient(
		a.appID, a.appSecret,
		larkws.WithEventHandler(dispatcher),
		larkws.WithAutoReconnect(true),
	)
	if logger != nil {
		cli = larkws.NewClient(
			a.appID, a.appSecret,
			larkws.WithEventHandler(dispatcher),
			larkws.WithAutoReconnect(true),
			larkws.WithLogLevel(larkcore.LogLevelInfo),
		)
	}

	if logger != nil {
		logger.Info("lark long-connection starting", zap.String("app_id", a.appID))
	}
	// 阻塞调用,ctx 取消时返回。SDK 自带重连,通常不会退出。
	if err := cli.Start(ctx); err != nil {
		if logger != nil {
			logger.Error("lark long-connection exited", zap.Error(err))
		}
		return err
	}
	return nil
}

// convertP2Message 把 SDK 的 *P2MessageReceiveV1 事件转成 Pieqi 内部的
// model.Message。逻辑与 webhook 模式的 convertMessage 等价,只是数据
// 源从手撸 larkEvent struct 变成 SDK 的 typed event。
//
// 重点字段:
//   - Sender.SenderId.OpenId  → model.Message.UserID(身份绑定核心字段)
//   - Message.Content         → JSON 字符串,需解析出 {"text":"..."}
//   - Message.ChatId          → model.Message.ChatID(回推目标)
//   - Message.ChatType        → p2p/group,影响下游处理(目前仅记录)
//
// nil-safe:所有 SDK 字段都是 *string,需要解引用前判空。
func convertP2Message(event *larkim.P2MessageReceiveV1) model.Message {
	if event == nil || event.Event == nil {
		return model.Message{Channel: model.ChannelLark}
	}
	ev := event.Event

	// userID:OpenID 优先(身份绑定用),fallback UserID
	userID := ""
	if ev.Sender.SenderId != nil {
		if ev.Sender.SenderId.OpenId != nil {
			userID = *ev.Sender.SenderId.OpenId
		} else if ev.Sender.SenderId.UserId != nil {
			userID = *ev.Sender.SenderId.UserId
		}
	}

	// content:解析 {"text":"..."}
	text := ""
	if ev.Message != nil && ev.Message.Content != nil {
		var c struct{ Text string `json:"text"` }
		if json.Unmarshal([]byte(*ev.Message.Content), &c) == nil {
			text = c.Text
		} else {
			text = *ev.Message.Content // 非标准 JSON 时降级用原文
		}
	}

	// chatID / chatType
	chatID, chatType := "", ""
	if ev.Message != nil {
		if ev.Message.ChatId != nil {
			chatID = *ev.Message.ChatId
		}
		if ev.Message.ChatType != nil {
			chatType = *ev.Message.ChatType
		}
	}

	_ = chatType // 暂仅记录,后续按会话类型分支可加
	return model.Message{
		Channel: model.ChannelLark,
		ChatID:  chatID,
		UserID:  userID,
		Content: text,
		Raw:     event,
	}
}
