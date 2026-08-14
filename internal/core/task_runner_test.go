package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/model"
	"go.uber.org/zap"
)

// TestCleanTitle 验证模型生成标题的清洗：去引号/前缀、折叠空白、限长。
func TestCleanTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"修复登录接口超时"`, "修复登录接口超时"},
		{"标题：优化查询性能", "优化查询性能"},
		{"  重构任务调度模块  ", "重构任务调度模块"},
		{"第一行\n第二行 有 多余空格", "第一行 第二行 有 多余空格"},
		{"这", "这"},
		{"", ""},
		// 模型没理解任务时的元回复：应被拒绝（返回空），不污染任务标题
		{"你的消息里没有附带要概括的开发任务内容，请把任务描述发给我", ""},
		{"请提供需要概括的任务内容", ""},
		{"我无法概括，请说明具体任务", ""},
	}
	for _, c := range cases {
		if got := cleanTitle(c.in); got != c.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTaskHasContent 验证"是否已有可见产出"的判断：output 或 text/tool 事件任一存在即为 true。
func TestTaskHasContent(t *testing.T) {
	userEv := func() model.TaskEvent { return model.TaskEvent{Type: model.EventUser, Text: "prompt"} }
	cases := []struct {
		name   string
		mutate func(*model.Task)
		want   bool
	}{
		{"nil task", func(t *model.Task) {}, false},
		{"only user event", func(t *model.Task) { t.Events = []model.TaskEvent{userEv()} }, false},
		{"has output", func(t *model.Task) { t.Output = "result" }, true},
		{"has text event", func(t *model.Task) {
			t.Events = []model.TaskEvent{userEv(), {Type: model.EventText, Text: "hi"}}
		}, true},
		{"has tool_use", func(t *model.Task) {
			t.Events = []model.TaskEvent{userEv(), {Type: model.EventToolUse, ToolName: "Bash"}}
		}, true},
		{"only thinking event", func(t *model.Task) {
			t.Events = []model.TaskEvent{userEv(), {Type: model.EventThinking, Text: "..."}}
		}, false},
	}
	for _, c := range cases {
		task := &model.Task{}
		c.mutate(task)
		if got := taskHasContent(task); got != c.want {
			t.Errorf("%s: taskHasContent = %v, want %v", c.name, got, c.want)
		}
	}
}

// newTestTaskRunner 构造一个不依赖真实 claude 的 runner（hooks 用真 HookService）。
func newTestTaskRunner(t *testing.T) (*TaskRunner, *TaskStore, *EventBus, *HookService) {
	t.Helper()
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "test-model", "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	return tr, store, bus, hooks
}

func TestTaskRunner_ParseStreamCompletes(t *testing.T) {
	tr, store, bus, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	// 喂一段模拟的 stream-json：assistant text + result success
	stream := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hello world","session_id":"s1","num_turns":1}`,
	}, "\n")

	sub := bus.Subscribe(8)
	tr.parseStream(task.ID, strings.NewReader(stream))

	// 等状态落到 completed
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && t2.Status == model.TaskCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s, want completed", got.Status)
	}
	if got.Output != "hello world" {
		t.Fatalf("output=%q", got.Output)
	}

	// 应收到 completed 事件（前面可能有 task_updated from appendOutput）
	gotCompleted := false
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !gotCompleted {
		select {
		case ev := <-sub.Chan():
			if ev.Type == "task_completed" {
				gotCompleted = true
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !gotCompleted {
		t.Fatal("no completed event")
	}
}

// TestTaskRunner_ParseStreamAccumulatesEvents 验证 text/tool_use/tool_result 都进 Events。
func TestTaskRunner_ParseStreamAccumulatesEvents(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})

	stream := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"看看文件"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"git log --oneline -3"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"c8d8a9c revert\n5e0950e feat"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"总结"}]}}`,
	}, "\n")
	tr.parseStream(task.ID, strings.NewReader(stream))

	// 等 appendEvent 落库
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && len(t2.Events) >= 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if len(got.Events) < 4 {
		t.Fatalf("events=%d, want >=4: %+v", len(got.Events), got.Events)
	}
	// 第1个 text
	if got.Events[0].Type != model.EventText || got.Events[0].Text != "看看文件" {
		t.Fatalf("events[0]=%+v", got.Events[0])
	}
	// tool_use
	tu := got.Events[1]
	if tu.Type != model.EventToolUse || tu.ToolName != "Bash" || tu.ToolUseID != "call_1" {
		t.Fatalf("events[1]=%+v", tu)
	}
	if !strings.Contains(string(tu.Input), "git log") {
		t.Fatalf("tool_use input=%s", string(tu.Input))
	}
	// tool_result 关联到 call_1,带结果文本
	tr3 := got.Events[2]
	if tr3.Type != model.EventToolResult || tr3.ToolUseID != "call_1" || tr3.ToolName != "Bash" {
		t.Fatalf("events[2]=%+v", tr3)
	}
	if !strings.Contains(tr3.Result, "c8d8a9c") {
		t.Fatalf("tool_result=%q", tr3.Result)
	}
	// 最后一个 text
	if got.Events[3].Type != model.EventText || got.Events[3].Text != "总结" {
		t.Fatalf("events[3]=%+v", got.Events[3])
	}
}

// TestTaskRunner_CapturesRealSessionID 验证 init 行报告的真实 session_id 会覆盖预生成的
// ClaudeSessionID，保证 --resume 用真实会话 id（修复坏运行下会话 id 不一致导致 "No conversation found"）。
func TestTaskRunner_CapturesRealSessionID(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})
	original := task.ClaudeSessionID
	if original == "" {
		t.Fatal("Create should assign ClaudeSessionID")
	}

	stream := `{"type":"system","subtype":"init","session_id":"real-sess-123","tools":["Read"],"model":"m"}`
	tr.parseStream(task.ID, strings.NewReader(stream))

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && t2.ClaudeSessionID == "real-sess-123" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if got.ClaudeSessionID != "real-sess-123" {
		t.Fatalf("ClaudeSessionID=%q, want real-sess-123 (pre-generated was %q)", got.ClaudeSessionID, original)
	}
}

func TestTaskRunner_ParseStreamResultErrorFails(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})

	stream := `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"too many turns"}`
	tr.parseStream(task.ID, strings.NewReader(stream))

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && t2.Status == model.TaskFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s, want failed", got.Status)
	}
	if got.Error == "" {
		t.Fatal("error message should be set")
	}
}

func TestTaskRunner_HookPendingSetsWaitingInput(t *testing.T) {
	tr, store, _, hooks := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})

	// 模拟 hook 触发（onPending 已在 NewTaskRunner 注入）
	resultCh := make(chan HookResult, 1)
	go func() {
		resultCh <- hooks.RegisterPending(HookPayload{
			TaskID: task.ID, ToolName: "Bash", ToolUseID: "call_1", Summary: "rm -rf x",
		})
	}()

	// 等 task 进 waiting_input
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && t2.Status == model.TaskWaitingInput {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskWaitingInput {
		t.Fatalf("status=%s, want waiting_input", got.Status)
	}
	if got.CurrentDecision == nil || got.CurrentDecision.ToolName != "Bash" {
		t.Fatalf("decision=%+v", got.CurrentDecision)
	}

	// Intervene approve -> hook 解除
	if err := tr.Intervene(task.ID, model.Intervention{
		Kind: "decision", DecisionID: "call_1", Choice: "approve",
	}); err != nil {
		t.Fatal(err)
	}
	r := <-resultCh
	if r.PermissionDecision != "allow" {
		t.Fatalf("decision=%s, want allow", r.PermissionDecision)
	}
}

func TestTaskRunner_CancelTerminalTask(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})
	// 手动置 running（不真跑 claude）
	_, _ = store.Update(task.ID, func(t *model.Task) bool { t.Status = model.TaskRunning; return true })
	// Cancel 需要一个 liveProc；构造假的
	tr.mu.Lock()
	tr.running[task.ID] = &liveProc{taskID: task.ID, done: make(chan struct{})}
	tr.mu.Unlock()

	// Cancel 会调 lp.cancel()，但没有真 cmd，cancel 是空 context.CancelFunc
	// 为避免 nil，注入一个 no-op cancel
	tr.running[task.ID].cancel = func() {}

	if err := tr.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCancelled {
		t.Fatalf("status=%s, want cancelled", got.Status)
	}
}

func TestTaskRunner_TransitionTerminalIgnored(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"})
	_, _ = store.Update(task.ID, func(t *model.Task) bool { t.Status = model.TaskCompleted; return true })

	// 已 completed，再 setRunning 应被忽略
	tr.setRunning(task.ID)
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s, completed should be terminal", got.Status)
	}
}

// TestTaskRunner_InjectHookSettings 验证 bypassPermissions + 注入参数齐备时，
// injectHookSettings 会往 worktree 写出含 PreToolUse hook 的 settings.json。
func TestTaskRunner_InjectHookSettings(t *testing.T) {
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks,
		"m", "", "bypassPermissions", false,
		"/path/to/pieqi", 39998, []string{"Bash", "Edit"}, 60, 0, "main")

	wt := t.TempDir() // 假 worktree，不真跑 git
	task, _ := store.Create(&model.Task{ProjectID: "cb", WorktreePath: wt})

	tr.injectHookSettings(task)

	data, err := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	var s settingsFile
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	if len(s.Hooks.PreToolUse) != 2 {
		t.Fatalf("got %d matchers, want 2", len(s.Hooks.PreToolUse))
	}
	// command 应含 task ID 与 port
	cmd := s.Hooks.PreToolUse[0].Hooks[0].Command
	if !strings.Contains(cmd, task.ID) || !strings.Contains(cmd, "39998") {
		t.Fatalf("hook command=%q missing task id or port", cmd)
	}
	if s.Hooks.PreToolUse[0].Hooks[0].Timeout != 60 {
		t.Fatalf("timeout=%d, want 60", s.Hooks.PreToolUse[0].Hooks[0].Timeout)
	}
}

// TestTaskRunner_InjectHookSettings_SkippedWhenNoExecPath 验证注入参数缺失时不写文件。
func TestTaskRunner_InjectHookSettings_SkippedWhenNoExecPath(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t) // execPath="", hookTools=nil
	wt := t.TempDir()
	task, _ := store.Create(&model.Task{ProjectID: "cb", WorktreePath: wt})

	tr.injectHookSettings(task)

	if _, err := os.Stat(filepath.Join(wt, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("settings.json should NOT be written when execPath empty")
	}
}

// TestTaskRunner_NotifyWaitingInput 验证 task 进 waiting_input 时触发 IM 回执回调，
// 且通知文本含决策摘要。
func TestTaskRunner_NotifyWaitingInput(t *testing.T) {
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks,
		"m", "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")

	task, _ := store.Create(&model.Task{
		ProjectID: "cb",
		OriginChannel:  "wechat",
		OriginChatID:   "chat-1",
		OriginIdentity: "u1",
	})

	var gotTask *model.Task
	var gotText string
	tr.SetNotifier(func(tk *model.Task, text string) {
		gotTask = tk
		gotText = text
	})

	tr.setWaitingInput(task.ID, "call_1", "Bash", "rm -rf node_modules")

	// 等 transition 落库 + notify 触发（同步调用，应已就绪）
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && gotTask == nil {
		time.Sleep(10 * time.Millisecond)
	}
	if gotTask == nil {
		t.Fatal("notify callback not invoked")
	}
	if gotTask.ID != task.ID {
		t.Fatalf("notify task id=%s, want %s", gotTask.ID, task.ID)
	}
	if !strings.Contains(gotText, "rm -rf node_modules") {
		t.Fatalf("notify text=%q missing summary", gotText)
	}
	if !strings.Contains(gotText, "决策") {
		t.Fatalf("notify text=%q missing 决策 keyword", gotText)
	}
}

// TestTaskRunner_NotifySkippedWhenNoOrigin 验证 HTTP/CLI 来源（无 OriginChannel）不触发通知。
func TestTaskRunner_NotifySkippedWhenNoOrigin(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb"}) // 无 OriginChannel

	called := false
	tr.SetNotifier(func(tk *model.Task, text string) { called = true })

	tr.setWaitingInput(task.ID, "call_1", "Bash", "ls")

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && !called {
		time.Sleep(10 * time.Millisecond)
	}
	if called {
		t.Fatal("notify should NOT fire when task has no OriginChannel")
	}
}

// TestTaskRunner_MaxConcurrentPerProject 验证同项目并发被信号量限流：
// maxConcurrent=2 时，3 个任务中第 3 个会阻塞，直到前某个 release。
func TestTaskRunner_MaxConcurrentPerProject(t *testing.T) {
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	// maxConcurrent=2
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks,
		"m", "", "bypassPermissions", false, "", 0, nil, 0, 2, "main")

	sem := tr.projectSem("proj-x")
	if sem == nil {
		t.Fatal("sem should not be nil when maxConcurrent=2")
	}
	sem.acquire()
	sem.acquire()

	// 两个槽已满，第三个 acquire 应阻塞。用 select 验证不立即返回
	done := make(chan struct{})
	go func() {
		sem.acquire()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("third acquire should block when slots full")
	case <-time.After(100 * time.Millisecond):
	}

	// release 一个，第三个应能拿到
	sem.release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("third acquire should succeed after a release")
	}
	sem.release()
	sem.release()
}

// TestTaskRunner_NoConcurrencyLimitWhenZero 验证 maxConcurrent<=0 时不限制（sem 为 nil）。
func TestTaskRunner_NoConcurrencyLimitWhenZero(t *testing.T) {
	tr, _, _, _ := newTestTaskRunner(t) // maxConcurrent=0
	if sem := tr.projectSem("any"); sem != nil {
		t.Fatalf("sem should be nil when maxConcurrent<=0, got %v", sem)
	}
}

// --- 路径 B：多选一交互（[CHOICE] 格式）---

// TestTaskRunner_ChoiceBlockPausesOnResult 验证 result success 时若最后 text 含 [CHOICE] 块，
// task 进 waiting_input 而非 completed，且 text event 被改写为只含正文。
func TestTaskRunner_ChoiceBlockPausesOnResult(t *testing.T) {
	// 路径 B 拦截已禁用（改纯文本方案拍板协议）：即使输出含 [CHOICE] 块，
	// maybePauseForChoice 直接返回 false，任务正常 completed，不进 waiting_input。
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	stream := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"先分析一下需求。\n\n[CHOICE]\nQ: 用哪种签名算法？\nA: HS256\nA: RS256\n[/CHOICE]"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1","num_turns":1}`,
	}, "\n")
	tr.parseStream(task.ID, strings.NewReader(stream))

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && t2.Status != model.TaskPending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s, want completed (path B disabled)", got.Status)
	}
	if got.CurrentDecision != nil {
		t.Fatalf("decision should be nil, got %+v", got.CurrentDecision)
	}
}

