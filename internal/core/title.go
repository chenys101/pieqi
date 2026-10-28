package core

// title.go：任务一句话标题（大模型摘要）的生成。
//
// 为什么从 task_runner.go 抽出来单列：标题生成**不是**这条任务会话的一部分
// （不占 turn 队列、不进任务事件流、失败不影响任务），凭这一点就该和驱动任务的那套
// 代码分开；更重要的是它是唯一一处"用别的 agent 去服务一个任务"的地方，判据必须
// 集中在一处才说得清。
//
// 历史事故（2026-10-16 ~ 10-20）：标题生成写死 spawn `claude -p`，而这个 claude 的
// 凭据来自 ~/.claude/settings.json（当时指向 DeepSeek 的 anthropic 兼容端点）。
// 那个 API key 失效后，每一次生成都以 `exit status 1`（401）告终 —— 而失败只打了
// DEBUG 级日志（线上是 INFO）就 return。结果是 dsh / qoder 任务创建后标题永不更新，
// 日志里一条线索都没有，前端还照常渲染 prompt 截断的启发式标题，看不出任何异常。
//
// 由此定下两条规则（本文件是它们的唯一落点）：
//  1. **标题优先用任务自己的 agent 生成**。任务跑在 dsh 上，标题就用 dsh 生成 ——
//     不再让"用 dsh 的人"去依赖 Claude 的 API key。claude -p 只在拿不到任务自己的
//     会话时兜底（例如任务已终态、会话已被回收）。
//  2. **失败绝不静默**。每一级后端失败都打 WARN（带原因），不是 DEBUG。

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/exechide"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// titlePromptPrefix 标题 prompt 的固定前缀，后接任务原文。
//
// 实测选型：要求"只输出标题"而不是"总结成一句话"。此前两条自称开发的提示词
// 都产出过元回复（"没有可概括的开发任务…"），现由 cleanTitle 的否定词表 + 本函数
// 的残渣过滤双重拦截。
const titlePromptPrefix = "为下面这个开发任务起一个不超过 20 个字的标题。" +
	"只输出标题本身：不要引号、不要前缀、不要解释、不要句号。"

// errTitleEmpty 所有后端都跑通了但没产出可用标题（模型给了拒绝/元回复/空串）。
var errTitleEmpty = errors.New("标题生成无可用结果")

// titleAgentTimeout 用任务自己的 agent 生成标题的上限。
//
// 要比 claude -p 那 60s 宽松得多：dsh 冷启动单是 initialize 实测就 >90s
// （见 provider.go 的 ColdStartCold）。命中 keep-alive 的存量会话则只需几秒。
const titleAgentTimeout = 3 * time.Minute

// maxTitlePromptLen 截断送进标题 prompt 的任务原文长度。
//
// 保留原长（claude -p 时代是全文）：截短会让"长需求"的标题丢掉后半段的关键词。
// 30k 字符已经远超任何真实需求，这里只是防一个病态任务把 prompt 撑成几 MB。
const maxTitlePromptLen = 30000

// cleanTitle 清洗模型生成的标题：去引号/前后缀/多余空白，压成单行并限长。
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'"“”‘’`)
	s = strings.TrimPrefix(s, "标题：")
	s = strings.TrimPrefix(s, "标题:")
	s = strings.TrimSpace(s)
	// 折叠空白为单行
	s = strings.Join(strings.Fields(s), " ")
	// 拒绝"模型没理解任务"的元回复（如"你的消息里没有附带要概括的开发任务内容"）。
	// 这类文本不是标题，命中则返回空串，调用方保留前端启发式标题，避免污染任务列表。
	for _, phrase := range []string{"没有附带", "请把", "请提供", "请说明", "请告诉我", "无法概括", "发给我", "undefined"} {
		if strings.Contains(s, phrase) {
			return ""
		}
	}
	runes := []rune(s)
	const max = 30
	if len(runes) > max {
		s = string(runes[:max]) + "…"
	}
	return strings.TrimSpace(s)
}

