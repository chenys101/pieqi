package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pieqi/internal/config"
)

func TestOpenQoderProviderRouting(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	// 用不存在的 spawn 命令：Open("qoder") 应路由到 ACP provider 并报 spawn 错（而非 ErrUnknownAgent）。
	acpProviderCfg.Agents = map[string]config.ACPConfig{
		AgentQoder: {SpawnCommand: []string{"no-such-qodercli-binary-xyz"}},
	}

	_, err := Open(context.Background(), OpenParams{Agent: "qoder", Cwd: t.TempDir()})
	if err == nil {
		t.Fatal("expected spawn error from qoder provider")
	}
	if errors.Is(err, ErrUnknownAgent) {
		t.Fatal("Open(qoder) should route to ACP provider, got ErrUnknownAgent")
	}
	if !strings.Contains(err.Error(), "no-such-qodercli-binary-xyz") {
		t.Fatalf("error should mention spawn command, got: %v", err)
	}
}

// TestOpenDshProviderRouting 同 qoder：注册名必须能路由到 ACP provider，
// 否则前端选了 dsh 只会拿到 ErrUnknownAgent。
func TestOpenDshProviderRouting(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	acpProviderCfg.Agents = map[string]config.ACPConfig{
		AgentDsh: {AgentType: "dsh", SpawnCommand: []string{"no-such-dsh-binary-xyz", "--profile", "acp"}},
	}

	_, err := Open(context.Background(), OpenParams{Agent: "dsh", Cwd: t.TempDir()})
	if err == nil {
		t.Fatal("expected spawn error from dsh provider")
	}
	if errors.Is(err, ErrUnknownAgent) {
		t.Fatal("Open(dsh) should route to ACP provider, got ErrUnknownAgent")
	}
	if !strings.Contains(err.Error(), "no-such-dsh-binary-xyz") {
		t.Fatalf("error should mention spawn command, got: %v", err)
	}
}

// TestOpenCodebuddyProviderRouting 同 qoder/dsh：注册名必须能路由到 ACP provider，
// 否则前端选了 codebuddy 只会拿到 ErrUnknownAgent。
func TestOpenCodebuddyProviderRouting(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	acpProviderCfg.Agents = map[string]config.ACPConfig{
		AgentCodebuddy: {AgentType: "codebuddy", SpawnCommand: []string{"no-such-codebuddy-binary-xyz", "--acp"}},
	}

	_, err := Open(context.Background(), OpenParams{Agent: "codebuddy", Cwd: t.TempDir()})
	if err == nil {
		t.Fatal("expected spawn error from codebuddy provider")
	}
	if errors.Is(err, ErrUnknownAgent) {
		t.Fatal("Open(codebuddy) should route to ACP provider, got ErrUnknownAgent")
	}
	if !strings.Contains(err.Error(), "no-such-codebuddy-binary-xyz") {
		t.Fatalf("error should mention spawn command, got: %v", err)
	}
}

// TestACPConfigForPerAgentDefault 钉住「兜底按 agent 区分」：provider 表里没有这个名字时，
// qoder 落到 qodercli、dsh 落到 dsh、codebuddy 落到 codebuddy。曾经共用的一个兜底会让
// 选了 dsh 的会话静默起 qodercli。
func TestACPConfigForPerAgentDefault(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	acpProviderCfg = ACPProviderConfig{}
	if got := acpConfigFor(AgentQoder).AgentType; got != "qodercli" {
		t.Fatalf("qoder 兜底 agent_type=%q want qodercli", got)
	}
	if got := acpConfigFor(AgentDsh).AgentType; got != "dsh" {
		t.Fatalf("dsh 兜底 agent_type=%q want dsh（不能串到 qodercli）", got)
	}
	if got := acpConfigFor(AgentCodebuddy).AgentType; got != "codebuddy" {
		t.Fatalf("codebuddy 兜底 agent_type=%q want codebuddy", got)
	}

	// 显式配置优先于兜底。
	acpProviderCfg.Agents = map[string]config.ACPConfig{AgentDsh: {AgentType: "dsh-acp"}}
	if got := acpConfigFor(AgentDsh).AgentType; got != "dsh-acp" {
		t.Fatalf("显式 agent_type 被兜底覆盖: %q", got)
	}
}

