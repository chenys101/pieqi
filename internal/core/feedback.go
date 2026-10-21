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
	"path/filepath"
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

// FileStat 单文件的累计增删（口径：baseline → 当前）。
type FileStat struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// CumulativeSummary 累计统计：合计 + 每路径明细。
//
// 明细与合计**出自同一次计算**，这是要点不是装饰：此前「累计变化」列表里
// 每个文件的行数取自「该文件最后一个 Turn 的回填值」，而点开看到的是
// baseline → 当前 的累计 diff —— 同一个文件、同一个面板，两个数字各说各话。
type CumulativeSummary struct {
	ChangeSummary
	Entries []FileStat `json:"entries,omitempty"`
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
	Cumulative  CumulativeSummary   `json:"cumulative"`
	Checkpoints []int               `json:"checkpoints"`
	Preview     *FeedbackPreview    `json:"preview,omitempty"`
}

// 纳入 FileChange 派生的工具 → 操作类型映射。
// Bash 等无法可靠解析文件语义的工具不纳入（已知限制，p0-design.md §4.1）。
//
// ★ 这张表是**按 agent 分开**的，不能合并成一张大小写通吃的表（2026-10-21 修）。
//
// 旧实现只有一张大写表的三个条目（Edit/Write/NotebookEdit），而 dsh 的工具名是
// **全小写**的 `edit` / `write` —— 查表必落空 ⇒ dsh 任务每一轮派生出的 FileChange
// 都是空的：反馈面板「本轮变化」恒为 0 文件 +0/-0、`changes` 永远 `[]`，
// 快照/Rewind 也因此不认这些文件（checkpoint.go 与本表共用）。
// 实测会话 414b848a：78 次 edit + 2 次 write 全部被跳过，而行内那几个文件的
// 单文件 diff 接口却返回得好好的 —— 坏的是"认出这是哪一轮改了哪个文件"这一步。
//
// 为什么不直接做小写归一：`Write`（整文件覆盖）与 `write`（dsh 的整文件写入）语义相同，
// 但 qoder 的工具名是 "Read <path>" 这种"名字+参数"形态，归一后再取首词会把
// `Edit D:\x.vue` 误认成一条 edit —— 顺序与边界都要每家单独定，所以分开写。
var toolFileOperations = map[string]string{
	// claude（sdk-bridge 原样透传的 SDK 工具名，首字母大写）
	"Edit":         "modify",
	"Write":        "write", // create/modify 需结合文件历史判定（assembleOperation）
	"NotebookEdit": "modify",
	"Delete":       "delete",
	"Rename":       "rename",
	// dsh（ACP，工具名全小写，实测 15 个里与文件相关的就这三个写入口）
	"edit":  "modify",
	"write": "write",
}

// dshFileToolNames 是 dsh 的**文件写入口**集合（全小写）。
//
// 它的用途只有一个：`write` 在 dsh 里是"整文件覆盖"，与 claude 的 `Write` 同义；
// 而 dsh 的 `edit` 是**局部替换**（`old_string` → `new_string`）。
// 两者在"一行没变"时都会失败，所以**不会**产生一份"空改动"的 FileChange ——
// 见 operationOfTool 的说明。这里显式写出这个名字集合，是为了让
// "哪个 agent 的名字走哪条判据"在一处可见，而不是散在字面量比较里。
var dshFileToolNames = map[string]bool{"edit": true, "write": true}

// operationOfTool 按工具名求 FileChange 的操作类型；空串 = 不纳入派生。
//
// 判据是**工具名本身**（不是 agent 类型）：事件里没有 agent 字段，而这几个名字
// 在各自 agent 下语义稳定，不会互相撞上（claude 的大写名与 dsh 的小写名不重叠）。
func operationOfTool(toolName string) string {
	if op, ok := toolFileOperations[toolName]; ok {
		return op
	}
	return ""
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
// worktree 用于把事件里的绝对路径规范成 repo 相对路径（快照/git/ReadWorktreeFile
// 全部按相对路径工作，见 relativizeChanges）；传空串则原样保留。
// seenBefore 用于 Write 的 create/modify 判定：路径既不在其中且之前未出现过 → 视为 create。
// 传入 nil 表示无法判定（全部按 modify 处理，P0 可接受的降级）。
func DeriveFileChanges(events []model.TaskEvent, worktree string, seenBefore func(path string) bool) []FileChange {
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
			op := operationOfTool(ev.ToolName)
			if op == "" {
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
			a := m[p]
			// 整份「声明了改动、但**每一次**都失败」的不算变更（判据见 effectiveFileOp）。
			// 放在最后判定：失败也会改写 Status（同 Turn 内后到的失败覆盖先到的成功），
			// 若在建条目时就跳过，那条失败就再也盖不到前面那条上了。
			//
			// 只有"全部失败"才丢；有一次成功就留 —— 那一次真的写过盘。
			allFailed := len(a.fc.ToolUseIDs) > 0
			for _, id := range a.fc.ToolUseIDs {
				if effectiveFileOp(events, a.fc.Operation, id) != "" {
					allFailed = false
					break
				}
			}
			if allFailed {
				continue
			}
			fc := a.fc
			if fc.Operation == "write" {
				fc.Operation = classifyWrite(p, a.firstSeen, events, seenBefore)
			}
			out = append(out, fc)
		}
	}
	return relativizeChanges(worktree, out)
}

