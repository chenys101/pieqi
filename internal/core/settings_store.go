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
	return s, nil
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

	// 免打扰开着但起止相同 = 一个永远不生效的时段。它不会报错、只会安安静静
	// 什么都不做 —— 正是最该拦下的那类配置。
	if next.DNDEnabled && next.DNDStart == next.DNDEnd {
		s.mu.Unlock()
		return prev, fmt.Errorf("dnd period must not be empty")
	}

	s.cur = next
	if err := s.persistLocked(); err != nil {
		s.cur = prev
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
