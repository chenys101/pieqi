// git_diff.go 只读 Git 助手（ADR-0002：绝不写用户分支）+ 纯函数 unified diff 生成。
//
// 职责边界：
//   - Git 只作为「真实代码状态的校验/差异 Provider」，所有命令只读
//   - 单 Turn Diff 用纯函数 diff（before/after 内容可能来自 checkpoint 快照而非 git）
//   - Baseline 累计 Diff 优先走 `git diff <head> -- <paths>`（真实、含上下文）
package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gitCmdTimeout 单条 git 命令的超时（防止大 repo 卡死请求）。
const gitCmdTimeout = 15 * time.Second

// gitOutput 在 repo 下执行只读 git 命令（combined output）。
func gitOutput(repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return string(out), fmt.Errorf("git %s: %s", args[0], string(ee.Stderr))
		}
		return string(out), err
	}
	return string(out), nil
}

// GitHeadSHA 返回 repo 当前 HEAD（非 git repo / 无 commit 返回空串）。
func GitHeadSHA(repo string) string {
	out, err := gitOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// GitStatusPorcelain 返回 `git status --porcelain` 的脏文件列表（path 相对 repo 根）。
// 非 git repo 返回 nil。
func GitStatusPorcelain(repo string) []string {
	out, err := gitOutput(repo, "status", "--porcelain")
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		// 格式 "XY path"（X=暂存区状态，Y=工作区状态）；重命名是 "R  old -> new"
		p := strings.TrimSpace(line[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		p = normalizeRepoPath(p)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// GitCatFileAtHead 读取 repo 在指定 HEAD 下的文件内容；不存在返回 (nil, false)。
func GitCatFileAtHead(repo, head, path string) ([]byte, bool) {
	if head == "" || path == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "cat-file", "-e", head+":"+path)
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	out, err := gitOutput(repo, "cat-file", "-p", head+":"+path)
	if err != nil {
		return nil, false
	}
	return []byte(out), true
}

// GitDiffFiltered 只读 diff：`git diff <head> -- <paths>`，返回 unified diff 文本。
// head 为空时退化为 `git diff`（工作区 vs 暂存区，语义不完整，仅兜底）。
func GitDiffFiltered(repo, head string, paths []string) (string, error) {
	args := []string{"diff"}
	if head != "" {
		args = append(args, head)
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := gitOutput(repo, args...)
	return out, err
}

// numstatEntry numstat 的一行：additions/deletions 与路径。
type numstatEntry struct {
	Additions int
	Deletions int
	Path      string
}

// GitNumstatFiltered 只读统计：`git diff --numstat <head> -- <paths>`。
// 未跟踪文件不会出现（调用方需自行按全增补齐）。
func GitNumstatFiltered(repo, head string, paths []string) map[string]numstatEntry {
	args := []string{"diff", "--numstat"}
	if head != "" {
		args = append(args, head)
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := gitOutput(repo, args...)
	if err != nil {
		return nil
	}
	result := map[string]numstatEntry{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		add, errA := strconv.Atoi(fields[0])
		del, errB := strconv.Atoi(fields[1])
		if errA != nil || errB != nil {
			continue // 二进制文件 numstat 为 "-"，跳过
		}
		path := fields[len(fields)-1]
		if i := strings.Index(path, "\t"); i >= 0 { // "old => new" 重命名形态取 new
			path = strings.TrimPrefix(path[i+1:], "\t")
		}
		result[normalizeRepoPath(path)] = numstatEntry{Additions: add, Deletions: del, Path: path}
	}
	return result
}

// --- 纯函数 unified diff（单 Turn Diff 用，不依赖 git 进程） ---

// diffOp 一条行级差异指令。
type diffOp struct {
	Kind byte // '=' equal | '-' delete | '+' insert
	Text string
}

// diffLines 计算行级 LCS 差异。规模超限（防 O(n*m) 内存爆炸）时退化为整段替换。
func diffLines(a, b []string) []diffOp {
	const maxCells = 4 << 20 // 约 4M 单元（≈2048×2048 行）
	if len(a)*len(b) > maxCells {
		ops := make([]diffOp, 0, len(a)+len(b))
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
		return ops
	}
	// DP 求 LCS 长度
	n, m := len(a), len(b)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	// 回溯生成指令序列
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{'=', a[i]})
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// splitLines 按行切分（保留空行；结尾无换行也成一行）。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// UnifiedDiff 生成 a→b 的 unified diff 文本（含 @@ hunk 头，上下文默认 3 行）。
// 返回 (diff 文本, additions, deletions)。
func UnifiedDiff(path string, before, after string, context int) (string, int, int) {
	if before == after {
		return "", 0, 0
	}
	if context <= 0 {
		context = 3
	}
	a, b := splitLines(before), splitLines(after)
	ops := diffLines(a, b)

	var buf strings.Builder
	fmt.Fprintf(&buf, "--- a/%s\n+++ b/%s\n", path, path)

	additions, deletions := 0, 0
	// 找到所有需要保留的区间（差异行 ± context），再逐段输出 hunk
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.Kind != '=' {
			for k := i - context; k <= i+context; k++ {
				if k >= 0 && k < len(ops) {
					keep[k] = true
				}
			}
		}
	}

	i := 0
	for i < len(ops) {
		if !keep[i] {
			i++
			continue
		}
		// hunk 起点：向前扩 context（keep 已含），实际起点取 keep 连续段
		start := i
		end := i
		for end < len(ops) && keep[end] {
			end++
		}
		// 计算 hunk 双侧起始行号与行数
		aStart, bStart, aCount, bCount := 0, 0, 0, 0
		// 前置统计：从 0 扫到 start 前的 '='/'-' 计 a 行，'='/'+' 计 b 行
		for k := 0; k < start; k++ {
			switch ops[k].Kind {
			case '=':
				aStart, bStart = aStart+1, bStart+1
			case '-':
				aStart++
			case '+':
				bStart++
			}
		}
		for k := start; k < end; k++ {
			switch ops[k].Kind {
			case '=':
				aCount, bCount = aCount+1, bCount+1
			case '-':
				aCount++
				deletions++
			case '+':
				bCount++
				additions++
			}
		}
		// git 惯例：空侧起始行号显示 0（如 @@ -0,0 +1,2 @@），非空侧 1-based
		aNum, bNum := aStart, bStart
		if aCount > 0 {
			aNum = aStart + 1
		}
		if bCount > 0 {
			bNum = bStart + 1
		}
		fmt.Fprintf(&buf, "@@ -%d,%d +%d,%d @@\n", aNum, aCount, bNum, bCount)
		for k := start; k < end; k++ {
			buf.WriteByte(ops[k].Kind)
			buf.WriteString(ops[k].Text)
			buf.WriteByte('\n')
		}
		i = end
	}
	return buf.String(), additions, deletions
}

// IsBinaryContent 粗判二进制：首 8KB 含 NUL 字节视为二进制（与 git 启发式一致）。
func IsBinaryContent(content []byte) bool {
	const sniff = 8 * 1024
	if len(content) > sniff {
		content = content[:sniff]
	}
	return bytes.IndexByte(content, 0) >= 0
}

// CountLines 统计文本行数（供新文件全增统计）。
func CountLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	n := bytes.Count(content, []byte("\n"))
	if content[len(content)-1] != '\n' {
		n++
	}
	return n
}

// ReadWorktreeFile 读取 worktree 下的文件；不存在返回 (nil, false)。
// path 为相对 worktree 根的 repo 风格路径。
func ReadWorktreeFile(worktree, path string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
	if err != nil {
		return nil, false
	}
	return data, true
}