// TestTaskRunner_ChoiceBlockDisabledLogic 验证禁用前的原始拦截逻辑仍保留在
// maybePauseForChoiceDisabled 里（换真 Claude 后端可复活）。直接调未导出方法测试。
func TestTaskRunner_ChoiceBlockDisabledLogic(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb2", Prompt: "p"})

	// 喂一个 text 事件到 events，再调 maybePauseForChoiceDisabled
	tr.transition(task.ID, "", func(t *model.Task) {
		t.Events = append(t.Events, model.TaskEvent{
			Type: model.EventText, Seq: 1, At: time.Now(),
			Text: "先分析一下需求。\n\n[CHOICE]\nQ: 用哪种签名算法？\nA: HS256\nA: RS256\n[/CHOICE]",
		})
	})

	if !tr.maybePauseForChoiceDisabled(task.ID, "") {
		t.Fatal("maybePauseForChoiceDisabled should return true on [CHOICE] block")
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskWaitingInput {
		t.Fatalf("status=%s, want waiting_input", got.Status)
	}
	if got.CurrentDecision == nil || got.CurrentDecision.Kind != model.DecisionKindChoice {
		t.Fatalf("decision=%+v, want kind=choice", got.CurrentDecision)
	}
	if len(got.CurrentDecision.Options) != 2 ||
		got.CurrentDecision.Options[0] != "HS256" || got.CurrentDecision.Options[1] != "RS256" {
		t.Fatalf("options=%v", got.CurrentDecision.Options)
	}
	if got.CurrentDecision.Summary != "用哪种签名算法？" {
		t.Fatalf("summary=%q", got.CurrentDecision.Summary)
	}
	// 最后一个 text event 应被改写为只含正文（剥离 CHOICE 块）
	if len(got.Events) == 0 {
		t.Fatal("no events")
	}
	last := got.Events[len(got.Events)-1]
	if last.Type != model.EventText {
		t.Fatalf("last event type=%v, want text", last.Type)
	}
	if strings.Contains(last.Text, "[CHOICE]") {
		t.Fatalf("last text event still contains CHOICE block: %q", last.Text)
	}
	if !strings.Contains(last.Text, "先分析一下需求") {
		t.Fatalf("last text event lost leading text: %q", last.Text)
	}
}

// TestTaskRunner_NoChoiceBlockCompletes 验证无 [CHOICE] 块时正常 completed（降级路径）。
func TestTaskRunner_NoChoiceBlockCompletes(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	stream := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"直接回答，无需选择"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"直接回答","session_id":"s1","num_turns":1}`,
	}, "\n")
	tr.parseStream(task.ID, strings.NewReader(stream))

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && t2.Status == model.TaskCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s, want completed", got.Status)
	}
}

// TestTaskRunner_ChoiceInThinkingIgnored 验证 thinking 里的 [CHOICE] 格式不触发暂停。
func TestTaskRunner_ChoiceInThinkingIgnored(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	// thinking 块含 [CHOICE] 格式，但 text 块是普通回答
	stream := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"考虑用 [CHOICE]\nQ: ?\nA: a\nA: b\n[/CHOICE]"},{"type":"text","text":"我决定用 HS256"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"用 HS256","session_id":"s1","num_turns":1}`,
	}, "\n")
	tr.parseStream(task.ID, strings.NewReader(stream))

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := store.Get(task.ID); t2 != nil && t2.Status == model.TaskCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCompleted {
		t.Fatalf("status=%s, want completed (thinking CHOICE should be ignored)", got.Status)
	}
}

