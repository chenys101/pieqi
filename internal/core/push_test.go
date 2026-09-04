// push_test.go Evidence Push 单测（p2-design.md §8）：
// 注册表、SenderProvider 适配、WebhookProvider HTTP、终态自动推送。
package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pieqi/internal/channel"
	"pieqi/internal/model"
)

// pushFakeSender 记录发送内容的 MessageSender 桩。
type pushFakeSender struct {
	mu    chan struct{}
	sent  []string
	gate  chan struct{} // 一次性闸门：收到第一条后关闭
}

func (f *pushFakeSender) Send(_ context.Context, _ model.ReplyTarget, text string) error {
	f.sent = append(f.sent, text)
	if f.gate != nil {
		close(f.gate)
		f.gate = nil
	}
	return nil
}

func (f *pushFakeSender) Name() string { return "fake" }

func TestPushRegistry_SenderProvider(t *testing.T) {
	fs := &pushFakeSender{}
	r := NewPushRegistry(nil, nil)
	r.Register(NewSenderProvider("lark", fs))

	err := r.Push(context.Background(), "lark", PushTarget{ChatID: "c1"},
		EvidencePushContent{Kind: "outcome", Text: "done"})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(fs.sent) != 1 || fs.sent[0] != "done" {
		t.Fatalf("sent: %+v", fs.sent)
	}
	// 空 ChatID 拒绝
	if err := r.Push(context.Background(), "lark", PushTarget{}, EvidencePushContent{}); err == nil {
		t.Fatal("empty chat_id must fail")
	}
	// 未注册 provider
	if err := r.Push(context.Background(), "nope", PushTarget{ChatID: "c"}, EvidencePushContent{}); err == nil {
		t.Fatal("unknown provider must fail")
	}
}

func TestPushRegistry_WebhookProvider(t *testing.T) {
	var got EvidencePushContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	p := NewWebhookProvider(srv.URL)
	if p.Name() != "webhook" {
		t.Fatalf("name: %s", p.Name())
	}
	if err := p.Push(context.Background(), PushTarget{}, EvidencePushContent{Kind: "evidence", Text: "hi"}); err != nil {
		t.Fatalf("webhook push: %v", err)
	}
	if got.Kind != "evidence" || got.Text != "hi" {
		t.Fatalf("webhook received: %+v", got)
	}
}

func TestPushRegistry_PushOrigin(t *testing.T) {
	fs := &pushFakeSender{}
	r := NewPushRegistry(nil, nil)
	r.Register(NewSenderProvider("lark", fs))

	// 无 OriginChannel → 明确错误
	if err := r.PushOrigin(context.Background(), &model.Task{ID: "t"}, EvidencePushContent{}); err == nil {
		t.Fatal("no origin must fail")
	}
	task := &model.Task{ID: "t", OriginChannel: "lark", OriginChatID: "oc1"}
	if err := r.PushOrigin(context.Background(), task, EvidencePushContent{Text: "hello"}); err != nil {
		t.Fatalf("push origin: %v", err)
	}
	if len(fs.sent) != 1 || fs.sent[0] != "hello" {
		t.Fatalf("sent: %+v", fs.sent)
	}
}

func TestPushRegistry_WatchBus_AutoPushOnTerminal(t *testing.T) {
	store, _ := NewTaskStore(t.TempDir())
	task, _ := store.Create(&model.Task{
		ProjectID: "p", ProjectPath: t.TempDir(), WorktreePath: t.TempDir(),
		Prompt: "do it", Status: model.TaskPending,
		OriginChannel: "lark", OriginChatID: "oc1",
	})

	fs := &pushFakeSender{gate: make(chan struct{})}
	r := NewPushRegistry(nil, store)
	r.Register(NewSenderProvider("lark", fs))

	bus := NewEventBus()
	r.WatchBus(bus)

	// 终态事件 → 自动推 Outcome 摘要
	bus.Publish(Event{Type: "task_completed", TaskID: task.ID, Task: task})
	select {
	case <-fs.gate:
	case <-time.After(2 * time.Second):
		t.Fatal("auto push not received within 2s")
	}
	if len(fs.sent) != 1 || fs.sent[0] == "" {
		t.Fatalf("sent: %+v", fs.sent)
	}
}

// 编译期接口断言：pushFakeSender 同时实现 MessageSender（供 SenderProvider 适配）。
var _ channel.MessageSender = (*pushFakeSender)(nil)
