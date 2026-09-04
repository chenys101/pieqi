// feedback.go Feedback Domain 的纯派生逻辑（ADR-0001）：
// TaskEvent 是事实源；FileChange / Turn / Summary 全部从事件流纯函数派生，不单独持久化。
//
// 派生规则（p0-design.md §3/§4）：
//   - Turn = 两条 EventUser 之间的全部事件；Turn 号 = EventUser 序号（1-based），永不重编号
//   - tool_use → FileChange(pending)；对应 tool_result（按 ToolUseID join）→ success/failed
//   - 同一路径同一 Turn 多次修改合并为一条，ToolUseIDs 累加
//   - Edit/Write/NotebookEdit（Edit 语义同构）/Delete/Rename 纳入；Bash 不纳入（已知限制）
package core

import (
	"encoding/json"
	"sort"
	"strings"

	"pieqi/internal/model"
)

// FileChange 派生领域模型：「Agent 声明改了什么」，不绑定 Git。
type FileChange struct {
	Path       string   `json:"path"`
	Operation  string   `json:"operation"` // create | modify | delete | rename
	Turn       int      `json:"turn"`
	ToolUseIDs []string `json:"tool_use_ids"`
	Status     string   `json:"status"` // pending | success | failed
	Additions  int      `json:"additions,omitempty"`
	Deletions  int      `json:"deletions,omitempty"`
}

// ChangeSummary 一个 Turn 的变更统计（规则生成，不调模型）。
type ChangeSummary struct {
	Files      int `json:"files"`
	Additions  int `json:"additions"`
	Deletions  int `json:"deletions"`
	Creates    int `json:"creates,omitempty"`
	Deletes    int `json:"deletes,omitempty"`
	Modifies   int `json:"modifies,omitempty"`
}

// TurnInfo Feedback 总览里的一个 Turn（含派生变更与起始事件 seq）。
type TurnInfo struct {
	Turn          int           `json:"turn"`
	StartEventSeq int           `json:"start_event_seq"`
	UserPrompt    string        `json:"user_prompt,omitempty"`
	Summary       ChangeSummary `json:"summary"`
	Changes       []FileChange  `json:"changes"`
}

// FeedbackPreview feedback bundle 内嵌的 preview 状态（可选能力，P0 设计 §5.1）。
type FeedbackPreview struct {
	State     string `json:"state"`               // unavailable | available | starting | running | stopped | error
	Framework string `json:"framework,omitempty"` // vite | next | nuxt | node
	Port      int    `json:"port,omitempty"`
	URL       string `json:"url,omitempty"` // 子路径代理入口 /api/tasks/:id/preview/
}

// FeedbackBundle GET /api/tasks/:id/feedback 的响应体。
type FeedbackBundle struct {
	TaskID      string              `json:"task_id"`
	Baseline    *model.TaskBaseline `json:"baseline,omitempty"`
	Turns       []TurnInfo          `json:"turns"`
	Cumulative  ChangeSummary       `json:"cumulative"`
	Checkpoints []int               `json:"checkpoints"`
	Preview     *FeedbackPreview    `json:"preview,omitempty"`
}

// 纳入 FileChange 派生的工具 → 操作类型映射。
// Bash 等无法可靠解析文件语义的工具不纳入（已知限制，p0-design.md §4.1）。
var toolFileOperations = map[string]string{
	"Edit":        "modify",
	"Write":       "write", // create/modify 需结合文件历史判定（assembleOperation）
	"NotebookEdit": "modify",
	"Delete":      "delete",
	"Rename":      "rename",
}