// TestTaskRunner_ResumeChoiceWaitingInput 验证路径 B 的 waiting_input 可被 Resume 续跑。
func TestTaskRunner_ResumeChoiceWaitingInput(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", WorktreePath: "/wt", Prompt: "p"})
	// 构造 choice waiting_input（不真跑 claude，手动置状态）
	_, _ = store.Update(task.ID, func(t *model.Task) bool {
		t.Status = model.TaskWaitingInput
		t.WorktreePath = "/wt"
		t.ClaudeSessionID = "sess-1"
		t.CurrentDecision = &model.Decision{
			ID: "c1", Kind: model.DecisionKindChoice,
			Summary: "选哪个？", Options: []string{"A", "B"},
		}
		return true
	})

	// Resume 应成功（进程不在 running map，choice kind 允许）
	if err := tr.Resume(task.ID, "A"); err != nil {
		t.Fatalf("Resume choice waiting_input failed: %v", err)
	}
}

// TestTaskRunner_ResumeApprovalWaitingInputRejected 验证路径 A 的 waiting_input 不可 Resume。
func TestTaskRunner_ResumeApprovalWaitingInputRejected(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})
	_, _ = store.Update(task.ID, func(t *model.Task) bool {
		t.Status = model.TaskWaitingInput
		t.WorktreePath = "/wt"
		t.ClaudeSessionID = "sess-1"
		t.CurrentDecision = &model.Decision{
			ID: "c1", Kind: model.DecisionKindApproval,
			Options: []string{"approve", "deny"},
		}
		return true
	})

	if err := tr.Resume(task.ID, "approve"); err == nil {
		t.Fatal("Resume approval waiting_input should fail")
	}
}