// relativizeChanges 把 FileChange.Path 里以 worktree 为前缀的绝对路径裁剪成 repo 相对路径。
// 事件里的 file_path 常为绝对路径（Windows 盘符 / 绝对目录）；而快照落盘、git cat-file/diff、
// ReadWorktreeFile 全部按 repo 相对路径工作。不规范化时绝对路径拼到快照根/工作区下会成为
// 非法路径 → AssembleBefore/TurnContent 都取空 → diff 恒 0、快照落盘错位。已在相对路径下的
// 原样保留（前缀不匹配）。大小写不敏感（Windows 盘符 G: vs g:）。
func relativizeChanges(worktree string, changes []FileChange) []FileChange {
	if worktree == "" {
		return changes
	}
	base := strings.TrimRight(normalizeRepoPath(filepath.Clean(worktree)), "/") + "/"
	for i := range changes {
		p := normalizeRepoPath(changes[i].Path)
		if len(p) >= len(base) && strings.EqualFold(p[:len(base)], base) {
			changes[i].Path = p[len(base):]
		}
	}
	return changes
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
			if op := operationOfTool(events[i].ToolName); op != "" && (op == "modify" || op == "write" || op == "delete") {
				if toolInputPath(events[i].Input) == path {
					return "modify"
				}
			}
		}
	}
	return "create"
}

// effectiveFileOp 把一次"工具声明"折算成 FileChange 的操作类型；空串 = 不产生变更。
//
// 这一步存在的原因只有一条：**声明 ≠ 真的改了**。
//
// dsh 的 `edit` 是 `old_string` → `new_string` 的局部替换，一模一样的入参重放会失败；
// 而 `write` 是整文件覆盖，内容相同就是空操作。这些工具**失败时同样会落一条
// tool_use**（审批放行后才执行，执行失败并不撤销已记录的调用），于是事件流里躺着
// 一批"改了 a.go"，而 a.go 从未变过。把它们算进本轮变更，反馈面板就会给出
// "改了 1 个文件 +0 -0" —— 数字没有错，但它对应用户点开看不到任何 diff。
//
// 判据（保守侧，靠得住的那部分）：该 tool_use 的 tool_result 明确是**失败**
// （is_error）⇒ 不算变更。没有 result / 结果为成功 ⇒ 照算（只要 succeeded 才算，
// 会把"进行中"的调用整段漏掉，那比多算一条更糟）。
//
// 注意字段名不参与判据：这里只看 **is_error**，不看 result 文本 ——
// 各 agent 的错误文案（"Error:"、"cannot edit file"…）不可枚举，读文案就是在猜。
func effectiveFileOp(events []model.TaskEvent, op string, toolUseID string) string {
	if op == "" || toolUseID == "" {
		return op
	}
	for _, ev := range events {
		if ev.Type == model.EventToolResult && ev.ToolUseID == toolUseID {
			if ev.IsError {
				return ""
			}
			return op
		}
	}
	return op
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

// UniqueByPath 把「(Turn, Path) 展开」的变更列表折叠成每路径一条，保留最后一次出现
// （列表按 Turn 升序，所以留下的是该路径的最终态）。
//
// 任务级统计**必须**先过它。同一文件改了三轮就是三条 FileChange（每条一个 Turn），
// 直接数条目会把「1 个文件」说成 3 个、行数再加 3 遍。
func UniqueByPath(changes []FileChange) []FileChange {
	idx := make(map[string]int, len(changes))
	out := make([]FileChange, 0, len(changes))
	for _, fc := range changes {
		if i, ok := idx[fc.Path]; ok {
			out[i] = fc
			continue
		}
		idx[fc.Path] = len(out)
		out = append(out, fc)
	}
	return out
}

// summarize 汇总一组 FileChange 的统计。增删行数来自回填后的 Additions/Deletions。
//
// 先按路径折叠再统计：入参可能是全任务的展开列表（同一路径每轮一条）。
// 单 Turn 汇总时本就每路径一条，折叠是幂等的 —— 也就是说这一层对两种入参都安全。
func summarize(changes []FileChange) ChangeSummary {
	uniq := UniqueByPath(changes)
	s := ChangeSummary{Files: len(uniq)}
	for _, fc := range uniq {
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
