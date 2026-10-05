package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/agent/claude/bridge"
	"pieqi/internal/config"
)

// fakeBridge 模拟 claude-sdk-bridge 的 HTTP/SSE 协议。
type fakeBridge struct {
	srv *httptest.Server
	mu  sync.Mutex

	sessions      map[string]*fakeSess
	autoComplete  time.Duration // /prompt 后多久自动补 turn_end（0=禁用自动完成）
	createCount   int
	createdCwd    string
	createdResume string
	lastPermRID   string
	lastPermAllow bool
	lastPermOptID string
	cancelCount   int
	closeCount    int
}

type fakeSess struct {
	clients map[http.ResponseWriter]bool
}

func newFakeBridge(t *testing.T) *fakeBridge {
	fb := &fakeBridge{sessions: map[string]*fakeSess{}, autoComplete: 20 * time.Millisecond}
	mux := http.NewServeMux()
	fb.srv = httptest.NewServer(mux)

	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true,"sessions":1}`)
	})

	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
			return
		}
		var req bridge.CreateSessionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		fb.mu.Lock()
		id := "fake-" + strconv.Itoa(fb.createCount)
		fb.createCount++
		fb.createdCwd = req.Cwd
		fb.createdResume = req.ResumeSdkSessionID
		fb.sessions[id] = &fakeSess{clients: map[http.ResponseWriter]bool{}}
		fb.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	})

	mux.HandleFunc("/v1/sessions/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
		parts := strings.Split(rest, "/")
		fb.mu.Lock()
		sess := fb.sessions[parts[0]]
		fb.mu.Unlock()
		if sess == nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "session not found"})
			return
		}
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		case r.Method == http.MethodPost && action == "prompt":
			var body struct {
				Text      string `json:"text"`
				ClientRef string `json:"clientRef"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
			// 异步补一轮（text_delta + turn_end），模拟桥的异步 turn 完成
			fb.mu.Lock()
			delay := fb.autoComplete
			fb.mu.Unlock()
			go func(ref string, delay time.Duration) {
				if delay <= 0 {
					return
				}
				time.Sleep(delay)
				fb.pushEvent(parts[0], "text_delta", map[string]any{"kind": "text_delta", "text": "hi", "turnSeq": 1})
				fb.pushEvent(parts[0], "turn_end", map[string]any{
					"kind": "turn_end", "clientRef": ref, "subtype": "success", "isError": false,
					"turn": map[string]any{"resumeId": "sdk-1", "costUsd": 0.1},
				})
			}(body.ClientRef, delay)

		case r.Method == http.MethodPost && action == "permissions" && len(parts) >= 3:
			var body struct {
				Allow    bool   `json:"allow"`
				OptionID string `json:"optionID"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			fb.mu.Lock()
			fb.lastPermRID = parts[2]
			fb.lastPermAllow = body.Allow
			fb.lastPermOptID = body.OptionID
			fb.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"resolved": "true"})

		case r.Method == http.MethodPost && action == "cancel":
			fb.mu.Lock()
			fb.cancelCount++
			fb.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})

		case r.Method == http.MethodGet && action == "events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			fb.mu.Lock()
			sess.clients[w] = true
			fb.mu.Unlock()
			<-r.Context().Done()
			fb.mu.Lock()
			delete(sess.clients, w)
			fb.mu.Unlock()

		case r.Method == http.MethodDelete && len(parts) == 1:
			fb.mu.Lock()
			fb.closeCount++
			delete(fb.sessions, parts[0])
			fb.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"closed": "true"})

		default:
			http.Error(w, `{"error":"no route"}`, http.StatusNotFound)
		}
	})

	return fb
}

// pushEvent 向该会话的所有 SSE 客户端写一帧。
func (fb *fakeBridge) pushEvent(id, kind string, data any) {
	fb.mu.Lock()
	sess := fb.sessions[id]
	var clients []http.ResponseWriter
	if sess != nil {
		for c := range sess.clients {
			clients = append(clients, c)
		}
	}
	fb.mu.Unlock()
	b, _ := json.Marshal(data)
	for _, c := range clients {
		fmt.Fprintf(c, "event: %s\ndata: %s\n\n", kind, b)
		if f, ok := c.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func (fb *fakeBridge) addSession(id string) {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	fb.sessions[id] = &fakeSess{clients: map[http.ResponseWriter]bool{}}
}

func (fb *fakeBridge) newSession(t *testing.T) *session {
	t.Helper()
	fb.addSession("fake-0")
	sess := newSession(bridge.NewClient(fb.srv.URL), "fake-0", "/tmp", nil)
	go sess.runEventLoop()
	// 注意：不用 t.Cleanup 关会话——测试里必须显式 defer sess.Close（在 defer srv.Close 之后声明，
	// 保证先关会话释放 SSE 长连接，再关服务器；否则 srv.Close 等挂起的 SSE 请求会死锁）。
	return sess
}

func hasEvent(events []agent.Event, kind agent.EventKind) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// TestSessionPermissionCarriesToolKind 桥发来的 permission_needed 必须**带上 toolKind**。
//
// 这条链路此前断在这里：桥发 toolKind:""（恒为空），Go 侧 permission_needed 分支
// 又根本没读该字段。后果是 pieqi 的 L0/L1 免审名单（按 ToolKind 匹配）永不命中 ——
// 连 Edit 都要人工点，而且 RiskOfKind("") 兜底成 L2，审批卡一律显示"执行命令"。
func TestSessionPermissionCarriesToolKind(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		kind     string
		rawInput map[string]any
		wantKind string
	}{
		{"Edit → edit(L1 免审)", "Edit", "edit", map[string]any{"file_path": "a.go"}, "edit"},
		{"Write → edit", "Write", "edit", map[string]any{"file_path": "a.go"}, "edit"},
		{"Delete → delete(L3)", "Delete", "delete", map[string]any{"file_path": "a.go"}, "delete"},
		{"Bash 写命令 → execute", "Bash", "execute", map[string]any{"command": "rm -rf build/"}, "execute"},
		// 只读降级（ADR-0008）：Bash 的只读形态降为 read(L0) 免审。
		{"Bash 只读命令 → read", "Bash", "execute", map[string]any{"command": "sed -n '1,35p' api.go"}, "read"},
		{"Bash 复合命令不降级", "Bash", "execute", map[string]any{"command": "cd x; rm -rf y"}, "execute"},
		{"Bash 无 command 字段不降级", "Bash", "execute", map[string]any{"foo": "bar"}, "execute"},
		// 未知工具：空 kind → RiskOfKind 兜底 L2（保守侧，不能猜成 read）
		{"未知工具 → 空 kind", "SomeNewTool", "", map[string]any{}, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fb := newFakeBridge(t)
			defer fb.srv.Close()
			fb.autoComplete = 0 // 手动推事件，不要自动补 turn_end
			sess := fb.newSession(t)
			defer sess.Close(context.Background())

			got := make(chan agent.Event, 8)
			sess.OnEvent(func(ev agent.Event) {
				if ev.Kind == agent.EventPermissionNeeded {
					got <- ev
				}
			})
			// Prompt 会阻塞等 turn_end（waitTurn），所以必须在 goroutine 里发；
			// 本测试只关心 permission_needed 的字段，不关心这一轮是否结束。
			go func() { _ = sess.Prompt(context.Background(), "hi") }()

			// 等 SSE 连接建立后再推事件，否则事件会丢在订阅之前。
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				fb.mu.Lock()
				n := len(fb.sessions[sess.ID()].clients)
				fb.mu.Unlock()
				if n > 0 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}

			fb.pushEvent(sess.ID(), "permission_needed", map[string]any{
				"kind": "permission_needed", "reqId": "r1", "toolName": c.toolName,
				"toolKind": c.kind, "toolUseID": "tu1", "rawInput": c.rawInput,
			})

			select {
			case ev := <-got:
				if ev.Permission.ToolKind != c.wantKind {
					t.Errorf("ToolKind = %q, want %q（丢了它免审名单永不命中）",
						ev.Permission.ToolKind, c.wantKind)
				}
				if ev.Permission.ReqID != "r1" {
					t.Errorf("ReqID = %q, want r1", ev.Permission.ReqID)
				}
				if ev.Permission.ToolTitle != c.toolName {
					t.Errorf("ToolTitle = %q, want %q", ev.Permission.ToolTitle, c.toolName)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no permission_needed event dispatched")
			}
		})
	}
}

func TestSessionPromptDispatchesAndBlocks(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	sess := fb.newSession(t)
	defer sess.Close(context.Background())

	var got []agent.Event
	sess.OnEvent(func(ev agent.Event) { got = append(got, ev) })

	if err := sess.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !hasEvent(got, agent.EventTextDelta) {
		t.Fatalf("no text_delta dispatched: %+v", got)
	}
	var te *agent.Event
	for i := range got {
		if got[i].Kind == agent.EventTurnEnd {
			te = &got[i]
		}
	}
	if te == nil || te.Turn == nil || te.Turn.ResumeID != "sdk-1" {
		t.Fatalf("turn_end missing resumeId: %+v", got)
	}
	if te.Turn.CostUSD != 0.1 {
		t.Fatalf("turn_end cost = %v, want 0.1", te.Turn.CostUSD)
	}
}

func TestSessionPromptTurnError(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	fb.mu.Lock()
	fb.autoComplete = time.Second // 延迟自动完成，让下面的 error 事件先到
	fb.mu.Unlock()
	sess := fb.newSession(t)
	defer sess.Close(context.Background())

	// 覆盖 /prompt 的异步回放：这里直接手动推一条 error turn_end（对应任意 clientRef 无法匹配，
	// 因此改测：桥推 error 事件 → Prompt 应因 fatal 返回）。
	// 先起一个正在等待的 Prompt，再推致命 error。
	errCh := make(chan error, 1)
	go func() {
		errCh <- sess.Prompt(context.Background(), "hi")
	}()
	time.Sleep(50 * time.Millisecond)
	fb.pushEvent("fake-0", "error", map[string]any{"kind": "error", "message": "Claude Code process exited"})
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "process exited") {
			t.Fatalf("Prompt err = %v, want process-exited fatal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prompt did not return after fatal error")
	}
}

func TestSessionRespondPermission(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	fb.addSession("fake-0")
	sess := newSession(bridge.NewClient(fb.srv.URL), "fake-0", "/tmp", nil)
	defer sess.Close(context.Background())

	if err := sess.RespondPermission(context.Background(), "rid1", true, "opt1"); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.lastPermRID != "rid1" || !fb.lastPermAllow || fb.lastPermOptID != "opt1" {
		t.Fatalf("permission not forwarded: rid=%s allow=%v opt=%s", fb.lastPermRID, fb.lastPermAllow, fb.lastPermOptID)
	}
}

func TestSessionCancel(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	fb.addSession("fake-0")
	sess := newSession(bridge.NewClient(fb.srv.URL), "fake-0", "/tmp", nil)
	defer sess.Close(context.Background())

	if err := sess.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.cancelCount != 1 {
		t.Fatalf("cancel calls = %d, want 1", fb.cancelCount)
	}
}

func TestSessionCloseIdempotent(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	fb.addSession("fake-0")
	sess := newSession(bridge.NewClient(fb.srv.URL), "fake-0", "/tmp", nil)
	go sess.runEventLoop()

	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close (idempotent): %v", err)
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.closeCount != 1 {
		t.Fatalf("close calls = %d, want 1 (idempotent)", fb.closeCount)
	}
	// 事件循环应已终止且未把 fatal 置为流关闭（会话主动 close 不是错误）
}

func TestFactoryOpenClaude(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	Configure(Config{BaseURL: fb.srv.URL})
	defer Configure(Config{BaseURL: bridge.DefaultBaseURL})

	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Open claude: %v", err)
	}
	defer sess.Close(context.Background())
	if sess.ID() == "" {
		t.Fatal("empty session id")
	}
	caps := sess.Caps()
	if !caps.MultiTurnPersistent || !caps.ResumeSupported || !caps.Streaming {
		t.Fatalf("bridge caps = %+v, want all true", caps)
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.createdCwd != "/tmp" {
		t.Fatalf("created cwd = %q, want /tmp", fb.createdCwd)
	}
}

func TestFactoryOpenResume(t *testing.T) {
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	Configure(Config{BaseURL: fb.srv.URL})
	defer Configure(Config{BaseURL: bridge.DefaultBaseURL})

	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: "/tmp", ResumeFrom: "sdk-123"})
	if err != nil {
		t.Fatalf("Open resume: %v", err)
	}
	defer sess.Close(context.Background())
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.createdResume != "sdk-123" {
		t.Fatalf("resume id = %q, want sdk-123", fb.createdResume)
	}
}

func TestFactoryPrintFallback(t *testing.T) {
	// 桥不可达：用一个已关闭的端口（连接拒绝，立即失败）
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadURL := "http://" + l.Addr().String()
	_ = l.Close()

	Configure(Config{BaseURL: deadURL})
	defer Configure(Config{BaseURL: bridge.DefaultBaseURL})

	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("expected print fallback session, got err: %v", err)
	}
	defer sess.Close(context.Background())
	if sess.Caps().MultiTurnPersistent {
		t.Fatal("print fallback should not claim multi-turn persistent")
	}
	if !sess.Caps().ResumeSupported {
		t.Fatal("print fallback should support resume")
	}
}

func TestFactoryTransportPrintSkipsBridge(t *testing.T) {
	// transport=print 时即使桥可达也不碰桥，直接走 claude -p 回退。
	fb := newFakeBridge(t)
	defer fb.srv.Close()
	Configure(Config{Transport: "print", BaseURL: fb.srv.URL})
	// 复位必须连 Transport 一起（defaultConfig 是包级全局，不复位会污染后续测试）
	defer Configure(Config{Transport: "sdk-bridge", BaseURL: bridge.DefaultBaseURL})

	sess, err := agent.Open(context.Background(), agent.OpenParams{Agent: "claude", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Open print transport: %v", err)
	}
	defer sess.Close(context.Background())
	if sess.Caps().MultiTurnPersistent {
		t.Fatal("print transport session should be print fallback (no multi-turn)")
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.createCount != 0 {
		t.Fatalf("print transport must not touch bridge, createCount = %d", fb.createCount)
	}
}

func TestConfigFromAgents(t *testing.T) {
	pc := ConfigFromAgents(config.AgentsConfig{
		Claude: config.AgentClaudeConfig{
			Transport: "print",
			Bridge:    config.ClaudeBridgeConfig{BaseURL: "http://127.0.0.1:19999", Token: "t0k3n"},
			Print: config.AgentPrintConfig{
				Command: "claude", PermissionMode: "default", Model: "opus", SysPrompt: "be brief",
			},
		},
	})
	if pc.Transport != "print" || pc.BaseURL != "http://127.0.0.1:19999" || pc.Token != "t0k3n" {
		t.Fatalf("ConfigFromAgents transport/baseurl/token wrong: %+v", pc)
	}
	if pc.Print.Binary != "claude" || pc.Print.PermissionMode != "default" || pc.Print.Model != "opus" || pc.Print.SysPrompt != "be brief" {
		t.Fatalf("ConfigFromAgents print wrong: %+v", pc.Print)
	}
}
