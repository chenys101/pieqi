// checks.go P1 Checks（p1-design.md §5）：验证改得对不对。
//
// 两个来源（ADR-0005 复用优先）：
//   - 来源一（默认）：扫描 Task 事件流——Bash tool_use 命令命中 check 模式，
//     以对应 tool_result 生成 Check（success/failed by is_error）。零额外执行。
//   - 来源二（重跑）：CheckRunner 在 worktree 内独立执行命令（Proc 模式：
//     超时 / 输出截断 / exit code / 运行态可见 / 落盘审计）。
//
// Check 事实源仍是 TaskEvent（派生部分）；重跑记录落盘 <root>/<taskID>/checks.json（审计）。
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Check 状态机：pending → running → success | failed | skipped（重跑产生新记录，不覆盖旧记录）。
const (
	CheckPending = "pending"
	CheckRunning = "running"
	CheckSuccess = "success"
	CheckFailed  = "failed"
	CheckSkipped = "skipped"
)

// Check 一次可验证性检查（test / lint / build 等）。
type Check struct {
	ID         string     `json:"id"`
	TaskID     string     `json:"task_id"`
	Turn       int        `json:"turn,omitempty"` // Agent 自跑时归属的 Turn；重跑为 0
	Name       string     `json:"name"`           // 人读命令名（如 "npm test"）
	Command    string     `json:"command"`         // 完整 shell 命令（sh -c 执行）
	Origin     string     `json:"origin"`          // agent（事件流复用）| rerun（用户重跑）
	Status     string     `json:"status"`
	DurationMs int64      `json:"duration_ms,omitempty"`
	ExitCode   int        `json:"exit_code,omitempty"`
	Output     string     `json:"output,omitempty"` // 截断输出（保留尾部错误段）
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// checkCommandRe check 模式匹配：包管理器 test/lint/build 类命令 + go 工具链。
// 允许 `cd <dir> && npm test` 前缀（Agent 常见写法）；排除 dev/start/serve（非检查）。
var checkCommandRe = regexp.MustCompile(
	`^(?:(?:cd\s+\S+\s*&&\s*)?(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?(?:test|tests|lint|build|typecheck|tsc|check)\b|go\s+(?:test|vet|build|fmt)\b)`)

// checkOutputCap 单条 Check 输出保留上限（保留尾部，错误摘要通常在末尾）。
const checkOutputCap = 8 * 1024

// DeriveChecks 来源一：从事件流纯函数派生 Agent 自跑的 checks。
// Bash tool_use 命令命中模式 → 以对应 tool_result（按 ToolUseID join）生成 Check。
func DeriveChecks(events []model.TaskEvent) []Check {
	turn := 0
	useAt := map[string]model.TaskEvent{} // toolUseID → tool_use 事件（求时长）
	var out []Check

	for _, ev := range events {
		switch ev.Type {
		case model.EventUser:
			turn++
		case model.EventToolUse:
			if ev.ToolName != "Bash" {
				continue
			}
			var in struct {
				Command string `json:"command"`
			}
			if len(ev.Input) == 0 || json.Unmarshal(ev.Input, &in) != nil || in.Command == "" {
				continue
			}
			if !checkCommandRe.MatchString(strings.TrimSpace(in.Command)) {
				continue
			}
			useAt[ev.ToolUseID] = ev
			out = append(out, Check{
				ID: ev.ToolUseID, TaskID: "", Turn: turn,
				Name: strings.TrimSpace(in.Command), Command: strings.TrimSpace(in.Command),
				Origin: "agent", Status: CheckPending, StartedAt: ev.At,
			})
		case model.EventToolResult:
			if ck, ok := useAt[ev.ToolUseID]; ok {
				for i := range out {
					if out[i].ID != ev.ToolUseID {
						continue
					}
					out[i].Status = CheckSuccess
					if ev.IsError {
						out[i].Status = CheckFailed
					}
					out[i].ExitCode = checkExitCode(ev)
					out[i].Output = tailTruncate(ev.Result, checkOutputCap)
					at := ev.At
					out[i].FinishedAt = &at
					if d := ev.At.Sub(ck.At).Milliseconds(); d > 0 {
						out[i].DurationMs = d
					}
				}
			}
		}
	}
	return out
}

// checkExitCode 从 tool_result 文本尽力提取 exit code（Agent 输出格式不定，仅尽力而为）。
func checkExitCode(ev model.TaskEvent) int {
	// 常见形态：exit 1 / Exit code 1 / (exit 1)
	re := regexp.MustCompile(`(?:[Ee]xit(?:\s+code)?\s*[:=]?\s*)(\d+)`)
	if m := re.FindStringSubmatch(ev.Result); m != nil {
		var n int
		if _, err := fmt.Sscanf(m[1], "%d", &n); err == nil {
			return n
		}
	}
	if ev.IsError {
		return 1
	}
	return 0
}

// tailTruncate 保尾截断（错误摘要通常在输出末尾）。
func tailTruncate(s string, cap int) string {
	if len(s) <= cap {
		return s
	}
	return "…（前文截断）" + s[len(s)-cap:]
}

// --- 来源二：CheckRunner（重跑） ---

// checkRunTimeout 重跑超时（p1-design §5.3：超时是重跑硬性要求）。
const checkRunTimeout = 5 * time.Minute

// CheckRunner 独立执行 check 命令（用户点击即授权）。
// 运行态在内存可见（List 合并），完成记录落盘审计。
type CheckRunner struct {
	logger *zap.Logger
	root   string // <dataRoot>/checks

	mu       sync.Mutex
	running  map[string]*Check // sourceID → running 态记录（防同源重复跑）
	persist  map[string][]Check
}

// NewCheckRunner 创建 runner。root 为重跑记录落盘目录。
func NewCheckRunner(logger *zap.Logger, root string) *CheckRunner {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &CheckRunner{
		logger: logger, root: root,
		running: map[string]*Check{}, persist: map[string][]Check{},
	}
}

// Rerun 在 task worktree 内重跑命令（异步：立即返回 running 态 Check）。
// sourceID = 来源 Check 的 ID（仅用于防重）；新记录产生新 ID（不覆盖旧记录，保留审计）。
func (cr *CheckRunner) Rerun(task *model.Task, sourceID, name, command string) (*Check, error) {
	if task == nil || task.WorktreePath == "" {
		return nil, fmt.Errorf("check: task missing worktree")
	}
	if command == "" {
		return nil, fmt.Errorf("check: empty command")
	}
	cr.mu.Lock()
	if _, busy := cr.running[sourceID]; busy {
		cr.mu.Unlock()
		return nil, fmt.Errorf("check already running: %s", sourceID)
	}
	ck := &Check{
		ID: "rerun-" + uuid.NewString()[:8], TaskID: task.ID,
		Name: name, Command: command,
		Origin: "rerun", Status: CheckRunning, StartedAt: time.Now(),
	}
	cr.running[sourceID] = ck
	cr.mu.Unlock()

	go cr.exec(task, ck)
	return ck, nil
}

// exec 实际执行：sh -c command，超时 kill，输出保尾截断，结束落盘。
func (cr *CheckRunner) exec(task *model.Task, ck *Check) {
	ctx, cancel := context.WithTimeout(context.Background(), checkRunTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", ck.Command)
	cmd.Dir = task.WorktreePath
	cmd.Env = previewEnv() // 白名单环境（同 preview：剔除 token，保 PATH/NODE_ 等）
	out, err := cmd.CombinedOutput()

	finished := time.Now()
	ck.FinishedAt = &finished
	ck.DurationMs = finished.Sub(ck.StartedAt).Milliseconds()
	ck.Output = tailTruncate(string(out), checkOutputCap)
	ck.ExitCode = 0
	if ctx.Err() == context.DeadlineExceeded {
		ck.Status = CheckFailed
		ck.Output = tailTruncate(ck.Output+"\n（超时终止）", checkOutputCap)
	} else if err != nil {
		ck.Status = CheckFailed
		if ee, ok := err.(*exec.ExitError); ok {
			ck.ExitCode = ee.ExitCode()
		} else {
			ck.ExitCode = 1
		}
	} else {
		ck.Status = CheckSuccess
	}

	cr.mu.Lock()
	for src, r := range cr.running {
		if r == ck {
			delete(cr.running, src)
		}
	}
	cr.mu.Unlock()
	cr.persistRecord(ck)
	cr.logger.Info("check rerun finished",
		zap.String("task", ck.TaskID), zap.String("name", ck.Name),
		zap.String("status", ck.Status), zap.Int("exit", ck.ExitCode))
}

// persistRecord 追加重跑记录到 <root>/<taskID>/checks.json（审计）。
func (cr *CheckRunner) persistRecord(ck *Check) {
	cr.mu.Lock()
	records := append(cr.persist[ck.TaskID], *ck)
	cr.persist[ck.TaskID] = records
	cr.mu.Unlock()

	// 双写磁盘（内存为主，磁盘重启后可读）
	dir := filepath.Join(cr.root, ck.TaskID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		cr.logger.Warn("check persist mkdir", zap.Error(err))
		return
	}
	data, err := json.Marshal(records)
	if err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "checks.json"), data, 0644); err != nil {
		cr.logger.Warn("check persist write", zap.Error(err))
	}
}

// List 返回该 task 的重跑记录（内存 running + 落盘历史；磁盘读一次后缓存）。
func (cr *CheckRunner) List(taskID string) []Check {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	var out []Check
	// running 态优先
	for _, ck := range cr.running {
		if ck.TaskID == taskID {
			out = append(out, *ck)
		}
	}
	// 落盘历史（未加载过则从磁盘读一次）
	if _, loaded := cr.persist[taskID]; !loaded {
		if data, err := os.ReadFile(filepath.Join(cr.root, taskID, "checks.json")); err == nil {
			var records []Check
			if json.Unmarshal(data, &records) == nil {
				cr.persist[taskID] = records
			}
		}
	}
	out = append(out, cr.persist[taskID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// Cleanup 删除 task 的重跑记录（task 删除时调用）。
func (cr *CheckRunner) Cleanup(taskID string) {
	cr.mu.Lock()
	delete(cr.persist, taskID)
	cr.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(cr.root, taskID)); err != nil {
		cr.logger.Warn("check cleanup", zap.Error(err))
	}
}

// MergeChecks 合并派生（agent）与重跑（rerun）记录，按开始时间升序。
func MergeChecks(derived, reruns []Check) []Check {
	out := append(append([]Check{}, derived...), reruns...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}
