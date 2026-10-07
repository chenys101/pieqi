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
