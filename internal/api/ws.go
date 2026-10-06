package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"

	"pieqi/internal/model"
)

// wsPingInterval 心跳 ping 间隔。包级变量以便测试缩短（回归测试验证 ping 后事件仍送达）。
var wsPingInterval = 30 * time.Second

// handleWS WebSocket 状态推送（REQ-06）。
// 连接后先发当前任务快照，再转发 EventBus 事件。
//
// 修复（2026-08-27）：曾只写不读，心跳 conn.Ping 等待 pong 时无人读取 socket，
// 客户端 pong 永远不被消费 → Ping 永久阻塞 → 事件转发循环挂死，
// 表现为"会话 30s 空闲后不再实时更新，刷新才恢复"。
// 现加读协程消费控制帧（pong/close/ping），并给 Ping 加超时兜底。
func (s *Server) handleWS(c *gin.Context) {
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		// 本地：不校验 Origin
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "closing")

	ctx := c.Request.Context()
	sub := s.bus.Subscribe(64)
	defer s.bus.Unsubscribe(sub)

	// 读协程：消费客户端的控制帧。coder/websocket 的 Ping 依赖 Reader 读取 pong
	// 才返回；同时读协程能及时发现客户端断开并 Close 连接，让写循环退出（前端
	// onclose 自动重连）。客户端不发消息时 Reader 阻塞等待，无 CPU 开销。
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.Reader(ctx); err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	}()

	// 1. 发当前任务快照
	//
	// ⚠️ 快照只带**轻量视图**（无 events）：早期实现用 s.store.List() 推全量，
	// 实测 24 个任务达 11MB —— 而快照是在订阅之后**阻塞写**的第一条消息，
	// 前端解析这 11MB 期间订阅缓冲（64）必然溢出，恰好在重连那一刻把
	// 终态事件丢掉（见 event_bus.go 的说明）。事件流改为前端按需拉详情。
	if err := s.writeSnapshot(ctx, conn); err != nil {
		return
	}

	// 2. 转发事件
	evCh := sub.Chan()
	for {
		select {
		case <-ctx.Done():
			return
		case <-readDone:
			return // 客户端断开（读协程已 Close 连接）
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		case <-resyncTick():
			// 订阅缓冲曾满 → 有事件被丢弃，本连接已不可信。
			// 必须显式告知前端重拉，否则它会永久停在过期视图上
			// （历史 bug：终态 task_completed 被丢 → 详情不刷新、输入框一直禁用）。
			if !sub.TakeDropped() {
				continue
			}
			if err := s.writeSnapshot(ctx, conn); err != nil {
				return
			}
		case <-time.After(wsPingInterval):
			// 心跳 ping：探测静默死连接（TCP 假死）。带 3s 超时兜底——
			// 若客户端不回 pong，超时即关闭连接交给前端重连，绝不阻塞写循环。
			pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// writeSnapshot 下发一次全量状态快照。
//
// 用途有二：连接建立时的首帧；**丢弃事件后的重同步**（见 Publish 的 dropped 标记）。
// 重同步直接复用同一条 snapshot 消息 —— 前端对 snapshot 的处理本来就是"全量替换 + 去重"，
// 不需要为补拉再造一套协议，也就不会出现两条路径行为不一致。
func (s *Server) writeSnapshot(ctx context.Context, conn *websocket.Conn) error {
	snapshot := gin.H{
		"type":  "snapshot",
		"tasks": model.NewTaskSummaries(s.store.List()),
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// wsResyncInterval 丢弃检查间隔。包级变量以便测试缩短。
var wsResyncInterval = 500 * time.Millisecond

func resyncTick() <-chan time.Time { return time.After(wsResyncInterval) }

// 静默引用 http 以备未来扩展
var _ = http.StatusOK
