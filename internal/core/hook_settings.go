package core

// hook_settings.go 在 worktree 里写入 .claude/settings.json，配置 PreToolUse hook，
// 把 claude 的工具调用导向 `pieqi pre-tool-use` 子命令，从而触发 HookService 决策闭环。
//
// 协议事实来自本机生产 ~/.claude/settings.json（jira-permission.ps1 hook）：
//   - hooks.PreToolUse 是数组，每项 {matcher, hooks[]}，hook 项 {type:"command", command, timeout}
//   - timeout 单位秒；matcher 是单个工具名（不支持 "A|B" 正则），故每工具一个 matcher-group

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// hookEntry 单个 hook 命令项。
type hookEntry struct {
	Type    string `json:"type"`    // 固定 "command"
	Command string `json:"command"` // 完整命令行
	Timeout int    `json:"timeout"` // 秒
}

// preToolUseMatcher 一个 matcher-group：匹配某工具名 + 该组下的 hook 列表。
type preToolUseMatcher struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

// settingsFile .claude/settings.json 的可序列化结构（只含本项目用到的字段）。
type settingsFile struct {
	Hooks struct {
		PreToolUse []preToolUseMatcher `json:"PreToolUse"`
	} `json:"hooks"`
}

// WriteHookSettings 往 worktreePath/.claude/settings.json 写入 PreToolUse hook 配置。
//
// hookCmd 是调 pieqi pre-tool-use 的完整命令行（含 --task/--port）。
// tools 是要拦截的工具名列表（如 ["Bash","Write","Edit","NotebookEdit"]）。
// timeoutSec 是 hook 等人类决策的上限（秒），应 ≥ HookService 超时，否则 hook 先超时放行/拒绝。
//
// 每个 tool 一个 matcher-group，共享同一 hookCmd。worktree 是新建隔离目录，直接覆盖写。
func WriteHookSettings(worktreePath, hookCmd string, tools []string, timeoutSec int) error {
	settings := settingsFile{}
	for _, t := range tools {
		settings.Hooks.PreToolUse = append(settings.Hooks.PreToolUse, preToolUseMatcher{
			Matcher: t,
			Hooks:   []hookEntry{{Type: "command", Command: hookCmd, Timeout: timeoutSec}},
		})
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	claudeDir := filepath.Join(worktreePath, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)
}

// buildHookCmd 构造 PreToolUse hook 的 command 字符串。
// execPath 是 pieqi 可执行文件绝对路径；taskID 是当前任务 ID；port 是主进程端口。
// 返回形如 `"C:\...\pieqi.exe" pre-tool-use --task <id> --port <port>`。
func buildHookCmd(execPath, taskID string, port int) string {
	return `"` + execPath + `" pre-tool-use --task ` + taskID + ` --port ` + strconv.Itoa(port)
}
