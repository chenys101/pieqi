// model_catalog.go 会话级模型选择：清单从哪来、怎么读、以及上游限流的识别。
//
// 为什么清单必须**从 agent 现取**、不能写死在前端或配置里：
// ACP 的模型选择值是**不透明串**（dsh 实测是 `JSON.stringify([provider,model])`，
// 如 `["magpie","workbuddy/glm-5.3-flash"]`），且只有 agent 自己知道有哪些。
// 自己拼串的下场是建会话直接失败：
//
//	pi-ai provider "magpie" has no configured model "workbuddy-ai/glm-5.3-flash"
//
// （2026-10-07 实际踩过：把 -ai 加到 glm 上——glm 属 `workbuddy/`，没有 -ai。）
// 所以：清单的唯一事实源 = agent 在 session/new（及 load/resume）响应里下发的
// configOptions；本文件只做「读出来」与「缓存」两件事。
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
)

// ModelConfigID 是 ACP 里「模型」这一配置项的 id（session/set_config_option 的 configId）。
const ModelConfigID = "model"

// ModelOption 一个可被选择的模型。
//
// Value 是**不透明选择值**：必须原样取自 agent 下发的清单、再原样回传给 agent。
// 它同时是 Task.Model 的取值与 set_config_option 的 value —— 别在前端/配置里手拼。
type ModelOption struct {
	Value       string `json:"value"`
	Name        string `json:"name"`                  // 人类可读名（选择器展示）
	Group       string `json:"group,omitempty"`       // 分组名（dsh 按 provider 分组：magpie / deepseek-official）
	Description string `json:"description,omitempty"` // 可选说明（如"更强、更贵"）
}

// ModelCatalog 某 agent 当前可供选择的模型清单。
type ModelCatalog struct {
	Agent   string        `json:"agent"`
	Current string        `json:"current,omitempty"` // 当前生效的选择值（会话级；未开过会话时为空）
	Options []ModelOption `json:"options"`
}

