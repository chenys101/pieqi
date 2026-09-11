package lark

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// recordingAdapter 记录收到的消息条数（按 bot id 断言分发是否精确）。
type recorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recorder) hook() func(model.Message) {
	return func(m model.Message) {
		r.mu.Lock()
		r.msgs = append(r.msgs, m.BotID)
		r.mu.Unlock()
	}
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

// newMuxRouter 建一个已注册路由的 router + mux。
func newMuxRouter() (*gin.Engine, *WebhookMux) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	m := NewWebhookMux()
	m.Init(r)
	return r, m
}

func eventBody(eventType string) string {
	return `{"header":{"event_type":"` + eventType + `","token":"vt"},` +
		`"event":{"sender":{"sender_id":{"open_id":"ou_x"}},` +
		`"message":{"chat_id":"oc_1","content":"{\"text\":\"隧道\"}"}}}`
}

func post(t *testing.T, r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// 按 bot_id 分发：两个具名实例各收各的，且消息带正确的 BotID。
func TestWebhookMux_DispatchesByBotID(t *testing.T) {
	r, m := newMuxRouter()

	recA, recB := &recorder{}, &recorder{}
	a := New("app", "secret", "", "").WithBotID("bot_a")
	a.OnMessage(recA.hook())
	b := New("app", "secret", "", "").WithBotID("bot_b")
	b.OnMessage(recB.hook())
	m.Replace([]*Adapter{a, b})

	if w := post(t, r, "/webhook/lark/bot_a", eventBody("im.message.receive_v1")); w.Code != http.StatusOK {
		t.Fatalf("bot_a route: status=%d body=%s", w.Code, w.Body.String())
	}
	if recA.count() != 1 {
		t.Fatalf("bot_a should receive 1 message, got %d", recA.count())
	}
	if recB.count() != 0 {
		t.Fatalf("bot_b must not receive bot_a's message, got %d", recB.count())
	}
	if got := recA.msgs[0]; got != "bot_a" {
		t.Errorf("message BotID = %q, want bot_a", got)
	}

	if w := post(t, r, "/webhook/lark/bot_b", eventBody("im.message.receive_v1")); w.Code != http.StatusOK {
		t.Fatalf("bot_b route: status=%d", w.Code)
	}
	if recB.count() != 1 {
		t.Fatalf("bot_b should receive 1 message, got %d", recB.count())
	}
}

// 未注册的 bot → 404（而不是被别的实例吞掉）。
func TestWebhookMux_UnknownBot404(t *testing.T) {
	r, m := newMuxRouter()
	rec := &recorder{}
	a := New("app", "secret", "", "").WithBotID("bot_a")
	a.OnMessage(rec.hook())
	m.Replace([]*Adapter{a})

	w := post(t, r, "/webhook/lark/bot_ghost", eventBody("im.message.receive_v1"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown bot: status=%d want 404", w.Code)
	}
	if rec.count() != 0 {
		t.Fatal("unknown bot must not be routed to another instance")
	}
}

// 渠道级实例（BotID 空）走无参路径。
func TestWebhookMux_LegacyPath(t *testing.T) {
	r, m := newMuxRouter()
	rec := &recorder{}
	a := New("app", "secret", "", "")
	a.OnMessage(rec.hook())
	m.Replace([]*Adapter{a})

	if w := post(t, r, "/webhook/lark", eventBody("im.message.receive_v1")); w.Code != http.StatusOK {
		t.Fatalf("legacy route: status=%d body=%s", w.Code, w.Body.String())
	}
	if rec.count() != 1 {
		t.Fatalf("legacy instance should receive 1 message, got %d", rec.count())
	}
}

// 长连接实例不受理 HTTP 回调 —— 路由常驻后这条判定是必要的，
// 否则 longconn 部署会凭空多出一个可被伪造的入口。
func TestWebhookMux_LongConnInstanceRejectsHTTP(t *testing.T) {
	r, m := newMuxRouter()
	rec := &recorder{}
	a := NewLongConn("app", "secret").WithBotID("bot_a")
	a.OnMessage(rec.hook())
	m.Replace([]*Adapter{a})

	w := post(t, r, "/webhook/lark/bot_a", eventBody("im.message.receive_v1"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("longconn instance: status=%d want 404", w.Code)
	}
	if rec.count() != 0 {
		t.Fatal("longconn instance must not accept HTTP webhook events")
	}
}

// Replace 是整体替换：被移除的实例立即失效。
func TestWebhookMux_ReplaceDropsOldInstances(t *testing.T) {
	r, m := newMuxRouter()
	a := New("app", "secret", "", "").WithBotID("bot_a")
	m.Replace([]*Adapter{a})
	if _, ok := m.Lookup("bot_a"); !ok {
		t.Fatal("bot_a should be registered")
	}
	m.Replace(nil)
	if _, ok := m.Lookup("bot_a"); ok {
		t.Fatal("Replace(nil) must clear all instances")
	}
	if w := post(t, r, "/webhook/lark/bot_a", eventBody("im.message.receive_v1")); w.Code != http.StatusNotFound {
		t.Fatalf("cleared instance: status=%d want 404", w.Code)
	}
}

// verify_token 配置了就必须匹配（路由常驻后不能再无条件信任回调来源）。
func TestWebhook_VerifyTokenEnforced(t *testing.T) {
	r, m := newMuxRouter()
	rec := &recorder{}
	a := New("app", "secret", "vt", "").WithBotID("bot_a")
	a.OnMessage(rec.hook())
	m.Replace([]*Adapter{a})

	bad := `{"header":{"event_type":"im.message.receive_v1","token":"wrong"},` +
		`"event":{"sender":{"sender_id":{"open_id":"ou_x"}},"message":{"chat_id":"oc_1","content":"{}"}}}`
	if w := post(t, r, "/webhook/lark/bot_a", bad); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status=%d want 401", w.Code)
	}
	if rec.count() != 0 {
		t.Fatal("wrong token must not be delivered")
	}

	if w := post(t, r, "/webhook/lark/bot_a", eventBody("im.message.receive_v1")); w.Code != http.StatusOK {
		t.Fatalf("correct token: status=%d body=%s", w.Code, w.Body.String())
	}
	if rec.count() != 1 {
		t.Fatalf("correct token should deliver, got %d", rec.count())
	}
}

// verify_token 未配置时保持向后兼容（不校验）。
func TestWebhook_EmptyVerifyTokenSkipsCheck(t *testing.T) {
	a := New("app", "secret", "", "").WithBotID("bot_a")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/webhook/lark/:bot_id", a.handleWebhook)

	body, _ := json.Marshal(map[string]any{
		"header": map[string]any{"event_type": "im.message.receive_v1", "token": "anything"},
	})
	if w := post(t, r, "/webhook/lark/bot_a", string(body)); w.Code != http.StatusOK {
		t.Fatalf("empty verify token must skip the check: status=%d", w.Code)
	}
}
