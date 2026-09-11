package core

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"pieqi/internal/model"
)

// TestSettings_DefaultAutoApprove 默认放行 L0/L1、**永不**放行 L2/L3。
func TestSettings_DefaultAutoApprove(t *testing.T) {
	got := DefaultSettings().AutoApproveTools()
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	for _, k := range []string{"read", "search", "fetch", "think", "edit", "move"} {
		if !set[k] {
			t.Errorf("L0/L1 ToolKind %q should be auto-approved by default", k)
		}
	}
	// 这条是整套风险模型的**核心断言**：执行命令与破坏性操作永远不进免审名单。
	for _, k := range []string{"execute", "delete", "switch_mode", "other"} {
		if set[k] {
			t.Errorf("L2/L3 ToolKind %q must NEVER be auto-approved", k)
		}
	}
}

// TestSettings_AutoApproveToggles 两个开关各自独立生效，且**任何组合下**
// L2/L3 都不出现 —— 后者是硬边界，不随开关变化。
func TestSettings_AutoApproveToggles(t *testing.T) {
	cases := []struct {
		l0, l1 bool
		want   []string
	}{
		{false, false, nil},
		{true, false, riskLevelKinds[RiskL0]},
		{false, true, riskLevelKinds[RiskL1]},
	}
	for _, tc := range cases {
		s := Settings{AutoApproveL0: tc.l0, AutoApproveL1: tc.l1}
		got := append([]string(nil), s.AutoApproveTools()...)
		want := append([]string(nil), tc.want...)
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("l0=%v l1=%v → %v, want %v", tc.l0, tc.l1, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("l0=%v l1=%v → %v, want %v", tc.l0, tc.l1, got, want)
			}
		}
	}
}

// TestSettings_AutoApprovableLevels 分级自身声明了谁可配。
func TestSettings_AutoApprovableLevels(t *testing.T) {
	if !RiskL0.AutoApprovable() || !RiskL1.AutoApprovable() {
		t.Error("L0/L1 must be configurable")
	}
	if RiskL2.AutoApprovable() || RiskL3.AutoApprovable() {
		t.Error("L2/L3 must be locked (not configurable)")
	}
}

// TestSettings_RiskMatrixCoversAllLevels 四档都必须有 ToolKind 归属 ——
// 漏一档意味着某个 ToolKind 会**无人认领**，而无人认领在运行时表现为
// "不在免审名单里"，也就是"必须人工确认"，恰好是安全的一侧，但设计上仍是漏洞。
func TestSettings_RiskMatrixCoversAllLevels(t *testing.T) {
	m := RiskMatrix()
	for _, lv := range []RiskLevel{RiskL0, RiskL1, RiskL2, RiskL3} {
		if len(m[string(lv)]) == 0 {
			t.Errorf("risk level %s has no ToolKind mapping", lv)
		}
	}
}

// TestSettings_InDND 免打扰判定，含**跨零点**这个常态而非特例的区间。
func TestSettings_InDND(t *testing.T) {
	at := func(h, m int) time.Time {
		return time.Date(2026, 9, 11, h, m, 0, 0, time.Local)
	}
	night := Settings{DNDEnabled: true, DNDStart: "22:00", DNDEnd: "08:00"}
	if !night.InDND(at(23, 30)) {
		t.Error("23:30 should be inside 22:00-08:00")
	}
	if !night.InDND(at(3, 0)) {
		t.Error("03:00 should be inside a wrapping window")
	}
	if night.InDND(at(12, 0)) {
		t.Error("12:00 should be outside 22:00-08:00")
	}
	day := Settings{DNDEnabled: true, DNDStart: "09:00", DNDEnd: "18:00"}
	if !day.InDND(at(9, 0)) || !day.InDND(at(17, 59)) {
		t.Error("09:00-18:00 should include its start and exclude its end")
	}
	if day.InDND(at(18, 0)) {
		t.Error("end should be exclusive")
	}
	if day.InDND(at(8, 59)) {
		t.Error("8:59 should be outside 09:00-18:00")
	}

	off := Settings{DNDEnabled: false, DNDStart: "22:00", DNDEnd: "08:00"}
	if off.InDND(at(23, 0)) {
		t.Error("disabled dnd must never report in-window")
	}
	// 配置坏了当作没开：静默不推送的代价远大于多推一次。
	broken := Settings{DNDEnabled: true, DNDStart: "", DNDEnd: "08:00"}
	if broken.InDND(at(23, 0)) {
		t.Error("unparsable window must fail open (not in dnd)")
	}
	empty := Settings{DNDEnabled: true, DNDStart: "10:00", DNDEnd: "10:00"}
	if empty.InDND(at(10, 0)) {
		t.Error("start == end is an empty period, not a 24h one")
	}
}

// TestSettingsStore_PatchAndPersist 补丁语义 + 落盘往返。
func TestSettingsStore_PatchAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := NewSettingsStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if got := s.Get(); got != DefaultSettings() {
		t.Fatalf("fresh store should be defaults, got %+v", got)
	}

	off := false
	ret := 1000
	next, err := s.Patch(SettingsPatch{AutoApproveL1: &off, EventRetention: &ret})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if next.AutoApproveL1 {
		t.Error("AutoApproveL1 should be false after patch")
	}
	if !next.AutoApproveL0 {
		t.Error("unspecified field must keep its value")
	}
	if next.EventRetention != 1000 {
		t.Errorf("EventRetention = %d, want 1000", next.EventRetention)
	}

	// 重开：值应持久化
	reopened, err := NewSettingsStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Get(); got != next {
		t.Fatalf("persisted %+v, want %+v", got, next)
	}
}

