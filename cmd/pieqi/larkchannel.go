package main

import (
	"context"
	"fmt"
	"sync"

	"pieqi/internal/channel/lark"
	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/larkreg"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// larkChannelController 持有运行中的飞书渠道实例，提供配置热应用能力。
// 取代 main.go 里内联的 lark adapter 创建/注册/起 goroutine 逻辑，
// 使 POST /api/larkreg/config 保存后能即时重建凭据生效（无需重启）。
//
// 热应用边界：
//   - 同接入方式（longconn→longconn / webhook→webhook）：立即生效。
//     longconn 会 cancel 旧 wss goroutine、用新凭据重建 client（SDK 在
//     Start 时读 adapter 字段）；webhook 路由每次请求读 adapter 最新字段。
//   - webhook→longconn：可热切换（残留的 webhook 路由无害，longconn 新起）。
//   - longconn→webhook：webhook 路由从未注册（longconn 的 Init 是 no-op），
//     运行时无法补注册 gin 路由 → 返回 restartRequired=true。
type larkChannelController struct {
	logger *zap.Logger
	bridge *core.Bridge
	router gin.IRouter

	mu      sync.Mutex
	adapter *lark.Adapter
	cancel  context.CancelFunc // longconn goroutine 的取消句柄
	mode    string             // 当前接入方式
}

// newLarkChannelController 构造控制器。
func newLarkChannelController(logger *zap.Logger, bridge *core.Bridge, router gin.IRouter) *larkChannelController {
	return &larkChannelController{logger: logger, bridge: bridge, router: router}
}

// Init 按配置创建 adapter、注册路由并（longconn 时）启动长连接。
// 等价于原 main.go 内联的 lark 渠道接线。仅启动时调用一次。
func (c *larkChannelController) Init(cfg config.LarkConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var adapter *lark.Adapter
	if cfg.EventMode == "longconn" {
		adapter = lark.NewLongConn(cfg.AppID, cfg.AppSecret).WithLogger(c.logger)
	} else {
		adapter = lark.New(cfg.AppID, cfg.AppSecret, cfg.VerifyToken, cfg.EncryptKey)
	}
	if err := adapter.Init(c.router); err != nil {
		return fmt.Errorf("init lark: %w", err)
	}
	c.bridge.RegisterReceiver(adapter)
	c.adapter = adapter
	c.mode = cfg.EventMode
	if cfg.EventMode == "longconn" {
		c.startLongConn()
	} else {
		c.logger.Info("lark channel enabled", zap.String("event_mode", "webhook"))
	}
	return nil
}

// Apply 把新配置热应用到运行中的渠道，返回是否需要重启生效。
// 供 api.Server 的 POST /api/larkreg/config 在落盘后调用。
func (c *larkChannelController) Apply(cfg larkreg.ChannelConfig) (restartRequired bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.adapter == nil {
		return false, fmt.Errorf("lark channel not initialized")
	}
	modeChanged := c.mode != cfg.EventMode
	c.adapter.SetConfig(cfg.AppID, cfg.AppSecret, cfg.VerifyToken, cfg.EncryptKey, cfg.EventMode)
	c.mode = cfg.EventMode
	if cfg.EventMode == "longconn" {
		// 用新凭据重建 wss client（startLongConnection 在调用时读 adapter 字段）
		if c.cancel != nil {
			c.cancel()
		}
		c.startLongConn()
	}
	// longconn→webhook 需重启补注册 webhook 路由
	return modeChanged && cfg.EventMode == "webhook", nil
}

// startLongConn 在后台启动长连接 goroutine（调用方须持有 c.mu）。
func (c *larkChannelController) startLongConn() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		if err := c.adapter.Start(ctx); err != nil {
			c.logger.Error("lark long-connection exited", zap.Error(err))
		}
	}()
	c.logger.Info("lark channel enabled", zap.String("event_mode", "longconn"))
}
