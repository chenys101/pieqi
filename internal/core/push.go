// push.go Evidence Push / Notification Provider（p2-design.md §8）。
//
// 不绑定具体 IM：基于 channel.MessageSender（Bridge.senders）实现 lark 等 IM provider，
// Webhook 做通用 Provider。Task 进终态时自动推 Outcome 摘要（有 OriginChannel 才推）；
// 可手动推 Evidence（POST /api/tasks/:id/push）。
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"pieqi/internal/channel"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// EvidencePushContent 推送内容（p2-design.md §8）。
type EvidencePushContent struct {
	Kind     string       `json:"kind"` // outcome | evidence | error
	Outcome  *TaskOutcome `json:"outcome,omitempty"`
	Evidence *Evidence    `json:"evidence,omitempty"`
	Text     string       `json:"text"` // 精简可读文本（移动端卡片）
}

// ReplyTarget 推送目标（兼容 channel 层的 model.ReplyTarget 语义）。
type PushTarget = model.ReplyTarget

// NotificationProvider 推送通道抽象（provider 可替换，不绑定单一 IM）。
type NotificationProvider interface {
	// Name 注册名（Task.OriginChannel 匹配用，如 "lark"）。
	Name() string
	Push(ctx context.Context, target PushTarget, content EvidencePushContent) error
}

// SenderProvider 把 channel.MessageSender 适配为 NotificationProvider（lark/企微等 IM 复用）。
type SenderProvider struct {
	name   string
	sender channel.MessageSender
}

// NewSenderProvider 创建 IM sender 适配 provider。
func NewSenderProvider(name string, sender channel.MessageSender) *SenderProvider {
	return &SenderProvider{name: name, sender: sender}
}

func (p *SenderProvider) Name() string { return p.name }

func (p *SenderProvider) Push(ctx context.Context, target PushTarget, content EvidencePushContent) error {
	return p.sender.Send(ctx, model.ReplyTarget{ChatID: target.ChatID}, content.Text)
}

// WebhookProvider 通用 HTTP Webhook（POST JSON 到配置 URL）。
type WebhookProvider struct {
	url    string
	client *http.Client
}

// NewWebhookProvider 创建 webhook provider。url 为接收端点（接收 EvidencePushContent JSON）。
func NewWebhookProvider(url string) *WebhookProvider {
	return &WebhookProvider{url: url, client: &http.Client{Timeout: 10 * time.Second}}
}

func (p *WebhookProvider) Name() string { return "webhook" }

func (p *WebhookProvider) Push(ctx context.Context, _ PushTarget, content EvidencePushContent) error {
	body, err := json.Marshal(content)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

// PushRegistry provider 注册表 + 终态自动推送（订阅 EventBus）。
type PushRegistry struct {
	logger    *zap.Logger
	mu        sync.RWMutex
	providers map[string]NotificationProvider

	store *TaskStore
}

// NewPushRegistry 创建注册表。
func NewPushRegistry(logger *zap.Logger, store *TaskStore) *PushRegistry {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PushRegistry{logger: logger, providers: map[string]NotificationProvider{}, store: store}
}

// Register 注册 provider（同名覆盖：可替换）。
func (r *PushRegistry) Register(p NotificationProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Names 已注册 provider 名列表。
func (r *PushRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for n := range r.providers {
		names = append(names, n)
	}
	return names
}

// Push 推送到指定 provider（channel 名）；target 为空 ChatID 时报错。
func (r *PushRegistry) Push(ctx context.Context, channelName string, target PushTarget, content EvidencePushContent) error {
	r.mu.RLock()
	p, ok := r.providers[channelName]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no provider registered: %s", channelName)
	}
	if target.ChatID == "" {
		return fmt.Errorf("target chat_id is empty")
	}
	return p.Push(ctx, target, content)
}

// PushOrigin 推送到 task 的 OriginChannel（未配置或不存在的 provider → 明确错误）。
func (r *PushRegistry) PushOrigin(ctx context.Context, task *model.Task, content EvidencePushContent) error {
	if task.OriginChannel == "" || task.OriginChatID == "" {
		return fmt.Errorf("task has no origin channel")
	}
	return r.Push(ctx, task.OriginChannel, PushTarget{ChatID: task.OriginChatID}, content)
}

// WatchBus 订阅任务事件：Task 终态时自动推 Outcome 摘要到 OriginChannel（§8）。
// 无 OriginChannel / provider 缺失 → 静默跳过（不报错：自动推送是尽力而为）。
func (r *PushRegistry) WatchBus(bus *EventBus) {
	sub := bus.Subscribe(64)
	go func() {
		for ev := range sub.Chan() {
			if ev.Type != "task_completed" && ev.Type != "task_failed" {
				continue
			}
			if ev.Task == nil || r.store == nil {
				continue
			}
			task, ok := r.store.Get(ev.TaskID)
			if !ok || task.OriginChannel == "" || task.OriginChatID == "" {
				continue
			}
			content := r.outcomePushContent(task)
			pushCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			if err := r.Push(pushCtx, task.OriginChannel, PushTarget{ChatID: task.OriginChatID}, content); err != nil {
				r.logger.Warn("push outcome failed",
					zap.String("task", task.ID), zap.String("channel", task.OriginChannel), zap.Error(err))
			}
			cancel()
		}
	}()
}

// outcomePushContent 组装终态 Outcome 推送（精简文本，移动端卡片）。
func (r *PushRegistry) outcomePushContent(task *model.Task) EvidencePushContent {
	kind := "outcome"
	if task.Status == model.TaskFailed {
		kind = "error"
	}
	return EvidencePushContent{
		Kind:  kind,
		Text:  OutcomePushText(task),
	}
}

// OutcomePushText 终态推送的精简可读文本。
func OutcomePushText(task *model.Task) string {
	status := string(task.Status)
	if task.Status == model.TaskCompleted {
		status = "已完成"
	} else if task.Status == model.TaskFailed {
		status = "失败"
	}
	return fmt.Sprintf("📋 任务 %s\n状态：%s\n描述：%s", task.ID, status, task.Prompt)
}
