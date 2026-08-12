package core

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

// WorktreeManager 为每个任务在项目 repo 下创建并管理 git worktree。
//
// 每任务一个 worktree + 新分支 pieqi/<taskID>，让多个编码任务在同一 repo 上并行隔离。
// git worktree 操作碰 .git/worktrees/ 不并发安全，单个 sync.Mutex 串行所有 Create。
type WorktreeManager struct {
	logger      *zap.Logger
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

	// git worktree add -b <branch> <path> <baseBranch>：一条命令原子建分支+worktree
	out, err := runGit(ctx, project.RepoPath, "worktree", "add", "-b", branch, worktreePath, project.BaseBranch)
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w\n%s", err, out)
	}
	wm.logger.Info("worktree created",
		zap.String("project", project.ID),
		zap.String("branch", branch),
		zap.String("path", worktreePath),
	)
	return worktreePath, nil
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
