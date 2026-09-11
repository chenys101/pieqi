package core

import (
	"os"
	"path/filepath"
	"testing"

	"pieqi/internal/model"
)

// TestSnapshotDiffStat_NilCases 取不到数据时返回 nil，而不是返回一个全是 0 的壳。
//
// 这条看着琐碎，实际是概览的可信度底线：`{files:0, additions:0}` 和"没统计到"
// 在 UI 上是完全不同的两件事，前者会被当成"这周一行没改"。
// 我们现在只持久化非 nil 的结果，所以这两个状态不会混。
func TestSnapshotDiffStat_NilCases(t *testing.T) {
	cases := []struct {
		name string
		task *model.Task
	}{
		{"nil task", nil},
		{"no worktree", &model.Task{}},
		{"worktree dir missing", &model.Task{WorktreePath: filepath.Join(t.TempDir(), "nope")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SnapshotDiffStat(c.task); got != nil {
				t.Fatalf("want nil, got %+v", got)
			}
		})
	}
}

// TestSnapshotDiffStat_CleanRepoReturnsNil 有 Baseline 但一行未改 → nil。
// Agent 跑了一圈什么都没动时，"0 个文件" 不是概览该显示的指标。
func TestSnapshotDiffStat_CleanRepoReturnsNil(t *testing.T) {
	repo := newGitRepo(t)
	task := &model.Task{WorktreePath: repo, Baseline: &model.TaskBaseline{HeadSHA: GitHeadSHA(repo)}}
	if got := SnapshotDiffStat(task); got != nil {
		t.Fatalf("clean repo should be nil, got %+v", got)
	}
}

