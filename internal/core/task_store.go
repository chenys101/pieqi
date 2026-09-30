package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
)

// TaskStore 任务的内存索引 + 文件持久化。
//
// 一任务一文件 data/tasks/<id>.json，每次状态变更加 os.Rename 原子写。
// 启动时遍历 data/tasks/ 重建索引；waiting_input 的孤儿任务（claude 子进程已死）
// 标记 failed，保留 worktree 供手动 resume。
type TaskStore struct {
	mu       sync.RWMutex
	tasksDir string
	tasks    map[string]*model.Task
	// eventRetention 单任务事件保留上限（0 = 全部保留，见 AppendEvent）。
	// 与 tasks 共用 mu：它在 Update 的 mutator 内被读取。
	eventRetention int
}

// NewTaskStore 创建并从磁盘恢复任务索引。
func NewTaskStore(tasksDir string) (*TaskStore, error) {
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir tasks dir: %w", err)
	}
	s := &TaskStore{
		tasksDir: tasksDir,
		tasks:    make(map[string]*model.Task),
	}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("load tasks: %w", err)
	}
	return s, nil
}

// Create 新建一个 pending 任务并持久化。
func (s *TaskStore) Create(t *model.Task) (*model.Task, error) {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.ClaudeSessionID == "" {
		t.ClaudeSessionID = uuid.New().String()
	}
	now := time.Now()
	t.Status = model.TaskPending
	t.CreatedAt = now
	t.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t

	if err := s.persist(t); err != nil {
		delete(s.tasks, t.ID)
		return nil, err
	}
	return t, nil
}

// snapshot 返回可交给调用方的任务副本：结构体连同 **Events 元素数组**一起拷贝。
//
// 为什么连元素数组也要拷，而不是 `cp := *t` 了事：Events 是**唯一**会被原地改写
// 的切片 —— agent_stream / task_runner 的批量合并都直接写最后一个元素
// （`last.Text += text`、`last.At = now`）。浅拷贝会让副本与活体共享底层数组，
// 于是这条真实路径上会出问题：取快照 → 释放锁 → （前端/WS 序列化这份快照）
// 与（流式 delta 原地改写同一个元素）并发。Go 的 string 头是两个机器字、非原子写，
// 撕裂读轻则让前端看到截断/串味的文本，重则按一个「指针是旧的、长度是新的」的
// 头去读内存 —— 这属于数据竞争（未定义行为），不是观感问题。
//
// 只拷元素数组、不深拷 string/RawMessage 字节：那些是不可变的，共享没有风险；
// 写方 `last.Text += text` 是**换一个新 string 写回字段**，不是改旧 string 的字节。
// 代价按元素算（TaskEvent ≈ 128B，190 个事件 ≈ 24KB），相对紧随其后的整份
// JSON 序列化可以忽略。
func snapshot(t *model.Task) *model.Task {
	cp := *t
	cp.Events = slices.Clone(t.Events)
	return &cp
}

// Get 返回任务副本（调用方可安全修改）。
func (s *TaskStore) Get(id string) (*model.Task, bool) {
	s.mu.RLock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.RUnlock()
		return nil, false
	}
	cp := snapshot(t)
	s.mu.RUnlock()
	return cp, true
}

