package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/larkreg"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// newTestController 建一个 webhook 模式可用的控制器（不碰网络）。
// 用 webhook 而非 longconn：longconn 会真的去连飞书。
func newTestController(t *testing.T) (*larkChannelController, *core.BotStore, *core.Bridge, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	bots, err := core.NewBotStore(filepath.Join(dir, "bots"))
	if err != nil {
		t.Fatalf("NewBotStore: %v", err)
	}
	bridge := core.NewBridge(zap.NewNop())
	r := gin.New()
	c := newLarkChannelController(zap.NewNop(), bridge, r, bots)
	return c, bots, bridge, r
}

// writeCreds 给一台机器人落一份 webhook 模式凭据。
func writeCreds(t *testing.T, bots *core.BotStore, id string) {
	t.Helper()
	cfg := larkreg.ChannelConfig{AppID: "cli_" + id, AppSecret: "secret", EventMode: "webhook"}
	if err := larkreg.SaveConfig(bots.CredsPath(id), cfg); err != nil {
		t.Fatalf("SaveConfig(%s): %v", id, err)
	}
}

func postWebhook(r *gin.Engine, path string) *httptest.ResponseRecorder {
	body := `{"header":{"event_type":"im.message.receive_v1"},` +
		`"event":{"sender":{"sender_id":{"open_id":"ou_x"}},` +
		`"message":{"chat_id":"oc_1","content":"{}"}}}`
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// 每台机器人一个实例，且各自可被 /webhook/lark/:bot_id 命中。
func TestController_OneInstancePerBot(t *testing.T) {
	c, bots, _, r := newTestController(t)
	var ids []string
	for _, name := range []string{"A", "B"} {
		b, err := bots.Create(model.Bot{Channel: model.ChannelLark, Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		writeCreds(t, bots, b.ID)
		ids = append(ids, b.ID)
	}

	if err := c.Init(config.LarkConfig{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(c.instances) != 2 {
		t.Fatalf("want 2 instances, got %d", len(c.instances))
	}
	for _, id := range ids {
		inst, ok := c.instances[id]
		if !ok {
			t.Fatalf("bot %s has no instance", id)
		}
		if inst.adapter.BotID() != id {
			t.Errorf("adapter BotID = %q, want %q", inst.adapter.BotID(), id)
		}
		if w := postWebhook(r, "/webhook/lark/"+id); w.Code != http.StatusOK {
			t.Errorf("POST /webhook/lark/%s => %d, want 200", id, w.Code)
		}
	}
}

// 差量重建：配置没变的实例必须原地复用 —— 否则一次机器人重命名会让所有机器人掉线重连。
func TestController_RebuildReusesUnchangedInstance(t *testing.T) {
	c, bots, _, _ := newTestController(t)
	b, _ := bots.Create(model.Bot{Channel: model.ChannelLark, Name: "A"})
	writeCreds(t, bots, b.ID)
	if err := c.Init(config.LarkConfig{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	before := c.instances[b.ID]

	if err := c.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if after := c.instances[b.ID]; after != before {
		t.Fatal("unchanged instance must be reused (rebuilding it would drop the connection)")
	}
}

// 删除一台机器人后，它的实例与路由必须一起消失 ——
// 否则"已删"的机器人仍在收发消息，而它已经从列表里看不到了。
func TestController_RebuildAfterDeleteDropsInstance(t *testing.T) {
	c, bots, bridge, r := newTestController(t)
	keep, _ := bots.Create(model.Bot{Channel: model.ChannelLark, Name: "A"})
	drop, _ := bots.Create(model.Bot{Channel: model.ChannelLark, Name: "B"})
	writeCreds(t, bots, keep.ID)
	writeCreds(t, bots, drop.ID)
	if err := c.Init(config.LarkConfig{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, ok := c.mux.Lookup(drop.ID); !ok {
		t.Fatal("setup: bot B should be routed before delete")
	}

	if err := bots.Delete(drop.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := c.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if _, ok := c.instances[drop.ID]; ok {
		t.Fatal("deleted bot must not keep an instance")
	}
	if _, ok := c.mux.Lookup(drop.ID); ok {
		t.Fatal("deleted bot must not be routable")
	}
	if w := postWebhook(r, "/webhook/lark/"+drop.ID); w.Code != http.StatusNotFound {
		t.Errorf("deleted bot webhook => %d, want 404", w.Code)
	}
	if w := postWebhook(r, "/webhook/lark/"+keep.ID); w.Code != http.StatusOK {
		t.Errorf("surviving bot webhook => %d, want 200", w.Code)
	}
	// 回执路由表也要跟着收敛（删除的 bot 不再可寻址）。
	if _, ok := bridge.Sender("lark"); !ok {
		t.Fatal("surviving bot should still be reachable by channel name")
	}
}

// BotStore 为空 → 退回渠道级单实例（接入多机器人之前的既有行为）。
func TestController_FallsBackToChannelCredentials(t *testing.T) {
	c, _, _, r := newTestController(t)
	if err := c.Init(config.LarkConfig{AppID: "cli_x", AppSecret: "s", EventMode: "webhook"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	inst, ok := c.instances[""]
	if !ok {
		t.Fatalf("want a channel-level instance, got %d instances", len(c.instances))
	}
	if inst.adapter.BotID() != "" {
		t.Errorf("channel-level instance must have empty BotID, got %q", inst.adapter.BotID())
	}
	if w := postWebhook(r, "/webhook/lark"); w.Code != http.StatusOK {
		t.Errorf("legacy path => %d, want 200", w.Code)
	}
}

// 一台机器人有记录但没凭据 → 跳过它，不阻塞其余机器人。
func TestController_SkipsBotWithoutCredentials(t *testing.T) {
	c, bots, _, _ := newTestController(t)
	ok, _ := bots.Create(model.Bot{Channel: model.ChannelLark, Name: "ok"})
	_, _ = bots.Create(model.Bot{Channel: model.ChannelLark, Name: "nocrceds"})
	writeCreds(t, bots, ok.ID)

	if err := c.Init(config.LarkConfig{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(c.instances) != 1 {
		t.Fatalf("want only the credentialed bot to run, got %d instances", len(c.instances))
	}
	if _, found := c.instances[ok.ID]; !found {
		t.Fatal("the credentialed bot must still run")
	}
}

// 完全无凭据 → 不建实例，也不报错。
func TestController_NoCredentialsStartsNothing(t *testing.T) {
	c, _, _, _ := newTestController(t)
	if err := c.Init(config.LarkConfig{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(c.instances) != 0 {
		t.Fatalf("want 0 instances, got %d", len(c.instances))
	}
}

// 渠道级配置的 event_mode 会被机器人凭据继承（缺省时）。
func TestController_ModeInheritedFromChannel(t *testing.T) {
	c, bots, _, _ := newTestController(t)
	b, _ := bots.Create(model.Bot{Channel: model.ChannelLark, Name: "A"})
	// 凭据不写 event_mode → 应继承渠道级（此处留空 → 归一到 webhook）
	if err := larkreg.SaveConfig(bots.CredsPath(b.ID), larkreg.ChannelConfig{AppID: "cli_a", AppSecret: "s"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if err := c.Init(config.LarkConfig{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := c.instances[b.ID].mode; got != "webhook" {
		t.Errorf("mode = %q, want webhook", got)
	}
}
