package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pieqi/internal/channel"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// Bridge IM 渠道编排器。
//
// Pieqi 模式下 IM 渠道仅作通知通道：收到消息回复固定提示，引导用户在 PWA 新建任务；
// 任务执行结果通过 NotifyOrigin 回推到原 IM 会话（由 TaskRunner.SetNotifier 注入）。
type Bridge struct {
	logger    *zap.Logger
	receivers []channel.MessageReceiver
	senders   map[string]channel.MessageSender

	// Pieqi 模式（pieqi.enabled 时注入）
	pieqi *pieqiMode
}

// pieqiMode Pieqi 模式的依赖集合。
type pieqiMode struct {
	store  *TaskStore
	runner *TaskRunner
	bus    *EventBus
}

// EnablePieqi 注入 Pieqi 依赖，IM 消息走 TaskCreator 路径。
func (b *Bridge) EnablePieqi(store *TaskStore, runner *TaskRunner, bus *EventBus) {
	b.pieqi = &pieqiMode{store: store, runner: runner, bus: bus}
}

func NewBridge(logger *zap.Logger) *Bridge {
	return &Bridge{
		logger:  logger,
		senders: make(map[string]channel.MessageSender),
	}
}

func (b *Bridge) RegisterReceiver(receiver channel.MessageReceiver) {
	b.receivers = append(b.receivers, receiver)
	if sender, ok := receiver.(channel.MessageSender); ok {
		b.senders[receiver.Name()] = sender
	}
	receiver.OnMessage(func(msg model.Message) {
		go b.handleMessage(msg)
	})
}

func (b *Bridge) RegisterSender(name string, sender channel.MessageSender) {
	b.senders[name] = sender
}

// -- Message handling --

func (b *Bridge) handleMessage(msg model.Message) {
	b.logger.Info("received",
		zap.String("channel", string(msg.Channel)),
		zap.String("content", msg.Content),
	)

	// Pieqi 模式：IM 仅作通知通道，引导用户在 PWA 新建任务。
	if b.pieqi != nil {
		b.handlePieqiMessage(msg, strings.TrimSpace(msg.Content))
		return
	}

	// 未启用 Pieqi：IM 渠道无 agent 驱动，提示未启用。
	b.reply(msg, "⚠️ Pieqi 未启用，请在配置中开启 pieqi.enabled 后重启。")
}

// -- Reply --

func (b *Bridge) reply(msg model.Message, text string) {
	s, ok := b.senders[string(msg.Channel)]
	if !ok {
		return
	}
	const max = 4000
	if len(text) <= max {
		b.sendChunk(s, msg.ChatID, text)
		return
	}
	chunks := splitText(text, max)
	for i, c := range chunks {
		p := ""
		if len(chunks) > 1 {
			p = fmt.Sprintf("[%d/%d] ", i+1, len(chunks))
		}
		b.sendChunk(s, msg.ChatID, p+c)
	}
}

func (b *Bridge) sendChunk(sender channel.MessageSender, chatID, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sender.Send(ctx, model.ReplyTarget{ChatID: chatID}, text); err != nil {
		b.logger.Error("send", zap.Error(err))
	}
}

// NotifyOrigin 往 task 的 IM 原渠道推送通知（waiting_input/完成/失败回执）。
// 由 TaskRunner 通过 SetNotifier 回调调用。无对应 sender 时静默跳过。
func (b *Bridge) NotifyOrigin(task *model.Task, text string) {
	if task.OriginChannel == "" || task.OriginChatID == "" {
		return
	}
	s, ok := b.senders[task.OriginChannel]
	if !ok {
		return
	}
	const max = 4000
	if len(text) <= max {
		b.sendChunk(s, task.OriginChatID, text)
		return
	}
	chunks := splitText(text, max)
	for i, c := range chunks {
		p := ""
		if len(chunks) > 1 {
			p = fmt.Sprintf("[%d/%d] ", i+1, len(chunks))
		}
		b.sendChunk(s, task.OriginChatID, p+c)
	}
}

// -- Helpers --

func splitText(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > maxLen {
		cut := maxLen
		if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
			cut = idx + 1
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	return chunks
}

// handlePieqiMessage Pieqi 模式下处理 IM 消息。
//
// 项目不再预注册，IM 渠道无法用 #标签 解析目标项目，故 IM 仅作通知通道：
// 回复固定提示，引导用户在 PWA 新建任务。任务执行结果仍会通过 OriginChannel
// 回推到原 IM 会话（由 TaskRunner.notify 触发）。
func (b *Bridge) handlePieqiMessage(msg model.Message, content string) {
	b.reply(msg, "💡 请在 PWA 新建任务（选择项目 + 填写描述），任务进展会推送到这里。")
}
