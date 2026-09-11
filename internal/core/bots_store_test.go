package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"pieqi/internal/model"
)

func newTestBotStore(t *testing.T) (*BotStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bots")
	s, err := NewBotStore(dir)
	if err != nil {
		t.Fatalf("NewBotStore: %v", err)
	}
	return s, dir
}

// 空目录 = 还没有机器人，是合法状态（不是错误）。
func TestBotStore_EmptyIsValid(t *testing.T) {
	s, _ := newTestBotStore(t)
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List() = %d bots, want 0", len(got))
	}
	if _, ok := s.AdminBot(); ok {
		t.Fatal("AdminBot() ok = true on empty store, want false")
	}
	if id := s.AdminBotID(); id != "" {
		t.Fatalf("AdminBotID() = %q, want empty", id)
	}
}

// 首个绑定自动成为管理员 —— 这是规则，不是配置项。
func TestBotStore_FirstBotBecomesAdmin(t *testing.T) {
	s, _ := newTestBotStore(t)

	first, err := s.Create(model.Bot{Channel: model.ChannelLark})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if first.Role != model.BotRoleAdmin {
		t.Fatalf("first bot role = %q, want admin", first.Role)
	}
	if first.Name != "飞书 · 管理员" {
		t.Fatalf("first bot name = %q, want 飞书 · 管理员", first.Name)
	}
	if first.ID == "" {
		t.Fatal("first bot ID is empty")
	}
	if first.CreatedAt.IsZero() {
		t.Fatal("first bot CreatedAt is zero")
	}

	second, err := s.Create(model.Bot{Channel: model.ChannelLark})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if second.Role != model.BotRoleMember {
		t.Fatalf("second bot role = %q, want member", second.Role)
	}
	if second.Name != "飞书 · 机器人" {
		t.Fatalf("second bot name = %q, want 飞书 · 机器人", second.Name)
	}
	if second.ID == first.ID {
		t.Fatal("bot IDs must be unique")
	}

	if a, ok := s.AdminBot(); !ok || a.ID != first.ID {
		t.Fatalf("AdminBot() = %+v ok=%v, want first bot %s", a, ok, first.ID)
	}
	if id := s.AdminBotID(); id != first.ID {
		t.Fatalf("AdminBotID() = %q, want %q", id, first.ID)
	}
}

// 至多一台管理员：显式要求第二台为 admin 必须失败（否则不变量被破坏）。
func TestBotStore_AtMostOneAdmin(t *testing.T) {
	s, _ := newTestBotStore(t)
	if _, err := s.Create(model.Bot{Channel: model.ChannelLark}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := s.Create(model.Bot{Channel: model.ChannelWeCom, Role: model.BotRoleAdmin}); err == nil {
		t.Fatal("Create second admin: err = nil, want error")
	}
	// 失败后不应留下残留记录
	if got := s.List(); len(got) != 1 {
		t.Fatalf("List() = %d bots after failed create, want 1", len(got))
	}
}

func TestBotStore_CreateValidations(t *testing.T) {
	s, _ := newTestBotStore(t)
	if _, err := s.Create(model.Bot{}); err == nil {
		t.Fatal("Create with empty channel: err = nil, want error")
	}
	if _, err := s.Create(model.Bot{Channel: model.ChannelLark, Role: "root"}); err == nil {
		t.Fatal("Create with invalid role: err = nil, want error")
	}
}

// 列表按创建时间升序（原型按创建顺序展示）。
func TestBotStore_ListSortedByCreatedAt(t *testing.T) {
	s, _ := newTestBotStore(t)
	base := time.Now()
	// 故意逆序写入，且第一台不能是 admin（避免触发"至多一台"）
	for _, in := range []model.Bot{
		{ID: "c", Channel: model.ChannelLark, CreatedAt: base.Add(2 * time.Minute)},
		{ID: "a", Channel: model.ChannelLark, CreatedAt: base},
		{ID: "b", Channel: model.ChannelLark, CreatedAt: base.Add(time.Minute)},
	} {
		if _, err := s.Create(in); err != nil {
			t.Fatalf("Create %s: %v", in.ID, err)
		}
	}
	got := s.List()
	want := []string{"a", "b", "c"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("List()[%d].ID = %q, want %q (order=%v)", i, got[i].ID, id, ids(got))
		}
	}
}

