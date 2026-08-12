package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestCommandScanner_Scan(t *testing.T) {
	dir := t.TempDir()
	// dev-task.md: 标题行 # /dev-task 描述
	os.WriteFile(filepath.Join(dir, "dev-task.md"), []byte("# /dev-task 需求开发登记\n\n## 触发方式\n```\n/dev-task <Jira>\n```\n"), 0644)
	// prod-issue.md: 标题行带描述
	os.WriteFile(filepath.Join(dir, "prod-issue.md"), []byte("# /prod-issue 生产问题排查\n\n正文"), 0644)
	// plain.md: 无标题，首行作描述
	os.WriteFile(filepath.Join(dir, "plain.md"), []byte("这是一个普通命令\n\n更多内容"), 0644)
	// 非 .md 文件应跳过
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0644)
	// 子目录应跳过（commands 只扫顶层 .md）
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "nested.md"), []byte("# /nested"), 0644)

	scanner := NewCommandScanner(zap.NewNop(), []string{dir})
	cmds := scanner.Scan()
	if len(cmds) != 3 {
		t.Fatalf("got %d commands: %+v", len(cmds), cmds)
	}
	byName := map[string]CommandInfo{}
	for _, c := range cmds {
		byName[c.Name] = c
	}
	if byName["dev-task"].Description != "需求开发登记" {
		t.Fatalf("dev-task desc=%q", byName["dev-task"].Description)
	}
	if byName["prod-issue"].Description != "生产问题排查" {
		t.Fatalf("prod-issue desc=%q", byName["prod-issue"].Description)
	}
	if byName["plain"].Description != "这是一个普通命令" {
		t.Fatalf("plain desc=%q", byName["plain"].Description)
	}
}

func TestCommandScanner_Dedup(t *testing.T) {
	// 两个目录都有同名 command：第一个目录优先
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "dup.md"), []byte("# /dup from dir1"), 0644)
	os.WriteFile(filepath.Join(dir2, "dup.md"), []byte("# /dup from dir2"), 0644)

	scanner := NewCommandScanner(zap.NewNop(), []string{dir1, dir2})
	cmds := scanner.Scan()
	if len(cmds) != 1 {
		t.Fatalf("got %d, want 1 (dedup): %+v", len(cmds), cmds)
	}
	if cmds[0].Description != "from dir1" {
		t.Fatalf("desc=%q, want from dir1 (first wins)", cmds[0].Description)
	}
}

func TestDefaultClaudeDirs(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/cfg")
	dirs := defaultClaudeDirs("commands")
	// CLAUDE_CONFIG_DIR/.claude/commands 应在结果中（路径分隔符跨平台，用 Contains 判断）
	found := false
	for _, d := range dirs {
		if strings.Contains(d, "custom") && strings.Contains(d, "commands") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CLAUDE_CONFIG_DIR/.claude/commands not in %v", dirs)
	}
}
