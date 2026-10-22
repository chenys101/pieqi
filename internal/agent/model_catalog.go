// model_catalog.go 会话级模型选择：清单从哪来、怎么读。
//
// 为什么清单必须**从 agent 现取**、不能写死在前端或配置里：
// ACP 的模型选择值是**不透明串**（dsh 实测是 `JSON.stringify([provider,model])`，
// 如 `["magpie","group/auto-deepseek-v4-1-flash"]`），且只有 agent 自己知道有哪些。
// 自己拼串的下场是建会话直接失败：
//
//	pi-ai provider "magpie" has no configured model "workbuddy-ai/glm-5.3-flash"
//
// （2026-10-07 实际踩过：把 -ai 加到 glm 上——glm 属 `workbuddy/`，没有 -ai。）
//
// 清单从哪读：**两条通道都读，按 agent 实际下发的那个走**。
//
//   - **标准 `configOptions`**：ACP 协议正路。原生 agent（qodercli 实测 v1.1.64）
//     就走这条：`session/new` 响应里带 id="model" 的 select 项。
//   - **`_meta["pieqi/configOptions"]`**：为一份**已弃用**的本地 dsh-acp fork 加的私有通道。
//     那个 fork 出于「不向通用客户端暴露 ACP config」的考虑把标准 configOptions 隐藏成恒空，
//     改把真实清单挂在这个 _meta 键上（A3）。fork 已删（见 docs/adr/0011），
//     **当前没有任何 agent 走这条通道**，但分支刻意保留 —— 见下方"为什么保留"。
//
// 为什么要分通道而不能只读一个（2026-10-08 实证）：bd0cd28 为迁就 fork 后的 dsh
// 把读取从标准字段整体换成 _meta，**qoder 是这次迁移的附带损伤** —— 它是原生 ACP
// 实现，_meta 整个缺失，于是清单恒空、前端显示"该 Agent 不提供可选模型"，
// 而它其实既下发清单（`qmodel_38max` / `qfmodel`）又接受 `session/set_config_option`。
//
// **为什么 fork 已删仍保留 catalogMeta 分支**：它是"标准字段恒空时怎么活"的协议容错，
// 不是 fork 专属。删掉等于假定所有 ACP agent 都合规 —— 而 bd0cd28 已经演示过
// 这种假定会怎样误伤一个正常 agent。将来任何不合规/被改造的 agent 都会落到这里。
//
// 「从哪读到」还决定「往哪写」：标准通道要用 `session/set_config_option`，
// _meta 通道要用请求的 `_meta.model`（dsh 侧 configOptions 恒空，set_config_option
// 会直接抛错）。所以读到的通道要记下来，见 catalogSource。
//
// 清单的唯一事实源仍是 agent 自己，本文件只做「读出来」与「缓存」两件事。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
)

// catalogSource 说明"这份清单是从哪条通道读到的"，同时决定**写**回哪条通道。
//
// 读与写必须同源：标准通道的 agent 不认 `_meta.model`（原生实现会忽略扩展位），
// 而 _meta 通道的 agent 标准 configOptions 恒空、`set_config_option` 会抛错
// （隐藏 configOptions 正是那条通道存在的前提）。猜错任一侧，
// 表现都是"界面显示已切换、实际没换"——最难排查的那类静默失效。
type catalogSource int

const (
	catalogNone     catalogSource = iota // 没读到清单：不支持会话内选模型
	catalogStandard                      // 标准 configOptions → 用 session/set_config_option 设
	catalogMeta                          // _meta["pieqi/configOptions"] → 用请求 _meta.model 设
)

// ModelConfigID 是 ACP 里「模型」这一配置项的 id。
const ModelConfigID = "model"

// modelCatalogMetaKey 是响应 _meta 里承载真实配置清单的键。
//
// 键名来自那份**已弃用**的本地 dsh-acp fork（其 lib/index.js 的 catalogMeta()）——
// 双方改键名必须同步，否则清单恒空（表现为前端没有模型下拉）。
// fork 已删（见 docs/adr/0011），但常量与 catalogMeta 分支**保留**：
// 它是"标准 configOptions 恒空"时的兜底通道，见文件头的说明。
const modelCatalogMetaKey = "pieqi/configOptions"

