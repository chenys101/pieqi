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
