package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"pieqi/internal/model"
)

// RiskLevel 是 model.RiskLevel 的别名。
//
// 分级定义在 model（因为 Decision 要带它），这里保留别名让 core 内的调用点不必改写法。
type RiskLevel = model.RiskLevel

const (
	RiskL0 = model.RiskL0 // 只读探测
	RiskL1 = model.RiskL1 // 写入
	RiskL2 = model.RiskL2 // 执行命令
	RiskL3 = model.RiskL3 // 破坏性操作
)

// riskLevelKinds 风险分级 → ACP ToolKind 的映射。
//
// ToolKind 取值来自 ACP 标准（Claude Code 适配器把 Edit/Write/MultiEdit/NotebookEdit
// 都映射到 "edit"，Delete→"delete"，Rename/Move→"move"）。
//
// ⚠️ `other` 被放进 **L2** 而不是 L0，这不是随手一放：
// 免审名单的作用是**枚举**可以放行的东西；`other` 是"没枚举到"的兜底，
// 把它放进自动放行等于把白名单变成通配符 —— 以后协议新增任何 ToolKind
// 都会默默获得放行权，而没有人会注意到。
var riskLevelKinds = map[RiskLevel][]string{
	RiskL0: {"read", "search", "fetch", "think"},
	RiskL1: {"edit", "move"},
	RiskL2: {"execute", "switch_mode", "other"},
	RiskL3: {"delete"},
}

// kindRisk ToolKind → 风险分级的反查表，由 riskLevelKinds 派生成，避免两处各写一份。
var kindRisk = func() map[string]RiskLevel {
	m := make(map[string]RiskLevel, 16)
	for lvl, kinds := range riskLevelKinds {
		for _, k := range kinds {
			m[k] = lvl
		}
	}
	return m
}()

// kindLabels ACP ToolKind 的人读标签。审批卡标题用它。
//
// 为什么需要它：ACP 的 `toolCall.title` 语义是"给人看的一句话"，但各家 agent
// 实现不一 —— claude-code 适配器给的是 "Bash" 这种短名，qodercli 给的是**整条命令原文**
// （实测平均 191 字符、最长 1109、9% 带换行）。标题栏塞进整条命令，卡片就得滚半屏，
// 而且和摘要行是同一串文本（重复渲染）。
//
// 与 riskLevelKinds 同表维护：新增 ToolKind 时两边一起加，否则它会落到 `other` 的兜底标签，
// 卡片上看不出那是什么操作。
var kindLabels = map[string]string{
	"read":        "读取",
	"search":      "搜索",
	"fetch":       "网络请求",
	"think":       "思考",
	"edit":        "文件编辑",
	"move":        "移动/重命名",
	"execute":     "执行命令",
	"delete":      "删除",
	"switch_mode": "切换模式",
	"other":       "工具调用",
}

// KindLabel 由 ACP ToolKind 取人读标签；未知/空 kind 落到"工具调用"。
func KindLabel(kind string) string {
	if l, ok := kindLabels[kind]; ok {
		return l
	}
	return "工具调用"
}

// riskLabels 风险等级的人读标签。与前端 RISK_LABELS（web/src/types/approval.ts）同源，
// 两边必须一致 —— IM 文案与卡片上显示的档位说明是同一件事的两处呈现。
var riskLabels = map[RiskLevel]string{
	RiskL0: "只读",
	RiskL1: "写入",
	RiskL2: "执行命令",
	RiskL3: "破坏性",
}

// RiskLabel 由风险等级取人读标签；未知/空一律按 L2 的标签（与 RiskOfKind 同一判据）。
func RiskLabel(l RiskLevel) string {
	if s, ok := riskLabels[l]; ok {
		return s
	}
	return riskLabels[RiskL2]
}

// RiskOfKind 由 ACP ToolKind 反查风险分级。
//
// **未知 / 空 kind 一律算 L2**，这不是保守过头的默认值，而是与 `other` 归 L2 同一个判据：
// 分级表的作用是**枚举**已知操作；没枚举到的东西若按低风险处理，等于让"我们不认识的操作"
// 悄悄获得一张弱强度的卡 —— 以后协议新增任何 ToolKind 都会默默被当成无害的。
// 「不知道」必须往保守那侧倒。
func RiskOfKind(kind string) RiskLevel {
	if lvl, ok := kindRisk[kind]; ok {
		return lvl
	}
	return RiskL2
}

