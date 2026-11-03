package core

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"pieqi/internal/model"
	"pieqi/internal/store"

	"github.com/google/uuid"
)

// RequirementStore 需求 CRUD + SplitProposal/confirm（ADR-0021）。
// 与 Task 共用同一个 store.Store，禁止双真相。
type RequirementStore struct {
	mu        sync.RWMutex
	backend   store.Store
	tasks     *TaskStore                      // confirm 时创建 planned Task
	proposals map[string]*model.SplitProposal // requirementID → 暂存提案（不落盘）
}

// NewRequirementStore 包装已打开的 Store（通常来自 TaskStore.Backend()）。
func NewRequirementStore(backend store.Store) *RequirementStore {
	return &RequirementStore{backend: backend}
}

// Create 新建 draft 需求并持久化。
func (s *RequirementStore) Create(r *model.Requirement) (*model.Requirement, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	if r == nil {
		return nil, fmt.Errorf("nil requirement")
	}
	if strings.TrimSpace(r.ProjectPath) == "" {
		return nil, fmt.Errorf("project_path required")
	}
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	now := time.Now()
	r.Status = model.RequirementDraft
	r.CreatedAt = now
	r.UpdatedAt = now
	r.GraphVersion = 0
	if r.Events == nil {
		r.Events = []model.RequirementEvent{{
			Seq:  1,
			Type: model.ReqEventCreated,
			Text: "created",
			At:   now,
		}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.backend.UpsertRequirement(r); err != nil {
		return nil, err
	}
	cp := *r
	return &cp, nil
}

// Get 返回需求副本。
func (s *RequirementStore) Get(id string) (*model.Requirement, bool) {
	if s == nil || s.backend == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok, err := s.backend.GetRequirement(id)
	if err != nil || !ok {
		return nil, false
	}
	cp := *r
	return &cp, true
}

// List 按 CreatedAt 升序。
func (s *RequirementStore) List() []*model.Requirement {
	if s == nil || s.backend == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	list, err := s.backend.ListRequirements()
	if err != nil {
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	return list
}

// Update 用 mutator 改需求并落盘。
func (s *RequirementStore) Update(id string, mutator func(*model.Requirement) bool) (*model.Requirement, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok, err := s.backend.GetRequirement(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", id)
	}
	if !mutator(r) {
		cp := *r
		return &cp, nil
	}
	r.UpdatedAt = time.Now()
	if err := s.backend.UpsertRequirement(r); err != nil {
		return nil, err
	}
	cp := *r
	return &cp, nil
}

// Delete 删除需求。
func (s *RequirementStore) Delete(id string) error {
	if s == nil || s.backend == nil {
		return fmt.Errorf("requirement store not wired")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend.DeleteRequirement(id)
}
