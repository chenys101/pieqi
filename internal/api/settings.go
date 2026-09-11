package api

import (
	"net/http"

	"pieqi/internal/core"

	"github.com/gin-gonic/gin"
)

// settingsDto 是 Settings 的对外投影。
//
// 它是**显式**的一层，不是直接序列化 core.Settings：这样"哪些字段可以下发"
// 是一个可以读出来的决定，而不是"结构体改了什么就自动漏出去什么"。
// L2 / L3 **没有对应字段** —— 它们的不可配置性由"字段不存在"承载。
type settingsDto struct {
	AutoApproveL0  bool   `json:"auto_approve_l0"`
	AutoApproveL1  bool   `json:"auto_approve_l1"`
	DNDEnabled     bool   `json:"dnd_enabled"`
	DNDStart       string `json:"dnd_start"`
	DNDEnd         string `json:"dnd_end"`
	EventRetention int    `json:"event_retention"`
}

func toSettingsDTO(s core.Settings) settingsDto {
	return settingsDto{
		AutoApproveL0:  s.AutoApproveL0,
		AutoApproveL1:  s.AutoApproveL1,
		DNDEnabled:     s.DNDEnabled,
		DNDStart:       s.DNDStart,
		DNDEnd:         s.DNDEnd,
		EventRetention: s.EventRetention,
	}
}

// getSettings 处理 GET /api/settings。
//
// 与 /api 同套鉴权（内网放行 / 外网需 token）：设置页在手机上也要能读。
// 这几项都不含秘密，读放开没有代价。
func (s *Server) getSettings(c *gin.Context) {
	if s.settings == nil {
		// 未接线：回落到出厂默认，而不是报错 —— 前端只有一份渲染逻辑，
		// 不该为"后端没接上"长一套降级界面。
		c.JSON(http.StatusOK, toSettingsDTO(core.DefaultSettings()))
		return
	}
	c.JSON(http.StatusOK, toSettingsDTO(s.settings.Get()))
}

// patchSettings 处理 PATCH /api/settings。补丁语义：未传字段保持不变。
//
// 鉴权与 /api 主组一致（内网放行 / 外网需有效 token）。理由：token 本身
// 就是**管理员的代理凭据**（只经 IM 交付给绑定的管理员），所以持有 token 的
// 外部请求就是管理员在操作。这与 /api/tasks 的粒度一致 —— 后者同样允许
// 用 token 创建会跑 agent 的任务，严格说比改自动放行开关更有威力。
func (s *Server) patchSettings(c *gin.Context) {
	if s.settings == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "settings store not configured"})
		return
	}
	var body struct {
		AutoApproveL0  *bool   `json:"auto_approve_l0"`
		AutoApproveL1  *bool   `json:"auto_approve_l1"`
		DNDEnabled     *bool   `json:"dnd_enabled"`
		DNDStart       *string `json:"dnd_start"`
		DNDEnd         *string `json:"dnd_end"`
		EventRetention *int    `json:"event_retention"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	next, err := s.settings.Patch(core.SettingsPatch{
		AutoApproveL0:  body.AutoApproveL0,
		AutoApproveL1:  body.AutoApproveL1,
		DNDEnabled:     body.DNDEnabled,
		DNDStart:       body.DNDStart,
		DNDEnd:         body.DNDEnd,
		EventRetention: body.EventRetention,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toSettingsDTO(next))
}
