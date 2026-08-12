package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// handleWS WebSocket 状态推送（REQ-06）。
// 连接后先发当前任务快照，再转发 EventBus 事件。
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

	// 1. 发当前任务快照
	snapshot := gin.H{
		"type":   "snapshot",
		"tasks":  s.store.List(),
	}
	if data, err := json.Marshal(snapshot); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, data)
	}

	// 2. 转发事件
	evCh := sub.Chan()
	for {
		select {
		case <-ctx.Done():
			return
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
		case <-time.After(30 * time.Second):
			// 心跳 ping，防止代理断连
			if err := conn.Ping(ctx); err != nil {
				return
			}
		}
	}
}

// 静默引用 http 以备未来扩展
var _ = http.StatusOK