// looksLikeMetaReply 判断一段模型输出像不像"拒绝/澄清"而不是标题。
//
// 为什么 cleanTitle 之外还要这一层：某些 agent 用英文回答（"I can't summarize…"），
// 或把思考过程一起吐出来（"用户希望我为这段文字生成一个标题…"）。只靠中文否定词表
// 会漏，而漏掉的代价是任务列表里出现一行以"用户希望我"开头的假标题。
//
// 判据刻意保守（宁可判为"无标题"而保留前端截断）：只否定**明确的第一人称/指令口吻**。
func looksLikeMetaReply(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range []string{
		"用户希望", "用户要求", "用户想要", "我需要为", "我先", "以下标题",
		"这似乎是", "i can't", "i cannot", "as an ai", "sure,", "here is", "here's",
	} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// titlePromptBody 任务原文的截断副本（超长只截不改写）。
func titlePromptBody(prompt string) string {
	r := []rune(prompt)
	if len(r) <= maxTitlePromptLen {
		return prompt
	}
	return string(r[:maxTitlePromptLen]) + "…（任务原文过长，已截断）"
}

// buildTitlePrompt 组装送给模型的一轮 prompt。
func buildTitlePrompt(prompt string) string {
	return titlePromptPrefix + "\n\n---\n任务原文：\n" + titlePromptBody(prompt)
}

// titleFromReply 把模型的一整轮回复变成可用标题；不可用时返回 ""。
//
// 取最后一行非空文本：模型常先"复述任务"再给标题，而标题总是收尾那一行。
func titleFromReply(reply string) string {
	lines := strings.Split(reply, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		t := cleanTitle(line)
		if t == "" || looksLikeMetaReply(t) {
			return ""
		}
		return t
	}
	return ""
}

// titleFromClaudeStdout 从 claude -p 的 stdout 抽标题。
//
// 两种形态都要吃下：
//   - 正常：stdout 就是标题那一行（--output-format text）；
//   - 走 SDK 形态时：stdout 里混着 `[claude-code:unrecognized_model] {...}` 这类
//     诊断行、甚至一整包 JSON。它们不是标题，直接当标题会污染任务列表。
func titleFromClaudeStdout(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "[claude-code:") {
			continue
		}
		if t := titleFromReply(line); t != "" {
			return t
		}
	}
	return ""
}

// claudeStderrTail 取 claude 报错文本的尾巴（单行、限长），供日志与错误信息使用。
//
// 为什么不能只报 "exit status 1"：这正是那 4 天空窗期最致命的地方 —— 真实原因是
// stderr 里的 "API Error: 401 Authentication Fails"，而 exec.CombinedOutput 之外的
// 每个调用点都把它丢掉了。
func claudeStderrTail(b []byte) string {
	re := regexp.MustCompile(`(?i)(api error[^\n]*|failed to authenticate[^\n]*|error[^\n]{0,160})`)
	m := re.Find(b)
	s := ""
	if m != nil {
		s = string(m)
	}
	if s == "" {
		s = string(b)
	}
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 240 {
		s = string(r[:240]) + "…"
	}
	return s
}

// titleBackend 一个标题生成后端。测试注入替身，生产是 claude -p / 任务自己的 agent。
type titleBackend struct {
	name string
	gen  func(ctx context.Context) (string, error)
}

// TitleInFlight 记录正在生成标题的任务，避免"创建时已排期"与"补齐遗漏"重复生成。
//
// 为什么需要它：补齐入口（BackfillTitles）会在服务启动时、以及手动调用时扫一遍
// **全部**空标题任务；而创建任务时那次生成可能刚好还在跑（dsh 冷启动可达数分钟）。
// 没有这层去重，同一个任务会被两条路径同时生成、后完成者覆盖先完成者，白白多起一轮
// agent 会话。
type TitleInFlight struct {
	mu   sync.Mutex
	ids  map[string]struct{}
	done map[string]struct{} // 本次进程内已尝试过（无论成功）——补齐只关心"还没试过的"
}