// Settings 全局偏好（跨任务 / 跨项目 / 跨 agent）。
//
// 它只装「设置页判据」允许留下的东西：属于某个对象的配置就地设置，
// 不进这里。每加一个字段前先问一句"它属于哪个对象"。
type Settings struct {
	// ---------- ① 审批与自动化 ----------

	// AutoApproveL0 / AutoApproveL1 是**仅有的两个**可配置的自动放行开关。
	// L2 / L3 没有对应字段 —— 它们的不可配置性由"字段不存在"承载，
	// 比"字段存在但永远读作 false"更难被误改回来。
	AutoApproveL0 bool `json:"auto_approve_l0"`
	AutoApproveL1 bool `json:"auto_approve_l1"`

	// 免打扰时段：此时段内审批**只排队、不推送**，打开界面时集中呈现。
	// 它留在「审批与自动化」而不是"通知"里：它管的是**要不要打断你**，
	// 与自动放行是同一件事的两面。
	DNDEnabled bool   `json:"dnd_enabled"`
	DNDStart   string `json:"dnd_start"` // "22:00"
	DNDEnd     string `json:"dnd_end"`   // "08:00"

	// ---------- ④ 数据与关于 ----------

	// EventRetention 事件保留上限（每个任务）。超出的从**最旧的**开始丢弃。
	// 0 = 全部保留。默认 5000。
	EventRetention int `json:"event_retention"`

	// ---------- ⑥ 新建任务缺省值 ----------

	// DefaultProjectPath 新建任务的**缺省工作目录**（绝对路径）。
	//
	// 为什么需要它：项目不再是必选项。此前每次新建任务都必须先点一个项目或手打一条
	// 绝对路径，而绝大多数人的用法是"在一个固定目录里让 agent 干活" —— 那个目录
	// 每次都一样，却每次都要求人再选一遍。
	//
	// 空串 = 未配置：此时 POST /api/tasks 不带 project_path 会被**明确拒绝**
	// （400，提示先到设置页配置），而不是拿某个猜测目录兜底。猜一个目录的后果是
	// agent 在用户没预期的仓库里改代码，比一句报错严重得多。
	// 取值由 main.go 用 <dataRoot>/workspace 作为出厂缺省注入（见 SetFallbackProjectPath）。
	//
	// ⚠️ **不能加 omitempty**：空串是"用户显式清空"的合法取值，加 omitempty 会让这个键
	// 在清空时从 JSON 里消失 ⇒ 重启后与"从没配过"不可区分 ⇒ 出厂缺省被填回来
	// （设置页表现为"清空了又自己回来"）。回归测试见
	// TestSettings_DefaultProjectPath_ExplicitClearSticks。
	DefaultProjectPath string `json:"default_project_path"`

	// ---------- ⑤ Agent 选择（2026-10-22 新增，运行期） ----------

	// Agents 启用了哪些 agent，以及各 agent 的机器相关覆盖项。
	//
	// 为什么放 Settings 而不是 config.yaml：agent 的**代码事实**（怎么 spawn、
	// agent_type 是什么、有什么能力）已整体搬进 internal/agent/manifest.go；
	// 剩下的只有"这台机器上开不开、装在哪"——那是用户意图，属于运行期数据，
	// 应当可热改、不必重启、不随代码走。参见 AGENTS.md「配置迁移注意」。
	//
	// 为什么不是"属于某个对象的配置"（Settings 原先的唯一准入标准）：
	// 它跨任务、跨项目，作用域就是**全局**——恰好是本类型的定义域。它既不描述
	// 某个任务，也不描述某个渠道，所以放在这里不违反那条纪律。
	Agents AgentSelection `json:"agents"`
}

// AgentSelection agent 的运行期选择结果。
//
// 字段刻意保持"用户意图"粒度：**不出现** agent_type / 能力声明 / 默认 spawn 这类
// 代码事实（它们由 manifest 提供）。这样接入一个新 agent 不需要任何人动这个文件。
type AgentSelection struct {
	// Enabled 启用的业务名（claude / qoder / dsh / codebuddy ...）。
	//
	// nil 或空 = 用 manifest 的默认值（即所有非 OptIn 的 agent）。显式列出时以列表为准 ——
	// 可以只开一个，也可以把默认开着的关掉。nil 与空切片语义相同是有意的：
	// 让"从没配过"和"清空"都回到默认，避免用户在设置页删光后得到"一个都没有"。
	Enabled []string `json:"enabled,omitempty"`

	// SpawnOverrides 按业务名覆盖 spawn 命令（例如把 dsh 指到独立安装位置）。
	//
	// 这是**机器相关**知识（"我的 node / dsh 装在哪"），所以归运行期；manifest 里
	// 只放跨机器不变的裸名。键是业务名，值是命令分词。
	SpawnOverrides map[string][]string `json:"spawn_overrides,omitempty"`

	// ClaudeTransport claude 的传输选择："sdk-bridge"（默认）| "print"。
	// 空 = sdk-bridge。它是"这台机器上桥能不能跑"的运行期事实。
	ClaudeTransport string `json:"claude_transport,omitempty"`
}

