// model_catalog_test.go：模型清单两条通道的读取与写回。
//
// 守的是什么（2026-10-08 的实证结论）：清单有**两条**通道，读哪条决定写哪条。
//
//   - 原生 agent（qodercli 实测 v1.1.64）→ 标准 `configOptions` +
//     `session/set_config_option`；
//   - fork 的 dsh-acp → `_meta["pieqi/configOptions"]` + 请求 `_meta.model`
//     （它把标准 configOptions 隐藏成恒空，set_config_option 会抛错）。
//
// 回归背景：bd0cd28 为迁就 fork 后的 dsh，把清单读取从标准字段整体换成 _meta。
// **qoder 是那次迁移的附带损伤** —— 它是原生 ACP 实现，_meta 整个缺失，于是清单恒空、
// 前端显示"该 Agent 不提供可选模型"，而它其实既下发清单（qmodel_38max / qfmodel）
// 又接受 set_config_option。这里把这四个组合都钉住。
package agent

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
)

// --- 清单解析（纯函数） ---

// modelSelect 造一个 id=model 的 select 项（2 个选项，与 qodercli 实测形态一致）。
func modelSelect(current string, opts ...acp.SessionConfigSelectOption) acp.SessionConfigOption {
	ungrouped := acp.SessionConfigSelectOptionsUngrouped(opts)
	sel := acp.SessionConfigOptionSelect{
		Id:           ModelConfigID,
		Name:         "Model",
		CurrentValue: acp.SessionConfigValueId(current),
		Options:      acp.SessionConfigSelectOptions{Ungrouped: &ungrouped},
	}
	return acp.SessionConfigOption{Select: &sel}
}

func opt(value, name, desc string) acp.SessionConfigSelectOption {
	o := acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(value), Name: name}
	if desc != "" {
		d := desc
		o.Description = &d
	}
	return o
}

// TestExtractModelCatalog_StandardOptions 标准字段能抽出清单（qodercli 形态）。
func TestExtractModelCatalog_StandardOptions(t *testing.T) {
	opts := []acp.SessionConfigOption{
		// 干扰项：同类但 category=mode 的 select，不能被当成模型清单
		{Select: &acp.SessionConfigOptionSelect{
			Id: "mode", Category: ptr(acp.SessionConfigOptionCategoryMode),
			CurrentValue: "default",
		}},
		modelSelect("qmodel_38max",
			opt("qmodel_38max", "Qwen3.8-Max (default)", "0.50x Credit"),
			opt("qfmodel", "Qwen3.8-Flash", "0.00x Credit"),
		),
	}
	cat := ExtractModelCatalog(opts)
	if cat.Current != "qmodel_38max" {
		t.Fatalf("Current=%q want qmodel_38max", cat.Current)
	}
	if len(cat.Options) != 2 {
		t.Fatalf("Options=%+v want 2 项", cat.Options)
	}
	if cat.Options[0].Value != "qmodel_38max" || cat.Options[0].Name != "Qwen3.8-Max (default)" {
		t.Errorf("选项[0]=%+v", cat.Options[0])
	}
	if cat.Options[0].Description != "0.50x Credit" {
		t.Errorf("选项[0].Description=%q want 0.50x Credit", cat.Options[0].Description)
	}
	if cat.Options[1].Value != "qfmodel" {
		t.Errorf("选项[1].Value=%q want qfmodel", cat.Options[1].Value)
	}
}

// TestPickCatalog_PrefersStandardThenMeta 挑清单的优先级：
// 标准字段优先，没有才落到 _meta —— 反过来会把"两者都发"的原生 agent 误判成 fork 通道，
// 于是用 _meta.model 去设模型，原生 agent 忽略它，静默失效。
func TestPickCatalog_PrefersStandardThenMeta(t *testing.T) {
	std := []acp.SessionConfigOption{modelSelect("std-model", opt("std-model", "Std", ""))}
	meta := map[string]any{modelCatalogMetaKey: []any{
		map[string]any{
			"type": "select", "id": "model", "name": "Model", "currentValue": "meta-model",
			"options": []any{map[string]any{"value": "meta-model", "name": "Meta"}},
		},
	}}
	noneMeta := map[string]any{"something/else": 1}

	t.Run("两者都有 → 标准优先", func(t *testing.T) {
		cat, src := pickCatalog(std, meta)
		if src != catalogStandard {
			t.Fatalf("src=%v want catalogStandard", src)
		}
		if cat.Current != "std-model" {
			t.Fatalf("Current=%q want std-model", cat.Current)
		}
	})
	t.Run("只有 _meta → catalogMeta（fork 的 dsh-acp）", func(t *testing.T) {
		cat, src := pickCatalog(nil, meta)
		if src != catalogMeta {
			t.Fatalf("src=%v want catalogMeta", src)
		}
		if cat.Current != "meta-model" {
			t.Fatalf("Current=%q want meta-model", cat.Current)
		}
	})
	t.Run("都没有 → catalogNone", func(t *testing.T) {
		cat, src := pickCatalog(nil, noneMeta)
		if src != catalogNone || len(cat.Options) != 0 {
			t.Fatalf("cat=%+v src=%v want 空 + catalogNone", cat, src)
		}
	})
	t.Run("标准为空 + _meta 为空 → catalogNone", func(t *testing.T) {
		if _, src := pickCatalog(nil, nil); src != catalogNone {
			t.Fatalf("src=%v want catalogNone", src)
		}
	})
}