// NewTitleInFlight 构造一个空的在途表。
func NewTitleInFlight() *TitleInFlight {
	return &TitleInFlight{ids: map[string]struct{}{}, done: map[string]struct{}{}}
}

// claim 尝试占位；已被占用返回 false。
func (f *TitleInFlight) claim(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, busy := f.ids[id]; busy {
		return false
	}
	f.ids[id] = struct{}{}
	return true
}

// release 释放占位，并（attempted 时）记为"本进程已尝试过"。
func (f *TitleInFlight) release(id string, attempted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.ids, id)
	if attempted {
		f.done[id] = struct{}{}
	}
}

// Busy 该任务此刻是否正在生成标题。
func (f *TitleInFlight) Busy(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.ids[id]
	return ok
}

// AlreadyTried 本进程是否已为该任务尝试过生成（无论成败）。
func (f *TitleInFlight) AlreadyTried(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.done[id]
	return ok
}

// resetDone 清空"已尝试"记录，让下一次补齐重新扫描（手动触发用）。
//
// 为什么手动触发要重扫已尝试过的：用户修好凭据后，最想要的是"把那批空标题补上"，
// 而它们是上次失败过的 —— 不重扫就得重启服务。
func (f *TitleInFlight) resetDone() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = map[string]struct{}{}
}

// GenerateTitleAsync 异步生成任务的一句话标题（大模型摘要）。
//
// 不阻塞任务创建流程；成功后写入 task.Title 并经 WS 推送。失败**不影响主流程**，
// 但一定留痕（WARN）。
func (tr *TaskRunner) GenerateTitleAsync(taskID string) {
	if !tr.titleInflight.claim(taskID) {
		return // 已在生成中（补齐与创建撞上），谁都不该再起一轮
	}
	go func() {
		attempted := false
		defer func() { tr.titleInflight.release(taskID, attempted) }()
		t, ok := tr.store.Get(taskID)
		if !ok || t == nil || strings.TrimSpace(t.Prompt) == "" {
			return
		}
		attempted = true
		if title, err := tr.generateTitle(t); err != nil {
			// WARN 而非 DEBUG：这条路径曾经让"全线失败"在 INFO 级日志里完全隐形。
			tr.logger.Warn("generate title failed; task keeps the heuristic title",
				zap.String("task", taskID), zap.String("agent", t.Agent), zap.Error(err))
			return
		} else {
			tr.applyTitle(taskID, title)
		}
	}()
}

// applyTitle 落库并推送（与旧实现同一套语义：内容没变则不发事件）。
func (tr *TaskRunner) applyTitle(taskID, title string) {
	updated, err := tr.store.Update(taskID, func(t *model.Task) bool {
		if t.Title == title {
			return false
		}
		t.Title = title
		return true
	})
	if err != nil {
		tr.logger.Warn("store title failed", zap.String("task", taskID), zap.Error(err))
		return
	}
	if updated != nil {
		tr.bus.Publish(Event{Type: "task_updated", TaskID: taskID, Task: updated})
	}
}

// generateTitle 依次尝试各后端，返回第一个可用的标题。
//
// 顺序即优先级：任务自己的 agent → claude -p。全部失败时把**每一级**的原因都写进
// 返回的 error（而不是只报最后一级），否则排查时永远只看到兜底那一项。
func (tr *TaskRunner) generateTitle(t *model.Task) (string, error) {
	var errs []error
	for _, b := range tr.titleBackends(t) {
		title, err := b.gen(context.Background())
		if err == nil && title != "" {
			if b.name != "agent" {
				tr.logger.Info("title generated by fallback backend",
					zap.String("task", t.ID), zap.String("backend", b.name))
			}
			return title, nil
		}
		if err == nil {
			err = errTitleEmpty
		}
		err = fmt.Errorf("%s: %w", b.name, err)
		tr.logger.Warn("title backend failed", zap.String("task", t.ID), zap.Error(err))
		errs = append(errs, err)
	}
	return "", errors.Join(errs...)
}