// DefaultSettings 出厂默认值。
//
// L0 / L1 默认放行，L2 / L3 永远不放行：
//   - L0 不产生任何改动，自动放行没有风险；
//   - L1 每轮都有 Checkpoint 可回退，因此也允许放行；
//   - L2（装依赖、跑脚本，影响溢出到工作区之外）与 L3（删除、覆盖、强制推送）
//     除内联二次确认外没有别的路径。
func DefaultSettings() Settings {
	return Settings{
		AutoApproveL0:  true,
		AutoApproveL1:  true,
		DNDEnabled:     false,
		DNDStart:       "22:00",
		DNDEnd:         "08:00",
		EventRetention: 5000,
	}
}

// AutoApproveTools 由 L0 / L1 两个开关推导出生效的 ToolKind 免审名单。
//
// 这是 Settings 与 agent 层之间**唯一**的接口：agent 侧不需要知道"风险分级"
// 这个概念，它只认 ToolKind 白名单。分级是给人看的，白名单是给机器看的。
func (s Settings) AutoApproveTools() []string {
	var out []string
	if s.AutoApproveL0 {
		out = append(out, riskLevelKinds[RiskL0]...)
	}
	if s.AutoApproveL1 {
		out = append(out, riskLevelKinds[RiskL1]...)
	}
	return out
}

// InDND 判断给定时刻是否落在免打扰时段内（本地时间）。
// 时段可跨零点（22:00–08:00 是常态而非特例），所以不能只比较 start <= t < end。
func (s Settings) InDND(t time.Time) bool {
	if !s.DNDEnabled {
		return false
	}
	start, ok1 := parseClock(s.DNDStart)
	end, ok2 := parseClock(s.DNDEnd)
	if !ok1 || !ok2 {
		return false // 配置坏了就当作没开 —— 静默不推送的代价远大于多推一次
	}
	cur := t.Hour()*60 + t.Minute()
	switch {
	case start == end:
		return false // 空区间
	case start < end:
		return cur >= start && cur < end
	default: // 跨零点
		return cur >= start || cur < end
	}
}

