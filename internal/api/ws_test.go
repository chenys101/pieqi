package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func setupWSTest(t *testing.T) (*gin.Engine, *core.TaskStore, *core.EventBus) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(5 * time.Second)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "", false, "", 0, nil, 0, 0, "main")
	cfg := &config.Config{}
	srv := NewServer(cfg, store, runner, hooks, bus, nil, nil)
	r := gin.New()
	srv.Register(r)
	return r, store, bus
}

func TestWS_SnapshotAndEvent(t *testing.T) {
	r, store, bus := setupWSTest(t)
	// 预置一个任务
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	server := httptest.NewServer(r)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// 1. 应收到 snapshot
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Type  string                `json:"type"`
		Tasks []*model.TaskSummary  `json:"tasks"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("snapshot parse: %v (data=%s)", err, data)
	}
	if snap.Type != "snapshot" || len(snap.Tasks) != 1 || snap.Tasks[0].ID != task.ID {
		t.Fatalf("snapshot: %+v", snap)
	}
	// 回归：快照不得携带事件流。用 List() 推全量时 24 个任务达 11MB，
	// 前端解析期间订阅缓冲（64）必然溢出 → 恰好丢掉终态事件。
	if strings.Contains(string(data), `"events"`) {
		t.Fatalf("快照不应包含 events 字段: %.300s", data)
	}

	// 2. 发布事件 -> 应收到
	bus.Publish(core.Event{Type: "task_updated", TaskID: task.ID, Task: task})

	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ev core.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("event parse: %v", err)
	}
	if ev.Type != "task_updated" || ev.TaskID != task.ID {
		t.Fatalf("event: %+v", ev)
	}
}

// TestWS_PingDoesNotHangLoop 回归测试（2026-08-27）：旧代码只写不读，心跳 conn.Ping
// 等待 pong 但无人消费 socket 上的 pong → Ping 永久阻塞 → 事件转发循环挂死，
// 表现为"会话空闲 30s 后不再实时更新，刷新才恢复"。修复后读协程消费 pong，Ping 正常返回。
func TestWS_PingDoesNotHangLoop(t *testing.T) {
	r, store, bus := setupWSTest(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	// 缩短心跳间隔，避免等 30s
	old := wsPingInterval
	wsPingInterval = 200 * time.Millisecond
	defer func() { wsPingInterval = old }()

	server := httptest.NewServer(r)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// 1. 收到 snapshot
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}

	// 2. 空闲超过一个心跳周期（期间服务端会发 ping，客户端自动回 pong）
	time.Sleep(wsPingInterval * 2)

	// 3. 心跳之后事件必须仍能送达（旧 bug：服务端循环挂死在 Ping 里，此处会超时）
	bus.Publish(core.Event{Type: "task_updated", TaskID: task.ID, Task: task})
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("event not delivered after heartbeat: %v", err)
	}
	var ev core.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("event parse: %v", err)
	}
	if ev.Type != "task_updated" || ev.TaskID != task.ID {
		t.Fatalf("event: %+v", ev)
	}
}

// 回归（2026-10-06「重启后详情不实时刷新、追加内容不展示、输入框一直禁用」）：
//
// 订阅缓冲溢出时事件被丢弃，但订阅者必须**被通知去重同步** ——
// 否则前端永久停在过期视图（那次被丢的恰是 task_completed）。
// 这里直接验证：把缓冲打满触发丢弃后，连接上必须再收到一条 snapshot。
func TestWS_ResyncsAfterDroppedEvents(t *testing.T) {
	r, store, bus := setupWSTest(t)
	store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	// 缩短丢弃检查间隔，避免等 500ms
	oldResync := wsResyncInterval
	wsResyncInterval = 50 * time.Millisecond
	defer func() { wsResyncInterval = oldResync }()

	server := httptest.NewServer(r)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// 1. 首帧 snapshot
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}

	// 2. 高频发布把订阅缓冲（Subscribe(64)）打满。
	//    客户端此刻**不读**，缓冲必然溢出 → 置位 dropped 标记。
	for i := 0; i < 400; i++ {
		bus.Publish(core.Event{Type: "task_delta", TaskID: "flood"})
	}

	// 3. 丢弃被观察到后，服务端应主动补发一条 snapshot（重同步）。
	//    期间会读到积压的 task_delta，所以要循环找到 snapshot 为止。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			break
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &probe) == nil && probe.Type == "snapshot" {
			return // 收到重同步快照，符合预期
		}
	}
	t.Fatal("事件被丢弃后未收到重同步 snapshot —— 前端会永久停留在过期视图")
}
