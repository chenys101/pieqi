package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
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
	snapshot := gin.H{
		"type":  "snapshot",
		"tasks": s.store.List(),
	}
	if data, err := json.Marshal(snapshot); err == nil {
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			return
		}
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

// 静默引用 http 以备未来扩展
var _ = http.StatusOK
