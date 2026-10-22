package api

import (
	"fmt"
	"net/http"

	"pieqi/internal/agent"
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
	// Agents agent 的运行期选择（2026-10-22）。只含**用户意图**：
	// 开不开、装在哪、claude 走哪条传输。代码事实（agent_type / 默认 spawn /
	// 能力声明）在 internal/agent/manifest.go，**不在**这里下发 —— 那是只读的。
	Agents core.AgentSelection `json:"agents"`
}

func toSettingsDTO(s core.Settings) settingsDto {
	return settingsDto{
		AutoApproveL0:  s.AutoApproveL0,
		AutoApproveL1:  s.AutoApproveL1,
		DNDEnabled:     s.DNDEnabled,
		DNDStart:       s.DNDStart,
		DNDEnd:         s.DNDEnd,
		EventRetention: s.EventRetention,
		Agents:         s.Agents,
	}
}

// validateAgentSelection 校验 agent 选择写进来的名字是否都是已知 agent。
//
// 为什么必须校验而不是静默忽略未知名字：写错一个名字（例如 "code-buddy"）会
// 让对应 agent 悄无声息地不出现，用户只会看到"怎么没生效"。回 400 并列出
// 已知名字，比让人猜要好。
//
// 不复用 agent 包的 manifest 查询是因为 api 已经依赖 agent，用它的导出版本即可；
// 这里只在 api 层做**入口校验**（名字合法性），不涉及任何代码事实。
func validateAgentSelection(sel core.AgentSelection) error {
	if len(sel.Enabled) > 0 {
		known := make(map[string]bool)
		for _, m := range agent.AllManifests() {
			known[m.Name] = true
		}
		var unknown []string
		for _, n := range sel.Enabled {
			if !known[n] {
				unknown = append(unknown, n)
			}
		}
		if len(unknown) > 0 {
			names := make([]string, 0, len(known))
			for _, m := range agent.AllManifests() {
				names = append(names, m.Name)
			}
			return fmt.Errorf("unknown agent(s) %v; known: %v", unknown, names)
		}
	}
	if sel.ClaudeTransport != "" && sel.ClaudeTransport != "sdk-bridge" && sel.ClaudeTransport != "print" {
		return fmt.Errorf("claude_transport must be sdk-bridge or print, got %q", sel.ClaudeTransport)
	}
	return nil
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
		// Agents nil = 不修改；非 nil = **整体替换**。
		// 用整体替换而不是逐字段补丁：agent 列表本质是一个集合，
		// "加一个/删一个"的补丁在并发下会互相覆盖（两人同时开关不同 agent 会丢改动）。
		Agents *core.AgentSelection `json:"agents"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	if body.Agents != nil {
		if err := validateAgentSelection(*body.Agents); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	next, err := s.settings.Patch(core.SettingsPatch{
		AutoApproveL0:  body.AutoApproveL0,
		AutoApproveL1:  body.AutoApproveL1,
		DNDEnabled:     body.DNDEnabled,
		DNDStart:       body.DNDStart,
		DNDEnd:         body.DNDEnd,
		EventRetention: body.EventRetention,
		Agents:         body.Agents,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toSettingsDTO(next))
}