// TestTaskRunner_CancelChoiceWaitingInput 验证路径 B 的 waiting_input 可被 Cancel（进程已死）。
func TestTaskRunner_CancelChoiceWaitingInput(t *testing.T) {
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "cb", Prompt: "p"})
	_, _ = store.Update(task.ID, func(t *model.Task) bool {
		t.Status = model.TaskWaitingInput
		t.CurrentDecision = &model.Decision{
			ID: "c1", Kind: model.DecisionKindChoice,
			Summary: "?", Options: []string{"A", "B"},
		}
		return true
	})
	// 不在 running map（进程已死）

	if err := tr.Cancel(task.ID); err != nil {
		t.Fatalf("Cancel choice waiting_input failed: %v", err)
	}
	got, _ := store.Get(task.ID)
	if got.Status != model.TaskCancelled {
		t.Fatalf("status=%s, want cancelled", got.Status)
	}
}

// TestTaskRunner_NotifyWaitingInputChoice 验证 choice kind 的通知文案含选项列表。
func TestTaskRunner_NotifyWaitingInputChoice(t *testing.T) {
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks,
		"m", "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")

	task, _ := store.Create(&model.Task{
		ProjectID:     "cb",
		OriginChannel: "wechat",
		OriginChatID:  "chat-1",
	})

	var gotText string
	tr.SetNotifier(func(tk *model.Task, text string) { gotText = text })

	tr.setWaitingInputChoice(task.ID, "用哪种签名？", []string{"HS256", "RS256"})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && gotText == "" {
		time.Sleep(10 * time.Millisecond)
	}
	if gotText == "" {
		t.Fatal("notify not invoked")
	}
	if !strings.Contains(gotText, "选择") {
		t.Fatalf("notify text=%q missing 选择 keyword", gotText)
	}
	if !strings.Contains(gotText, "HS256") || !strings.Contains(gotText, "RS256") {
		t.Fatalf("notify text=%q missing options", gotText)
	}
}