// ModelOption 一个可被选择的模型。
//
// Value 是**不透明选择值**：必须原样取自 agent 下发的清单、再原样回传给 agent
// （建会话时经请求 _meta.Model，按轮换时经 prompt 的 _meta.Model）。别手拼。
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

// catalogOptionsFromMeta 把响应 _meta 里的清单还原成 SDK 的配置选项类型。
//
// 为什么要绕一趟 JSON：_meta 是「任意结构」的扩展位，SDK 只给 map[string]any；
// 而清单的线格式就是标准 ACP 的 SessionConfigOption（有自定义 UnmarshalJSON 按
// `type` 判别变体）。序列化回去再解，等于复用 SDK 自己的解析，不手抄一遍形状。
// 解析不出来**不报错**，按"没下发"处理：_meta 是各方共用的扩展位，容错比严格重要。
func catalogOptionsFromMeta(meta map[string]any) []acp.SessionConfigOption {
	if meta == nil {
		return nil
	}
	raw, ok := meta[modelCatalogMetaKey]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var opts []acp.SessionConfigOption
	if err := json.Unmarshal(b, &opts); err != nil {
		return nil
	}
	return opts
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// pickCatalog 从一次会话协商的两种来源里挑出清单，并报出它是从哪条通道来的。
//
// 优先级：**标准字段优先**。理由：标准字段存在即说明 agent 是原生实现、走协议正路，
// 而走正路的 agent 支持 `set_config_option`；标准字段恒空的 agent 自然落到 _meta 分支。
// 反过来先看 _meta 的话，一个"两者都发"的 agent 会被误判成 _meta 通道，
// 于是用 `_meta.model` 去设模型——原生 agent 会忽略它，静默失效。
func pickCatalog(opts []acp.SessionConfigOption, meta map[string]any) (ModelCatalog, catalogSource) {
	if cat := ExtractModelCatalog(opts); len(cat.Options) > 0 {
		return cat, catalogStandard
	}
	if cat := ExtractModelCatalog(catalogOptionsFromMeta(meta)); len(cat.Options) > 0 {
		return cat, catalogMeta
	}
	return ModelCatalog{}, catalogNone
}

// --- 清单探测（两级缓存 + 单飞） ---

// modelCatalogTTL 清单缓存时长。
//
// 为什么要缓存：取清单的唯一办法是 spawn 一个**真会话**（清单只有 agent 在
// session/new 的响应里给，没有独立的"列模型"方法），一次探测要起一个 agent 进程
// （实测 dsh 冷启 ~1-3s，最慢见过 77s；2026-10-22 复测 qodercli 冷探测 5.7~7.0s）。
// 用户在新任务页切几次 agent 就够把代价放大到不可接受，而模型清单本身几天才变一次。
//
// TTL 分档的判据是**值的可靠性**，不是耗时：
//   - **非空清单 → 1 天**。条目几天才变；过期最多读到已下架的值，而建会话时一次失败
//     会把该条目**立即摘掉**并重探（见 InvalidateAgentModels 的第二个调用点），
//     所以长 TTL 不等于"更久地错着"。
//   - **空清单 → 10 分钟**。它可能是"真没清单"（claude 走桥），也可能是"这次没拿到"，
//     后者应当较快重试。
//   - **失败 → 1 分钟**：配置修好后应尽快自愈。
const (
	modelCatalogTTL    = 24 * time.Hour
	modelCatalogEmpty  = 10 * time.Minute
	modelCatalogErrTTL = 1 * time.Minute
	modelCatalogProbe  = 45 * time.Second
	// modelCatalogStaleMax 磁盘缓存的最大可接受年龄（跨进程）。
	//
	// 为什么内存缓存之外还要磁盘缓存：内存档每次重启即清空，而重启在本项目是常态
	// （发版、自重启、崩溃恢复）。实测重启后第一次打开会话页要等 **6.98s** 才拿到
	// 下拉框 —— 那正是"model 列表变慢了"的观感来源。清单是 agent 侧的静态事实，
	// 重启不会让它改变，所以按天放过期，只有超过一天才重新起进程确认。
	modelCatalogStaleMax = 7 * 24 * time.Hour
	// modelCatalogCacheMax 内存条目上限（agent 数量级为个位数，纯属兜底）。
	modelCatalogCacheMax = 32
)

type modelCatalogEntry struct {
	cat     ModelCatalog
	err     error
	expires time.Time
}

var (
	modelCatalogMu    sync.Mutex
	modelCatalogCache = map[string]modelCatalogEntry{}

	// 单飞：同一 agent 的并发探测只允许一个真进程。
	//
	// 不引 golang.org/x/sync/singleflight 是为维持零新增依赖；语义等价。
	// 没有单飞时，会话页与新建页各拉一次、或用户快速切 agent，会同时起多个 agent
	// 进程 —— 每个 5~7s、都要吃内存，而它们的答案必然相同。
	modelCatalogProbing = map[string]*catalogProbeWait{}

	// 探测函数与缓存目录做成变量：测试要注入假探测，且不能写进真实的 ~/.pieqi。
	probeModelsFn = probeAgentModels
	catalogDirFn  = defaultCatalogDir
)

type catalogProbeWait struct {
	done chan struct{}
	cat  ModelCatalog
	err  error
}

// SetModelCatalogDir 指定磁盘缓存目录（空 = 不落盘，退化为仅内存缓存）。
//
// 由 main 注入 dataRoot：agent 包**不该**自己推导 PIEQI_HOME —— 否则会出现
// 两份"数据根"，且一旦规则改动就会静默写到别处。
func SetModelCatalogDir(dir string) {
	modelCatalogMu.Lock()
	defer modelCatalogMu.Unlock()
	if dir == "" {
		return
	}
	catalogDirFn = func() string { return dir }
}

func defaultCatalogDir() string { return "" }

// ListAgentModels 返回某 agent 当前可选的模型清单（内存 → 磁盘 → 现探测）。
//
// 非 ACP agent（如 claude 的桥）或未改造、不下发清单的 agent 返回**空清单且无错误**
// —— 语义是"这个 agent 不支持外部指定模型"，前端据此隐藏下拉框，不是故障。
// 只有探测本身失败（进程起不来 / 未登录 / profile 的模型配置失效）才返回错误。
func ListAgentModels(ctx context.Context, agentName string) (ModelCatalog, error) {
	modelCatalogMu.Lock()
	if e, ok := modelCatalogCache[agentName]; ok && time.Now().Before(e.expires) {
		modelCatalogMu.Unlock()
		return e.cat, e.err
	}
	if disk, ok := loadCatalogFromDisk(agentName); ok {
		modelCatalogCache[agentName] = disk
		modelCatalogMu.Unlock()
		return disk.cat, disk.err
	}
	if w, ok := modelCatalogProbing[agentName]; ok {
		modelCatalogMu.Unlock()
		select {
		case <-w.done:
			return w.cat, w.err
		case <-ctx.Done():
			// 调用方已放弃：返回空清单而非错误（报错也到不了它那里）。
			return ModelCatalog{Agent: agentName}, nil
		}
	}
	w := &catalogProbeWait{done: make(chan struct{})}
	modelCatalogProbing[agentName] = w
	modelCatalogMu.Unlock()

	cat, err := probeModelsFn(ctx, agentName)

	modelCatalogMu.Lock()
	modelCatalogCache[agentName] = modelCatalogEntry{
		cat: cat, err: err, expires: time.Now().Add(catalogTTL(cat, err)),
	}
	if len(modelCatalogCache) > modelCatalogCacheMax {
		evictExpiredCatalogsLocked(time.Now())
	}
	delete(modelCatalogProbing, agentName)
	modelCatalogMu.Unlock()

	if err == nil {
		// 落盘在锁外：IO 不能占着锁，否则探测路径上的读者被磁盘写入拖住。
		saveCatalogToDisk(agentName, cat)
	}

	w.cat, w.err = cat, err
	close(w.done)
	return cat, err
}

// catalogTTL 按"这份值有多可靠"选过期时间，见 modelCatalogTTL 的说明。
func catalogTTL(cat ModelCatalog, err error) time.Duration {
	switch {
	case err != nil:
		return modelCatalogErrTTL
	case len(cat.Options) == 0:
		return modelCatalogEmpty
	default:
		return modelCatalogTTL
	}
}

// InvalidateAgentModels 丢弃某 agent 的清单缓存（内存 + 磁盘），下次调用重新探测。
//
// 目前两个调用点，共同点是"缓存已被证明是错的"：
//   - 运行期设置变更（agents.enabled / spawn_overrides）：探测配置换了，旧结果不代表现状；
//   - **建会话失败**（见 acp.go）：agent 报"没有这个模型"时，最可能是清单里那一项
//     已经被上游下架。若还抱着 1 天的缓存，用户会连续失败到缓存自然过期。
func InvalidateAgentModels(agentName string) {
	modelCatalogMu.Lock()
	delete(modelCatalogCache, agentName)
	modelCatalogMu.Unlock()
	if p := catalogFile(agentName); p != "" {
		_ = os.Remove(p)
	}
}

// --- 磁盘缓存（跨重启） ---

// catalogCacheFile 磁盘缓存的 JSON 形状。
//
// 带 ProbedAt 而不是看文件 mtime：mtime 会被备份/同步/拷贝改掉，
// 而"这份清单是哪一刻从 agent 那里读到的"只有我们自己知道。
type catalogCacheFile struct {
	Agent     string        `json:"agent"`
	ProbedAt  time.Time     `json:"probed_at"`
	Options   []ModelOption `json:"options"`
	Current   string        `json:"current,omitempty"`
	AgentType string        `json:"agent_type,omitempty"` // 探测时的 agent_type，变了就作废
}

// catalogFile 缓存文件路径；目录未注入时返回空串 = 不落盘。
func catalogFile(agentName string) string {
	dir := catalogDirFn()
	if dir == "" || agentName == "" {
		return ""
	}
	// agent 名来自路由参数，必须清洗后才拼路径（防 `../` 逃出缓存目录）。
	safe := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, agentName)
	return filepath.Join(dir, "agent-models-"+safe+".json")
}