// parseClock 解析 "HH:MM" 为当日分钟数。
func parseClock(v string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 2 {
		return 0, false
	}
	var h, m int
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// SettingsPatch 补丁语义：nil = 不修改。
// 与 BotPatch 同一套理由 —— 要能区分「显式改成 false」与「没传」。
type SettingsPatch struct {
	AutoApproveL0  *bool
	AutoApproveL1  *bool
	DNDEnabled     *bool
	DNDStart       *string
	DNDEnd         *string
	EventRetention *int
	// DefaultProjectPath 新建任务的缺省工作目录（nil = 不修改）。
	//
	// 补丁语义而不是"整体替换"：它是一台机器上一个固定目录，本身就是单值，
	// 不存在 Agents 那种"集合并发改丢一项"的问题。
	DefaultProjectPath *string
	// Agents 整体替换 agent 选择（nil = 不修改）。
	//
	// 用整体替换而非逐字段补丁：agent 列表本来就是一个集合，"加一个/删一个"
	// 的补丁语义在并发下容易互相覆盖（两个人同时开关不同 agent 会丢一个改动）。
	Agents *AgentSelection
}

// SettingsStore 全局偏好的落盘存储（~/.pieqi/settings.json）。
//
// 与 auth.BindingStore / BotStore 同模式：RWMutex + 「写 .tmp 再 rename」原子替换。
// 文件不存在 = 用出厂默认值（合法状态），文件损坏则报错 —— 不静默重置用户的配置。
type SettingsStore struct {
	mu       sync.RWMutex
	path     string
	cur      Settings
	onChange []func(Settings)

	// fallbackProjectPath 出厂缺省工作目录（main.go 注入 <dataRoot>/workspace）。
	//
	// 为什么需要这一层，而不是把默认值直接写进 DefaultSettings()：
	//   - 它依赖 PIEQI_HOME（运行期事实），core 包里算不出来；
	//   - 更要紧的是**必须能区分「从没配过」与「用户显式清空」**。两者若同样表现为
	//     空串，用户就永远关不掉这个缺省（清空后又被默认值填回来），设置页上
	//     表现为"删了又回来了"。
	//
	// 判据取自落盘文件：见 NewSettingsStore 的 applyFallbackLocked 调用点。
	fallbackProjectPath string
	// projectPathSet 用户是否**显式**配置过 default_project_path（落盘 JSON 里有这个键）。
	// 只由加载/落盘路径维护，不参与对外语义。
	projectPathSet bool
}

// NewSettingsStore 打开（或首次创建）设置文件。path 为空时退化为纯内存存储
// （旧测试 / 未接线场景仍可用）。
func NewSettingsStore(path string) (*SettingsStore, error) {
	s := &SettingsStore{path: path, cur: DefaultSettings()}
	if path == "" {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("mkdir settings dir: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil // 还没配过 —— 用默认值，首次写入时才落盘
		}
		return nil, fmt.Errorf("read settings: %w", err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var loaded Settings
	if err := json.Unmarshal(data, &loaded); err != nil {
		return nil, fmt.Errorf("parse settings %s: %w", path, err)
	}
	s.cur = loaded
	// 「显式配过」的判据只有一个：落盘 JSON 里**有没有这个键**。
	// 用"值是否为空串"来判断是错的 —— 空串正是用户清空后的正常取值。
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err == nil {
		_, s.projectPathSet = probe["default_project_path"]
	}
	s.applyFallbackLocked()
	return s, nil
}

// SetFallbackProjectPath 注入出厂缺省工作目录（main.go 在创建 store 后立即调用）。
//
// 必须在 NewSettingsStore **之后**调用：加载时还无从知道这个值，applyFallbackLocked
// 走的是"没有缺省"的分支；这里补上后再重新应用一次。
// 已显式配置过（含显式清空）的用户**不受影响** —— 那是本方法的核心约束。
func (s *SettingsStore) SetFallbackProjectPath(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fallbackProjectPath = strings.TrimSpace(dir)
	s.applyFallbackLocked()
}

// applyFallbackLocked 在用户没显式配置时用出厂缺省填上。调用方需持锁。
func (s *SettingsStore) applyFallbackLocked() {
	if s.projectPathSet {
		return
	}
	s.cur.DefaultProjectPath = s.fallbackProjectPath
}

// Get 返回当前设置的副本。
func (s *SettingsStore) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// OnChange 注册变更回调（main.go 用它把新的免审名单推给 TaskRunner）。
// 注册时**不立刻回调** —— 初始值由调用方自己装一次，避免"注册顺序决定初值"。
func (s *SettingsStore) OnChange(fn func(Settings)) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	s.onChange = append(s.onChange, fn)
	s.mu.Unlock()
}

// Patch 应用补丁并落盘。落盘失败时回滚内存态（保持内存与磁盘一致）。
func (s *SettingsStore) Patch(p SettingsPatch) (Settings, error) {
	s.mu.Lock()
	prev := s.cur
	next := prev
	explicitProjectPath := false

	if p.AutoApproveL0 != nil {
		next.AutoApproveL0 = *p.AutoApproveL0
	}
	if p.AutoApproveL1 != nil {
		next.AutoApproveL1 = *p.AutoApproveL1
	}
	if p.DNDEnabled != nil {
		next.DNDEnabled = *p.DNDEnabled
	}
	if p.DNDStart != nil {
		if _, ok := parseClock(*p.DNDStart); !ok {
			s.mu.Unlock()
			return prev, fmt.Errorf("dnd_start must be HH:MM")
		}
		next.DNDStart = *p.DNDStart
	}
	if p.DNDEnd != nil {
		if _, ok := parseClock(*p.DNDEnd); !ok {
			s.mu.Unlock()
			return prev, fmt.Errorf("dnd_end must be HH:MM")
		}
		next.DNDEnd = *p.DNDEnd
	}
	if p.EventRetention != nil {
		if *p.EventRetention < 0 {
			s.mu.Unlock()
			return prev, fmt.Errorf("event_retention must be >= 0")
		}
		next.EventRetention = *p.EventRetention
	}
	if p.DefaultProjectPath != nil {
		v, err := normalizeDefaultProjectPath(*p.DefaultProjectPath)
		if err != nil {
			s.mu.Unlock()
			return prev, err
		}
		next.DefaultProjectPath = v
		// 显式写一次（哪怕写的是空串）就永久转入"用户自己决定"的分支，
		// 出厂缺省不再回填 —— 否则用户清空后重启，默认目录又回来了。
		// 只在**落盘成功之后**置位，避免写到一半失败时内存态与磁盘态分叉。
		explicitProjectPath = true
	}
	if p.Agents != nil {
		next.Agents = *p.Agents
	}

	// 免打扰开着但起止相同 = 一个永远不生效的时段。它不会报错、只会安安静静
	// 什么都不做 —— 正是最该拦下的那类配置。
	if next.DNDEnabled && next.DNDStart == next.DNDEnd {
		s.mu.Unlock()
		return prev, fmt.Errorf("dnd period must not be empty")
	}

	s.cur = next
	prevSet := s.projectPathSet
	if explicitProjectPath {
		s.projectPathSet = true
	}
	if err := s.persistLocked(); err != nil {
		s.cur = prev
		s.projectPathSet = prevSet
		s.mu.Unlock()
		return prev, err
	}
	callbacks := make([]func(Settings), len(s.onChange))
	copy(callbacks, s.onChange)
	s.mu.Unlock()

	// 回调在锁外执行：它们会去改 runner / bridge 的状态，
	// 在锁内调用等于把 SettingsStore 的锁与别处的锁串起来（死锁风险）。
	for _, fn := range callbacks {
		fn(next)
	}
	return next, nil
}

// DefaultWorkspaceDirName 缺省工作目录在数据根下的目录名（即 <dataRoot>/workspace）。
//
// 为什么是"数据根下的子目录"而不是用户的某个项目目录：它必须**存在且可写**
// （agent 要在里面改文件），而 ~/.pieqi 是 pieqi 自己创建并保证可写的地方；
// 反过来挑一个"看起来像项目"的目录（如 ~/code）在别人的机器上可能根本不存在，
// 于是默认值在别人的机器上表现为"建任务 400" —— 一个只在别人那里坏掉的默认值。
const DefaultWorkspaceDirName = "workspace"

// normalizeDefaultProjectPath 归一并校验缺省工作目录。
//
// 三条判据：
//  1. 空串 = **清空配置**（合法），此时新建任务必须显式带路径；
//  2. 相对路径一律拒绝 —— 它的含义取决于进程 cwd（服务跑在 ~/.pieqi/bin），
//     而用户在设置页写的是"他心里的那个目录"，两者不是一回事；
//  3. 必须存在且是目录 —— 否则第一次建任务才失败，而成因（设置写错了）
//     离现象（建任务报错）很远。
//
// 返回的是 filepath.Clean 过的原样路径（不 EvalSymlinks）：用户看到的就是他写的那条，
// 换成真实路径会让设置页里显示的东西与他输入的不一致。
func normalizeDefaultProjectPath(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if !filepath.IsAbs(v) {
		return "", fmt.Errorf("default_project_path must be an absolute path, got %q", v)
	}
	abs := filepath.Clean(v)
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("default_project_path not accessible: %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("default_project_path is not a directory: %s", abs)
	}
	return abs, nil
}

// NormalizeDefaultProjectPath 是 normalizeDefaultProjectPath 的导出版本。
//
// 为什么要导出：新建任务时可以**不带** project_path（项目已不是必选项），
// api 层需要与设置页**同一套判据**来解析它 —— 两处各写一份必然漂移
// （典型症状：设置页存得进、建任务时却 400）。
func NormalizeDefaultProjectPath(v string) (string, error) {
	return normalizeDefaultProjectPath(v)
}

func (s *SettingsStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.cur, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write settings tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename settings: %w", err)
	}
	return nil
}

// RiskMatrix 返回分级 → ToolKind 的只读视图（供 API 下发说明文案）。
func RiskMatrix() map[string][]string {
	out := make(map[string][]string, len(riskLevelKinds))
	for lv, kinds := range riskLevelKinds {
		cp := append([]string(nil), kinds...)
		sort.Strings(cp)
		out[string(lv)] = cp
	}
	return out
}