// List 返回全部任务副本，按 CreatedAt 升序。
func (s *TaskStore) List() []*model.Task {
	s.mu.RLock()
	out := make([]*model.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, snapshot(t))
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Update 用 mutator 修改任务并持久化，返回更新后的副本。
// 若 mutator 返回 false 表示无变更，跳过持久化。
//
// **返回契约**：改动一旦在内存生效就一定会返回快照，即便落盘失败 —— 落盘失败时
// 返回 (已生效的快照, 非 nil error)。调用方**必须**先看 error 再决定要不要用这个
// 快照，但**不能**把 error 当成「任务不存在」：两者语义完全不同（见下）。
//
// ⚠️ 写锁**贯穿到落盘结束**，不能只护住内存改动：
//  1. 同一个任务会被多处并发 Update —— 流式文本增量（agent_stream.appendTextDelta，
//     每个 delta 一次）、ACP 权限回调（agent_perm）、用户干预（RecordIntervention）。
//     提前放锁的话，两个 goroutine 会同时操作同一个 `<id>.json.tmp`：一个还在
//     os.WriteFile（句柄未关，Windows 的 Go 默认 share mode 不含 FILE_SHARE_DELETE），
//     另一个已经 os.Rename → ERROR_SHARING_VIOLATION
//     「The process cannot access the file because it is being used by another process.」
//     生产上表现为间歇性 `record intervention failed`。
//  2. **落盘顺序必须等于变更顺序**。快照是浅拷贝，两个快照的 Rename
//     先后不受控，旧快照可能后落地，把刚追加的事件**写回退**掉（实测磁盘事件数
//     少于成功 Update 次数）。锁内 marshal 同时保证了快照的一致性（见 snapshot）。
//
// mutator 只允许改内存字段，**不得**回调 store 的任何取锁方法（Update/Get/List/
// Delete）—— 那会在持写锁时自锁。
func (s *TaskStore) Update(id string, mutator func(*model.Task) bool) (*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if !mutator(t) {
		return snapshot(t), nil
	}
	t.UpdatedAt = time.Now()
	cp := snapshot(t)
	if err := s.persist(cp); err != nil {
		// 内存已生效、磁盘没跟上。**必须**连同快照一起返回错误，而不是返回 nil：
		// 返回 nil 会让调用方把「落盘失败」误判成「任务不存在」——
		// agent_perm.setWaitingApproval 正是这么被坑的：err != nil 被当成
		// 「task 不存在或已终态」，于是审批卡不展示、直接静默 Deny，同时内存里
		// 留一个永远没人能 Resolve 的 waiting_input。
		return cp, err
	}
	return cp, nil
}

// SetEventRetention 设置单任务事件保留上限（0 = 全部保留）。
// 由全局偏好（Settings.EventRetention）驱动；运行期可改，只影响之后产生的事件。
func (s *TaskStore) SetEventRetention(n int) {
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	s.eventRetention = n
	s.mu.Unlock()
}

// EventRetention 返回当前上限。
func (s *TaskStore) EventRetention() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eventRetention
}

// AppendEvent 追加一条任务事件：统一分配**单调** Seq，并按上限裁剪最旧的事件。
//
// ⚠️ 调用方**必须**已经持有 s.mu —— 它只为 `TaskStore.Update` 的 mutator 设计
// （mutator 在锁内运行，所以这里读 s.eventRetention 是安全的）。
// 单独调用而不持锁会与外部的 Update 竞争。
//
// 为什么裁剪放在这里而不是各自 append 后再收拾：这是**唯一**的追加入口，
// 保留上限才不会在某个新写的路径上被漏掉（漏掉的表现是内存缓慢上涨，
// 而长任务是这个产品里最正常的用法）。
func (s *TaskStore) AppendEvent(t *model.Task, ev model.TaskEvent) {
	if t.NextEventSeq <= 0 {
		// 旧任务（本次改动前落盘）没有计数器：用最后一个事件的 Seq 推起点。
		// 不能用 len(Events)+1 —— 若该任务此前已被裁过，len 会偏小，
		// 于是整条序列从中间开始重复。lastEventSeq 取的是**实际序号**。
		t.NextEventSeq = lastEventSeq(t.Events) + 1
		if t.NextEventSeq <= 0 {
			t.NextEventSeq = 1
		}
	}
	ev.Seq = t.NextEventSeq
	t.NextEventSeq++
	t.Events = append(t.Events, ev)

	if s.eventRetention > 0 && len(t.Events) > s.eventRetention {
		drop := len(t.Events) - s.eventRetention
		kept := make([]model.TaskEvent, s.eventRetention)
		copy(kept, t.Events[drop:])
		// 换成新底层数组，而不是 t.Events[drop:] —— 后者会让被裁掉的部分
		// 一直挂在同一个数组上（append 只是移动切片的起点，容量不释放），
		// 那正是这个开关要解决的问题。
		t.Events = kept
	}
}

// lastEventSeq 返回事件流里最大的 Seq（空则 0）。
func lastEventSeq(events []model.TaskEvent) int {
	last := 0
	for _, e := range events {
		if e.Seq > last {
			last = e.Seq
		}
	}
	return last
}