// TestTaskStore_RestoreKeepsChoiceWaitingInput 验证重启后 choice waiting_input 保留、
// approval waiting_input 标 failed。
func TestTaskStore_RestoreKeepsChoiceWaitingInput(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewTaskStore(dir)

	// choice waiting_input：重启后应保留
	choiceTask, _ := s1.Create(&model.Task{ProjectID: "cb", Prompt: "choice"})
	_, _ = s1.Update(choiceTask.ID, func(t *model.Task) bool {
		t.Status = model.TaskWaitingInput
		t.WorktreePath = "/wt"
		t.ClaudeSessionID = "sess-1"
		t.CurrentDecision = &model.Decision{
			ID: "c1", Kind: model.DecisionKindChoice,
			Summary: "?", Options: []string{"A", "B"},
		}
		return true
	})

	// approval waiting_input（旧式，无 Kind）：重启后应标 failed
	approvalTask, _ := s1.Create(&model.Task{ProjectID: "cb", Prompt: "approval"})
	_, _ = s1.Update(approvalTask.ID, func(t *model.Task) bool {
		t.Status = model.TaskWaitingInput
		t.CurrentDecision = &model.Decision{ID: "d1", ToolName: "Bash"} // Kind 空
		return true
	})

	s2, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	choiceRestored, _ := s2.Get(choiceTask.ID)
	if choiceRestored.Status != model.TaskWaitingInput {
		t.Fatalf("choice waiting_input should survive restart, got %s", choiceRestored.Status)
	}
	approvalRestored, _ := s2.Get(approvalTask.ID)
	if approvalRestored.Status != model.TaskFailed {
		t.Fatalf("approval waiting_input should be failed after restart, got %s", approvalRestored.Status)
	}
}
