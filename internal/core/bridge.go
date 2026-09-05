package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"pieqi/internal/auth"
	"pieqi/internal/channel"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// TunnelOps 是 Bridge 对隧道系统的依赖抽象（由 auth.TunnelManager 实现）。
// 定义为接口便于单测注入 fake，避免测试真正拉起 cloudflared 子进程。
type TunnelOps interface {
	Start(ctx context.Context, ttl time.Duration) (auth.TunnelResult, error)
	Stop(ctx context.Context) error
	RenewToken(ctx context.Context, ttl time.Duration) (auth.TunnelResult, error)
}

// AdminBinding 判定消息发送者是否为绑定管理员（由 auth.BindingStore 实现）。
type AdminBinding interface {
	Match(openid string) bool
}

// Bridge IM 渠道编排器。
//
// Pieqi 模式下 IM 渠道承担两件事：
//   - 隧道命令：绑定管理员发「隧道」启动 / 「关隧道」停止 cloudflared 临时隧道，
//     机器人把 lark:// 深链 + 隧道 URL 回发到聊天（PRD §4.3/§5.2 从飞书 IM 发起）。
//   - 通知通道：非命令消息回复固定提示，引导用户在 PWA 新建任务；
//     任务执行结果通过 NotifyOrigin 回推到原 IM 会话（由 TaskRunner.SetNotifier 注入）。
type Bridge struct {
	logger    *zap.Logger
	receivers []channel.MessageReceiver
	senders   map[string]channel.MessageSender

	// Pieqi 模式（pieqi.enabled 时注入）
	pieqi *pieqiMode

	// 隧道命令（main.go 注入；nil = 隧道未启用）
	tunnel       TunnelOps
	adminBinding AdminBinding

	// 隧道自动自愈通知：最近一次由管理员在 IM 操作隧道的飞书会话 chat_id。
	// TunnelManager 的自愈回调（OnHeal / OnHealErr）经 NotifyTunnelHeal /
	// NotifyTunnelHealErr 往这里推送新链接。只记最近一个操作者：若 A 开启后
	// B 又操作过，B 会收到自愈通知（可接受，见 NotifyTunnelHeal 注释）。
	// 若隧道仅由 API（非 IM）启动则此字段为空，推送安全降级为 no-op。
	notifyMu         sync.Mutex
	lastTunnelChatID string
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

// EnableTunnelOps 注入隧道系统 + 管理员判定，开启 IM 隧道命令。
// 由 main.go 在 TunnelManager / BindingStore 创建后调用；nil 安全。
func (b *Bridge) EnableTunnelOps(t TunnelOps, admin AdminBinding) {
	b.tunnel = t
	b.adminBinding = admin
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

// Sender 按渠道名取已注册 sender（P2 PushRegistry 注册 IM provider 用）。
func (b *Bridge) Sender(name string) (channel.MessageSender, bool) {
	s, ok := b.senders[name]
	return s, ok
}

// -- Message handling --

func (b *Bridge) handleMessage(msg model.Message) {
	b.logger.Info("received",
		zap.String("channel", string(msg.Channel)),
		zap.String("user_id", msg.UserID),
		zap.String("chat_id", msg.ChatID),
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

// NotifyTunnelHeal 在隧道被自动健康检查换新域名后，向最近一次在 IM 操作隧道的
// 飞书会话推送新链接（由 TunnelManager 的 OnHeal 回调触发，见 main.go 接线）。
// 旧链接已被 Cloudflare 回收，必须让管理员拿到新链接重新分发。
func (b *Bridge) NotifyTunnelHeal(res auth.TunnelResult) {
	b.notifyMu.Lock()
	chatID := b.lastTunnelChatID
	b.notifyMu.Unlock()
	if chatID == "" {
		// 隧道可能由 API（非 IM）启动：没有可推送的会话，静默降级。
		b.logger.Debug("tunnel healed but no admin chat recorded; skip push")
		return
	}
	s, ok := b.senders[string(model.ChannelLark)]
	if !ok {
		b.logger.Debug("tunnel healed but lark sender not registered; skip push")
		return
	}
	text := fmt.Sprintf(
		"⚠️ 隧道域名已被 Cloudflare 回收，旧链接已失效。\n🛠 已自动重启并换用新域名：\n🔗 飞书内打开: %s\n🌐 直接访问: %s\n⏰ 到期: %s",
		res.LarkDeepLink, res.TunnelURL, res.ExpiresAt.Format("15:04:05"))
	b.sendChunk(s, chatID, text)
}

// NotifyTunnelHealErr 在自动自愈失败（旧隧道已死、新 cloudflared 起不来）时，
// 通知管理员隧道已完全下线，需手动重启。
func (b *Bridge) NotifyTunnelHealErr(_ error) {
	b.notifyMu.Lock()
	chatID := b.lastTunnelChatID
	b.notifyMu.Unlock()
	if chatID == "" {
		b.logger.Debug("tunnel heal failed but no admin chat recorded; skip push")
		return
	}
	s, ok := b.senders[string(model.ChannelLark)]
	if !ok {
		b.logger.Debug("tunnel heal failed but lark sender not registered; skip push")
		return
	}
	text := "🚨 隧道自动恢复失败，当前已无可用隧道。\n请在聊天里发送「隧道」手动重启。"
	b.sendChunk(s, chatID, text)
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
// 隧道命令（绑定管理员）：
//   - 「隧道」/「tunnel」/「/tunnel」→ 启动（默认 15m）
//   - 「隧道 1h」「隧道 4h」→ 指定 TTL（15m/1h/4h）
//   - 「续期」「延期」「续隧道」/「tunnel renew」→ 续期（默认 +15m，token 不变）
//   - 「续期 1h」「续期 4h」→ 指定续期时长
//   - 「关隧道」「停隧道」「tunnel stop」→ 停止
//
// 其他消息：项目不再预注册，IM 渠道无法用 #标签 解析目标项目，故回复固定
// 提示，引导用户在 PWA 新建任务。任务执行结果仍会通过 OriginChannel
// 回推到原 IM 会话（由 TaskRunner.notify 触发）。
func (b *Bridge) handlePieqiMessage(msg model.Message, content string) {
	op, ttl := parseTunnelCommand(content)
	if op == "" {
		b.reply(msg, "💡 请在 PWA 新建任务（选择项目 + 填写描述），任务进展会推送到这里。")
		return
	}
	b.handleTunnelCommand(msg, op, ttl)
}

// handleTunnelCommand 处理 IM 隧道命令。
// 权限：仅飞书渠道 + 绑定管理员（PRD §3.5 —— 隧道是外网入口，属特权操作）。
func (b *Bridge) handleTunnelCommand(msg model.Message, op string, ttl time.Duration) {
	if b.tunnel == nil {
		b.reply(msg, "⚠️ 隧道系统未启用（config 未接线）。")
		return
	}
	// 身份：仅 lark 渠道（消息 UserID 即飞书 open_id）+ 绑定管理员可操作。
	// 其他渠道无 open_id 语义，一律拒绝。
	if msg.Channel != model.ChannelLark || b.adminBinding == nil || !b.adminBinding.Match(msg.UserID) {
		b.reply(msg, "⛔ 仅绑定的飞书管理员可操作隧道。")
		return
	}

	// 记录最近操作隧道的飞书会话，供自动自愈（NotifyTunnelHeal/Err）推送新链接。
	b.notifyMu.Lock()
	b.lastTunnelChatID = msg.ChatID
	b.notifyMu.Unlock()

	switch op {
	case "stop":
		if err := b.tunnel.Stop(context.Background()); err != nil {
			b.reply(msg, "❌ 关闭隧道失败: "+err.Error())
			return
		}
		b.reply(msg, "🔒 隧道已关闭。")
	case "start":
		res, err := b.tunnel.Start(context.Background(), ttl)
		if err != nil {
			b.reply(msg, "❌ 隧道启动失败: "+err.Error())
			return
		}
		b.reply(msg, fmt.Sprintf(
			"🔓 隧道已开启（%s）\n🔗 飞书内打开: %s\n🌐 直接访问: %s\n⏰ 到期: %s",
			formatTTL(ttl), res.LarkDeepLink, res.TunnelURL,
			res.ExpiresAt.Format("15:04:05")))
	case "renew":
		res, err := b.tunnel.RenewToken(context.Background(), ttl)
		if err != nil {
			b.reply(msg, "❌ 续期失败: "+err.Error())
			return
		}
		b.reply(msg, fmt.Sprintf(
			"♻️ 隧道已续期 +%s\n🔗 飞书内打开: %s\n🌐 直接访问: %s\n⏰ 新到期: %s",
			formatTTL(ttl), res.LarkDeepLink, res.TunnelURL,
			res.ExpiresAt.Format("15:04:05")))
	}
}

// parseTunnelCommand 解析 IM 消息是否命中隧道命令。
// 返回 op（"start"/"stop"/"renew"）+ 期望 TTL；非命令返回 op=""。
func parseTunnelCommand(content string) (op string, ttl time.Duration) {
	c := strings.ToLower(strings.TrimSpace(content))
	if c == "" {
		return "", 0
	}
	// 停止命令
	switch c {
	case "关隧道", "停隧道", "关闭隧道", "tunnel stop", "/tunnel stop":
		return "stop", 0
	}
	// 续期命令：前缀 + 可选 TTL。必须在启动前缀之前匹配（否则 "tunnel renew"
	// 会被启动前缀 "tunnel " 截获并按默认 TTL 启动）。
	for _, prefix := range []string{"续期", "延期", "续隧道", "tunnel renew", "/tunnel renew"} {
		if c == prefix {
			return "renew", 15 * time.Minute
		}
		if strings.HasPrefix(c, prefix+" ") {
			return "renew", parseTTL(strings.TrimSpace(strings.TrimPrefix(c, prefix)))
		}
	}
	// 启动命令：前缀 + 可选 TTL
	for _, prefix := range []string{"隧道", "/tunnel", "tunnel"} {
		if c == prefix {
			return "start", 15 * time.Minute
		}
		if strings.HasPrefix(c, prefix+" ") {
			return "start", parseTTL(strings.TrimSpace(strings.TrimPrefix(c, prefix)))
		}
	}
	return "", 0
}

// parseTTL 解析用户指定的隧道时长，非法输入回退默认 15m。
func parseTTL(s string) time.Duration {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "1h", "60m":
		return time.Hour
	case "4h", "240m":
		return 4 * time.Hour
	default:
		return 15 * time.Minute
	}
}

// formatTTL 把 duration 格式化成人类可读的简短形式（15m / 1h / 4h）。
func formatTTL(d time.Duration) string {
	switch d {
	case time.Hour:
		return "1h"
	case 4 * time.Hour:
		return "4h"
	default:
		return "15m"
	}
}