// titleBackends 按优先级列出可用的标题后端。
func (tr *TaskRunner) titleBackends(t *model.Task) []titleBackend {
	out := make([]titleBackend, 0, 2)
	if tr.titleAgentAvailable(t) {
		out = append(out, titleBackend{name: "agent", gen: func(ctx context.Context) (string, error) {
			return tr.titleFromTaskAgent(ctx, t)
		}})
	}
	out = append(out, titleBackend{name: "claude -p", gen: func(ctx context.Context) (string, error) {
		return tr.titleFromClaudePrint(ctx, t)
	}})
	return out
}

// titleAgentAvailable 任务自己的 agent 此刻能否用来生成标题。
//
// 判据（任一不满足就走 claude -p 兜底）：
//   - 会话驱动路径已接线（agentMgr 由 NewTaskRunner 保证非 nil；这里的 useACP 是
//     SessionManager 形态的语义 —— 见 SetAgentManager）；
//   - 任务已有 agent 名（旧任务可能为空 → 库默认 claude，Open 自己能兜）；
//   - 任务没有正在跑的轮次：同一 taskID 的会话一旦被"标题会话"占住，后续 Prompt
//     会撞 ErrSessionBusy，而那条线上的排队逻辑只服务于真正的任务轮次。
func (tr *TaskRunner) titleAgentAvailable(t *model.Task) bool {
	if tr.agentMgr == nil || !tr.useACP || t == nil {
		return false
	}
	if tr.sessionLive(t.ID) {
		return false
	}
	return true
}

// sessionLive 该任务是否正处于"会话在忙"的状态（有轮次在跑）。
func (tr *TaskRunner) sessionLive(taskID string) bool {
	t, ok := tr.store.Get(taskID)
	return ok && t != nil && t.Status == model.TaskRunning
}

// titleFromTaskAgent 用**任务自己的 agent** 起一个独立会话生成标题。
//
// 关键取舍：不占任务自己的会话与 turn 队列。
//   - 不占会话：任务正在跑时（比如用户创建后立即追问）标题生成不应插进那串对话，
//     也不该让标题轮的失败影响任务状态；
//   - 不复用 AgentManager：那边的会话生命周期挂在 taskID 上，标题会话一旦 Close
//     就会触发 onSessionClosed（清理 wires / 延迟删 worktree），误伤真实会话。
//
// 代价是每个任务多起一次 agent 进程（冷启动可达数分钟），换来的是"标题不属于任务
// 语义"这条判据在代码里成立。
func (tr *TaskRunner) titleFromTaskAgent(ctx context.Context, t *model.Task) (string, error) {
	req := t.Prompt
	dir := t.WorktreePath
	if dir == "" {
		dir = t.ProjectPath
	}
	if dir == "" {
		return "", errors.New("任务没有可用工作目录")
	}
	ctx, cancel := context.WithTimeout(ctx, titleAgentTimeout)
	defer cancel()

	sess, err := agent.Open(ctx, agent.OpenParams{Agent: t.Agent, Cwd: dir})
	if err != nil {
		return "", fmt.Errorf("打开 %s 会话: %w", t.Agent, err)
	}
	defer func() {
		closeCtx, c := context.WithTimeout(context.Background(), 20*time.Second)
		defer c()
		_ = sess.Close(closeCtx)
	}()

	// 收集助手正文增量。标题轮不挂审批：拿不到审批就**明确报错**，
	// 不让它卡在"等人点批准"上直到超时 —— 标题生成没人看得见，卡住等于永远没有标题。
	var mu sync.Mutex
	var sb strings.Builder
	var permErr error
	sess.OnEvent(func(ev agent.Event) {
		switch ev.Kind {
		case agent.EventTextDelta:
			if ev.IsThought {
				return
			}
			mu.Lock()
			sb.WriteString(ev.Text)
			mu.Unlock()
		case agent.EventPermissionNeeded:
			mu.Lock()
			if permErr == nil {
				permErr = fmt.Errorf("标题轮触发了工具审批（%s），已取消", ev.Permission.ToolTitle)
			}
			mu.Unlock()
			cancel()
		}
	})
	if err := sess.Prompt(ctx, buildTitlePrompt(req)); err != nil {
		mu.Lock()
		pe := permErr
		mu.Unlock()
		if pe != nil {
			return "", pe
		}
		return "", err
	}
	mu.Lock()
	reply := sb.String()
	mu.Unlock()
	if title := titleFromReply(reply); title != "" {
		return title, nil
	}
	if strings.TrimSpace(reply) == "" {
		return "", errors.New("agent 未返回文本")
	}
	return "", errTitleEmpty
}