func TestBotStore_Update(t *testing.T) {
	s, _ := newTestBotStore(t)
	bot, err := s.Create(model.Bot{Channel: model.ChannelLark})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 补丁语义：只改传了的字段
	name := "飞书 · 我的机器人"
	prompt := "只改我让你改的"
	updated, err := s.Update(bot.ID, BotPatch{Name: &name, SysPrompt: &prompt})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != name || updated.SysPrompt != prompt {
		t.Fatalf("Update result = %+v, want name=%q prompt=%q", updated, name, prompt)
	}
	if updated.Role != model.BotRoleAdmin {
		t.Fatalf("Update changed role to %q, want untouched admin", updated.Role)
	}

	// 未知 id
	if _, err := s.Update("nope", BotPatch{Name: &name}); err == nil {
		t.Fatal("Update unknown id: err = nil, want error")
	}
}

// 不含 admin 时，可以把某台提升为 admin。
func TestBotStore_UpdatePromoteToAdmin(t *testing.T) {
	s, _ := newTestBotStore(t)
	first, _ := s.Create(model.Bot{Channel: model.ChannelLark})  // admin
	second, _ := s.Create(model.Bot{Channel: model.ChannelLark}) // member

	admin := model.BotRoleAdmin
	member := model.BotRoleMember

	// 已有 admin → 提升另一台必须失败
	if _, err := s.Update(second.ID, BotPatch{Role: &admin}); err == nil {
		t.Fatal("promote while an admin exists: err = nil, want error")
	}
	// 先降级原 admin，再提升第二台 → 成功（这就是"管理员转移"的最小可行形态）
	if _, err := s.Update(first.ID, BotPatch{Role: &member}); err != nil {
		t.Fatalf("demote first: %v", err)
	}
	if _, err := s.Update(second.ID, BotPatch{Role: &admin}); err != nil {
		t.Fatalf("promote second: %v", err)
	}
	if id := s.AdminBotID(); id != second.ID {
		t.Fatalf("AdminBotID() = %q, want %q", id, second.ID)
	}
}

func TestBotStore_Delete(t *testing.T) {
	s, dir := newTestBotStore(t)
	bot, _ := s.Create(model.Bot{Channel: model.ChannelLark})

	// 造一个凭据文件，验证删除会连带清掉
	creds := s.CredsPath(bot.ID)
	if err := os.WriteFile(creds, []byte("{}"), 0600); err != nil {
		t.Fatalf("seed creds: %v", err)
	}

	if err := s.Delete(bot.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s.Get(bot.ID); ok {
		t.Fatal("Get after Delete: ok = true, want false")
	}
	if _, err := os.Stat(creds); !os.IsNotExist(err) {
		t.Fatalf("creds file still exists after Delete: err=%v", err)
	}
	if err := s.Delete(bot.ID); err == nil {
		t.Fatal("Delete twice: err = nil, want error")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Fatalf("index.json missing after Delete: %v", err)
	}
}

// 落盘必须真的持久：重开一个 store 能读到同样的数据。
func TestBotStore_PersistenceRoundTrip(t *testing.T) {
	s, dir := newTestBotStore(t)
	if _, err := s.Create(model.Bot{
		Channel:   model.ChannelLark,
		Name:      "飞书 · 管理员",
		SysPrompt: "写测试",
		AppID:     "cli_xxx",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reopened, err := NewBotStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := reopened.List()
	if len(got) != 1 {
		t.Fatalf("reopened List() = %d, want 1", len(got))
	}
	if got[0].SysPrompt != "写测试" || got[0].AppID != "cli_xxx" {
		t.Fatalf("reopened bot = %+v, want sys_prompt=写测试 app_id=cli_xxx", got[0])
	}
	if got[0].Role != model.BotRoleAdmin {
		t.Fatalf("reopened role = %q, want admin", got[0].Role)
	}
}

// index 损坏时返回错误，而不是静默丢失全部绑定。
func TestBotStore_CorruptIndexErrors(t *testing.T) {
	s, dir := newTestBotStore(t)
	if _, err := s.Create(model.Bot{Channel: model.ChannelLark}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("{not json"), 0600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := NewBotStore(dir); err == nil {
		t.Fatal("NewBotStore on corrupt index: err = nil, want error")
	}
}

// CredsPath 指向 per-bot 文件，且与 index.json 不同名（避免互相覆盖）。
func TestBotStore_CredsPath(t *testing.T) {
	s, dir := newTestBotStore(t)
	bot, _ := s.Create(model.Bot{Channel: model.ChannelLark})
	want := filepath.Join(dir, bot.ID+".json")
	if got := s.CredsPath(bot.ID); got != want {
		t.Fatalf("CredsPath() = %q, want %q", got, want)
	}
	if s.CredsPath(bot.ID) == filepath.Join(dir, "index.json") {
		t.Fatal("CredsPath must not collide with index.json")
	}
}

func ids(bots []model.Bot) []string {
	out := make([]string, len(bots))
	for i, b := range bots {
		out[i] = b.ID
	}
	return out
}
