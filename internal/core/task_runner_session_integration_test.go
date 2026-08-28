//go:build integration

// 全链路集成：TaskRunner.runACP → NewAgentSessionManager → sessionBackedAdapter →
// agent.Open("claude") → 真实 claude-sdk-bridge → 真实 claude CLI。
//
// 这是 #2（sdk-bridge 默认驱动）的决定性验证：验证默认驱动在真实任务上的完整闭环——
// 文本产出、SDK resume id 持久化、冷续问（原桥会话关闭后凭 resume id 重建上下文）、
// 权限挂起 → 审批 → 工具执行。
//
//	go test -tags integration ./internal/core/ -run SessionManager -v
//
// 依赖：node、claude CLI、~/.claude 已登录、代理可达。
package core

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/agent/claude"
	"pieqi/internal/agent/claude/bridge"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

func taskByID(t *testing.T, store *TaskStore, id string) *model.Task {
	t.Helper()
	tk, ok := store.Get(id)
	if !ok || tk == nil {
		t.Fatalf("task %s not found", id)
	}
	return tk
}

func waitTaskStatus(t *testing.T, store *TaskStore, id string, status model.TaskStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tk, ok := store.Get(id); ok && tk != nil && tk.Status == status {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("task %s not in status %s within %s (last=%v)", id, status, timeout, func() model.TaskStatus {
		if tk, ok := store.Get(id); ok {
			return tk.Status
		}
		return ""
	}())
}

// waitTaskContains 轮询直到 task.Output 包含 substr（或任务进入 failed）。
// 续问时 task 首轮已是终态，waitTaskStatus(completed) 会立即返回，须等输出真正落地。
// 期间出现 waiting_input(approval) 自动 approve（桥 ask:["*"] 对模型偶发工具调用的审批）。
func waitTaskContains(t *testing.T, store *TaskStore, tr *TaskRunner, id, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tk, ok := store.Get(id); ok && tk != nil {
			if strings.Contains(tk.Output, substr) {
				return
			}
			if tk.Status == model.TaskFailed {
				t.Fatalf("task %s failed while waiting for output %q: %s", id, substr, tk.Error)
			}
			if tk.Status == model.TaskWaitingInput && tk.CurrentDecision != nil &&
				tk.CurrentDecision.Kind == model.DecisionKindApproval {
				if err := tr.Intervene(id, model.Intervention{Kind: "decision", DecisionID: tk.CurrentDecision.ID, Choice: "approve"}); err != nil {
					t.Fatalf("auto approve: %v", err)
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tk, ok := store.Get(id); ok && tk != nil {
		t.Fatalf("task %s output %q not seen within %s (output=%q status=%s)", id, substr, timeout, tk.Output, tk.Status)
	}
	t.Fatalf("task %s not found while waiting for output %q", id, substr)
}

// waitTaskDoneAutoApprove 等 task 到终态（completed/failed）；期间出现 waiting_input(approval)
// 一律自动 approve（桥的 ask:["*"] 会对模型偶发的工具调用触发审批，非测试关注点时自动放行）。
// 返回终态 task。
func waitTaskDoneAutoApprove(t *testing.T, store *TaskStore, tr *TaskRunner, id string, timeout time.Duration) *model.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tk, ok := store.Get(id)
		if !ok || tk == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if tk.Status == model.TaskCompleted || tk.Status == model.TaskFailed {
			return tk
		}
		if tk.Status == model.TaskWaitingInput && tk.CurrentDecision != nil &&
			tk.CurrentDecision.Kind == model.DecisionKindApproval {
			if err := tr.Intervene(id, model.Intervention{Kind: "decision", DecisionID: tk.CurrentDecision.ID, Choice: "approve"}); err != nil {
				t.Fatalf("auto approve: %v", err)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tk, ok := store.Get(id); ok && tk != nil {
		t.Fatalf("task %s not terminal within %s (status=%s)", id, timeout, tk.Status)
	}
	t.Fatalf("task %s not found while waiting for terminal", id)
	return nil
}

// startSessionManagerBridge 起真实 bridge，返回 baseURL。
func startSessionManagerBridge(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bridgeSrc := filepath.Join(repoRoot, "services", "claude-sdk-bridge", "src", "index.js")
	if _, err := os.Stat(bridgeSrc); err != nil {
		t.Skipf("bridge not found: %v", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}

	cmd := exec.Command("node", bridgeSrc)
	cmd.Env = append(os.Environ(), "BRIDGE_PORT=0", "BRIDGE_HOST=127.0.0.1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// 注意：用 t.Cleanup 而非 defer —— defer 会在本 helper 返回时立即执行（杀桥），
	// 调用方拿到的 baseURL 指向已死的桥。Cleanup 在测试结束才执行，桥存活到测试完毕。
	t.Cleanup(func() { time.Sleep(3 * time.Second) }) // 释放 claude.exe cwd 句柄（Windows TempDir 清理）
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	baseURLCh := make(chan string, 1)
	go func() {
		re := regexp.MustCompile(`listening on http://[^:]+:(\d+)`)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				baseURLCh <- "http://127.0.0.1:" + m[1]
				return
			}
		}
	}()
	select {
	case u := <-baseURLCh:
		return u
	case <-time.After(20 * time.Second):
		t.Fatal("bridge did not report listening port")
		return ""
	}
}

func TestRunACPSessionManagerIntegration(t *testing.T) {
	baseURL := startSessionManagerBridge(t)
	dev, _ := zap.NewDevelopment()
	defer dev.Sync()
	claude.Configure(claude.Config{BaseURL: baseURL, Logger: dev})
	defer claude.Configure(claude.Config{BaseURL: bridge.DefaultBaseURL})

	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(30 * time.Minute)
	tr := NewTaskRunner(dev, store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	mgr := agent.NewAgentSessionManager("claude", agent.ManagerConfig{MaxConcurrent: 1}, zap.NewNop())
	tr.SetAgentManager(mgr, true, 0)
	// 测试结束前兜底关所有会话（杀 claude 子进程），避免孤儿 claude 持 wt 句柄导致 TempDir 清理失败。
	t.Cleanup(func() {
		if mgr != nil {
			_ = mgr.CloseAll()
		}
		time.Sleep(2 * time.Second) // 给桥 DELETE 杀子进程的时间
	})

	// 1. 首轮：记词。经 session manager → 桥 → claude。
	wt := t.TempDir()
	t1, err := store.Create(&model.Task{
		ProjectID: "proj-sess", ProjectPath: wt, WorktreePath: wt,
		Prompt: "记住一个词 banana，然后回复'记住了'。",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tr.Start(context.Background(), t1)
	waitTaskDoneAutoApprove(t, store, tr, t1.ID, 120*time.Second)

	// 首轮后 SDK resume id 必须已持久化到 ACPSessionID（runACPTurn.refreshResumeID）
	tk1 := taskByID(t, store, t1.ID)
	resumeID := tk1.ACPSessionID
	if resumeID == "" {
		t.Fatal("ACPSessionID not persisted after turn 1 (SDK resume id expected)")
	}
	if a := mgr.Adapter(t1.ID); a != nil {
		if r, ok := a.(interface{ ResumeID() string }); ok {
			t.Logf("turn1 adapter ResumeID=%q, persisted ACPSessionID=%q", r.ResumeID(), resumeID)
			if r.ResumeID() != resumeID {
				t.Fatalf("persisted resume id != adapter resume id (refreshResumeID bug)")
			}
		}
	}
	t.Logf("turn1 OK, resumeID=%s", resumeID)

	// 2. 冷续问：关掉原桥会话（模拟 reaper/重启回收），凭 resume id 重建上下文。
	_ = mgr.Close(t1.ID) // 释放并发槽 + 杀 t1 首轮 claude 子进程
	if err := tr.Resume(t1.ID, "刚才让你记住的词是什么？直接回复那个词。"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// t1 首轮已是 completed，waitTaskStatus(completed) 会立即返回——须等 turn2 的输出真正落地。
	waitTaskContains(t, store, tr, t1.ID, "banana", 120*time.Second)
	t.Logf("turn2 OK, cold resume reconstructs context")

	// turn2 的会话跨轮保活仍占并发槽（MaxConcurrent=1）：关掉释放给 turn3。
	_ = mgr.Close(t1.ID)

	// 3. 权限闭环：新任务跑 shell → waiting_input → 审批 → 工具执行 → 完成。
	t3, err := store.Create(&model.Task{
		ProjectID: "proj-sess", ProjectPath: wt, WorktreePath: wt,
		Prompt: "运行 shell 命令 `echo SPIKE_OK` 并把输出告诉我。",
	})
	if err != nil {
		t.Fatalf("Create turn3: %v", err)
	}
	tr.Start(context.Background(), t3)
	waitTaskStatus(t, store, t3.ID, model.TaskWaitingInput, 90*time.Second)
	dec := taskByID(t, store, t3.ID).CurrentDecision
	if dec == nil || dec.Kind != model.DecisionKindApproval {
		t.Fatalf("expected approval decision, got %+v", dec)
	}
	t.Logf("permission_needed OK: tool=%s decision=%s", dec.ToolName, dec.ID)
	if err := tr.Intervene(t3.ID, model.Intervention{Kind: "decision", DecisionID: dec.ID, Choice: "approve"}); err != nil {
		t.Fatalf("Intervene approve: %v", err)
	}
	waitTaskStatus(t, store, t3.ID, model.TaskCompleted, 120*time.Second)
	out3 := taskByID(t, store, t3.ID).Output
	if !strings.Contains(out3, "SPIKE_OK") {
		t.Fatalf("tool not executed after approval: output=%q", out3)
	}
	t.Logf("turn3 OK, permission flow through session path works")
	// 关 t3 会话（杀 claude 子进程），避免孤儿 claude 持 wt 句柄导致 TempDir 清理失败。
	_ = mgr.Close(t3.ID)
	t.Log("SESSION MANAGER INTEGRATION PASS")
}