// Delete 删除任务记录与磁盘文件。
func (s *TaskStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	delete(s.tasks, id)

	// 与 Create/Update 一样把文件操作放在锁内：否则某个已在途的快照可能在
	// 删除之后才落地，凭空把刚删掉的文件写回来（内存里已经没有这条任务，
	// 磁盘上却多出一个孤儿 .json，重启时被 load() 当作真实任务捞回来）。
	path := s.path(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// load 启动恢复：遍历 tasksDir 重建索引，孤儿 waiting_input 标 failed。
func (s *TaskStore) load() error {
	entries, err := os.ReadDir(s.tasksDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.tasksDir, e.Name()))
		if err != nil {
			continue
		}
		var t model.Task
		if json.Unmarshal(data, &t) != nil {
			continue
		}
		if t.ID == "" {
			continue
		}
		// 进程重启后 claude 子进程已死。
		// - running：进程被杀，标 failed。
		// - waiting_input(approval, 路径 A)：进程挂在 hook channel，channel 已丢无法恢复，标 failed。
		// - waiting_input(choice, 路径 B)：进程本就 end_turn 退出（合法持久态），保留 waiting_input，
		//   用户隔天仍可选项触发 Resume 续跑。
		if t.Status == model.TaskRunning {
			t.Status = model.TaskFailed
			if t.Error == "" {
				t.Error = "进程被重启打断"
			}
			now := time.Now()
			t.FinishedAt = &now
			t.UpdatedAt = now
			_ = s.persist(&t) // 尽力持久化修正
		} else if t.Status == model.TaskWaitingInput {
			if t.CurrentDecision != nil && t.CurrentDecision.Kind == model.DecisionKindChoice {
				// 路径 B：保留 waiting_input，可继续选
			} else {
				t.Status = model.TaskFailed
				if t.Error == "" {
					t.Error = "进程被重启打断"
				}
				now := time.Now()
				t.FinishedAt = &now
				t.UpdatedAt = now
				_ = s.persist(&t)
			}
		}
		s.tasks[t.ID] = &t
	}
	return nil
}

// renameAttempts persist 里改名的尝试次数（含首次）。
// 依据见 rename_windows.go：Windows 上改名会被杀软/索引器瞬时挡住，
// 实测隔 1ms 重试即成功，所以给 5 次、退避 2/4/6/8ms（最坏多花 20ms）。
const renameAttempts = 5

// renameBackoffUnit 重试的退避单位：第 n 次重试前等 n * 该值。
const renameBackoffUnit = 2 * time.Millisecond

// renameWithRetry 对改名做瞬时错误重试；非瞬时错误立即上报，不做无谓等待。
// 抽成独立函数（而不是内联进 persist）是为了让「重试几次、什么错误不该重试」
// 能被确定性测试覆盖 —— 见 rename_windows_test.go。
func renameWithRetry(rename func() error) error {
	var err error
	for attempt := 0; attempt < renameAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * renameBackoffUnit)
		}
		if err = rename(); err == nil {
			return nil
		}
		if !isTransientRenameErr(err) {
			return err
		}
	}
	return err
}

// persist 原子落盘：写 tmp 再 rename。
//
// 调用方**必须**持有 s.mu（Create/Update/Delete 都在锁内调用；load 只在
// NewTaskStore 里单线程跑）。tmp 名带 pid：进程内已由写锁串行化，这个后缀防的是
// 「同一 data 目录被两个 pieqi 实例同时打开」—— 旧实例没退干净是这里的常见情形，
// 跨进程撞在同一个 tmp 上照样是 sharing violation。
//
// 改名带重试：**这不是补救并发的补丁，而是另一个独立成因**。写锁把同进程的
// 并发写串行化之后，单线程纯循环 400 次仍会失败 3~5 次（ERROR_ACCESS_DENIED，
// temp 与非 temp 目录都有），来源于杀软/索引器对刚落盘的目标文件的瞬时持有；
// 不加这层重试，生产上照样会间歇性丢一条干预/事件。
func (s *TaskStore) persist(t *model.Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	path := s.path(t.ID)
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	errRename := renameWithRetry(func() error { return os.Rename(tmp, path) })
	if errRename != nil {
		// 落盘失败别把 tmp 留在目录里（load 只认 *.json，无害但会越积越多）。
		_ = os.Remove(tmp)
	}
	return errRename
}

func (s *TaskStore) path(id string) string {
	return filepath.Join(s.tasksDir, id+".json")
}
