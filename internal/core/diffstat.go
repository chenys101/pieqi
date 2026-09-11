package core

import (
	"time"

	"pieqi/internal/model"
)

// SnapshotDiffStat 固化任务截止当下对 Baseline 的累计代码改动。
//
// **为什么必须在终态当场算**：这是随时间丢失的数据。worktree 一旦被清理，
// `git diff` 就再也取不到当时的结果——而「本周概览」这类回顾性视图要在任务结束很久之后
// 继续展示它。当时没存，事后一条都补不回来。
//
// **为什么只给 completed 任务算**：概览要回答的是"这周产出多少"。
// failed/cancelled 的改动不是有效产出，计入会把概览变成一根被失败任务撑起来的柱子。
// 这里的判断在**调用方**（task_runner.captureTurnEnd），本函数不重复判 status。
//
// 返回值 nil 表示"取不到"（非 git 项目 / worktree 不存在 / 没有改动），调用方据此跳过持久化。
func SnapshotDiffStat(t *model.Task) *model.DiffStat {
	if t == nil || t.WorktreePath == "" {
		return nil
	}
	// head 参照取 Baseline.HeadSHA：它记录的是 Task 创建那一刻的起点，
	// 只有相对它才能叫"本次任务的累计改动"。退化到 HEAD 会把 Task 期间之外的提交也算进来。
	head := ""
	if t.Baseline != nil {
		head = t.Baseline.HeadSHA
	}

	// ① tracked：git diff --numstat <head>（全量，不传 paths —— worktree 是隔离的，全仓即本任务）
	files, add, del := 0, 0, 0
	tracked := GitNumstatFiltered(t.WorktreePath, head, nil)
	for _, e := range tracked {
		files++
		add += e.Additions
		del += e.Deletions
	}

	// ② untracked：numstat 看不见，但它是本次任务真实新建的产出，漏掉会严重低估。
	// porcelain 返回全部脏路径，减去 numstat 已覆盖的，剩下的就是未跟踪文件 → 全增。
	if dirty := GitStatusPorcelain(t.WorktreePath); len(dirty) > 0 {
		for _, p := range dirty {
			if _, ok := tracked[p]; ok {
				continue
			}
			content, ok := ReadWorktreeFile(t.WorktreePath, p)
			if !ok || IsBinaryContent(content) {
				continue // 二进制不数行，但计数也不失真到需要单独处理的程度
			}
			n := CountLines(content)
			if n == 0 {
				continue
			}
			files++
			add += n
		}
	}

	if files == 0 {
		return nil
	}
	return &model.DiffStat{Files: files, Additions: add, Deletions: del, CapturedAt: time.Now()}
}