// --- 双通道读写（用假 conn 驱动真实 NewSession/SendPrompt 代码路径） ---

// fakeModelConn 是 acpConn 的假实现，只服务模型通道相关的用例。
// 记录 set_config_option 的调用，并可按需让 NewSession 下发标准/_meta 清单。
type fakeModelConn struct {
	// newSessionResp 是 session/new / load / resume 响应的模板。
	newSessionResp acp.NewSessionResponse
	// setErr 非空时让 SetSessionConfigOption 返回该错误（模拟 fork 的 dsh 抛错）。
	setErr error
	// setCalls 记录每次 set_config_option 的 (sessionId, configId, value)。
	setCalls []setCall
	// promptMetas 记录每次 prompt 请求携带的 _meta（看按轮换模型走的哪条通道）。
	promptMetas []map[string]any
	// setAfterPrompt 记录调用顺序：set 必须发生在 prompt **之前**（否则这一轮没换）。
	order []string
}

type setCall struct{ session, config, value string }

func (f *fakeModelConn) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{}, nil
}
func (f *fakeModelConn) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return f.newSessionResp, nil
}
func (f *fakeModelConn) LoadSession(context.Context, acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	return acp.LoadSessionResponse{ConfigOptions: f.newSessionResp.ConfigOptions, Meta: f.newSessionResp.Meta}, nil
}
func (f *fakeModelConn) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{ConfigOptions: f.newSessionResp.ConfigOptions, Meta: f.newSessionResp.Meta}, nil
}
func (f *fakeModelConn) SetSessionConfigOption(_ context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	if f.setErr != nil {
		return acp.SetSessionConfigOptionResponse{}, f.setErr
	}
	if p.ValueId == nil {
		return acp.SetSessionConfigOptionResponse{}, errors.New("fake: 期望 ValueId 变体")
	}
	f.setCalls = append(f.setCalls, setCall{
		session: string(p.ValueId.SessionId),
		config:  string(p.ValueId.ConfigId),
		value:   string(p.ValueId.Value),
	})
	f.order = append(f.order, "set")
	return acp.SetSessionConfigOptionResponse{
		ConfigOptions: []acp.SessionConfigOption{modelSelect(string(p.ValueId.Value))},
	}, nil
}
func (f *fakeModelConn) Prompt(_ context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	f.promptMetas = append(f.promptMetas, p.Meta)
	f.order = append(f.order, "prompt")
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}
func (f *fakeModelConn) Cancel(context.Context, acp.CancelNotification) error { return nil }
func (f *fakeModelConn) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}
func (f *fakeModelConn) Done() <-chan struct{} { return make(chan struct{}) }
func (f *fakeModelConn) SetLogger(*slog.Logger) {}

var _ acpConn = (*fakeModelConn)(nil)

// newModelAgent 造一个"已启动"的 ACPAgent 并注入假 conn。
//
// started=true 让 NewSession 跳过 ensureStarted（不 spawn 真进程）；InitTimeout 必须设，
// 否则 NewSession 里的 context.WithTimeout(ctx, 0) 立刻过期。
func newModelAgent(t *testing.T, conn *fakeModelConn, caps acp.AgentCapabilities) *ACPAgent {
	t.Helper()
	a := NewACPAgent(config.ACPConfig{AgentType: "qodercli", InitTimeout: 10 * time.Second}, nil)
	a.conn = conn
	a.agentCaps = caps
	a.started = true
	return a
}

