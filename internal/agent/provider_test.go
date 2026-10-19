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

// TestACPConfigForPerAgentDefault 钉住「兜底按 agent 区分」：provider 表里没有这个名字时，
// qoder 落到 qodercli、dsh 落到 dsh。曾经共用的一个兜底会让选了 dsh 的会话静默起 qodercli。
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

// TestACPProviderConfigFromAgents 钉住「哪些配置会注册 provider」——与 AvailableAgents 同一判据。
func TestACPProviderConfigFromAgents(t *testing.T) {
	got := ACPProviderConfigFromAgents(config.AgentsConfig{
		Qoder: config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
		Dsh:   config.AgentDshConfig{Transport: "acp", ACP: config.DshACPConfig{AgentType: "dsh"}},
	})
	if len(got.Agents) != 2 {
		t.Fatalf("agents=%+v, want qoder + dsh", got.Agents)
	}

	// transport 非 acp / agent_type 空缺都不入表。
	got = ACPProviderConfigFromAgents(config.AgentsConfig{
		Qoder: config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
		Dsh:   config.AgentDshConfig{Transport: "", ACP: config.DshACPConfig{AgentType: "dsh"}},
	})
	if _, ok := got.Agents[AgentDsh]; ok {
		t.Fatalf("dsh transport 空却入了表: %+v", got.Agents)
	}
	if len(got.Agents) != 1 {
		t.Fatalf("agents=%+v, want 仅 qoder", got.Agents)
	}

	// 两个都没配 → 零值（main.go 据此不注册、也不进 session 驱动路径）。
	if got := ACPProviderConfigFromAgents(config.AgentsConfig{}); got.Agents != nil {
		t.Fatalf("未配置时应为零值, got %+v", got.Agents)
	}
}

// TestAvailableAgents 目录本身即「新任务页能选什么」的唯一事实源：
// 顺序（默认 agent = 首位）与可用性判据都必须钉住。
func TestAvailableAgents(t *testing.T) {
	t.Run("qoder 未配置时只有 claude", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
		})
		if len(got) != 1 || got[0].Name != AgentClaude {
			t.Fatalf("agents=%+v, want 仅 claude", got)
		}
	})

	t.Run("claude 永远排首位（默认 agent = 首位 = Claude Code）", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder: config.AgentQoderConfig{
				Transport: "acp",
				ACP:       config.QoderACPConfig{AgentType: "qodercli"},
			},
		})
		if len(got) != 2 {
			t.Fatalf("agents=%+v, want 2 个", got)
		}
		if got[0].Name != AgentClaude {
			t.Fatalf("首位 = %q, want %q（默认 agent 取首位）", got[0].Name, AgentClaude)
		}
		if got[1].Name != AgentQoder {
			t.Fatalf("次位 = %q, want %q", got[1].Name, AgentQoder)
		}
		if got[0].DisplayName != "Claude Code" {
			t.Fatalf("claude DisplayName = %q", got[0].DisplayName)
		}
	})

	t.Run("qoder transport 非 acp 时不列", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder:  config.AgentQoderConfig{Transport: "off", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
		})
		if len(got) != 1 || got[0].Name != AgentClaude {
			t.Fatalf("agents=%+v, want 仅 claude", got)
		}
	})

	t.Run("claude print 模式下 transport 描述随之变化", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "print"},
		})
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
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
		})
		if !got[0].ImagePrompt {
			t.Error("桥路径 ImagePrompt=false：前端永远不显示加图入口，刚打通的图片能力被锁死")
		}
	})

	t.Run("claude transport 空缺（默认走桥）时也具备图片通路", func(t *testing.T) {
		// 空 transport 走默认 sdk-bridge（见 openSession）；判据必须与之一致，
		// 否则"没写 transport"这个最常见的配置会让入口消失。
		got := AvailableAgents(config.AgentsConfig{})
		if !got[0].ImagePrompt {
			t.Error("默认配置（未写 transport）应走桥并具备图片通路")
		}
	})

	dshConfig := config.AgentDshConfig{
		Transport: "acp",
		ACP:       config.DshACPConfig{AgentType: "dsh"},
	}

	t.Run("dsh transport=acp 才列，且排在 qoder 之后", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder:  config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
			Dsh:    dshConfig,
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

	t.Run("dsh 默认（transport 留空）不注册", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder:  config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
			Dsh:    config.AgentDshConfig{ACP: config.DshACPConfig{AgentType: "dsh"}},
		})
		for _, a := range got {
			if a.Name == AgentDsh {
				t.Fatalf("transport 留空却列出了 dsh: %+v", got)
			}
		}
	})

	t.Run("只配 dsh 不配 qoder 也成立（dsh 不需要 qoder 陪跑）", func(t *testing.T) {
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Dsh:    dshConfig,
		})
		if len(got) != 2 || got[1].Name != AgentDsh {
			t.Fatalf("agents=%+v, want claude + dsh", got)
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
		got := AvailableAgents(config.AgentsConfig{
			Claude: config.AgentClaudeConfig{Transport: "sdk-bridge"},
			Qoder:  config.AgentQoderConfig{Transport: "acp", ACP: config.QoderACPConfig{AgentType: "qodercli"}},
			Dsh:    dshConfig,
		})
		if len(got) != 3 {
			t.Fatalf("agents=%+v, want 3 个", got)
		}
		claude, qoder, dsh := got[0], got[1], got[2]

		// goal 形态：三家必须能区分开（这是用户最容易被误导的一格）
		if claude.GoalMode != GoalModeCommand {
			t.Errorf("claude GoalMode=%q, want %q（/goal 是 CLI 本地解析的命令）", claude.GoalMode, GoalModeCommand)
		}
		if !claude.GoalStateVisible {
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
		if claude.SessionAlways {
			t.Error("claude SessionAlways=true：hook 路径的 Resolve 不认 approve_session")
		}
		if !qoder.SessionAlways || !dsh.SessionAlways {
			t.Error("ACP 系应支持同类免审（proceed_always / approve_session）")
		}

		// 模型可选：claude 桥不下发清单
		if claude.ModelSelectable {
			t.Error("claude ModelSelectable=true：桥不下发模型清单")
		}
		if !qoder.ModelSelectable || !dsh.ModelSelectable {
			t.Error("ACP 系应能下发模型清单")
		}

		// 冷启动：只有 dsh 明显慢（实测 >90s，init_timeout 因此设 2m）
		if dsh.ColdStartCold != true || claude.ColdStartCold != false || qoder.ColdStartCold != false {
			t.Errorf("冷启动标记不对：claude=%v qoder=%v dsh=%v（dsh 实测 >90s）",
				claude.ColdStartCold, qoder.ColdStartCold, dsh.ColdStartCold)
		}
	})
}
