// prospective.go P1 Approval → Diff（p1-design.md §4）：前瞻性 Diff。
//
// 审批前看到「将发生什么」：Decision.ID 关联 pending tool_use TaskEvent，
// Diff 完全从工具入参派生（ADR-0001，无新存储）：
//   - Edit   → 当前文件 vs 替换后（old_string → new_string）
//   - Write  → 当前文件 vs 新内容
//   - Delete → 当前文件 vs 空
//   - NotebookEdit → 当前文件 vs new_source
package core

import (
	"encoding/json"
	"strings"

	"pieqi/internal/model"
)

// ProspectiveResult 前瞻性 Diff 计算结果。
type ProspectiveResult struct {
	Path      string `json:"path"`
	Operation string `json:"operation"` // create | modify | delete
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
}

// ProspectiveDiff 由 decisionID（= tool_use id）计算审批前的前瞻性 Diff。
// 未找到对应 tool_use、或工具无文件语义（Bash 等）→ ok=false。
func ProspectiveDiff(task *model.Task, decisionID string) (ProspectiveResult, bool) {
	var use *model.TaskEvent
	for i := range task.Events {
		if task.Events[i].Type == model.EventToolUse && task.Events[i].ToolUseID == decisionID {
			use = &task.Events[i]
			break
		}
	}
	if use == nil || len(use.Input) == 0 {
		return ProspectiveResult{}, false
	}

	var res ProspectiveResult
	var before, after []byte
	var beforeExists, afterExists bool

	switch use.ToolName {
	case "Write":
		var in struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		if json.Unmarshal(use.Input, &in) != nil || in.FilePath == "" {
			return ProspectiveResult{}, false
		}
		res.Path = normalizeRepoPath(in.FilePath)
		before, beforeExists = ReadWorktreeFile(task.WorktreePath, res.Path)
		after, afterExists = []byte(in.Content), true
		res.Operation = mapWriteOp(beforeExists)
	case "Edit":
		var in struct {
			FilePath   string `json:"file_path"`
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		}
		if json.Unmarshal(use.Input, &in) != nil || in.FilePath == "" {
			return ProspectiveResult{}, false
		}
		res.Path = normalizeRepoPath(in.FilePath)
		cur, curOK := ReadWorktreeFile(task.WorktreePath, res.Path)
		if curOK && (in.ReplaceAll || strings.Contains(string(cur), in.OldString)) {
			// 当前文件可读且包含 old_string → 预演真实替换结果
			before, beforeExists = cur, true
			after, afterExists = []byte(replaceEdit(string(cur), in.OldString, in.NewString, in.ReplaceAll)), true
		} else {
			// 文件缺失或不含 old_string：退化为片段对比（old vs new）
			before, beforeExists = []byte(in.OldString), true
			after, afterExists = []byte(in.NewString), true
		}
		res.Operation = "modify"
	case "Delete":
		var in struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(use.Input, &in) != nil || in.FilePath == "" {
			return ProspectiveResult{}, false
		}
		res.Path = normalizeRepoPath(in.FilePath)
		before, beforeExists = ReadWorktreeFile(task.WorktreePath, res.Path)
		after, afterExists = nil, false
		res.Operation = "delete"
	case "NotebookEdit":
		var in struct {
			NotebookPath string `json:"notebook_path"`
			NewSource    string `json:"new_source"`
		}
		if json.Unmarshal(use.Input, &in) != nil || in.NotebookPath == "" {
			return ProspectiveResult{}, false
		}
		res.Path = normalizeRepoPath(in.NotebookPath)
		before, beforeExists = ReadWorktreeFile(task.WorktreePath, res.Path)
		after, afterExists = []byte(in.NewSource), true
		res.Operation = mapWriteOp(beforeExists)
	default:
		// Bash 等无文件语义的工具：无前瞻性 Diff
		return ProspectiveResult{}, false
	}

	if IsBinaryContent(before) || IsBinaryContent(after) {
		res.Binary = true
		return res, true
	}
	res.Diff, res.Additions, res.Deletions = UnifiedDiff(res.Path,
		bytesOrEmpty(beforeExists, before), bytesOrEmpty(afterExists, after), 3)
	return res, true
}

// bytesOrEmpty 把 (content, exists) 折叠为字符串（不存在 → 空串）。
func bytesOrEmpty(exists bool, content []byte) string {
	if !exists {
		return ""
	}
	return string(content)
}

// mapWriteOp 已存在 → modify；不存在 → create。
func mapWriteOp(exists bool) string {
	if exists {
		return "modify"
	}
	return "create"
}

// replaceEdit 按 Edit 语义做预演替换（replace_all=false 只替换第一处；old 为空不替换）。
func replaceEdit(cur, oldStr, newStr string, all bool) string {
	if oldStr == "" {
		return cur
	}
	if all {
		return strings.ReplaceAll(cur, oldStr, newStr)
	}
	return strings.Replace(cur, oldStr, newStr, 1)
}