func TestConfigureACPProviders(t *testing.T) {
	prev := acpProviderCfg
	defer func() { acpProviderCfg = prev }()

	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{
		AgentQoder: {AgentType: "codex", SpawnCommand: []string{"codex", "--acp"}},
	}})
	cfg := acpProviderCfg.Agents[AgentQoder]
	if cfg.AgentType != "codex" {
		t.Fatalf("AgentType = %q, want codex", cfg.AgentType)
	}
	if len(cfg.SpawnCommand) != 2 || cfg.SpawnCommand[0] != "codex" {
		t.Fatalf("SpawnCommand not set: %v", cfg.SpawnCommand)
	}

	// 逐个合并：配了 dsh 不能把已登记的 qoder 顶掉。
	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{
		AgentDsh: {AgentType: "dsh"},
	}})
	if acpProviderCfg.Agents[AgentQoder].AgentType != "codex" {
		t.Fatalf("合并后 qoder 丢失: %+v", acpProviderCfg.Agents)
	}
	if acpProviderCfg.Agents[AgentDsh].AgentType != "dsh" {
		t.Fatalf("dsh 未登记: %+v", acpProviderCfg.Agents)
	}
}

// TestACPProviderConfigFromRuntime 钉住「哪些 agent 会注册 provider」——与 AvailableAgents 同一判据。
//
// 2026-10-22 重构后判据从「config 里 transport=acp 且 agent_type 非空」改为
// 「manifest 里有它（Kind=acp）+ 运行期被启用」。因此这组测试也不再需要构造
// config.AgentsConfig —— 那正是本次要消灭的"用配置表达代码事实"。
func TestACPProviderConfigFromRuntime(t *testing.T) {
	// 默认（Enabled 为空）= 只启用非 OptIn 的，即 claude + qoder。
	got := ACPProviderConfigFromRuntime(AgentRuntimeConfig{})
	if len(got.Agents) != 1 {
		t.Fatalf("默认应只注册 qoder，got %+v", got.Agents)
	}
	if _, ok := got.Agents[AgentQoder]; !ok {
		t.Fatalf("默认缺 qoder: %+v", got.Agents)
	}

	// 显式启用三个 ACP agent。
	got = ACPProviderConfigFromRuntime(AgentRuntimeConfig{
		Enabled: []string{AgentClaude, AgentQoder, AgentDsh, AgentCodebuddy},
	})
	if len(got.Agents) != 3 {
		t.Fatalf("agents=%+v, want qoder + dsh + codebuddy", got.Agents)
	}
	// claude 不是 ACP，绝不能被注册成 ACP provider。
	if _, ok := got.Agents[AgentClaude]; ok {
		t.Fatalf("claude 被注册成 ACP provider: %+v", got.Agents)
	}

	// 显式列出时以列表为准：可以只开一个，也可以把默认开的关掉。
	got = ACPProviderConfigFromRuntime(AgentRuntimeConfig{Enabled: []string{AgentDsh}})
	if len(got.Agents) != 1 {
		t.Fatalf("只开 dsh 时 got %+v", got.Agents)
	}
	if _, ok := got.Agents[AgentQoder]; ok {
		t.Fatalf("qoder 未列入却注册了: %+v", got.Agents)
	}

	// 空选择 = 回到默认（非 OptIn 的 qoder），不是"一个都不开"。
	// 这条语义是有意的：让"从没配过"和"清空"都回到默认，避免用户在设置页
	// 删光后得到一个空目录。
	got = ACPProviderConfigFromRuntime(AgentRuntimeConfig{Enabled: []string{}})
	if _, ok := got.Agents[AgentQoder]; !ok {
		t.Fatalf("空选择应回到默认（含 qoder），got %+v", got.Agents)
	}
	if _, ok := got.Agents[AgentDsh]; ok {
		t.Fatalf("空选择不应启用 OptIn 的 dsh，got %+v", got.Agents)
	}
}