// loadCatalogFromDisk 读磁盘缓存。不可用（没文件/坏 JSON/太旧/agent_type 变了）一律
// 视作 miss —— 缓存只负责加速，读不到只是慢一点，绝不能因此报错。
func loadCatalogFromDisk(agentName string) (modelCatalogEntry, bool) {
	path := catalogFile(agentName)
	if path == "" {
		return modelCatalogEntry{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return modelCatalogEntry{}, false
	}
	var f catalogCacheFile
	if err := json.Unmarshal(b, &f); err != nil {
		return modelCatalogEntry{}, false
	}
	age := time.Since(f.ProbedAt)
	if age < 0 || age > modelCatalogStaleMax {
		return modelCatalogEntry{}, false
	}
	// agent_type 变了说明"已经不是同一个程序"（如换了实现），旧清单没有参考价值。
	if cur := acpConfigFor(agentName).AgentType; cur != "" && f.AgentType != "" && cur != f.AgentType {
		return modelCatalogEntry{}, false
	}
	cat := ModelCatalog{Agent: agentName, Current: f.Current, Options: f.Options}
	ttl := catalogTTL(cat, nil)
	// 剩余寿命从**探测时刻**起算，与内存档一致：否则每次重启都在续命一条本应过期的缓存。
	expires := f.ProbedAt.Add(ttl)
	// 但短 TTL 档（空清单）跨进程只给一个下限：否则重启后读到它立刻又过期，磁盘档等于没有。
	if floor := time.Now().Add(ttl); expires.Before(floor) {
		expires = floor
	}
	return modelCatalogEntry{cat: cat, expires: expires}, true
}

// saveCatalogToDisk 原子写（tmp + rename），失败静默：写不进去只是重启后慢一次。
func saveCatalogToDisk(agentName string, cat ModelCatalog) {
	path := catalogFile(agentName)
	if path == "" {
		return
	}
	f := catalogCacheFile{
		Agent:     agentName,
		ProbedAt:  time.Now(),
		Options:   cat.Options,
		Current:   cat.Current,
		AgentType: acpConfigFor(agentName).AgentType,
	}
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

func evictExpiredCatalogsLocked(now time.Time) {
	for k, v := range modelCatalogCache {
		if now.After(v.expires) {
			delete(modelCatalogCache, k)
		}
	}
}

// probeAgentModels 起一个一次性会话把清单读出来，随即关掉进程。
//
// 副作用（知悉并接受）：会在 agent 自己的会话存储里留一条（空）会话记录。
// 用固定的探针工作目录把它约束在**一份**存储下，不随探测次数增长。
// 不用调用方传 cwd：那会把探针混进任务的项目目录里，进而出现在 dsh 的会话列表里。
func probeAgentModels(ctx context.Context, agentName string) (ModelCatalog, error) {
	cat := ModelCatalog{Agent: agentName}
	if _, ok := defaultACPAgentType[agentName]; !ok {
		return cat, nil // 非 ACP agent：不支持外部指定模型
	}
	cfg := acpConfigFor(agentName)
	if cfg.AgentType == "" {
		return cat, nil // 未配置 = 这个 agent 本来就不在可选目录里
	}
	probeDir := filepath.Join(os.TempDir(), "pieqi-model-probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		return cat, err
	}
	a := NewACPAgent(cfg, acpLogger())
	defer func() { _ = a.Close(context.Background()) }()

	probeCtx, cancel := context.WithTimeout(ctx, modelCatalogProbe)
	defer cancel()
	if _, err := a.NewSession(probeCtx, SessionConfig{Cwd: probeDir}); err != nil {
		return cat, explainProbeErr(err)
	}
	// 空清单不算错误：agent 支持会话但不对外给清单（未改造 / 关闭了该能力），
	// 前端隐藏下拉框即可，不该把它渲染成一个失败。
	cat = a.Models()
	cat.Agent = agentName
	return cat, nil
}

// explainProbeErr 给已知的「配置类」失败补上可操作提示。
//
// 为什么值得特判：`has no configured model` 的根因**不在 pieqi**，而在 agent 的
// profile 配置（如 ~/.dsh/profiles/acp/cordis.patch.yml 的 `- id: acp`.model）——
// 上游把模型下架/改名后，那份清单会变，而 pin 没跟着改，于是建会话直接失败。
// 原始错误只给出失效的 id，不说是哪份配置、更不说怎么修；2026-10-07 就因此
// 从外网只能看到一个语焉不详的失败，排查成本不低。
//
// 注：**pin 缺失与 pin 写错都会失败**（官方 dsh-acp 没有配置兜底）。曾经那份本地 fork
// 的 B1 能在 pin 缺失时回退到部署默认模型（agent-default-model），它已弃用
// （见 docs/adr/0011），所以现在 pin 必须是有效值。
//
// ⚠️ "没有 provider/model" 在 dsh 里**不等于"删掉 `- id: acp` 那一行"**：
// `@deepseek-ai/dsh-acp-app` bundle 自带 `provider: deepseek-official` /
// `model: deepseek-v4-flash`，删掉自己那行只会让 bundle 的值生效 —— 那个 provider
// 需要 DEEPSEEK_API_KEY，本机没有，于是 `session/prompt` 报
// `no API key for provider route "deepseek-official"`（**建会话能过，第一轮才炸**）。
//
// ⚠️⚠️ **不要**再用「显式写空 `provider/model: !!js undefined`」来表达"用部署默认模型"：
// 那条路依赖已弃用 fork 的 B1 兜底（见 docs/adr/0011）。官方 dsh-acp 下 pin 一空，
// 会话就没有模型，首轮报
// `prompt variable "{{model}}" has no value for this assembly`。
// 换言之：**pin 必须写成一个有效值**，没有"交给部署默认"这个选项。
func explainProbeErr(err error) error {
	if err == nil || !strings.Contains(err.Error(), "has no configured model") {
		return err
	}
	return fmt.Errorf("%w —— 这是 **agent 侧的模型配置失效**（不是 pieqi 故障）：profile 里 "+
		"`- id: acp` 的 model 已不在 `llm-pi-ai` 的 models 清单中，上游下架/改名模型时会这样。"+
		"修法：改 ~/.dsh/profiles/acp/cordis.patch.yml 的该行，改成清单里仍存在的 id"+
		"（注意 provider 前缀：`workbuddy/` 与 `workbuddy-ai/` 是两个不同的桶）；"+
		"**别把它清空或删掉整行** —— 官方 dsh-acp 没有配置兜底，pin 一空会话就没有模型"+
		"（清空报 `{{model}} has no value`；删整行会被 bundle 兜回需要 DEEPSEEK_API_KEY 的 "+
		"deepseek-official）。再用 `dsh --profile acp --dump-config` 校验"+
		"（清单的权威来源是网关 GET /v1/models）", err)
}
