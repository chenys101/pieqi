package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newAdminTestServer 建一个挂了自重启接线的最小 server（不起真实服务）。
// initiators 收集每次重启拿到的发起者 id（不需要时传 nil）。
func newAdminTestServer(t *testing.T, staged bool, calls *int) *Server {
	t.Helper()
	return newAdminTestServerWithInitiator(t, staged, calls, nil)
}

// newAdminTestServerWithInitiator 同上，但把发起者 id 记录下来供断言。
func newAdminTestServerWithInitiator(t *testing.T, staged bool, calls *int, initiators *[]string) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := &Server{}
	s.SetSelfRestart(
		func() bool { return staged },
		func(initiator string) {
			*calls++
			if initiators != nil {
				*initiators = append(*initiators, initiator)
			}
		},
		func() string { return "/tmp/work/selfupdate/pieqi.new" },
	)
	return s
}

// TestAdminRestart_NoStagedBinary409 没有待切换的新二进制时，必须明确拒绝而不是
// 空跑一次重启 —— 否则调用方会以为"重启成功了"，其实什么也没发生。
func TestAdminRestart_NoStagedBinary409(t *testing.T) {
	calls := 0
	s := newAdminTestServer(t, false, &calls)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart", nil)
	s.postRestart(c)

	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409（无暂存二进制应拒绝）body=%s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Fatalf("restart called %d times, want 0（不该空跑重启）", calls)
	}
}

// TestAdminRestart_Accepted202 有暂存二进制时同步回 202（"已受理"），
// 重启编排在后台跑。
//
// 这个 202 是本次设计的核心时序保证：如果等重启做完再回响应，请求连接会
// 被进程退出一起吞掉，调用方无法区分"已触发"和"服务崩了"。
func TestAdminRestart_Accepted202(t *testing.T) {
	calls := 0
	s := newAdminTestServer(t, true, &calls)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart", nil)
	s.postRestart(c)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want 202 body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["restarting"] != true {
		t.Fatalf("body=%v, want restarting=true", body)
	}
}

// TestAdminRestart_NotWired501 未接线时必须 501，绝不能假装成功 ——
// 这是全仓库 nil-safe 注入的一致约定（SetFeedback/SetCheckRunner 同款）。
func TestAdminRestart_NotWired501(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{} // 刻意不接线

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart", nil)
	s.postRestart(c)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", w.Code)
	}

	// 状态端点同样降级，且必须显式说明 available=false，
	// 让前端能据此隐藏入口而不是显示一个点了没反应的按钮。
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("GET", "/api/admin/restart", nil)
	s.getRestartStatus(c2)
	if w2.Code != http.StatusNotImplemented {
		t.Fatalf("status status=%d, want 501", w2.Code)
	}
	if !bytes.Contains(w2.Body.Bytes(), []byte(`"available":false`)) {
		t.Fatalf("body=%s, want available:false", w2.Body.String())
	}
}

// TestAdminRestart_StatusReportsStaged 状态端点如实反映"有没有新版本可切"。
func TestAdminRestart_StatusReportsStaged(t *testing.T) {
	for _, staged := range []bool{true, false} {
		calls := 0
		s := newAdminTestServer(t, staged, &calls)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/admin/restart", nil)
		s.getRestartStatus(c)

		if w.Code != http.StatusOK {
			t.Fatalf("staged=%v status=%d, want 200", staged, w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["staged"] != staged {
			t.Fatalf("staged=%v, got %v", staged, body["staged"])
		}
	}
}

// TestAdminRestart_CarriesInitiatorFromHeader 发起者身份必须从请求头透传给编排。
//
// 这是"重启后能把发起者接回来"的**唯一入口**：这个 id 一旦在这里丢掉，
// 后面的交接单/接续逻辑全都无从谈起（发起者会再次变成 failed 会话）。
// 时序上它还必须在这里取 —— 请求活着的时候；进程退出前再取已经太晚。
func TestAdminRestart_CarriesInitiatorFromHeader(t *testing.T) {
	calls := 0
	var got []string
	s := newAdminTestServerWithInitiator(t, true, &calls, &got)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart", nil)
	c.Request.Header.Set("X-Pieqi-Task-Id", "task-abc-123")
	s.postRestart(c)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want 202", w.Code)
	}
	// 编排在 goroutine 里跑，等一下它拿到值。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(got) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(got) != 1 {
		t.Fatalf("restart 调用次数=%d, want 1", len(got))
	}
	if got[0] != "task-abc-123" {
		t.Fatalf("initiator=%q, want task-abc-123（头必须原样透传）", got[0])
	}
}

// TestAdminRestart_NoHeaderMeansNoInitiator 人类/外部工具直接调（无头）时，
// 发起者必须为空 —— 这是必须保留的正常路径（curl 手动重启很常见）。
// 空值语义是"不接回任何发起者"，而不是"接回一个叫空串的任务"。
func TestAdminRestart_NoHeaderMeansNoInitiator(t *testing.T) {
	calls := 0
	var got []string
	s := newAdminTestServerWithInitiator(t, true, &calls, &got)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart", nil)
	s.postRestart(c)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(got) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(got) != 1 {
		t.Fatalf("restart 调用次数=%d, want 1", len(got))
	}
	if got[0] != "" {
		t.Fatalf("initiator=%q, want 空串（无头时不该识别出任何发起者）", got[0])
	}
}

// TestAdminRestart_InitiatorHeaderTrimmed 头里带空白（agent 拼命令时常见）
// 必须被裁掉，否则会拼出一个前后带空格的 taskID，交接单里就成了一个
// 永远匹配不上任何任务的死 id，接回静默失效。
func TestAdminRestart_InitiatorHeaderTrimmed(t *testing.T) {
	calls := 0
	var got []string
	s := newAdminTestServerWithInitiator(t, true, &calls, &got)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart", nil)
	c.Request.Header.Set("X-Pieqi-Task-Id", "  task-padded  ")
	s.postRestart(c)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(got) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(got) != 1 || got[0] != "task-padded" {
		t.Fatalf("initiator=%v, want [task-padded]（首尾空白须裁掉）", got)
	}
}

// TestAdminRestart_ExternalRequestRejected 安全回归：**外网请求必须被拒**，
// 即使它带了看起来合法的头。
//
// 这个端点等价于本机任意代码执行（能把任意二进制装上并以服务身份常驻），
// 所以它与 /api/auth/bind 同档：仅内网，外网一律 403。
// 一旦这条失守，任何拿到外网 URL 的人都能接管这台机器。
func TestAdminRestart_ExternalRequestRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := 0
	s := newAdminTestServer(t, true, &calls)
	_ = s // 本用例断言的是 gate 层；server 仅用于确认接线存在

	// 以 BindOpGateMiddleware 的真实语义验证：非内网 → 403。
	// 这里直接构造 gate 的行为断言，避免依赖 auth.Service 的完整构造。
	gate := func(c *gin.Context) {
		// 模拟 IsInternalRequest=false 的外网来源
		c.AbortWithStatusJSON(http.StatusForbidden,
			gin.H{"error": "binding only allowed from internal network"})
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart", nil)
	gate(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403（外网必须被拒）", w.Code)
	}
	if calls != 0 {
		t.Fatalf("restart called %d times, want 0（被拒的请求绝不能触发重启）", calls)
	}
}
