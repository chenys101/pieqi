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
		Type  string         `json:"type"`
		Tasks []*model.Task  `json:"tasks"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("snapshot parse: %v (data=%s)", err, data)
	}
	if snap.Type != "snapshot" || len(snap.Tasks) != 1 || snap.Tasks[0].ID != task.ID {
		t.Fatalf("snapshot: %+v", snap)
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
