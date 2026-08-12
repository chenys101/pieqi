package core

// command_scanner.go 扫描 Claude Code 的 .claude/commands/ 目录，读用户自定义命令。
//
// command 是 .md 文件，文件名即命令名（如 dev-task.md -> /dev-task），
// 内容是 prompt 模板。与 skill 的区别：skill 有 SKILL.md frontmatter 且是能力定义，
// command 是纯 prompt 模板，/name 触发时把内容作为 prompt 发送。
//
// 默认扫描 $CLAUDE_CONFIG_DIR/.claude/commands + ~/.claude/commands，去重。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"
)

// CommandInfo 一个用户自定义命令的摘要。
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Dir         string `json:"dir"`
}

// CommandScanner 扫描 .claude/commands/ 目录。
type CommandScanner struct {
	logger       *zap.Logger
	commandsDirs []string
}

// NewCommandScanner 创建扫描器。dirs 空则用默认：$CLAUDE_CONFIG_DIR/.claude/commands + ~/.claude/commands。
func NewCommandScanner(logger *zap.Logger, dirs []string) *CommandScanner {
	if len(dirs) == 0 {
		dirs = defaultClaudeDirs("commands")
	}
	return &CommandScanner{logger: logger, commandsDirs: dirs}
}

// Scan 扫描所有 commandsDirs，返回去重后的命令列表（按 name 排序）。
func (s *CommandScanner) Scan() []CommandInfo {
	seen := map[string]CommandInfo{}
	for _, dir := range s.commandsDirs {
		s.scanDir(dir, seen)
	}
	out := make([]CommandInfo, 0, len(seen))
	for _, c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *CommandScanner) scanDir(dir string, seen map[string]CommandInfo) {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		path := filepath.Join(dir, e.Name())
		desc := parseCommandDescription(path)
		if _, exists := seen[name]; !exists {
			seen[name] = CommandInfo{Name: name, Description: desc, Dir: dir}
		}
	}
}

// parseCommandDescription 从 .md 内容提取描述：取首个非空、非标题标记的行。
// 优先用 "# /name 描述" 标题行去掉前缀；否则取首段第一行。
func parseCommandDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 标题行：# /name 描述 -> 取标题文本
		if strings.HasPrefix(line, "#") {
			t := strings.TrimSpace(strings.TrimLeft(line, "#"))
			// 去掉开头的 /name
			if strings.HasPrefix(t, "/") {
				if sp := strings.IndexByte(t, ' '); sp >= 0 {
					return strings.TrimSpace(t[sp+1:])
				}
				return ""
			}
			return t
		}
		// 非标题的首行作为描述
		return line
	}
	return ""
}

// defaultClaudeDirs 返回 Claude 配置目录下的子目录（如 "commands"/"skills"）。
// 包含 $CLAUDE_CONFIG_DIR/.claude/<sub> 和 ~/.claude/<sub>，去重。
func defaultClaudeDirs(sub string) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		dirs = append(dirs, p)
	}
	// CLAUDE_CONFIG_DIR（用户自定义配置目录，如 F:\appData\AICache）
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		add(filepath.Join(cfg, ".claude", sub))
	}
	// 默认 ~/.claude
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".claude", sub))
	}
	return dirs
}
