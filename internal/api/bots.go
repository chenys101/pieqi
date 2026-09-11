package api

import (
	"net/http"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// listBots 处理 GET /api/bots —— **读接口，挂 ExternalAuth**（不是 BindOpGate）。
//
// 理由：机器人列表是手机 / PWA 上的常规视图（设置页「连接与账号」）。
// 若与写接口同挂 BindOpGate（仅内网），外网一律 403，手机端永远看不到列表。
// 只返回元数据（model.Bot 不含 secret），故读放开是安全的。
func (s *Server) listBots(c *gin.Context) {
	if s.bots == nil {
		c.JSON(http.StatusOK, gin.H{"bots": []model.Bot{}, "admin_bot_id": ""})
		return
	}
	adminID := ""
	if b, ok := s.bots.AdminBot(); ok {
		adminID = b.ID
	}
	c.JSON(http.StatusOK, gin.H{
		"bots":         s.bots.List(),
		"admin_bot_id": adminID,
	})
}

// createBot 处理 POST /api/bots —— 写接口，挂 BindOpGate（仅内网）。
//
// 注意这是**非设备流**的创建路径（手动配置 / 企业微信等）。
// 飞书「一键创建」由 /api/larkreg 的设备流在 poll 成功后落记录（带凭据），
// 两者最终都走 BotStore.Create —— 只有一份创建逻辑。
func (s *Server) createBot(c *gin.Context) {
	if s.bots == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bot store not configured"})
		return
	}
	var body struct {
		Channel   string `json:"channel"`
		Name      string `json:"name"`
		SysPrompt string `json:"sys_prompt"`
		AppID     string `json:"app_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	ch := model.Channel(body.Channel)
	switch ch {
	case model.ChannelLark, model.ChannelWeCom, model.ChannelWeChat:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "channel must be lark, wecom or wechat"})
		return
	}
	bot, err := s.bots.Create(model.Bot{
		Channel:   ch,
		Name:      body.Name,
		SysPrompt: body.SysPrompt,
		AppID:     body.AppID,
	})
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	s.notifyBotChanged()
	c.JSON(http.StatusOK, gin.H{"bot": bot})
}

// updateBot 处理 PATCH /api/bots/:id —— 写接口，挂 BindOpGate（仅内网）。
// 补丁语义：未传字段保持不变（姓名 / 提示词 / 角色各自独立可改）。
func (s *Server) updateBot(c *gin.Context) {
	if s.bots == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bot store not configured"})
		return
	}
	id := c.Param("id")
	var body struct {
		Name      *string `json:"name"`
		SysPrompt *string `json:"sys_prompt"`
		Role      *string `json:"role"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	patch := core.BotPatch{Name: body.Name, SysPrompt: body.SysPrompt}
	if body.Role != nil {
		r := model.BotRole(*body.Role)
		patch.Role = &r
	}
	bot, err := s.bots.Update(id, patch)
	if err != nil {
		status := http.StatusBadRequest
		if _, ok := s.bots.Get(id); !ok {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"bot": bot})
}

// deleteBot 处理 DELETE /api/bots/:id —— 写接口，挂 BindOpGate（仅内网）。
// 解绑：移除索引并尽力删除该机器人的凭据文件，随后触发实例重建 ——
// 否则被删的机器人**仍在收发消息**（它只是从列表里消失了，这最容易被误认为"已下线"）。
func (s *Server) deleteBot(c *gin.Context) {
	if s.bots == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bot store not configured"})
		return
	}
	id := c.Param("id")
	if err := s.bots.Delete(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	s.notifyBotChanged()
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// notifyBotChanged 通知渠道侧重建实例。nil-safe。
func (s *Server) notifyBotChanged() {
	if s.botChanged != nil {
		s.botChanged()
	}
}
