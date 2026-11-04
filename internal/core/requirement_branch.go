package core

import (
	"context"
	"fmt"
	"strings"

	"pieqi/internal/model"
)

const reqContextMarker = "pieqi 需求上下文"

// IntegrationBranchName 需求集成分支名：req/<id前8位>（去连字符）。
func IntegrationBranchName(requirementID string) string {
	id := strings.ReplaceAll(strings.TrimSpace(requirementID), "-", "")
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		id = "unknown"
	}
	return "req/" + strings.ToLower(id)
}

// EnsureIntegrationBranch 若分支不存在则从仓库默认基线（resolveBaseRef）拉出。
// 已存在则原样返回。不切换当前工作区检出。
func EnsureIntegrationBranch(ctx context.Context, repoPath, branch string) error {
	branch = strings.TrimSpace(branch)
	if repoPath == "" || branch == "" {
		return fmt.Errorf("repo path and branch required")
	}
	if gitRefExists(ctx, repoPath, branch) {
		return nil
	}
	base, err := resolveBaseRef(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("resolve base for integration branch: %w", err)
	}
	out, err := runGit(ctx, repoPath, "branch", branch, base)
	if err != nil {
		return fmt.Errorf("git branch %s from %s: %w\n%s", branch, base, err, out)
	}
	return nil
}

// FormatRequirementExecContext 实现/test 票开跑时追加的需求上下文（含需求分支）。
func FormatRequirementExecContext(r *model.Requirement, task *model.Task) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## ")
	b.WriteString(reqContextMarker)
	b.WriteString("\n")
	b.WriteString("- 需求 id：")
	b.WriteString(r.ID)
	b.WriteString("\n- 标题：")
	b.WriteString(r.Title)
	b.WriteString("\n")
	if br := strings.TrimSpace(r.IntegrationBranch); br != "" {
		b.WriteString("- **需求分支（合入目标）**：`")
		b.WriteString(br)
		b.WriteString("`\n")
		b.WriteString("  - worktree 已基于该分支创建；本票完成后请 merge 回此分支，不要直接推 main。\n")
	} else {
		b.WriteString("- 需求分支：尚未登记（异常）；合入目标请向用户确认。\n")
	}
	if task != nil && len(task.CoversAC) > 0 {
		b.WriteString("- 本票 covers_ac：")
		b.WriteString(strings.Join(task.CoversAC, ", "))
		b.WriteString("\n")
	}
	if len(r.Acceptance) > 0 {
		b.WriteString("- 验收 AC：\n")
		for _, ac := range r.Acceptance {
			if ac.Deferred {
				continue
			}
			b.WriteString("  - ")
			b.WriteString(ac.ID)
			b.WriteString(": ")
			b.WriteString(ac.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// AppendRequirementContext 若尚未注入则追加需求上下文。
func AppendRequirementContext(prompt, ctxBlock string) string {
	prompt = strings.TrimRight(prompt, " \t\n")
	ctxBlock = strings.TrimSpace(ctxBlock)
	if ctxBlock == "" {
		return prompt
	}
	if strings.Contains(prompt, reqContextMarker) {
		return prompt
	}
	return prompt + "\n" + ctxBlock
}
