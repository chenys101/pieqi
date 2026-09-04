// preview_attach_test.go P1 Preview Attach（p1-design.md §10）测试：
// URL 拼接单测 + 端点集成（preview running + tunnel active 前置校验）。
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pieqi/internal/auth"
	"pieqi/internal/config"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestAttachPreviewURL(t *testing.T) {
	cases := []struct {
		name, full, taskID, want string
	}{
		{
			name: "带 token",
			full: "https://abc.trycloudflare.com?token=seCret123",
			taskID: "t1",
			want: "https://abc.trycloudflare.com/api/tasks/t1/preview/?token=seCret123",
		},
		{
			name: "无 token",
			full: "https://abc.trycloudflare.com",
			taskID: "t2",
			want: "https://abc.trycloudflare.com/api/tasks/t2/preview/",
		},
		{
			name: "空 URL",
			full: "",
			taskID: "t3",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := attachPreviewURL(tc.full, tc.taskID); got != tc.want {
				t.Fatalf("attachPreviewURL(%q) = %q, want %q", tc.full, got, tc.want)
			}
		})
	}
}

// setupAttachTest 建 Server（含 preview + tunnel）与带 preview 覆盖文件的 worktree。
// 覆盖命令用 node 一行 HTTP server 监听 $PORT（framework=node → env 注入 PORT）。
func setupAttachTest(t *testing.T) (*Server, *core.TaskStore, *gin.Engine, *model.Task) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, _ := core.NewTaskStore(t.TempDir())
	bus := core.NewEventBus()
	hooks := core.NewHookService(0)
	wm := core.NewWorktreeManager(zap.NewNop(), t.TempDir())
	runner := core.NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")

	// worktree：.pieqi/preview.json 覆盖 → node 单行 server
	wt := t.TempDir()
	override := `{"framework":"node","command":["node","-e","require('http').createServer((q,s)=>s.end('ok')).listen(process.env.PORT)"],"port":0,"cwd":""}`
	if err := os.MkdirAll(filepath.Join(wt, ".pieqi"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".pieqi", "preview.json"), []byte(override), 0644); err != nil {
		t.Fatal(err)
	}
	task, err := store.Create(&model.Task{ProjectID: "attach", ProjectPath: wt, WorktreePath: wt, Prompt: "preview"})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(&config.Config{}, store, runner, hooks, bus, nil, nil)
	srv.SetFeedback(core.NewFeedbackStore(zap.NewNop(), t.TempDir()), core.NewPreviewManager(zap.NewNop()))
	srv.SetCheckRunner(core.NewCheckRunner(zap.NewNop(), t.TempDir()))

	// 隧道：fake cloudflared（复用 tunnel_test 的桩）
	svc := newAuthSvcForTest(t)
	tm := auth.NewTunnelManager(auth.TunnelConfig{
		BinaryPath: fakeCFForAPITest(t),
		LocalURL:   "http://localhost:3000",
		Tokens:     svc.Tokens,
	})
	srv.tunnel = tm

	r := gin.New()
	srv.Register(r)
	return srv, store, r, task
}

// waitPreviewRunning 轮询 preview 状态直到 running（node 启动 ~百毫秒）。
func waitPreviewRunning(t *testing.T, srv *Server, task *model.Task) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if st := srv.preview.Status(task); st.State == core.PreviewRunning {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("preview not running within 15s")
}

func TestAPI_P1_PreviewAttach(t *testing.T) {
	srv, _, r, task := setupAttachTest(t)

	// 前置不足：preview 未启动 → 409
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks/"+task.ID+"/preview/attach", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("no preview → 409, got %d body=%s", w.Code, w.Body.String())
	}

	// 启动 preview（node server 监听 $PORT）
	if err := srv.preview.Start(task); err != nil {
		t.Fatalf("preview start: %v", err)
	}
	waitPreviewRunning(t, srv, task)

	// 隧道未启动 → 409
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/preview/attach", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("no tunnel → 409, got %d body=%s", w.Code, w.Body.String())
	}

	// 启动隧道 → 200 {url, qr}
	if _, err := srv.tunnel.Start(context.Background(), time.Minute); err != nil {
		t.Fatalf("tunnel start: %v", err)
	}
	defer srv.tunnel.Stop(context.Background())

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks/"+task.ID+"/preview/attach", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("attach → 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		URL string `json:"url"`
		QR  string `json:"qr"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	wantPrefix := "https://api-test.trycloudflare.com/api/tasks/" + task.ID + "/preview/?token="
	if len(resp.URL) <= len(wantPrefix) || resp.URL[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("url = %q, want prefix %q", resp.URL, wantPrefix)
	}
	if resp.QR == "" || len(resp.QR) < len("/api/tunnel/qrcode?text=") {
		t.Fatalf("qr = %q", resp.QR)
	}

	// 收尾：停 preview
	srv.preview.Stop(task.ID)
}
