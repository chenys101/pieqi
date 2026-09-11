package lark

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// WebhookMux 独占飞书的 HTTP 入口，按 bot id 把回调分发给对应的 Adapter。
//
// 为什么需要它（而不是让每个 Adapter 自己注册路由）：
//
//  1. 多台机器人同属 ChannelLark，天然共享 /webhook/lark 前缀；gin 不允许
//     同一路由 pattern 被注册多次（会 panic），所以「一台 adapter 注册一条
//     路由」在 webhook 模式下无法扩展。
//  2. 新机器人在**运行期**产生（扫码一键创建 / 手动配置），而 gin 的服务
//     已启动后追加路由不是并发安全的。故路由只在启动时注册一次，实例集合
//     则随时可换 —— 这也是「接入方式切换不再需要重启」的来源。
//
// 路由约定：
//
//	/webhook/lark            → 渠道级实例（BotID == ""）
//	/webhook/lark/:bot_id    → 具名机器人实例
//
// 每台飞书应用的「事件订阅回调地址」在飞书开放平台侧填成对应路径即可。
type WebhookMux struct {
	mu     sync.RWMutex
	legacy *Adapter            // BotID == "" 的渠道级实例
	byBot  map[string]*Adapter // BotID != "" 的具名实例
}

// NewWebhookMux 构造一个空的分发器。
func NewWebhookMux() *WebhookMux {
	return &WebhookMux{byBot: make(map[string]*Adapter)}
}

// Init 注册两条路由。**仅启动时调用一次**（见类型注释）。
func (m *WebhookMux) Init(router gin.IRouter) {
	router.POST("/webhook/lark", m.handleLegacy)
	router.POST("/webhook/lark/:bot_id", m.handleBot)
}

// Replace 原子替换全部实例：BotID 为空者成为渠道级实例，其余按 id 归档。
// 传入 nil 或空切片 = 清空（此时所有回调一律 404）。
func (m *WebhookMux) Replace(adapters []*Adapter) {
	next := make(map[string]*Adapter, len(adapters))
	var legacy *Adapter
	for _, a := range adapters {
		if a == nil {
			continue
		}
		if a.BotID() == "" {
			legacy = a
			continue
		}
		next[a.BotID()] = a
	}
	m.mu.Lock()
	m.legacy, m.byBot = legacy, next
	m.mu.Unlock()
}

// Lookup 按 bot id 取实例（botID 为空调取渠道级实例）。测试与诊断用。
func (m *WebhookMux) Lookup(botID string) (*Adapter, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if botID == "" {
		return m.legacy, m.legacy != nil
	}
	a, ok := m.byBot[botID]
	return a, ok
}

func (m *WebhookMux) handleLegacy(c *gin.Context) { m.serve("", c) }

func (m *WebhookMux) handleBot(c *gin.Context) { m.serve(c.Param("bot_id"), c) }

func (m *WebhookMux) serve(botID string, c *gin.Context) {
	a, ok := m.Lookup(botID)
	if !ok {
		// 未注册的 bot（未接入 / 已删除 / 路径写错）。明确 404 便于排查，
		// 响应体不泄漏内部状态。
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown bot"})
		return
	}
	// 长连接实例不受理 HTTP 回调。路由现在是常驻的，若不做模式判定，
	// longconn 部署会凭空多出一个可被伪造的入口。
	if a.Mode() != "webhook" {
		c.JSON(http.StatusNotFound, gin.H{"error": "webhook not enabled for this bot"})
		return
	}
	a.handleWebhook(c)
}