// titleFromClaudePrint 兜底：spawn `claude -p` 生成标题。
//
// 只在任务自己的 agent 不可用（或失败）时走到这里。它依赖 ~/.claude 里的凭据，
// 所以**它的失败绝不能只打 DEBUG** —— 2026-10-16 起连续 4 天标题失效，正是这条
// 路径的 401 被 DEBUG 吞掉的结果。
func (tr *TaskRunner) titleFromClaudePrint(ctx context.Context, t *model.Task) (string, error) {
	dir := t.WorktreePath
	if dir == "" {
		dir = t.ProjectPath
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	bin := tr.titleClaudeBinary
	if bin == "" {
		bin = "claude"
	}
	args := tr.titleClaudeArgs
	if args == nil {
		args = []string{"-p", buildTitlePrompt(t.Prompt), "--output-format", "text"}
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	exechide.Command(cmd)            // Windows：不弹 cmd 黑窗（标题补齐是后台活）
	out, err := cmd.CombinedOutput() // CombinedOutput 而非 Output：stderr 是唯一的失败线索
	if err != nil {
		tail := claudeStderrTail(out)
		if tail == "" {
			return "", fmt.Errorf("%w（无 stderr）", err)
		}
		return "", fmt.Errorf("%w: %s", err, tail)
	}
	if title := titleFromClaudeStdout(string(out)); title != "" {
		return title, nil
	}
	return "", errTitleEmpty
}

// BackfillTitles 补齐所有"有 prompt 但没标题"的任务。
//
// 为什么需要它：标题生在任务创建那一刻（GenerateTitleAsync），失败就永远补不回来 ——
// 而失败是常态（agent 不可用、凭据过期）。没有补齐入口时，用户唯一的补救是重新建
// 一次任务，之前那批只能永远显示 prompt 截断。
//
// 幂等：只处理 Title 为空、且本进程还没尝试过（或 force 时重扫）的任务；
// 已尝试过的跳过，避免每次调用都为同一批任务重起 agent 进程。
//
// 顺序：全部异步、并发执行（各自受 TitleInFlight 去重保护），不阻塞调用方。
// 返回排上队的任务数。
func (tr *TaskRunner) BackfillTitles(force bool) int {
	if tr.titleInflight == nil {
		return 0
	}
	if force {
		tr.titleInflight.resetDone()
	}
	n := 0
	for _, t := range tr.store.List() {
		if t == nil || strings.TrimSpace(t.Title) != "" || strings.TrimSpace(t.Prompt) == "" {
			continue
		}
		if !force && tr.titleInflight.AlreadyTried(t.ID) {
			continue
		}
		tr.GenerateTitleAsync(t.ID)
		n++
	}
	if n > 0 {
		tr.logger.Info("backfill titles scheduled", zap.Int("tasks", n), zap.Bool("force", force))
	}
	return n
}
