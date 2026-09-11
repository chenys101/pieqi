package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	s.tasks[t.ID] = t
	s.mu.Unlock()

	if err := s.persist(t); err != nil {
		s.mu.Lock()
		delete(s.tasks, t.ID)
		s.mu.Unlock()
		return nil, err
	}
	return t, nil
}

// Get 返回任务副本（调用方可安全修改）。
func (s *TaskStore) Get(id string) (*model.Task, bool) {
	s.mu.RLock()
	t, ok := s.tasks[id]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// List 返回全部任务副本，按 CreatedAt 升序。
func (s *TaskStore) List() []*model.Task {
	s.mu.RLock()
	out := make([]*model.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		cp := *t
		out = append(out, &cp)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Update 用 mutator 修改任务并持久化，返回更新后的副本。
// 若 mutator 返回 false 表示无变更，跳过持久化。
func (s *TaskStore) Update(id string, mutator func(*model.Task) bool) (*model.Task, error) {
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("task not found: %s", id)
	}
	changed := mutator(t)
	if !changed {
		cp := *t
		s.mu.Unlock()
		return &cp, nil
	}
	t.UpdatedAt = time.Now()
	cp := *t
	s.mu.Unlock()

	if err := s.persist(&cp); err != nil {
		return nil, err
	}
	return &cp, nil
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
	_, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("task not found: %s", id)
	}
	delete(s.tasks, id)
	s.mu.Unlock()

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

func (s *TaskStore) persist(t *model.Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	path := s.path(t.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *TaskStore) path(id string) string {
	return filepath.Join(s.tasksDir, id+".json")
}
