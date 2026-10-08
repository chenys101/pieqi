// model_switch.go：会话内切换模型（POST /api/tasks/:id/model）。
//
// 为什么要有这个接口，而不是只靠续问时的 per-turn model：
// per-turn 那个**只在续问路径生效**、且**不落库**（见 api/tasks.go interveneReq.Model），
// 于是"这个会话现在用的是哪个模型"没有任何持久事实 —— 前端只能靠本地内存猜，
// 刷新页面就没了，用户也无从确认上轮到底跑在哪个模型上。
// 这里把切换做成**会话级、落库、有事件**的一等操作：
//
//	Task.Model 更新为新值（后续轮次的默认值）
//	+ 追加一条 EventModelSwitch（时间线上的切换记录，前端渲染成人读的一行）
//
// 取值仍是 agent 下发的**不透明串**，原样存取，不解析不拼接（见 agent/model_catalog.go）。
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type setTaskModelReq struct {
	// Model 目标模型（不透明选择值，来自 GET /api/agents/{agent}/models）。
	// 空串 = 交还给 agent 自己的默认路由（等价于清空 Task.Model）。
	Model string `json:"model"`
}

// setTaskModel POST /api/tasks/:id/model：把会话切到指定模型。
//
// 与 per-turn model 的分工：
//   - per-turn（intervene.model）：只影响**下一轮**，不落库；
//   - 本接口：改**会话的默认路由**并落库 + 留痕，之后的轮次都按它走（除非那一轮又用
//     per-turn 显式指定）。
//
// 状态限制：只在**非运行中**切换。运行中那一轮已经起了（ACP 的 setConfig 发生在
// prompt 边界），此时改 Task.Model 只会造成"界面显示已切换、这一轮其实没换"的错觉 ——
// 与其给一个不生效的开关，不如明确拒绝，让用户等这一轮结束（这也是 per-turn 选择器
// 只在续问态出现的原因，见 InterveneInput.showModelPicker）。
func (s *Server) setTaskModel(c *gin.Context) {
	id := c.Param("id")
	t, ok := s.store.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	var req setTaskModelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if t.Status == model.TaskRunning || t.Status == model.TaskPending {
		c.JSON(http.StatusConflict, gin.H{
			"error": "task is running: wait for the current turn to finish before switching model",
		})
		return
	}

	ctx := c.Request.Context()
	target := req.Model
	// 校验目标在清单内：前端只该提交清单里的值，但那是前端约定，不是信任边界。
	// 提交一个 agent 认不出的值 → 下一轮建会话直接失败（"no configured model"），
	// 那时报错太晚，用户已经点了发送。这里提前挡掉，并回 400。
	if target != "" {
		if err := verifyModelChoice(ctx, t.Agent, target); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	from := t.Model
	if from == target {
		// 没变就不写事件：时间线只记录**真实发生过的**切换，否则反复点同一个选项
		// 会在会话里刷出一串无意义记录。
		c.JSON(http.StatusOK, gin.H{"ok": true, "model": target, "changed": false})
		return
	}

	summary := modelSwitchSummary(ctx, t.Agent, from, target)
	payload, err := json.Marshal(model.ModelSwitchPayload{From: from, To: target})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	now := time.Now()
	updated, err := s.store.Update(id, func(tk *model.Task) bool {
		tk.Model = target
		s.store.AppendEvent(tk, model.TaskEvent{
			Type: model.EventModelSwitch, Input: payload, Text: summary, At: now,
		})
		return true
	})
	if err != nil || updated == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to switch model"})
		return
	}
	s.bus.Publish(core.Event{Type: "task_updated", TaskID: id, Task: updated})
	c.JSON(http.StatusOK, gin.H{"ok": true, "model": target, "changed": true})
}

// verifyModelChoice 确认 target 是该 agent 当前清单里的一个选项。
//
// 清单取不到（agent 不支持选模型 / 探测失败）时**放行**：那是"没法验证"而不是"验证不过"。
// 这种情况在实践中意味着该 agent 压根不吃外部指定的 model（claude / qoder），
// 此时 Task.Model 只是一个记录值，拦下来反而会让功能不可用。
func verifyModelChoice(ctx context.Context, agentName, target string) error {
	cat, err := agent.ListAgentModels(ctx, agentName)
	if err != nil || len(cat.Options) == 0 {
		return nil // 取不到清单 ≠ 非法值，见注释
	}
	for _, o := range cat.Options {
		if o.Value == target {
			return nil
		}
	}
	return fmt.Errorf("unknown model for agent %s: %s", agentName, target)
}

// modelSwitchSummary 生成人读的一行摘要（落进 TaskEvent.Text）。
//
// 清单里能查到名字就用名字（"deepseek-v4.1-flash"），查不到就退回不透明串本身 ——
// 后者不好看，但比"切到一个查不到名字的模型"这种含糊说法更容易排查。
// 每次都现查（不缓存）：切换是低频操作，而 agent 清单会随上游漂移（见 model_catalog.go）。
func modelSwitchSummary(ctx context.Context, agentName, from, to string) string {
	name := func(v string) string {
		if v == "" {
			return "Agent 默认"
		}
		cat, err := agent.ListAgentModels(ctx, agentName)
		if err != nil {
			return v
		}
		for _, o := range cat.Options {
			if o.Value == v {
				return o.Name
			}
		}
		return v
	}
	if to == "" {
		return "模型切换：" + name(from) + " → Agent 默认"
	}
	return "模型切换：" + name(from) + " → " + name(to)
}