// TestSettingsStore_PatchValidation 非法值被拒，且**不改内存态**。
func TestSettingsStore_PatchValidation(t *testing.T) {
	s, err := NewSettingsStore(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	before := s.Get()

	bad := "25:00"
	if _, err := s.Patch(SettingsPatch{DNDStart: &bad}); err == nil {
		t.Error("25:00 must be rejected")
	}
	neg := -1
	if _, err := s.Patch(SettingsPatch{EventRetention: &neg}); err == nil {
		t.Error("negative retention must be rejected")
	}
	// 开着免打扰但起止相同 = 一个永远不生效的时段。它不报错、只是安静地
	// 什么都不做 —— 正是最该拦下的那类配置。
	on, same := true, "10:00"
	if _, err := s.Patch(SettingsPatch{DNDEnabled: &on, DNDStart: &same, DNDEnd: &same}); err == nil {
		t.Error("empty dnd period must be rejected")
	}
	if got := s.Get(); got != before {
		t.Fatalf("failed patch must not mutate state: %+v, want %+v", got, before)
	}
}

// TestSettingsStore_OnChange 变更回调：成功才触发，校验失败不触发。
func TestSettingsStore_OnChange(t *testing.T) {
	s, err := NewSettingsStore(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	var seen []Settings
	s.OnChange(func(v Settings) { seen = append(seen, v) })

	off := false
	if _, err := s.Patch(SettingsPatch{AutoApproveL0: &off}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if len(seen) != 1 || seen[0].AutoApproveL0 {
		t.Fatalf("OnChange should fire once with the new value, got %+v", seen)
	}
	bad := "99:99"
	if _, err := s.Patch(SettingsPatch{DNDStart: &bad}); err == nil {
		t.Fatal("expected validation error")
	}
	if len(seen) != 1 {
		t.Fatalf("failed patch must not fire OnChange, got %d calls", len(seen))
	}
}

// TestSettingsStore_EmptyPathIsInMemory 空路径退化为纯内存（旧测试 / 未接线）。
func TestSettingsStore_EmptyPathIsInMemory(t *testing.T) {
	s, err := NewSettingsStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	off := false
	if _, err := s.Patch(SettingsPatch{AutoApproveL1: &off}); err != nil {
		t.Fatalf("patch without a path should succeed: %v", err)
	}
}

// TestRiskOfKind 风险分级反查：已知 kind 各归其档，未知一律 L2。
//
// "未知归 L2"这条是**本测试真正要守的东西**：分级表的作用是枚举已知操作，
// 若以后有人把兜底改成 L0，协议新增的任何 ToolKind 都会默默拿到一张弱强度的卡，
// 而没有任何一处会报错 —— 只能靠这条断言拦住。
func TestRiskOfKind(t *testing.T) {
	cases := []struct {
		kind string
		want model.RiskLevel
	}{
		{"read", model.RiskL0},
		{"search", model.RiskL0},
		{"edit", model.RiskL1},
		{"move", model.RiskL1},
		{"execute", model.RiskL2},
		{"other", model.RiskL2},
		{"delete", model.RiskL3},
		// 未知 / 空：保守侧
		{"", model.RiskL2},
		{"some_future_kind", model.RiskL2},
	}
	for _, c := range cases {
		if got := RiskOfKind(c.kind); got != c.want {
			t.Errorf("RiskOfKind(%q) = %s, want %s", c.kind, got, c.want)
		}
	}
}

// TestRiskOfKindMatchesAutoApprove 分级与自动放行必须共用同一张表。
//
// 这是「卡片强度不能说谎」的机器可验证形式：若某档被标为可自动放行，
// 那么属于该档的每一个 ToolKind 都得真的在免审名单里 —— 反之若一个 kind
// 被判为 L0/L1（卡片显示为弱强度），放行逻辑却拿它当 L2 拦下来，
// 用户看到的强度就是假的。
func TestRiskOfKindMatchesAutoApprove(t *testing.T) {
	// AutoApproveTools 由 riskLevelKinds 派生（见 Settings.AutoApproveTools），
	// 所以这里只需确认：可自动放行的档位恰好是 L0/L1，且它们的 kinds 不为空。
	for _, lvl := range []model.RiskLevel{model.RiskL0, model.RiskL1} {
		if !lvl.AutoApprovable() {
			t.Errorf("%s should be auto-approvable", lvl)
		}
		if len(riskLevelKinds[lvl]) == 0 {
			t.Errorf("%s has no ToolKinds mapped", lvl)
		}
		for _, k := range riskLevelKinds[lvl] {
			if got := RiskOfKind(k); got != lvl {
				t.Errorf("kind %q mapped to %s but RiskOfKind says %s", k, lvl, got)
			}
		}
	}
	for _, lvl := range []model.RiskLevel{model.RiskL2, model.RiskL3} {
		if lvl.AutoApprovable() {
			t.Errorf("%s must NOT be auto-approvable (hard lock)", lvl)
		}
	}
}