// toolInputPath 从 tool_use 入参中提取目标文件路径。
func toolInputPath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		OldPath      string `json:"old_path"` // Rename 变体
		NewPath      string `json:"new_path"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	if in.FilePath != "" {
		return normalizeRepoPath(in.FilePath)
	}
	if in.NotebookPath != "" {
		return normalizeRepoPath(in.NotebookPath)
	}
	if in.OldPath != "" {
		return normalizeRepoPath(in.OldPath)
	}
	return ""
}

// normalizeRepoPath 统一路径分隔符（Windows 反斜杠 → /），去开头 ./。
func normalizeRepoPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(p, "./")
}

// splitTurns 把事件流切成 Turn 边界：返回每个 Turn 的起始事件下标与用户文本。
// Turn 号 = EventUser 序号（1-based）；首条 EventUser 之前无事件（createTask 预置 seq=1）。
func splitTurns(events []model.TaskEvent) []struct {
	Index  int
	Prompt string
} {
	var turns []struct {
		Index  int
		Prompt string
	}
	for i, ev := range events {
		if ev.Type == model.EventUser {
			turns = append(turns, struct {
				Index  int
				Prompt string
			}{Index: i, Prompt: ev.Text})
		}
	}
	return turns
}

// CurrentTurnCount 事件流中的 Turn 总数（EventUser 条数）。0 = 尚无任何 Turn。
func CurrentTurnCount(events []model.TaskEvent) int {
	n := 0
	for _, ev := range events {
		if ev.Type == model.EventUser {
			n++
		}
	}
	return n
}

// DeriveFileChanges 从事件流纯函数派生 FileChange 列表（跨全部 Turn）。
// seenBefore 用于 Write 的 create/modify 判定：路径既不在其中且之前未出现过 → 视为 create。
// 传入 nil 表示无法判定（全部按 modify 处理，P0 可接受的降级）。
func DeriveFileChanges(events []model.TaskEvent, seenBefore func(path string) bool) []FileChange {
	type agg struct {
		fc        FileChange
		firstSeen int // 首次出现下标（同 Turn 内合并时保留最早）
	}
	byTurnPath := map[int]map[string]*agg{}
	turn := 0
	idx := 0

	for _, ev := range events {
		switch ev.Type {
		case model.EventUser:
			turn++
			continue
		case model.EventToolUse:
			op, ok := toolFileOperations[ev.ToolName]
			if !ok {
				continue
			}
			path := toolInputPath(ev.Input)
			if path == "" {
				continue
			}
			m := byTurnPath[turn]
			if m == nil {
				m = map[string]*agg{}
				byTurnPath[turn] = m
			}
			if existing, exists := m[path]; exists {
				existing.fc.ToolUseIDs = append(existing.fc.ToolUseIDs, ev.ToolUseID)
				continue
			}
			m[path] = &agg{fc: FileChange{
				Path: path, Operation: op, Turn: turn,
				ToolUseIDs: []string{ev.ToolUseID}, Status: "pending",
			}, firstSeen: idx}
		case model.EventToolResult:
			// 按 ToolUseID 反查归属（只更新 status，找不到就忽略——跨 Turn 的 result 不会出现）
			for _, m := range byTurnPath {
				for _, a := range m {
					for _, id := range a.fc.ToolUseIDs {
						if id == ev.ToolUseID {
							if ev.IsError {
								a.fc.Status = "failed"
							} else {
								a.fc.Status = "success"
							}
						}
					}
				}
			}
		}
		idx++
	}

	// 展开为有序列表（Turn 升序、路径字母序），并做 Write 的 create/modify 判定
	var out []FileChange
	turns := make([]int, 0, len(byTurnPath))
	for t := range byTurnPath {
		turns = append(turns, t)
	}
	sort.Ints(turns)
	for _, t := range turns {
		m := byTurnPath[t]
		paths := make([]string, 0, len(m))
		for p := range m {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fc := m[p].fc
			if fc.Operation == "write" {
				fc.Operation = classifyWrite(p, m[p].firstSeen, events, seenBefore)
			}
			out = append(out, fc)
		}
	}
	return out
}

// classifyWrite Write 工具的 create/modify 判定（p0-design.md §4.1）：
// 路径既不在 baseline tracked、也不在 pre/、之前也未出现过 → create，否则 modify。
func classifyWrite(path string, firstSeenIdx int, events []model.TaskEvent, seenBefore func(path string) bool) string {
	if seenBefore != nil && seenBefore(path) {
		return "modify"
	}
	// 之前（更早的事件）未出现过该路径 → create
	for i := 0; i < firstSeenIdx && i < len(events); i++ {
		if events[i].Type == model.EventToolUse {
			if op, ok := toolFileOperations[events[i].ToolName]; ok && (op == "modify" || op == "write" || op == "delete") {
				if toolInputPath(events[i].Input) == path {
					return "modify"
				}
			}
		}
	}
	return "create"
}

// BuildTurnInfos 组装 Feedback 总览的 Turn 列表（含每 Turn 变更与统计）。
// additions/deletions 由调用方（有 checkpoint/git 访问能力的一方）回填，此处只做结构组装。
func BuildTurnInfos(events []model.TaskEvent, changes []FileChange) []TurnInfo {
	splits := splitTurns(events)
	infos := make([]TurnInfo, 0, len(splits))
	for _, sp := range splits {
		info := TurnInfo{Turn: len(infos) + 1, StartEventSeq: events[sp.Index].Seq, UserPrompt: sp.Prompt}
		for _, fc := range changes {
			if fc.Turn == info.Turn {
				info.Changes = append(info.Changes, fc)
			}
		}
		info.Summary = summarize(info.Changes)
		infos = append(infos, info)
	}
	return infos
}

// summarize 汇总一组 FileChange 的统计。增删行数来自回填后的 Additions/Deletions。
func summarize(changes []FileChange) ChangeSummary {
	s := ChangeSummary{Files: len(changes)}
	for _, fc := range changes {
		s.Additions += fc.Additions
		s.Deletions += fc.Deletions
		switch fc.Operation {
		case "create":
			s.Creates++
		case "delete":
			s.Deletes++
		case "modify":
			s.Modifies++
		}
	}
	return s
}

// SummarizeAll 全任务累计统计（Agent-touched 路径集）。
func SummarizeAll(changes []FileChange) ChangeSummary { return summarize(changes) }
