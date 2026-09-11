package main

import (
	"context"
	"sync"

	"pieqi/internal/channel/lark"
	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/larkreg"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// larkChannelController 持有运行中的飞书渠道实例，提供配置热应用能力。
//
// 多机器人（D1）：**一个实例 = 一台机器人**。实例集合由 BotStore 决定 ——
//
//	BotStore 非空 → 每台机器人一个实例（botID = model.Bot.ID，凭据取
//	                 BotStore.CredsPath(id)），渠道级实例不存在；
//	BotStore 为空 → 退回渠道级单实例（botID = ""，凭据取 config.yaml +
//	                 全局凭据文件，即接入多机器人之前的既有行为）。
//
// 为什么保留渠道级实例：老部署（扫码只写了全局凭据、没有 bot 记录）
// 必须继续能跑；BotStore 一旦有内容，它就成为唯一事实来源。
//
// 路由：飞书是「同渠道多实例」，HTTP 入口由 lark.WebhookMux 独占并在
// 启动时一次性注册（新机器人在运行期产生，gin 无法安全追加路由）。
// 这也是**接入方式切换不再需要重启**的原因。
type larkChannelController struct {
	logger *zap.Logger
	bridge *core.Bridge
	router gin.IRouter
	bots   *core.BotStore // nil = 未启用多机器人

	mu        sync.Mutex
	cfg       config.LarkConfig // 渠道级兜底配置（已被全局凭据文件覆盖）
	mux       *lark.WebhookMux
	instances map[string]*larkInstance // botID -> 实例（"" = 渠道级）
}

// larkInstance 一台运行中的机器人实例。
type larkInstance struct {
	botID   string
	adapter *lark.Adapter
	cancel  context.CancelFunc // longconn goroutine 的取消句柄；webhook 模式为 nil
	mode    string
	spec    larkSpec // 构造时用的参数，用于差量判断「要不要重建」
}

// sameSpec 判断两台实例的构造参数是否完全一致（一致则原地复用，不重连）。
func sameSpec(a, b larkSpec) bool { return a == b }

// newLarkChannelController 构造控制器。bots 为 nil 时退化为单实例行为。
func newLarkChannelController(logger *zap.Logger, bridge *core.Bridge, router gin.IRouter, bots *core.BotStore) *larkChannelController {
	return &larkChannelController{logger: logger, bridge: bridge, router: router, bots: bots}
}

// Init 注册路由并按当前 BotStore 建实例。仅启动时调用一次。
func (c *larkChannelController) Init(cfg config.LarkConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
	c.mux = lark.NewWebhookMux()
	c.mux.Init(c.router)
	return c.rebuildLocked()
}

// Apply 把手工配置热应用到运行中的渠道（POST /api/larkreg/config 落盘后调用）。
//
// **它不写任何机器人自己的凭据文件。** 这一点是刻意划清的：per-bot 凭据由
// 「落 bot 记录」那条路负责（设备流在 api/larkreg.go 的 poll、手动在
// larkRegConfigUpdate 的 upsert），两边都写在**它自己那一台**的路径上。
//
// 早先这里会把新配置照样写进**管理员机器人**的凭据文件，当第二台机器人接入时
// 这等于用第二台的 app_secret 覆盖第一台 —— 两台机器人随后都指向同一个飞书应用，
// 而列表上它们仍是两台。多机器人下"改一台"绝不能波及另一台。
//
// 所以这里只做两件事：更新渠道级兜底配置（BotStore 为空时的唯一来源）
// 并按当前 BotStore 重建实例。
//
// 返回值恒为 false（不需要重启）：webhook 路由已常驻，接入方式切换靠
// 重建实例 + WebhookMux 的模式判定生效，不再依赖启动时注册路由。
func (c *larkChannelController) Apply(cfg larkreg.ChannelConfig) (restartRequired bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 渠道级镜像：BotStore 为空时它就是唯一来源；非空时它也仍被
	// loadLarkChannelConfig 当作启动兜底，保持两者一致避免困惑。
	c.cfg.AppID = cfg.AppID
	c.cfg.AppSecret = cfg.AppSecret
	c.cfg.VerifyToken = cfg.VerifyToken
	c.cfg.EncryptKey = cfg.EncryptKey
	c.cfg.EventMode = cfg.EventMode

	return false, c.rebuildLocked()
}

// Rebuild 按当前 BotStore 重建实例（新增 / 删除机器人后调用）。
func (c *larkChannelController) Rebuild() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rebuildLocked()
}

// larkSpec 一台实例的构造参数。
type larkSpec struct {
	botID       string
	appID       string
	appSecret   string
	verifyToken string
	encryptKey  string
	eventMode   string
}