// ExtractModelCatalog 从 ACP 下发的 configOptions 里抽出模型清单。
//
// 识别用「id == "model"」或「category == model」二者之一：category 在协议里是
// **可选**语义标注（"UX only"），严格实现可以不发，故以 id 为主、category 兜底，
// 只取第一个命中的配置项（模型选项天然唯一）。
func ExtractModelCatalog(opts []acp.SessionConfigOption) ModelCatalog {
	for i := range opts {
		sel := opts[i].Select
		if sel == nil {
			continue // 非 select 变体（如 boolean 开关）不是模型清单
		}
		if string(sel.Id) != ModelConfigID {
			if sel.Category == nil || *sel.Category != acp.SessionConfigOptionCategoryModel {
				continue
			}
		}
		cat := ModelCatalog{Current: string(sel.CurrentValue)}
		if sel.Options.Grouped != nil {
			for _, g := range *sel.Options.Grouped {
				for _, o := range g.Options {
					cat.Options = append(cat.Options, ModelOption{
						Value: string(o.Value), Name: o.Name, Group: g.Name, Description: strDeref(o.Description),
					})
				}
			}
		}
		if sel.Options.Ungrouped != nil {
			for _, o := range *sel.Options.Ungrouped {
				cat.Options = append(cat.Options, ModelOption{
					Value: string(o.Value), Name: o.Name, Description: strDeref(o.Description),
				})
			}
		}
		return cat
	}
	return ModelCatalog{}
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// IsRateLimitError 判断 err 是否为上游限流（429 / rate_limit_error）。
//
// 为什么按消息文本判而不是按 code：上游把 429 包在 JSON-RPC 的 -32603
// （Internal error）里透出来，code 没有任何区分度，可辨识的信息全在 message/data：
//
//	{"code":-32603,"message":"Internal error: turn failed: 429: {\"code\":null,
//	 \"message\":\"WorkBuddy AI: usage exceeds frequency limit, ...\",
//	 \"type\":\"rate_limit_error\"}"}
//
// 命中面刻意放宽（"429" / "rate_limit" / "frequency limit" 任一）：限流的文案由
// 上游网关决定，措辞会变；**认得宽一点只是多重试一次，认不出的代价是任务直接失败**。
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	var re *acp.RequestError
	if errors.As(err, &re) {
		msg = re.Message
		if re.Data != nil {
			// data 可能是 {"details":"..."} 之类，也可能直接是串；统一拍平再找关键字。
			msg += " " + fmt.Sprintf("%v", re.Data)
		}
	}
	for _, needle := range []string{"rate_limit", "429", "frequency limit", "usage limit"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// --- 清单探测（带缓存） ---

// modelCatalogTTL 清单缓存时长。
//
// 为什么要缓存：取清单的唯一办法是 spawn 一个**真会话**（ACP 只在 session/new 的
// 响应里下发 configOptions，没有独立的"列模型"方法），一次探测要起一个 agent 进程
// （实测 dsh 冷启 ~1-3s，最慢见过 77s）。用户在新任务页切几次 agent 就够把代价放大
// 到不可接受，而模型清单本身几天才变一次。
const (
	modelCatalogTTL    = 10 * time.Minute
	modelCatalogErrTTL = 1 * time.Minute // 失败缓存短一些：配置修好后应尽快自愈
	modelCatalogProbe  = 45 * time.Second
)

type modelCatalogEntry struct {
	cat     ModelCatalog
	err     error
	expires time.Time
}

var (
	modelCatalogMu    sync.Mutex
	modelCatalogCache = map[string]modelCatalogEntry{}
)

// ListAgentModels 返回某 agent 当前可选的模型清单（带缓存与失败退避）。
//
// 非 ACP agent（如 claude 的桥）返回**空清单且无错误** —— 语义是"这个 agent 不支持
// 会话内选模型"，前端据此隐藏下拉框，不是故障。
func ListAgentModels(ctx context.Context, agentName string) (ModelCatalog, error) {
	modelCatalogMu.Lock()
	if e, ok := modelCatalogCache[agentName]; ok && time.Now().Before(e.expires) {
		modelCatalogMu.Unlock()
		return e.cat, e.err
	}
	modelCatalogMu.Unlock()

	cat, err := probeAgentModels(ctx, agentName)

	modelCatalogMu.Lock()
	ttl := modelCatalogTTL
	if err != nil {
		ttl = modelCatalogErrTTL
	}
	modelCatalogCache[agentName] = modelCatalogEntry{cat: cat, err: err, expires: time.Now().Add(ttl)}
	modelCatalogMu.Unlock()
	return cat, err
}

// CachedAgentModels 读取**未过期**的缓存清单（不触发探测）。
// 供对时序敏感、不能阻塞的调用方使用（如 POST /api/tasks 的入参校验）。
func CachedAgentModels(agentName string) (ModelCatalog, bool) {
	modelCatalogMu.Lock()
	defer modelCatalogMu.Unlock()
	e, ok := modelCatalogCache[agentName]
	if !ok || time.Now().After(e.expires) || e.err != nil {
		return ModelCatalog{}, false
	}
	return e.cat, true
}

// probeAgentModels 起一个一次性会话把清单读出来，随即关掉进程。
//
// 副作用（知悉并接受）：会在 agent 自己的会话存储里留一条（空）会话记录。
// 用固定的探针工作目录把它约束在**一份**存储下，不随探测次数增长。
// 不用调用方传 cwd：那会把探针混进任务的项目目录里，进而出现在 dsh 的会话列表里。
func probeAgentModels(ctx context.Context, agentName string) (ModelCatalog, error) {
	cat := ModelCatalog{Agent: agentName}
	if _, ok := defaultACPAgentType[agentName]; !ok {
		return cat, nil // 非 ACP agent：不支持会话内选模型
	}
	cfg := acpConfigFor(agentName)
	if cfg.AgentType == "" {
		return cat, nil // 未配置 = 这个 agent 本来就不在可选目录里
	}
	probeDir := filepath.Join(os.TempDir(), "pieqi-model-probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		return cat, err
	}
	a := NewACPAgent(cfg, acpProviderCfg.Logger)
	defer func() { _ = a.Close(context.Background()) }()

	probeCtx, cancel := context.WithTimeout(ctx, modelCatalogProbe)
	defer cancel()
	if _, err := a.NewSession(probeCtx, SessionConfig{Cwd: probeDir}); err != nil {
		return cat, explainProbeErr(err)
	}
	cat = a.Models()
	cat.Agent = agentName
	if len(cat.Options) == 0 {
		return cat, errors.New("agent 未下发可选模型清单（不支持会话内选模型？）")
	}
	return cat, nil
}

// explainProbeErr 给已知的「配置类」失败补上可操作提示。
//
// 为什么值得特判：`has no configured model` 的根因**不在 pieqi**，而在 agent 的
// profile 配置（如 ~/.dsh/profiles/acp/cordis.patch.yml 的 `- id: acp`.model）——
// 上游把模型下架/改名后，那份清单会变，而 pin 没跟着改，于是建会话直接失败。
// 原始错误只给出失效的 id，不说是哪份配置、更不说怎么修；2026-10-07 就因此
// 从外网只能看到一个语焉不详的失败，排查成本不低。
func explainProbeErr(err error) error {
	if err == nil || !strings.Contains(err.Error(), "has no configured model") {
		return err
	}
	return fmt.Errorf("%w —— 这是 **agent 侧的模型配置失效**（不是 pieqi 故障）：profile 里 "+
		"`- id: acp` 的 model 已不在 `llm-pi-ai` 的 models 清单中，上游下架/改名模型时会这样。"+
		"修法：改 ~/.dsh/profiles/acp/cordis.patch.yml 的该行，再用 "+
		"`dsh --profile acp --dump-config` 校验（清单的权威来源是网关 GET /v1/models）", err)
}