// TestACPAgent_NativeAgentReadsStandardConfigOptions 回归（本次修复的主用例）：
// 原生 agent 只在标准 configOptions 下发清单、_meta 整个缺失时，
//
//	① 清单必须被读到（Models() 非空）—— 否则前端没有下拉框；
//	② 建会话时指定的 model 必须经 session/set_config_option 落定 —— 否则选了不生效；
//	③ 不能往 _meta 写 model（原生 agent 忽略它，写了等于没设）。
func TestACPAgent_NativeAgentReadsStandardConfigOptions(t *testing.T) {
	conn := &fakeModelConn{
		newSessionResp: acp.NewSessionResponse{
			SessionId: "qoder-sess",
			// 原生 agent 的真实形态：清单在标准字段，_meta 整个缺失
			ConfigOptions: []acp.SessionConfigOption{
				modelSelect("qmodel_38max",
					opt("qmodel_38max", "Qwen3.8-Max (default)", "0.50x Credit"),
					opt("qfmodel", "Qwen3.8-Flash", "0.00x Credit"),
				),
			},
		},
	}
	a := newModelAgent(t, conn, acp.AgentCapabilities{})

	sid, err := a.NewSession(context.Background(), SessionConfig{
		Cwd: t.TempDir(), Model: "qfmodel",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sid != "qoder-sess" {
		t.Fatalf("sid=%q want qoder-sess", sid)
	}

	// ① 清单读到了
	cat := a.Models()
	if len(cat.Options) != 2 {
		t.Fatalf("Models()=%+v，期望读到标准 configOptions 里的 2 个选项（返回空 = 前端没有下拉框）", cat)
	}
	if cat.Current != "qmodel_38max" {
		t.Errorf("Current=%q want qmodel_38max", cat.Current)
	}

	// ② 指定的模型经 set_config_option 落定
	if len(conn.setCalls) != 1 {
		t.Fatalf("set_config_option 调用 %d 次，期望 1 次（qodercli 靠它换模型）：%+v", len(conn.setCalls), conn.setCalls)
	}
	got := conn.setCalls[0]
	if got.session != "qoder-sess" || got.config != ModelConfigID || got.value != "qfmodel" {
		t.Errorf("set_config_option=%+v，期望 {qoder-sess model qfmodel}", got)
	}
}

// TestACPAgent_ForkDshKeepsMetaChannel dsh 侧不能被这次改动打破：
// 标准 configOptions 恒空、清单只在 _meta 时，
//
//	① 清单仍要读到；
//	② **不能**发 set_config_option（fork 隐藏了 config，调它会抛错）；
//	③ 指定模型仍走请求 _meta.model。
func TestACPAgent_ForkDshKeepsMetaChannel(t *testing.T) {
	conn := &fakeModelConn{
		newSessionResp: acp.NewSessionResponse{
			SessionId: "dsh-sess",
			// fork 后的形态：标准字段恒空，清单挂 _meta
			ConfigOptions: []acp.SessionConfigOption{},
			Meta: map[string]any{modelCatalogMetaKey: []any{
				map[string]any{
					"type": "select", "id": "model", "name": "Model",
					"currentValue": `["magpie","workbuddy/deepseek-v4.1-flash"]`,
					"options": []any{
						map[string]any{"value": `["magpie","workbuddy/deepseek-v4.1-flash"]`, "name": "v4.1-flash"},
					},
				},
			}},
		},
	}
	a := newModelAgent(t, conn, acp.AgentCapabilities{})

	if _, err := a.NewSession(context.Background(), SessionConfig{
		Cwd: t.TempDir(), Model: `["magpie","workbuddy/deepseek-v4.1-flash"]`,
	}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if cat := a.Models(); len(cat.Options) != 1 {
		t.Fatalf("Models()=%+v，期望读到 _meta 通道的清单", cat)
	}
	if len(conn.setCalls) != 0 {
		t.Fatalf("对 _meta 通道的 agent 发了 set_config_option（%+v）——"+
			"fork 的 dsh-acp 隐藏了 configOptions，调它会抛错", conn.setCalls)
	}
}

// TestACPAgent_NoCatalogSkipsSetQuietly 没有清单的 agent（不支持选模型）：
// 指定了模型也不报错、也不发 set_config_option —— Task.Model 此时只是记录值，
// 拦下来会让"先建任务后换 agent"之类流程不可用（同 api.verifyModelChoice 的取舍）。
func TestACPAgent_NoCatalogSkipsSetQuietly(t *testing.T) {
	conn := &fakeModelConn{newSessionResp: acp.NewSessionResponse{SessionId: "plain"}}
	a := newModelAgent(t, conn, acp.AgentCapabilities{})

	if _, err := a.NewSession(context.Background(), SessionConfig{Cwd: t.TempDir(), Model: "whatever"}); err != nil {
		t.Fatalf("NewSession 不该因「指定了模型但 agent 不支持」而失败：%v", err)
	}
	if len(conn.setCalls) != 0 {
		t.Fatalf("无清单却发了 set_config_option：%+v", conn.setCalls)
	}
	if len(a.Models().Options) != 0 {
		t.Fatalf("Models() 应为空，实得 %+v", a.Models())
	}
}

// TestACPAgent_TurnModelUsesStandardChannel 按轮换模型（续问时选模型）也要分通道：
// 原生 agent 走 set_config_option，且**必须在 prompt 之前**（set 对会话生效，
// 顺序反了这一轮还是跑在旧模型上）。
func TestACPAgent_TurnModelUsesStandardChannel(t *testing.T) {
	conn := &fakeModelConn{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "qoder-sess",
			ConfigOptions: []acp.SessionConfigOption{modelSelect("qmodel_38max", opt("qmodel_38max", "Max", ""), opt("qfmodel", "Flash", ""))},
		},
	}
	a := newModelAgent(t, conn, acp.AgentCapabilities{})

	sid, err := a.NewSession(context.Background(), SessionConfig{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	conn.setCalls = nil // 清掉建会话那次的记录，只看本轮

	a.SetTurnModel("qfmodel") // 调用方在每轮 Run 前设置（见 TurnModelSetter）
	if err := a.SendPrompt(context.Background(), sid, "换到 Flash 再回答"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	if len(conn.setCalls) != 1 || conn.setCalls[0].value != "qfmodel" {
		t.Fatalf("本轮 set_config_option=%+v，期望一次 qfmodel", conn.setCalls)
	}
	if len(conn.order) < 2 || conn.order[0] != "set" || conn.order[1] != "prompt" {
		t.Fatalf("调用顺序=%v，期望 set 在 prompt 之前", conn.order)
	}
	// 原生 agent 不该收到 _meta.model（它忽略 _meta，带了等于没设）
	if len(conn.promptMetas) != 1 {
		t.Fatalf("promptMetas=%v want 1 条", conn.promptMetas)
	}
	if m := conn.promptMetas[0]; m != nil {
		if _, ok := m["model"]; ok {
			t.Errorf("原生 agent 的 prompt 带了 _meta.model=%v —— 它会被忽略，应在 set_config_option 里设", m)
		}
	}
}

// TestACPAgent_SetModelFailureSurfaces 设置失败必须冒泡，不能静默：
// 用户显式选了模型却没生效，比直接报错糟糕得多。
func TestACPAgent_SetModelFailureSurfaces(t *testing.T) {
	conn := &fakeModelConn{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "s",
			ConfigOptions: []acp.SessionConfigOption{modelSelect("a", opt("a", "A", ""), opt("b", "B", ""))},
		},
		setErr: errors.New("Method not found"),
	}
	a := newModelAgent(t, conn, acp.AgentCapabilities{})

	_, err := a.NewSession(context.Background(), SessionConfig{Cwd: t.TempDir(), Model: "b"})
	if err == nil {
		t.Fatal("set_config_option 失败却返回了 nil —— 用户会以为模型已切换")
	}
}

// TestACPAgent_ResumeAppliesModel 续问路径也要落定模型：
// 会话被接回来后不一定还保持上次路由（重启/换进程），而 Task.Model 是持久事实。
func TestACPAgent_ResumeAppliesModel(t *testing.T) {
	conn := &fakeModelConn{
		newSessionResp: acp.NewSessionResponse{
			SessionId:     "resumed",
			ConfigOptions: []acp.SessionConfigOption{modelSelect("a", opt("a", "A", ""), opt("b", "B", ""))},
		},
	}
	// caps 无 LoadSession → 走 session/resume 分支
	a := newModelAgent(t, conn, acp.AgentCapabilities{})

	if _, err := a.NewSession(context.Background(), SessionConfig{
		Cwd: t.TempDir(), ResumeFrom: "prev-sid", Model: "b",
	}); err != nil {
		t.Fatalf("NewSession(resume): %v", err)
	}
	if len(conn.setCalls) != 1 || conn.setCalls[0].value != "b" {
		t.Fatalf("续问路径 set_config_option=%+v，期望一次 b", conn.setCalls)
	}
}

func ptr[T any](v T) *T { return &v }