// specsLocked 计算目标实例集合（调用方须持有 c.mu）。
func (c *larkChannelController) specsLocked() []larkSpec {
	if c.bots != nil {
		var specs []larkSpec
		for _, bot := range c.bots.List() {
			if bot.Channel != model.ChannelLark {
				continue
			}
			creds, ok := larkreg.LoadConfig(c.bots.CredsPath(bot.ID))
			if !ok {
				// 有记录无凭据（手删了文件 / 落盘失败）：跳过这台，不让
				// 它拖垮其余机器人。缺失本身由 /api/bots 侧暴露给用户。
				c.logger.Warn("lark bot has no usable credentials; skipping instance",
					zap.String("bot", bot.ID), zap.String("name", bot.Name))
				continue
			}
			specs = append(specs, larkSpec{
				botID:       bot.ID,
				appID:       creds.AppID,
				appSecret:   creds.AppSecret,
				verifyToken: creds.VerifyToken,
				encryptKey:  creds.EncryptKey,
				eventMode:   c.normalizeMode(creds.EventMode),
			})
		}
		if len(specs) > 0 {
			return specs
		}
		// BotStore 有记录但一台都不可用 → 落到渠道级兜底，保持渠道可用。
	}

	if c.cfg.AppID == "" || c.cfg.AppSecret == "" {
		return nil
	}
	return []larkSpec{{
		botID:       "", // 渠道级实例：渠道名寻址，不参与按机器人收紧
		appID:       c.cfg.AppID,
		appSecret:   c.cfg.AppSecret,
		verifyToken: c.cfg.VerifyToken,
		encryptKey:  c.cfg.EncryptKey,
		eventMode:   c.normalizeMode(c.cfg.EventMode),
	}}
}

// normalizeMode 归一接入方式：机器人凭据未写 event_mode 时继承渠道级设置，
// 再空则用 "webhook"（与 config 的 setDefault 一致）。
func (c *larkChannelController) normalizeMode(mode string) string {
	if mode == "" {
		mode = c.cfg.EventMode
	}
	if mode != "longconn" {
		return "webhook"
	}
	return "longconn"
}

// rebuildLocked 重建实例集合（调用方须持有 c.mu）。
//
// **差量**：构造参数未变的实例原地复用。全量重建会 cancel 掉所有 longconn ——
// 而绝大多数调用只影响一台（新增 / 删除一台机器人），没理由让其余机器人一起掉线重连。
// 变的与消失的才停：longconn 实例若不停，同一 app 会同时存在两条 wss 连接。
func (c *larkChannelController) rebuildLocked() error {
	specs := c.specsLocked()

	desired := make(map[string]larkSpec, len(specs))
	for _, sp := range specs {
		desired[sp.botID] = sp
	}

	next := make(map[string]*larkInstance, len(specs))
	for id, inst := range c.instances {
		if sp, ok := desired[id]; ok && sameSpec(sp, inst.spec) {
			next[id] = inst // 复用：连得上的继续连着
			delete(desired, id)
			continue
		}
		if inst.cancel != nil {
			inst.cancel()
		}
	}

	// 新建变化的/新增的实例
	fresh := make([]*larkInstance, 0, len(desired))
	for _, sp := range specs {
		if _, reused := next[sp.botID]; reused {
			continue
		}
		a := lark.New(sp.appID, sp.appSecret, sp.verifyToken, sp.encryptKey).WithLogger(c.logger)
		if sp.eventMode == "longconn" {
			a = lark.NewLongConn(sp.appID, sp.appSecret).WithLogger(c.logger)
		}
		if sp.botID != "" {
			a = a.WithBotID(sp.botID)
		}
		inst := &larkInstance{botID: sp.botID, adapter: a, mode: sp.eventMode, spec: sp}
		next[sp.botID] = inst
		fresh = append(fresh, inst)
	}

	// 按 specs 顺序产出路由表：顺序 = BotStore 的创建顺序（有序），
	// 这是 Bridge 侧「无管理员时回退首台」能**确定**的前提 —— 遍历 map 会丢顺序。
	adapters := make([]*lark.Adapter, 0, len(specs))
	refs := make([]core.BotSenderRef, 0, len(specs))
	for _, sp := range specs {
		inst := next[sp.botID]
		adapters = append(adapters, inst.adapter)
		refs = append(refs, core.BotSenderRef{BotID: sp.botID, Channel: "lark", Sender: inst.adapter})
	}

	c.instances = next
	if c.mux != nil { // Init 未调用时（测试 / 未启用渠道）没有 mux
		c.mux.Replace(adapters)
	}
	c.bridge.SyncBotSenders(refs)
	for _, inst := range fresh {
		c.bridge.BindReceiver(inst.adapter)
		if inst.mode == "longconn" {
			c.startLongConn(inst)
		} else {
			c.logger.Info("lark instance enabled",
				zap.String("bot", inst.botID), zap.String("event_mode", "webhook"))
		}
	}

	if len(next) == 0 {
		c.logger.Info("lark channel enabled but no usable credentials; no instance started")
	}
	return nil
}

// startLongConn 在后台启动长连接 goroutine（调用方须持有 c.mu）。
func (c *larkChannelController) startLongConn(inst *larkInstance) {
	ctx, cancel := context.WithCancel(context.Background())
	inst.cancel = cancel
	go func() {
		if err := inst.adapter.Start(ctx); err != nil {
			// 主动取消（重建 / 关停）不算故障，避免把正常热更新记成错误。
			if ctx.Err() != nil {
				c.logger.Debug("lark long-connection stopped",
					zap.String("bot", inst.botID))
				return
			}
			c.logger.Error("lark long-connection exited",
				zap.String("bot", inst.botID), zap.Error(err))
		}
	}()
	c.logger.Info("lark instance enabled",
		zap.String("bot", inst.botID), zap.String("event_mode", "longconn"))
}
