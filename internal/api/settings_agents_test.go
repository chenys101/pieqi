package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/config"
	"pieqi/internal/core"

	"github.com/gin-gonic/gin"
)

// TestSettings_AgentsRoundTrip agent 选择走设置接口能读能写（2026-10-22）。
//
// 这是"约定优于配置"整改的可观测出口：开/关 agent 不再改 config.yaml，
// 而是改运行期设置；设置页因此必须能把它读出来、写回去。
func TestSettings_AgentsRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := core.NewSettingsStore("") // 纯内存
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	s := &Server{}
	s.SetSettingsStore(store)

	// 1) 默认：agents 段是零值 —— 表示"回到 manifest 默认"（claude + qoder）。
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/settings", nil)
	s.getSettings(c)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status=%d", w.Code)
	}
	var got settingsDto
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Agents.Enabled) != 0 {
		t.Fatalf("默认 enabled 应为空（= 回落到 manifest 默认），got %v", got.Agents.Enabled)
	}

	// 2) 打开 codebuddy。
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = c2Req(w2, `{"agents":{"enabled":["claude","qoder","codebuddy"]}}`)
	s.patchSettings(c2)
	if w2.Code != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", w2.Code, w2.Body.String())
	}
	if got := store.Get().Agents.Enabled; len(got) != 3 {
		t.Fatalf("落盘 enabled = %v, want 3 项", got)
	}

	// 3) 写错名字必须 400，而不是静默不生效。
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = c2Req(w3, `{"agents":{"enabled":["code-buddy"]}}`)
	s.patchSettings(c3)
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("未知名应 400，got %d body=%s", w3.Code, w3.Body.String())
	}
	if !strings.Contains(w3.Body.String(), "unknown agent") {
		t.Fatalf("错误信息应指出未知 agent: %s", w3.Body.String())
	}

	// 4) claude_transport 只收两个合法值。
	w4 := httptest.NewRecorder()
	c4, _ := gin.CreateTestContext(w4)
	c4.Request = c2Req(w4, `{"agents":{"claude_transport":"bogus"}}`)
	s.patchSettings(c4)
	if w4.Code != http.StatusBadRequest {
		t.Fatalf("非法 claude_transport 应 400，got %d", w4.Code)
	}
}

// TestAgentCatalogSource_Dynamic 目录必须反映**当前**设置，而不是启动快照。
//
// 若这里失败，表现是：设置页打开了某个 agent，选择器里却始终看不到它 ——
// 而设置页显示"已开启"，排查时会一直怀疑前端。
func TestAgentCatalogSource_Dynamic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := core.NewSettingsStore("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	s := &Server{}
	// 模拟 main.go 的接线：目录按当前设置现算。
	s.SetAgentCatalogSource(func() []agent.AgentInfo {
		return agent.AvailableAgents(agentRuntimeForTest(store.Get()))
	})
	// 同时给一份"启动快照"，证明动态源优先于它。
	s.SetAgents([]agent.AgentInfo{{Name: agent.AgentClaude, DisplayName: "stale snapshot"}}, "claude")

	names := func() []string {
		var out []string
		for _, a := range s.agentCatalog() {
			out = append(out, a.Name)
		}
		return out
	}

	// 默认：claude + qoder
	before := names()
	if len(before) != 2 || before[0] != agent.AgentClaude || before[1] != agent.AgentQoder {
		t.Fatalf("默认目录 = %v, want [claude qoder]", before)
	}

	// 打开 codebuddy 后**无需重启**即出现。
	sel := core.AgentSelection{Enabled: []string{agent.AgentClaude, agent.AgentQoder, agent.AgentCodebuddy}}
	if _, err := store.Patch(core.SettingsPatch{Agents: &sel}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	after := names()
	found := false
	for _, n := range after {
		if n == agent.AgentCodebuddy {
			found = true
		}
	}
	if !found {
		t.Fatalf("打开后目录 = %v，仍不含 codebuddy（目录是启动快照而非动态计算）", after)
	}
}

// agentRuntimeForTest 复刻 main.agentRuntimeFromSettings 的映射（测试侧独立一份，
// 避免 api 包反向依赖 cmd 包）。
func agentRuntimeForTest(s core.Settings) agent.AgentRuntimeConfig {
	rt := agent.AgentRuntimeConfig{
		Enabled:         s.Agents.Enabled,
		ClaudeTransport: s.Agents.ClaudeTransport,
	}
	if len(s.Agents.SpawnOverrides) > 0 {
		rt.Overrides = make(map[string]config.ACPConfig, len(s.Agents.SpawnOverrides))
		for name, cmd := range s.Agents.SpawnOverrides {
			rt.Overrides[name] = config.ACPConfig{SpawnCommand: cmd}
		}
	}
	return rt
}

func c2Req(_ http.ResponseWriter, body string) *http.Request {
	r := httptest.NewRequest("PATCH", "/api/settings", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}


