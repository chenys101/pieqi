// agents.go 暴露「可被任务选择的 agent」目录，供新任务页的选择器渲染。
//
// 为什么不复用前端那份静态目录：agent 可用性由**服务端配置**决定（qoder 配没配、
// claude 走桥还是 print），前端维护第二份事实源必然会与服务端漂移——配了 qoder
// 前端不显示（选了也用不上），或没配却可选（选中即失败）。目录只有一份，在服务端。
package api

import (
	"fmt"
	"net/http"

	"pieqi/internal/agent"

	"github.com/gin-gonic/gin"
)

// SetAgents 注入可选 agent 目录与默认 agent（app 启动时调用一次）。
//
// 未接线时 server.agents 为 nil，listAgents 与 createTask 都退化成「只有 claude」——
// 与 agent 选择功能上线前的行为一致，最小构造路径（老测试）无需改动。
func (s *Server) SetAgents(agents []agent.AgentInfo, defaultAgent string) {
	s.agents = agents
	s.defaultAgent = defaultAgent
}

// agentCatalog 返回生效的目录（未接线时给 claude 兜底）。
func (s *Server) agentCatalog() []agent.AgentInfo {
	if len(s.agents) == 0 {
		return []agent.AgentInfo{{Name: agent.AgentClaude, DisplayName: "Claude Code"}}
	}
	return s.agents
}

// effectiveDefaultAgent 默认 agent（未接线时 claude —— 需求即「默认使用 Claude Code」）。
func (s *Server) effectiveDefaultAgent() string {
	if s.defaultAgent != "" {
		return s.defaultAgent
	}
	if len(s.agents) > 0 {
		return s.agents[0].Name
	}
	return agent.AgentClaude
}

// resolveAgent 校验并归一请求里的 agent 字段。
//
// 空串 → 默认 agent（新任务页未选时走后端默认，仍语义为 Claude Code）。
// 未知名 → 报错。**不做静默兜底**：用户明确选了 A 却跑了 B，比直接报错糟糕得多
// （错误可以重试，静默错跑会让人以为 qoder 的策略已经生效）。
func (s *Server) resolveAgent(name string) (string, error) {
	if name == "" {
		return s.effectiveDefaultAgent(), nil
	}
	for _, a := range s.agentCatalog() {
		if a.Name == name {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown agent: %s", name)
}

// listAgents GET /api/agents：可选 agent 目录 + 默认 agent。
func (s *Server) listAgents(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"agents":  s.agentCatalog(),
		"default": s.effectiveDefaultAgent(),
	})
}

// listAgentModels GET /api/agents/:name/models：该 agent 当前可选的模型清单。
//
// 为什么单独一个接口、而不是塞进 GET /api/agents：取清单的代价服务端**不能**替所有
// agent 预付 —— 清单只有 agent 自己知道（原生 agent 放标准 configOptions，改造过的
// dsh-acp 放 session/new 响应的 _meta，见 agent.ListAgentModels），拿一次就要起一个
// agent 进程。放这里 = 只有"用户真的选中了某个 agent"才付这份代价，且 agent 包内做了
// 10 分钟缓存。
//
// 两种"没有清单"的情形返回 200 + 空 models，不算错误路径（前端据此隐藏下拉框）：
//   - 该 agent 不支持外部指定模型（如 claude 的桥：它整个 HTTP 面没有 model 字段）
//     → error 为空；
//   - agent 支持但本次没下发清单。
//
// 探测**失败**（进程起不来 / 未登录 / profile 的模型配置失效）返回 **502** + error：
// 那是服务端/配置故障，必须让外网与监控一眼看出来。曾一律返回 200 —— 2026-10-07 的
// 「模型清单漂移」故障（pin 指向已下架的模型）在外部看起来就是 200，只能靠人读 body
// 才发现，徒增排查成本。
//
// 只有"agent 名不在可选目录里"才是 400（与 POST /api/tasks 的校验口径一致）。
func (s *Server) listAgentModels(c *gin.Context) {
	name := c.Param("name")
	if _, err := s.resolveAgent(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cat, err := agent.ListAgentModels(c.Request.Context(), name)
	models := cat.Options
	if models == nil {
		models = []agent.ModelOption{} // 空清单给 [] 而不是 null，前端少一个分支
	}
	resp := gin.H{"agent": name, "models": models}
	if cat.Current != "" {
		resp["current"] = cat.Current
	}
	if err != nil {
		resp["error"] = err.Error()
		c.JSON(http.StatusBadGateway, resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}