// TestSnapshotDiffStat_TrackedModify 已跟踪文件的修改要精确统计增删。
func TestSnapshotDiffStat_TrackedModify(t *testing.T) {
	repo := newGitRepo(t)
	head := GitHeadSHA(repo)
	// 原内容是 "head version\n"：删掉它、换成 3 行 → 增删各不同，故意不让两者相等（能抓到混淆）
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("line a\nline b\nline c\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := SnapshotDiffStat(&model.Task{WorktreePath: repo, Baseline: &model.TaskBaseline{HeadSHA: head}})
	if got == nil {
		t.Fatal("expect non-nil")
	}
	if got.Files != 1 {
		t.Errorf("files = %d, want 1", got.Files)
	}
	if got.Additions != 3 {
		t.Errorf("additions = %d, want 3", got.Additions)
	}
	if got.Deletions != 1 {
		t.Errorf("deletions = %d, want 1", got.Deletions)
	}
}

// TestSnapshotDiffStat_UntrackedCounted **新建文件不能被漏掉**。
//
// 这是最容易被漏的一类：`git diff --numstat <head>` 只看得见已跟踪文件，
// 而 Agent 新建文件恰恰是最典型的产出。漏掉它会让"代码改动"严重低估，
// 而且错得很隐蔽 —— 数字看着合理，只是偏小。
func TestSnapshotDiffStat_UntrackedCounted(t *testing.T) {
	repo := newGitRepo(t)
	head := GitHeadSHA(repo)
	if err := os.WriteFile(filepath.Join(repo, "brand-new.txt"), []byte("a\nb\nc\nd\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := SnapshotDiffStat(&model.Task{WorktreePath: repo, Baseline: &model.TaskBaseline{HeadSHA: head}})
	if got == nil {
		t.Fatal("untracked file must be counted")
	}
	if got.Files != 1 {
		t.Errorf("files = %d, want 1", got.Files)
	}
	if got.Additions != 4 {
		t.Errorf("additions = %d, want 4 (untracked 全增)", got.Additions)
	}
	if got.Deletions != 0 {
		t.Errorf("deletions = %d, want 0", got.Deletions)
	}
}

// TestSnapshotDiffStat_MixedNoDoubleCount tracked 与 untracked 混合时，文件数不能重复计数。
// 实现是 "porcelain 差集"，两边可能同时命中同一个路径 —— 必须靠 map 去重。
func TestSnapshotDiffStat_Mixed(t *testing.T) {
	repo := newGitRepo(t)
	head := GitHeadSHA(repo)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("head version\nplus one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\ny\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new2.txt"), []byte("z\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := SnapshotDiffStat(&model.Task{WorktreePath: repo, Baseline: &model.TaskBaseline{HeadSHA: head}})
	if got == nil {
		t.Fatal("expect non-nil")
	}
	// tracked.txt 改动 1 文件；new.txt / new2.txt 各 1 文件
	if got.Files != 3 {
		t.Errorf("files = %d, want 3", got.Files)
	}
	// additions: tracked +1 行，new.txt +2 行，new2.txt +1 行
	if got.Additions != 4 {
		t.Errorf("additions = %d, want 4", got.Additions)
	}
}

// TestSnapshotDiffStat_OnlyCompletedPersists **只有 completed 会落 DiffStat**。
//
// 这段测的是 snapshotDiffStat 的门控，而不是 SnapshotDiffStat 本身：
// 「终态都要调 captureTurnEnd」和「只有 completed 才有产出」是两条独立的规则，
// 前者差一行就全丢，后者差一行会把失败任务的改动算成产出。
func TestSnapshotDiffStat_OnlyCompletedPersists(t *testing.T) {
	cases := []struct {
		name    string
		status  model.TaskStatus
		wantNil bool
	}{
		{"completed", model.TaskCompleted, false},
		{"failed", model.TaskFailed, true},
		{"cancelled", model.TaskCancelled, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := newGitRepo(t)
			head := GitHeadSHA(repo)
			// 先让仓库脏起来，保证 SnapshotDiffStat 一定算得出东西
			if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("a\nb\n"), 0644); err != nil {
				t.Fatal(err)
			}
			tr, store, _, _ := newTestTaskRunner(t)
			task, _ := store.Create(&model.Task{
				ProjectID: "demo", Prompt: "p", WorktreePath: repo,
				Baseline: &model.TaskBaseline{HeadSHA: head},
			})
			store.Update(task.ID, func(t2 *model.Task) bool { t2.Status = c.status; return true })

			tr.snapshotDiffStat(task)

			got, _ := store.Get(task.ID)
			if c.wantNil && got.DiffStat != nil {
				t.Errorf("%s: DiffStat 应为空，got %+v", c.name, got.DiffStat)
			}
			if !c.wantNil {
				if got.DiffStat == nil {
					t.Fatalf("%s: DiffStat 应已持久化", c.name)
				}
				if got.DiffStat.Additions != 2 {
					t.Errorf("%s: additions = %d, want 2", c.name, got.DiffStat.Additions)
				}
			}
		})
	}
}

// TestSnapshotDiffStat_StatFailedKeepsPrevious 统计取不到时不写入，因此不会把已有的
// 好数据冲成 nil。这保护的是续问场景（终态→running→completed 会二次走到这里）。
func TestSnapshotDiffStat_StatFailedKeepsPrevious(t *testing.T) {
	repo := newGitRepo(t)
	head := GitHeadSHA(repo)
	tr, store, _, _ := newTestTaskRunner(t)
	task, _ := store.Create(&model.Task{
		ProjectID: "demo", Prompt: "p", WorktreePath: repo,
		Baseline: &model.TaskBaseline{HeadSHA: head},
	})
	store.Update(task.ID, func(t *model.Task) bool { t.Status = model.TaskCompleted; return true })

	// ① 仓库干净 → SnapshotDiffStat 返回 nil → 不应写入
	tr.snapshotDiffStat(task)
	if got, _ := store.Get(task.ID); got.DiffStat != nil {
		t.Fatalf("干净仓库不应写入 DiffStat，got %+v", got.DiffStat)
	}

	// ② 之后产生了改动 → 应写入
	if err := os.WriteFile(filepath.Join(repo, "later.txt"), []byte("x\ny\nz\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tr.snapshotDiffStat(task)
	if got, _ := store.Get(task.ID); got.DiffStat == nil || got.DiffStat.Additions != 3 {
		t.Fatalf("改动后应写入 DiffStat 且 additions=3, got %+v", got.DiffStat)
	}
}
