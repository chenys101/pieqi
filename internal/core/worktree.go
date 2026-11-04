package core

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"pieqi/internal/exechide"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// WorktreeManager 为每个任务在项目 repo 下创建并管理 git worktree。
//
// 每任务一个 worktree + 新分支 pieqi/<taskID>，让多个编码任务在同一 repo 上并行隔离。
// git worktree 操作碰 .git/worktrees/ 不并发安全，单个 sync.Mutex 串行所有 Create。
type WorktreeManager struct {
	logger       *zap.Logger
	worktreeBase string

	mu sync.Mutex
}

// NewWorktreeManager 创建管理器。worktreeBase 为所有 worktree 的根目录。
func NewWorktreeManager(logger *zap.Logger, worktreeBase string) *WorktreeManager {
	return &WorktreeManager{
		logger:       logger,
		worktreeBase: worktreeBase,
	}
}

// Create 在 project 的 repo 下建一个 worktree + 新分支 pieqi/<taskID>。
// 返回 worktree 绝对路径。
func (wm *WorktreeManager) Create(ctx context.Context, project *model.Project, taskID string) (string, error) {
	branch := "pieqi/" + taskID
	worktreePath := filepath.Join(wm.worktreeBase, project.ID, taskID)

	wm.mu.Lock()
	defer wm.mu.Unlock()

	base := strings.TrimSpace(project.BaseRef)
	if base == "" {
		var err error
		base, err = resolveBaseRef(ctx, project.RepoPath)
		if err != nil {
			return "", fmt.Errorf("resolve base branch: %w", err)
		}
	} else if !gitRefExists(ctx, project.RepoPath, base) {
		return "", fmt.Errorf("base ref %q not found in repo", base)
	}

	// git worktree add -b <branch> <path> <base>：一条命令原子建分支+worktree
	out, err := runGit(ctx, project.RepoPath, "worktree", "add", "-b", branch, worktreePath, base)
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w\n%s", err, out)
	}
	wm.logger.Info("worktree created",
		zap.String("project", project.ID),
		zap.String("branch", branch),
		zap.String("base", base),
		zap.String("path", worktreePath),
	)
	return worktreePath, nil
}

// resolveBaseRef 按**该仓库自身**选出 worktree 起点。
// 顺序：origin/HEAD（远端默认分支）→ main → master → 当前 HEAD。
// 判据：GitHub 默认 main、业务仓常为 master，全局配置无法覆盖「每项目不同」；
// 远端默认分支才是事实源。无 remote 时再试常见名与当前检出。
func resolveBaseRef(ctx context.Context, repoPath string) (string, error) {
	if out, err := runGit(ctx, repoPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(out) // e.g. origin/main
		if ref != "" {
			if short, ok := strings.CutPrefix(ref, "origin/"); ok && short != "" {
				if gitRefExists(ctx, repoPath, short) {
					return short, nil
				}
			}
			if gitRefExists(ctx, repoPath, ref) {
				return ref, nil
			}
		}
	}
	for _, cand := range []string{"main", "master"} {
		if gitRefExists(ctx, repoPath, cand) {
			return cand, nil
		}
	}
	if out, err := runGit(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		ref := strings.TrimSpace(out)
		if ref != "" && ref != "HEAD" && gitRefExists(ctx, repoPath, ref) {
			return ref, nil
		}
	}
	return "", fmt.Errorf("no usable base branch (tried origin/HEAD, main, master, HEAD)")
}

func gitRefExists(ctx context.Context, repoPath, ref string) bool {
	_, err := runGit(ctx, repoPath, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// Cleanup 删除 worktree 及其分支。对已删除的 worktree 幂等（吞 not found）。
func (wm *WorktreeManager) Cleanup(ctx context.Context, project *model.Project, taskID, worktreePath string) error {
	branch := "pieqi/" + taskID

	wm.mu.Lock()
	defer wm.mu.Unlock()

	// 先删 worktree（即使路径已不存在也继续）
	if worktreePath != "" {
		out, err := runGit(ctx, project.RepoPath, "worktree", "remove", "--force", worktreePath)
		if err != nil && !isNotFound(out, err) {
			wm.logger.Warn("git worktree remove", zap.Error(err), zap.String("out", out))
		}
	}
	// 再删分支
	out, err := runGit(ctx, project.RepoPath, "branch", "-D", branch)
	if err != nil && !isNotFound(out, err) {
		wm.logger.Warn("git branch delete", zap.Error(err), zap.String("out", out))
	}
	return nil
}

// runGit 在 dir 下执行 git 命令，返回 combined output。
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	exechide.Command(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// isNotFound 判断 git 输出是否为「不存在」类错误，用于 Cleanup 幂等。
func isNotFound(out string, err error) bool {
	if err == nil {
		return false
	}
	l := strings.ToLower(out)
	return strings.Contains(l, "not a working tree") ||
		strings.Contains(l, "not a git worktree") ||
		strings.Contains(l, "no such file or directory") ||
		strings.Contains(l, "does not exist") ||
		strings.Contains(l, "not found") ||
		strings.Contains(l, "already used") // worktree prune edge cases
}
