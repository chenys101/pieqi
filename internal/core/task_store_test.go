package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pieqi/internal/model"
)

func TestTaskStore_CreateGet(t *testing.T) {
	s, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := s.Create(&model.Task{
		Source:      model.SourceHTTP,
		ProjectID:   "cb",
		ProjectPath: "G:/repo",
		Prompt:      "fix bug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tt.ID == "" || tt.ClaudeSessionID == "" {
		t.Fatal("Create should assign ID and ClaudeSessionID")
	}
	if tt.Status != model.TaskPending {
		t.Fatalf("status = %s, want pending", tt.Status)
	}

	got, ok := s.Get(tt.ID)
	if !ok {
		t.Fatal("Get failed after Create")
	}
	if got.Prompt != "fix bug" {
		t.Fatalf("prompt = %q", got.Prompt)
	}
	// Get returns a copy: mutating it must not affect store
	got.Prompt = "mutated"
	got2, _ := s.Get(tt.ID)
	if got2.Prompt != "fix bug" {
		t.Fatal("Get did not return a copy")
	}
}

func TestTaskStore_Update(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	tt, _ := s.Create(&model.Task{ProjectID: "cb", Prompt: "p"})

	updated, err := s.Update(tt.ID, func(t *model.Task) bool {
		t.Status = model.TaskRunning
		t.WorktreePath = "/wt"
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.TaskRunning || updated.WorktreePath != "/wt" {
		t.Fatalf("update not applied: %+v", updated)
	}
	if updated.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be set after change")
	}

	// mutator returning false = no change, no error
	_, err = s.Update(tt.ID, func(t *model.Task) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskStore_UpdateNotFound(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	_, err := s.Update("nope", func(t *model.Task) bool { return true })
	if err == nil {
		t.Fatal("Update on missing task should error")
	}
}

func TestTaskStore_Delete(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	tt, _ := s.Create(&model.Task{ProjectID: "cb"})
	if err := s.Delete(tt.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(tt.ID); ok {
		t.Fatal("task should be gone after Delete")
	}
	if err := s.Delete(tt.ID); err == nil {
		t.Fatal("double Delete should error")
	}
}

func TestTaskStore_ListSortedByCreated(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	a, _ := s.Create(&model.Task{ProjectID: "cb"})
	time.Sleep(time.Millisecond)
	b, _ := s.Create(&model.Task{ProjectID: "cb"})
	time.Sleep(time.Millisecond)
	c, _ := s.Create(&model.Task{ProjectID: "cb"})

	list := s.List()
	if len(list) != 3 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].ID != a.ID || list[1].ID != b.ID || list[2].ID != c.ID {
		t.Fatal("List not sorted by CreatedAt")
	}
}

func TestTaskStore_RestoreRecoversAndMarksOrphans(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewTaskStore(dir)

	// 一个完成的、一个 running 的（模拟重启前还在跑）
	done, _ := s1.Create(&model.Task{ProjectID: "cb", Prompt: "done"})
	_, _ = s1.Update(done.ID, func(t *model.Task) bool { t.Status = model.TaskCompleted; return true })

	running, _ := s1.Create(&model.Task{ProjectID: "cb", Prompt: "wip"})
	_, _ = s1.Update(running.ID, func(t *model.Task) bool {
		t.Status = model.TaskRunning
		t.WorktreePath = "/wt"
		return true
	})
	waiting, _ := s1.Create(&model.Task{ProjectID: "cb", Prompt: "stuck"})
	_, _ = s1.Update(waiting.ID, func(t *model.Task) bool {
		t.Status = model.TaskWaitingInput
		t.CurrentDecision = &model.Decision{ID: "d1", ToolName: "Bash"}
		return true
	})

	// 重新打开同一个目录，模拟重启
	s2, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := s2.List()
	if len(list) != 3 {
		t.Fatalf("recovered len = %d, want 3", len(list))
	}
	for _, tt := range list {
		if tt.Status == model.TaskRunning || tt.Status == model.TaskWaitingInput {
			t.Fatalf("orphan task %s should be marked failed, got %s", tt.ID, tt.Status)
		}
		if tt.Status == model.TaskFailed && tt.Error == "" {
			t.Fatalf("failed orphan %s should have error message", tt.ID)
		}
	}
	// 完成的那个应仍是 completed
	for _, tt := range list {
		if tt.ID == done.ID && tt.Status != model.TaskCompleted {
			t.Fatalf("completed task should survive restart, got %s", tt.Status)
		}
	}
}

func TestTaskStore_CreateFillsDefaults(t *testing.T) {
	s, _ := NewTaskStore(t.TempDir())
	tt, err := s.Create(&model.Task{})
	if err != nil {
		t.Fatal(err)
	}
	if tt.ID == "" {
		t.Fatal("ID should default to uuid")
	}
	if tt.ClaudeSessionID == "" {
		t.Fatal("ClaudeSessionID should default to uuid")
	}
	if tt.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set")
	}
}

// TestTaskStore_ConcurrentUpdateSameTask 并发更新**同一个**任务必须全部成功。
//
// 回归对象：persist 用固定路径 `<id>.json.tmp` 且 Update 先释放锁再落盘，
// 于是同一任务的两个并发 Update 会同时操作那个 tmp 文件：一个还在
// os.WriteFile（句柄打开、Windows 默认 share mode 不带 FILE_SHARE_DELETE），
// 另一个已经 os.Rename → ERROR_SHARING_VIOLATION
// 「The process cannot access the file because it is being used by another
// process.」——生产上表现为间歇性 `record intervention failed`。
//
// 该形态在生产里是常态而非理论值：ACP 流式路径每个 delta 一次 Update
// （agent_stream.appendTextDelta），同时权限回调（agent_perm）与用户干预
// （RecordIntervention）也在写同一个 task。
func TestTaskStore_ConcurrentUpdateSameTask(t *testing.T) {
	s, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := s.Create(&model.Task{ProjectID: "cb", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	// 先把 payload 撑到几百 KB，把「tmp 正被打开」的时间窗放大到可稳定命中。
	blob := strings.Repeat("x", 64*1024)
	if _, err := s.Update(tt.ID, func(t *model.Task) bool {
		for i := 0; i < 8; i++ {
			s.AppendEvent(t, model.TaskEvent{Type: model.EventText, Text: blob})
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}

	const (
		goroutines = 8
		rounds     = 10
	)
	var (
		wg      sync.WaitGroup
		applied atomic.Int64
	)
	errCh := make(chan error, goroutines*rounds)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if _, err := s.Update(tt.ID, func(t *model.Task) bool {
					s.AppendEvent(t, model.TaskEvent{Type: model.EventText, Text: "e"})
					applied.Add(1)
					return true
				}); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Update failed: %v", err)
	}

	// 并发写完之后磁盘上必须是一份**可解析**的完整快照（不能是半个 tmp）。
	data, err := os.ReadFile(filepath.Join(s.tasksDir, tt.ID+".json"))
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	var onDisk model.Task
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("persisted file is not valid JSON: %v", err)
	}
	// 不变量：**每一个已应用的变更都必须落到磁盘**（8 个初始 + applied）。
	// 旧快照的 Rename 后落地时这里会偏少 —— 即「事件被写回退」。
	want := 8 + int(applied.Load())
	if got := len(onDisk.Events); got != want {
		t.Errorf("persisted events = %d, want %d（有变更落盘丢失/回退）", got, want)
	}
	// tmp 不该有任何残留。
	matches, _ := filepath.Glob(filepath.Join(s.tasksDir, tt.ID+".json.tmp*"))
	if len(matches) != 0 {
		t.Errorf("leftover tmp files: %v", matches)
	}
}

// TestTaskStore_SnapshotNotAliasedByInPlaceMutation 取到的副本必须与活体**解耦**。
//
// 回归对象：`cp := *t` 的浅拷贝让副本与活体共享 Events 底层数组，而流式路径
// （agent_stream.appendTextDelta）与批量合并（task_runner.appendEvent）都是
// **原地改写最后一个元素**（`last.Text += text`、`last.At = now`）。于是
// 「取快照 → 边流式落盘边把快照序列化给前端」这条真实路径上，读到的字段正被
// 另一个 goroutine 改写。这里不依赖 race 检测器（本机 CGO 不可用，跑不了 -race），
// 直接用「副本内容会不会被后续写入改掉」来判定别名是否泄漏。
func TestTaskStore_SnapshotNotAliasedByInPlaceMutation(t *testing.T) {
	s, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := s.Create(&model.Task{ProjectID: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(tt.ID, func(t *model.Task) bool {
		s.AppendEvent(t, model.TaskEvent{Type: model.EventText, Text: "a"})
		s.AppendEvent(t, model.TaskEvent{Type: model.EventText, Text: "b"})
		return true
	}); err != nil {
		t.Fatal(err)
	}

	// Get 拿到的副本
	got, ok := s.Get(tt.ID)
	if !ok {
		t.Fatal("Get failed")
	}
	// Update 返回的副本（会被 publish 到 bus / WS）
	upd, err := s.Update(tt.ID, func(t *model.Task) bool { t.Title = "t"; return true })
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("List len = %d", len(list))
	}

	before := []string{got.Events[1].Text, upd.Events[1].Text, list[0].Events[1].Text}
	beforeAt := []time.Time{got.Events[1].At, upd.Events[1].At, list[0].Events[1].At}

	// 模拟流式路径的原地追加（必须命中原先那个最后一个元素）
	time.Sleep(time.Millisecond)
	if _, err := s.Update(tt.ID, func(t *model.Task) bool {
		last := &t.Events[len(t.Events)-1]
		last.Text += "MUTATED"
		last.At = time.Now()
		return true
	}); err != nil {
		t.Fatal(err)
	}

	after := []string{got.Events[1].Text, upd.Events[1].Text, list[0].Events[1].Text}
	afterAt := []time.Time{got.Events[1].At, upd.Events[1].At, list[0].Events[1].At}
	names := []string{"Get", "Update 返回值", "List"}
	for i := range names {
		if after[i] != before[i] {
			t.Errorf("%s 的副本被后续写入改动了：%q -> %q（Events 底层数组别名泄漏）",
				names[i], before[i], after[i])
		}
		if !afterAt[i].Equal(beforeAt[i]) {
			t.Errorf("%s 的副本 At 被后续写入改动了（Events 底层数组别名泄漏）", names[i])
		}
	}
	// 活体本身当然要变（否则说明上面那次 Update 没生效）
	live, _ := s.Get(tt.ID)
	if !strings.HasSuffix(live.Events[1].Text, "MUTATED") {
		t.Fatalf("live task should have been mutated, got %q", live.Events[1].Text)
	}
}

// BenchmarkTaskStore_UpdateLargeTask 量一下「大任务上一次 Update」的开销。
// 关注的是 snapshot 的元素数组拷贝有没有把热路径拖垮 —— 流式路径每个 delta 一次
// Update，任务文件实测最大 384KB。
func BenchmarkTaskStore_UpdateLargeTask(b *testing.B) {
	s, err := NewTaskStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	tt, err := s.Create(&model.Task{ProjectID: "cb"})
	if err != nil {
		b.Fatal(err)
	}
	blob := strings.Repeat("x", 1600)
	if _, err := s.Update(tt.ID, func(t *model.Task) bool {
		for i := 0; i < 200; i++ {
			s.AppendEvent(t, model.TaskEvent{Type: model.EventText, Text: blob})
		}
		return true
	}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Update(tt.ID, func(t *model.Task) bool {
			last := &t.Events[len(t.Events)-1]
			last.Text += "x"
			return true
		}); err != nil {
			b.Fatal(err)
		}
	}
}
