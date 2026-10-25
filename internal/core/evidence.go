// evidence.go P1 Evidence（p1-design.md §7-8）：把验证状态聚合为「证据」，
// 供 Evidence Card 展示与 Evidence → Continue 组装续问 prompt（ADR-0004）。
//
// 默认随取随派生（不固化快照；POST /evidence 显式固化在设计中标为可选，P1 不做）。
package core

import (
	"fmt"
	"strings"
	"time"

	"pieqi/internal/model"
)

// EvidenceScope Evidence 的挂载层级。
const (
	ScopeTask    = "task"
	ScopeTurn    = "turn"
	ScopeOutcome = "outcome"
)

// Evidence 当前时刻的验证证据快照（派生）。
type Evidence struct {
	TaskID      string           `json:"task_id"`
	Scope       string           `json:"scope"` // task | turn | outcome
	Turn        int              `json:"turn,omitempty"`
	Preview     *FeedbackPreview `json:"preview,omitempty"`
	Checks      []CheckSummary   `json:"checks"`
	Errors      int              `json:"errors"` // 末轮 is_error tool_result 数
	Changes     ChangeSummary   `json:"changes"`
	DiffBrief   []string         `json:"diff_brief"` // 每文件一行摘要
	Files       []EvidenceFile   `json:"files"`      // 与 DiffBrief 同序的结构化版本（前端挂"打开"用）
	CreatedAt   string           `json:"created_at"`
}

// EvidenceFile 证据卡里的一个文件行。
//
// 为什么不能只给 DiffBrief 那些字符串：概览页的「文件清单」要能直接点开产物
// （HTML 方案页 / 图片），而字符串行是给人读的，前端从里面解析路径又脆又脏。
// 这里只放**结构性事实**（路径/动作/±行数），Kind 与 Open 由 api 层填 ——
// core 不关心 URL 是什么形态，也不该知道 preview 与 file 两条通道的差别。
type EvidenceFile struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`         // create | modify | delete
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
	Kind      string `json:"kind,omitempty"`    // html | markdown | image | pdf（api 填）
	Open      string `json:"open,omitempty"`    // 可直接打开的 URL（api 填）
}

// 注：P2 的 Console/Network 摘要（及更早的 Screenshot）已于 2026-10-15 整体移除，
// 见 docs/adr/0012（截图）与 docs/adr/0013（console/network）。
// 采集服务源码留档在独立工程 pieqi-ui-test/services/visual-capture。

// BuildEvidence 聚合派生 Evidence。
// changes 需已回填 +/- 行数（turn>0 时过滤到该 Turn）。
func BuildEvidence(task *model.Task, changes []FileChange, checks []Check, preview *FeedbackPreview, scope string, turn int) Evidence {
	ev := Evidence{
		TaskID:    task.ID,
		Scope:     scope,
		Turn:      turn,
		Preview:   preview,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if turn > 0 {
		var filtered []FileChange
		for _, fc := range changes {
			if fc.Turn == turn {
				filtered = append(filtered, fc)
			}
		}
		changes = filtered
	}

	ev.Changes = SummarizeAll(changes)
	for _, fc := range changes {
		line := fc.Operation + " " + fc.Path
		if fc.Additions > 0 || fc.Deletions > 0 {
			line += fmt.Sprintf(" (+%d -%d)", fc.Additions, fc.Deletions)
		}
		ev.DiffBrief = append(ev.DiffBrief, line)
		ev.Files = append(ev.Files, EvidenceFile{
			Path:      fc.Path,
			Operation: fc.Operation,
			Additions: fc.Additions,
			Deletions: fc.Deletions,
		})
	}

	for _, ck := range checks {
		if turn > 0 && ck.Turn != turn && ck.Origin == "agent" {
			continue // turn 范围只保留该 Turn 的 agent check + 全部 rerun
		}
		ev.Checks = append(ev.Checks, CheckSummary{
			ID: ck.ID, Name: ck.Name, Status: ck.Status, ExitCode: ck.ExitCode,
		})
	}
	ev.Errors = countLastTurnErrors(task.Events)
	return ev
}

// countLastTurnErrors 末轮 is_error tool_result 计数。
func countLastTurnErrors(events []model.TaskEvent) int {
	lastUser := -1
	for i, ev := range events {
		if ev.Type == model.EventUser {
			lastUser = i
		}
	}
	n := 0
	for i := lastUser + 1; i < len(events); i++ {
		if events[i].Type == model.EventToolResult && events[i].IsError {
			n++
		}
	}
	return n
}

// EvidencePrompt Evidence → Continue 的 prompt 组装（§8）：
// 用户指令 + 结构化证据（变更/检查/预览/错误），走既有 append_prompt（Resume）路径。
// failedChecks 携带完整 Check（含输出尾部），用于错误摘要。
func EvidencePrompt(instruction string, ev Evidence, failedChecks []Check) string {
	var b strings.Builder
	b.WriteString("请继续处理。")
	if s := strings.TrimSpace(instruction); s != "" {
		b.WriteString(s)
	}
	b.WriteString("\n\n当前证据：")

	// 变更摘要
	if ev.Changes.Files == 0 {
		b.WriteString("\n- 文件变更：无")
	} else {
		b.WriteString(fmt.Sprintf("\n- 文件变更：%d 个文件（+%d -%d）",
			ev.Changes.Files, ev.Changes.Additions, ev.Changes.Deletions))
		for _, line := range ev.DiffBrief {
			b.WriteString("\n  - " + line)
		}
	}

	// 检查摘要（成功简短，失败带尾部错误段）
	if len(ev.Checks) == 0 {
		b.WriteString("\n- 检查：未运行")
	}
	for _, ck := range ev.Checks {
		switch ck.Status {
		case CheckSuccess:
			b.WriteString("\n- 检查通过: " + ck.Name)
		case CheckFailed, CheckRunning, CheckPending:
			b.WriteString("\n- 检查未通过: " + ck.Name)
			if tail := checkOutputTail(failedChecks, ck.ID); tail != "" {
				b.WriteString("\n  错误摘要: " + tail)
			}
		}
	}

	// 预览状态
	if ev.Preview != nil {
		b.WriteString("\n- 预览: " + previewStateText(ev.Preview.State))
	} else {
		b.WriteString("\n- 预览: 不可用")
	}

	// 末轮错误计数
	if ev.Errors > 0 {
		b.WriteString(fmt.Sprintf("\n- 末轮错误: %d 个工具调用失败", ev.Errors))
	}

	// 注：这里曾有 P2 视觉证据两段 —— "- 截图证据: <url>"（更早移除）与
	// "- 页面 Console / 页面网络失败"（2026-10-15 随整个视觉采集层移除，ADR-0013）。
	return b.String()
}

// checkOutputTail 从完整 Check 列表取指定 check 的输出尾部（压缩空白，限 300 字符）。
func checkOutputTail(checks []Check, id string) string {
	for _, ck := range checks {
		if ck.ID != id {
			continue
		}
		tail := tailTruncate(strings.TrimSpace(ck.Output), 300)
		return firstLine(tail)
	}
	return ""
}

// previewStateText 预览状态的人读文本。
func previewStateText(state string) string {
	switch state {
	case PreviewRunning:
		return "ready"
	case PreviewStarting:
		return "starting"
	case PreviewAvailable:
		return "available"
	default:
		return state
	}
}