// TestManifestIsTheOnlySource 守「接入一个新 agent 只改 manifest」这条纪律：
// 每个 manifest 条目都必须自带足够的代码事实，不依赖任何配置。
func TestManifestIsTheOnlySource(t *testing.T) {
	for _, m := range AllManifests() {
		if m.Name == "" || m.DisplayName == "" {
			t.Fatalf("manifest 条目缺 Name/DisplayName: %+v", m)
		}
		if m.Kind == TransportACP {
			if m.ACPType == "" {
				t.Fatalf("%s 是 ACP 但没声明 ACPType —— 接入信息不完整", m.Name)
			}
			spawn := m.DefaultSpawnCommand()
			if len(spawn) == 0 {
				t.Fatalf("%s 算不出默认 spawn 命令", m.Name)
			}
			// 裸名纪律：manifest 里绝不写机器相关的绝对路径。
			if strings.ContainsAny(spawn[0], `/\`) {
				t.Fatalf("%s 的默认 spawn[0]=%q 含路径分隔符：manifest 必须写裸名", m.Name, spawn[0])
			}
		}
		// OptIn 的 agent 默认不启用 —— 它们需要额外凭据，默认注册只会让用户
		// 选到一个必然失败的东西。
		if m.OptIn {
			rt := AgentRuntimeConfig{}
			enabled := rt.enabledSet()
			if enabled[m.Name] {
				t.Fatalf("%s 标了 OptIn 却在默认选择里被启用", m.Name)
			}
		}
	}
}

// TestAvailableAgents 目录本身即「新任务页能选什么」的唯一事实源：
// 顺序（默认 agent = 首位）与可用性判据都必须钉住。
func TestAvailableAgents(t *testing.T) {
	t.Run("默认只有 claude + qoder（OptIn 的不默认启用）", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{})
		if len(got) != 2 {
			t.Fatalf("agents=%+v, want claude + qoder", got)
		}
		if got[0].Name != AgentClaude || got[1].Name != AgentQoder {
			t.Fatalf("顺序 = %q,%q，want claude,qoder", got[0].Name, got[1].Name)
		}
	})

	t.Run("claude 永远排首位（默认 agent = 首位 = Claude Code）", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{})
		if got[0].Name != AgentClaude || got[0].DisplayName != "Claude Code" {
			t.Fatalf("首位 = %+v, want Claude Code", got[0])
		}
	})

	t.Run("claude print 模式下 transport 描述随之变化", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{ClaudeTransport: "print"})
		if got[0].Transport != "claude -p" {
			t.Fatalf("transport=%q, want claude -p", got[0].Transport)
		}
		// ★ print 路径**没有**图片通路（claude -p 是纯文本通道），所以 ImagePrompt 必须是
		// false。若这里报 true，前端会显示加图入口而发送必然失败 —— 配置决定能力，
		// 不是 agent 名决定能力，这条正是"别在前端硬编码 claude 能不能收图"的理由。
		if got[0].ImagePrompt {
			t.Error("print 模式报 ImagePrompt=true：该路径没有图片通路，前端会显示一个必然失败的入口")
		}
	})

	t.Run("claude 走桥时具备图片通路", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{ClaudeTransport: "sdk-bridge"})
		if !got[0].ImagePrompt {
			t.Error("桥路径 ImagePrompt=false：前端永远不显示加图入口，刚打通的图片能力被锁死")
		}
	})

	t.Run("claude transport 空缺（默认走桥）时也具备图片通路", func(t *testing.T) {
		// 空 transport 走默认 sdk-bridge（见 openSession）；判据必须与之一致，
		// 否则"没写 transport"这个最常见的状态会让入口消失。
		got := AvailableAgents(AgentRuntimeConfig{})
		if !got[0].ImagePrompt {
			t.Error("默认（未设 transport）应走桥并具备图片通路")
		}
	})

	t.Run("dsh 需显式启用，且排在 qoder 之后", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{
			Enabled: []string{AgentClaude, AgentQoder, AgentDsh},
		})
		if len(got) != 3 {
			t.Fatalf("agents=%+v, want 3 个", got)
		}
		if got[2].Name != AgentDsh || got[2].DisplayName != "DeepSeek Harness" {
			t.Fatalf("末位 = %+v, want dsh / DeepSeek Harness", got[2])
		}
		if got[2].Transport != "ACP" {
			t.Fatalf("dsh transport = %q, want ACP", got[2].Transport)
		}
		// ACP 系具备图片入站通路（ACPAgent 实现 RichPromptSender）
		if !got[2].ImagePrompt {
			t.Error("dsh ImagePrompt=false：ACP 具备图片通路，应在目录里体现")
		}
	})

	t.Run("只开 dsh 不陪跑 qoder 也成立", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{Enabled: []string{AgentClaude, AgentDsh}})
		if len(got) != 2 || got[1].Name != AgentDsh {
			t.Fatalf("agents=%+v, want claude + dsh", got)
		}
	})

	t.Run("codebuddy 需显式启用，且排在 dsh 之后", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{
			Enabled: []string{AgentClaude, AgentQoder, AgentDsh, AgentCodebuddy},
		})
		if len(got) != 4 {
			t.Fatalf("agents=%+v, want 4 个", got)
		}
		cb := got[3]
		if cb.Name != AgentCodebuddy || cb.DisplayName != "CodeBuddy CLI" {
			t.Fatalf("末位 = %+v, want codebuddy / CodeBuddy CLI", cb)
		}
		if cb.Transport != "ACP" {
			t.Fatalf("codebuddy transport = %q, want ACP", cb.Transport)
		}
		// initialize 实测声明 promptCapabilities.image=true → 具备图片入站通路。
		if !cb.ImagePrompt {
			t.Error("codebuddy ImagePrompt=false：ACP 握手已声明图片能力，应在目录里体现")
		}
		// 与 qoder/dsh 同为 ACP RequestPermission 路径。
		if !cb.SessionAlways {
			t.Error("codebuddy SessionAlways=false：ACP 的 approve_session 可用")
		}
		// session/new 的标准 configOptions 下发 id="model"（实测 20 个模型）。
		if !cb.ModelSelectable {
			t.Error("codebuddy ModelSelectable=false：实测下发标准 configOptions 模型清单")
		}
		// 没有 goal 能力的实测证据 → 必须留空，不能谎报。
		if cb.GoalMode != "" || cb.GoalStateVisible {
			t.Errorf("codebuddy goal=(%q,%v)，无实测证据时应留空/false", cb.GoalMode, cb.GoalStateVisible)
		}
		if cb.ColdStartCold {
			t.Error("codebuddy ColdStartCold=true：Node CLI 的 initialize/session-new 在秒级")
		}
	})

	t.Run("codebuddy 默认不启用", func(t *testing.T) {
		for _, a := range AvailableAgents(AgentRuntimeConfig{}) {
			if a.Name == AgentCodebuddy {
				t.Fatalf("OptIn 的 codebuddy 出现在默认目录里")
			}
		}
	})

	// 显式启用一个不存在的名字不应制造幽灵条目。
	t.Run("未知名字被忽略", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{Enabled: []string{AgentClaude, "no-such-agent"}})
		if len(got) != 1 || got[0].Name != AgentClaude {
			t.Fatalf("agents=%+v, want 仅 claude", got)
		}
	})

	// ── 结构化能力（2026-10-18）─────────────────────────────────────────────
	//
	// 这组测试守的是一个具体的病：**三个 agent 的能力曾经完全相同**（都是
	// 「流式输出 / 工具审批 / 续问 / 取消」），Agents 页三张卡一模一样，
	// 用户看不出区别，换了个名字而已。
	//
	// 判据全是实测/文档结论（详见 docs/agents-goal-and-capabilities.md），
	// 不是"大概是这样"：
	//   · qoder 有 goal 但 ACP 只开放 4 个命令、且 goal 走 SDK control request
	//     ⇒ GoalMode 必须为空（实测两组共 62 次 tool_call，goal 工具 0 次）
	//   · claude 的 goal 是命令 + Stop hook ⇒ GoalModeCommand、无工具入口
	//   · dsh 的 goal 是工具 ⇒ GoalModeTool，实测收到 tool_call: create_goal
	t.Run("三个 agent 的结构化能力必须有差异", func(t *testing.T) {
		got := AvailableAgents(AgentRuntimeConfig{
			Enabled: []string{AgentClaude, AgentQoder, AgentDsh},
		})
		if len(got) != 3 {
			t.Fatalf("agents=%+v, want 3 个", got)
		}
		claudeInfo, qoder, dsh := got[0], got[1], got[2]

		// goal 形态：三家必须能区分开（这是用户最容易被误导的一格）
		if claudeInfo.GoalMode != GoalModeCommand {
			t.Errorf("claude GoalMode=%q, want %q（/goal 是 CLI 本地解析的命令）", claudeInfo.GoalMode, GoalModeCommand)
		}
		if !claudeInfo.GoalStateVisible {
			t.Error("claude GoalStateVisible=false：active_goal 已在 2026-10-18 接通，谎报会让用户以为状态不可见")
		}
		if qoder.GoalMode != "" {
			t.Errorf("qoder GoalMode=%q, want 空：ACP 只开放 4 个命令且 goal 是 SDK control request，"+
				"报成 tool/command 会让用户盲打 /goal 后没反应", qoder.GoalMode)
		}
		if qoder.GoalStateVisible {
			t.Error("qoder GoalStateVisible=true：ACP 路径拿不到 goal 状态")
		}
		if dsh.GoalMode != GoalModeTool {
			t.Errorf("dsh GoalMode=%q, want %q（create_goal 是模型可见工具）", dsh.GoalMode, GoalModeTool)
		}

		// 同类免审：只有 ACP 系有（claude 走 PreToolUse hook，没有 approve_session）
		if claudeInfo.SessionAlways {
			t.Error("claude SessionAlways=true：hook 路径的 Resolve 不认 approve_session")
		}
		if !qoder.SessionAlways || !dsh.SessionAlways {
			t.Error("ACP 系应支持同类免审（proceed_always / approve_session）")
		}

		// 模型可选：claude 桥不下发清单
		if claudeInfo.ModelSelectable {
			t.Error("claude ModelSelectable=true：桥不下发模型清单")
		}
		if !qoder.ModelSelectable || !dsh.ModelSelectable {
			t.Error("ACP 系应能下发模型清单")
		}

		// 冷启动：只有 dsh 明显慢（实测 >90s，init_timeout 因此设 2m）
		if !dsh.ColdStartCold || claudeInfo.ColdStartCold || qoder.ColdStartCold {
			t.Errorf("冷启动标记不对：claude=%v qoder=%v dsh=%v（dsh 实测 >90s）",
				claudeInfo.ColdStartCold, qoder.ColdStartCold, dsh.ColdStartCold)
		}
	})
}
