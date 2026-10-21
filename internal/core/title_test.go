package core

import (
	"strings"
	"testing"
	"time"

	"pieqi/internal/model"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestTitleFromReply 取最后一行非空文本当标题，并过滤元回复。
//
// 背景：dsh / qoder 的标题轮常先复述任务再给标题，也可能整段拒绝
// （"用户希望我为这段文字生成标题…"）。这两种都必须被正确处置：
// 前者取末行，后者判为"无标题"（保留前端截断），绝不能把这串话写进任务列表。
func TestTitleFromReply(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"单行直接是标题", `"修复登录接口超时"`, "修复登录接口超时"},
		{"末行才是标题", "任务原文：多轮会话时优先加载最近一轮…\n会话列表末轮加载优化", "会话列表末轮加载优化"},
		{"末尾空行不影响", "排查会话卡住原因\n\n", "排查会话卡住原因"},
		{"带前缀", "标题：修复 manifest 重定向循环", "修复 manifest 重定向循环"},
		// 元回复：必须判空（否则任务列表出现一行"用户希望我…"）
		{"中文复述任务的思考", "用户希望我为这段文字生成一个标题，根据内容…\n", ""},
		{"英文拒绝", "I cannot summarize this because no task text was provided.", ""},
		{"空输入", "   \n  ", ""},
	}
	for _, c := range cases {
		if got := titleFromReply(c.in); got != c.want {
			t.Errorf("%s: titleFromReply(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestTitleFromClaudeStdout 跳掉 claude 的诊断行。
//
// 实测过：claude 2.1.248 在 stdout 里会先吐
// `[claude-code:unrecognized_model] {...}`。旧实现把整个 stdout 交给 cleanTitle，
// 于是任务标题会变成那串 JSON。
func TestTitleFromClaudeStdout(t *testing.T) {
	out := "[claude-code:unrecognized_model] {\"model\":\"deepseek-flash\",\"query_source\":\"sdk\"}\n" +
		`"修复会话标题不更新的问题"` + "\n"
	if got := titleFromClaudeStdout(out); got != "修复会话标题不更新的问题" {
		t.Fatalf("titleFromClaudeStdout = %q, want 修复会话标题不更新的问题", got)
	}
}

// TestClaudeStderrTail 必须把 401 这类真实原因带出来。
//
// 这是本次事故的核心回归：旧实现只报 "exit status 1"，排查时完全看不出是凭据失效。
func TestClaudeStderrTail(t *testing.T) {
	b := []byte("some noise\n" +
		"Failed to authenticate. API Error: 401 Authentication Fails, Your api key: ****45ee is invalid (request_id: abc)\n")
	got := claudeStderrTail(b)
	if !containsAll(got, "401", "Authentication Fails") {
		t.Fatalf("claudeStderrTail 丢掉了关键原因: %q", got)
	}
	if len([]rune(got)) > 241 {
		t.Fatalf("claudeStderrTail 未限长: %d runes", len([]rune(got)))
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// observedRunner 构造一个能把日志留下证据的 runner（WARN 及以上）。
func observedRunner(t *testing.T) (*TaskRunner, *TaskStore, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.WarnLevel)
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	tr := NewTaskRunner(zap.New(core), store, wm, NewEventBus(), NewHookService(5*time.Second),
		"", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	return tr, store, logs
}

// TestGenerateTitle_FailureIsNotSilent 【回归】标题生成失败必须留 WARN。
//
// 事故经过：标题只走 claude -p，凭据失效后每次都 exit 1，而失败只打 DEBUG 级日志
// （线上是 INFO）就 return —— 4 天内 10 个任务标题全空，日志里一条线索都没有。
func TestGenerateTitle_FailureIsNotSilent(t *testing.T) {
	tr, store, logs := observedRunner(t)
	// 没有 agent 会话路径 → 只剩 claude -p 兜底；用一个不存在的二进制保证它失败。
	tr.titleClaudeBinary = "pieqi-nonexistent-binary-for-title-test"
	task, err := store.Create(&model.Task{ProjectID: "p", Prompt: "修复标题不更新", ProjectPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	tr.GenerateTitleAsync(task.ID)
	waitFor(t, 5*time.Second, "标题失败日志", func() bool {
		return logs.FilterMessage("generate title failed; task keeps the heuristic title").Len() == 1
	})

	entry := logs.FilterMessage("generate title failed; task keeps the heuristic title").All()[0]
	if entry.Level != zapcore.WarnLevel {
		t.Fatalf("失败日志级别 = %v，want WARN（DEBUG 会被生产日志吞掉）", entry.Level)
	}
	if entry.ContextMap()["task"] != task.ID {
		t.Fatalf("失败日志没带任务 id: %v", entry.ContextMap())
	}
	// 标题保持为空 = 前端继续用截断，而不是写入一条假标题。
	got, _ := store.Get(task.ID)
	if got.Title != "" {
		t.Fatalf("失败后 Title = %q，want 空（保留前端启发式标题）", got.Title)
	}
}

// TestGenerateTitle_SucceedsOverridesHeuristic 兜底后端产出标题时落库并推事件。
func TestGenerateTitle_SucceedsOverridesHeuristic(t *testing.T) {
	tr, store, _ := observedRunner(t)
	bus := NewEventBus()
	tr.bus = bus
	sub := bus.Subscribe(64)
	defer bus.Unsubscribe(sub)
	var gotEvent bool

	dir := t.TempDir()
	task, _ := store.Create(&model.Task{ProjectID: "p", Prompt: "修复标题不更新", ProjectPath: dir})
	// 用 shell 冒充 claude：覆盖二进制即可（不依赖本机 claude 装没装、凭据有没有效）。
	tr.titleClaudeBinary = "pwsh"
	tr.titleClaudeArgs = []string{"-NoProfile", "-Command", "Write-Output '修复会话标题不更新'"}

	// 必须推 task_updated，否则前端要刷新才看得到新标题 —— 与落库一起等。
	tr.GenerateTitleAsync(task.ID)
	waitFor(t, 10*time.Second, "标题落库", func() bool {
		got, _ := store.Get(task.ID)
		return got != nil && got.Title != ""
	})
	got, _ := store.Get(task.ID)
	if got.Title != "修复会话标题不更新" {
		t.Fatalf("Title = %q", got.Title)
	}
	waitFor(t, 2*time.Second, "task_updated 事件", func() bool {
		for !gotEvent {
			select {
			case ev := <-sub.Chan():
				if ev.Type == "task_updated" && ev.Task != nil && ev.Task.Title == "修复会话标题不更新" {
					gotEvent = true
				}
			default:
				return false
			}
		}
		return true
	})
}

// TestBackfillTitles 补齐入口：只扫"有 prompt 但空标题"，幂等，force 才重扫。
func TestBackfillTitles(t *testing.T) {
	tr, store, _ := observedRunner(t)
	dir := t.TempDir()
	tr.titleClaudeBinary = "pwsh"
	tr.titleClaudeArgs = []string{"-NoProfile", "-Command", "Write-Output '补齐出来的标题'"}

	empty, _ := store.Create(&model.Task{ProjectID: "p", Prompt: "p1", ProjectPath: dir})
	titled, _ := store.Create(&model.Task{ProjectID: "p", Prompt: "p2", ProjectPath: dir, Title: "已有标题"})
	noPrompt, _ := store.Create(&model.Task{ProjectID: "p", ProjectPath: dir})

	if n := tr.BackfillTitles(false); n != 1 {
		t.Fatalf("首次补齐排期 %d 个，want 1（只有空标题且有 prompt 的）", n)
	}
	waitFor(t, 10*time.Second, "补齐后标题落库", func() bool {
		got, _ := store.Get(empty.ID)
		return got != nil && got.Title != ""
	})
	// 必须在**生成收尾之后**再判后面几条：占位未释放时
	// （落库 → release 之间有窗口，进程退出前还得 Close 一次 agent 会话）
	// 再调用 BackfillTitles 会被去重拦下，那是正确行为、不是本用例要测的东西。
	waitFor(t, 10*time.Second, "生成收尾（占位释放）", func() bool {
		return !tr.titleInflight.Busy(empty.ID)
	})

	// 已有标题的任务不得被改写
	if got, _ := store.Get(titled.ID); got.Title != "已有标题" {
		t.Fatalf("有标题的任务被改写: %q", got.Title)
	}
	// 无 prompt 的任务不参与
	if got, _ := store.Get(noPrompt.ID); got.Title != "" {
		t.Fatalf("无 prompt 的任务被写了标题: %q", got.Title)
	}

	// 幂等：已尝试过的不再排期（否则每次调用都为同一批任务重起 agent 进程）。
	if n := tr.BackfillTitles(false); n != 0 {
		t.Fatalf("二次补齐排期 %d 个，want 0（本进程已尝试过）", n)
	}

	// force：重扫"上次失败/无结果"的那批。注意此时该任务已经有标题了，
	// 所以正确的口径是"**排期了**"，而不是"又按新 prompt 生成了一遍" ——
	// 后者会把用户/模型产出的现有标题冲掉，那不是补齐该做的事。
	// 为了验证重扫真的会走到生成，这里再放一个空标题任务进去。
	again, _ := store.Create(&model.Task{ProjectID: "p", Prompt: "p3", ProjectPath: dir})
	tr.titleClaudeArgs = []string{"-NoProfile", "-Command", "Write-Output '二次补齐的标题'"}
	if n := tr.BackfillTitles(true); n != 1 {
		t.Fatalf("force 补齐排期 %d 个，want 1（只有 again）", n)
	}
	waitFor(t, 10*time.Second, "force 补齐落库", func() bool {
		got, _ := store.Get(again.ID)
		return got != nil && got.Title == "二次补齐的标题"
	})
}

// TestTitleInflight_Dedup 同一任务不得并发生成两次（创建时 + 补齐撞上）。
func TestTitleInflight_Dedup(t *testing.T) {
	f := NewTitleInFlight()
	if !f.claim("t1") {
		t.Fatal("首次 claim 应成功")
	}
	if f.claim("t1") {
		t.Fatal("并发 claim 必须被拒（否则会起两轮 agent 会话）")
	}
	if !f.Busy("t1") {
		t.Fatal("Busy 应为 true")
	}
	f.release("t1", true)
	if f.Busy("t1") {
		t.Fatal("release 后 Busy 应为 false")
	}
	if !f.AlreadyTried("t1") {
		t.Fatal("attempted=true 时应记为已尝试")
	}
	f.resetDone()
	if f.AlreadyTried("t1") {
		t.Fatal("resetDone 后应可重扫")
	}
}

// TestTitleAgentAvailable 判定"能否用任务自己的 agent 生成标题"。
//
// 判据：会话驱动路径已接线 + 任务没有正在跑的轮次。
func TestTitleAgentAvailable(t *testing.T) {
	tr, store, _ := observedRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "p", Prompt: "x", Status: model.TaskCompleted})

	avail := func() bool {
		cur, _ := store.Get(task.ID)
		return tr.titleAgentAvailable(cur)
	}

	// 未接 agent 路径（useACP=false）→ 不可用，应走 claude -p 兜底
	if avail() {
		t.Fatal("未接线时应为 false")
	}

	// 接线后可用
	tr.agentMgr = newFakeAgentRunner(fakeScript{}, false)
	tr.useACP = true
	if !avail() {
		t.Fatal("接线且任务非 running 时应为 true")
	}

	// running 中不可用：标题轮一旦占住 taskID 的会话，会撞 ErrSessionBusy
	if _, err := store.Update(task.ID, func(x *model.Task) bool { x.Status = model.TaskRunning; return true }); err != nil {
		t.Fatal(err)
	}
	if avail() {
		t.Fatal("任务 running 时应为 false（标题会话不得插进正在跑的轮次）")
	}
}

// TestGenerateTitle_FallsBackWhenAgentPathFails 任务自己的 agent 失败时仍要能落到 claude -p。
func TestGenerateTitle_FallsBackWhenAgentPathFails(t *testing.T) {
	tr, store, logs := observedRunner(t)
	task, _ := store.Create(&model.Task{ProjectID: "p", Prompt: "p", ProjectPath: t.TempDir(), Status: model.TaskCompleted})
	// agent 路径接线但 Open 必然失败（未注册的 agent 名 → ErrUnknownAgent）
	tr.agentMgr = newFakeAgentRunner(fakeScript{}, false)
	tr.useACP = true
	if _, err := store.Update(task.ID, func(x *model.Task) bool { x.Agent = "no-such-agent"; return true }); err != nil {
		t.Fatal(err)
	}
	tr.titleClaudeBinary = "pwsh"
	tr.titleClaudeArgs = []string{"-NoProfile", "-Command", "Write-Output '兜底生成的标题'"}

	tr.GenerateTitleAsync(task.ID)
	waitFor(t, 10*time.Second, "兜底标题落库", func() bool {
		got, _ := store.Get(task.ID)
		return got != nil && got.Title != ""
	})
	got, _ := store.Get(task.ID)
	if got.Title != "兜底生成的标题" {
		t.Fatalf("Title = %q, want 兜底生成的标题", got.Title)
	}
	// 主后端失败必须留痕（WARN），而不是悄悄换成兜底
	if logs.FilterMessage("title backend failed").Len() == 0 {
		t.Fatal("主后端失败未留 WARN 日志")
	}
}

