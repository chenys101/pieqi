package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

// Registry 内置 + PIEQI_HOME/workflows 覆盖（同 id 时用户文件优先）。
type Registry struct {
	mu        sync.RWMutex
	packs     map[string]*Pack
	builtin   map[string]bool
	defaultID string
	userDir   string
}

// NewRegistry 加载内置 pack，再合并 dir（通常 <dataRoot>/workflows）。
func NewRegistry(userDir string, defaultID string) (*Registry, error) {
	r := &Registry{
		packs:     make(map[string]*Pack),
		builtin:   make(map[string]bool),
		defaultID: strings.TrimSpace(defaultID),
		userDir:   strings.TrimSpace(userDir),
	}
	if err := r.loadEmbedded(); err != nil {
		return nil, err
	}
	if r.userDir != "" {
		if err := r.loadDir(r.userDir); err != nil {
			return nil, err
		}
	}
	if r.defaultID == "" {
		r.defaultID = IDPlanSerial
	}
	if _, ok := r.packs[r.defaultID]; !ok {
		return nil, fmt.Errorf("default workflow %q not found", r.defaultID)
	}
	return r, nil
}

// MustNewRegistry 启动失败即 panic 的包装（测试可用 NewRegistry）。
func MustNewRegistry(userDir, defaultID string) *Registry {
	r, err := NewRegistry(userDir, defaultID)
	if err != nil {
		panic(err)
	}
	return r
}

// UserDir 用户覆盖目录。
func (r *Registry) UserDir() string {
	if r == nil {
		return ""
	}
	return r.userDir
}

// DefaultID 创建需求未指定时使用。
func (r *Registry) DefaultID() string {
	if r == nil {
		return IDPlanSerial
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultID
}

// SetDefaultID 设置默认（只影响之后新建；不校验已钉死需求）。
func (r *Registry) SetDefaultID(id string) error {
	if r == nil {
		return fmt.Errorf("workflow registry not wired")
	}
	id = strings.TrimSpace(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "" {
		id = IDPlanSerial
	}
	if _, ok := r.packs[id]; !ok {
		return fmt.Errorf("unknown workflow %q", id)
	}
	r.defaultID = id
	return nil
}

// Get 按 id 取 pack；空 id → 默认。
func (r *Registry) Get(id string) (*Pack, error) {
	if r == nil {
		return nil, fmt.Errorf("workflow registry not wired")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	id = strings.TrimSpace(id)
	if id == "" {
		id = r.defaultID
	}
	p, ok := r.packs[id]
	if !ok {
		return nil, fmt.Errorf("unknown workflow %q", id)
	}
	cp := *p
	return &cp, nil
}

// ResolveForRequirement 需求上的 workflow_id；历史空值回落 s2-dag（兼容旧拆分行为）。
func (r *Registry) ResolveForRequirement(workflowID string) (*Pack, error) {
	if strings.TrimSpace(workflowID) == "" {
		return r.Get(IDDAG)
	}
	return r.Get(workflowID)
}

// List 按 id 排序的摘要。
func (r *Registry) List() []Summary {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.packs))
	for id := range r.packs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Summary, 0, len(ids))
	for _, id := range ids {
		s := r.packs[id].Summary(r.builtin[id])
		s.IsDefault = id == r.defaultID
		out = append(out, s)
	}
	return out
}

// Detail 只读详情。
func (r *Registry) Detail(id string) (*Detail, error) {
	p, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	builtin := r.builtin[p.ID]
	def := r.defaultID
	r.mu.RUnlock()
	d := p.DetailOf(builtin)
	d.IsDefault = p.ID == def
	return &d, nil
}

// Reload 重新加载内置 + 用户目录（热扫描）。
func (r *Registry) Reload() error {
	if r == nil {
		return fmt.Errorf("workflow registry not wired")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	prevDefault := r.defaultID
	r.packs = make(map[string]*Pack)
	r.builtin = make(map[string]bool)
	if err := r.loadEmbeddedLocked(); err != nil {
		return err
	}
	if r.userDir != "" {
		if err := r.loadDirLocked(r.userDir); err != nil {
			return err
		}
	}
	if _, ok := r.packs[prevDefault]; ok {
		r.defaultID = prevDefault
	} else {
		r.defaultID = IDPlanSerial
		if _, ok := r.packs[r.defaultID]; !ok {
			return fmt.Errorf("default workflow %q missing after reload", r.defaultID)
		}
	}
	return nil
}

func (r *Registry) loadEmbedded() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadEmbeddedLocked()
}

func (r *Registry) loadEmbeddedLocked() error {
	entries, err := embeddedPacks.ReadDir("packs")
	if err != nil {
		return fmt.Errorf("read embedded packs: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := embeddedPacks.ReadFile("packs/" + e.Name())
		if err != nil {
			return err
		}
		p, err := parsePackYAML(raw)
		if err != nil {
			return fmt.Errorf("embedded %s: %w", e.Name(), err)
		}
		r.packs[p.ID] = p
		r.builtin[p.ID] = true
	}
	if len(r.packs) == 0 {
		return fmt.Errorf("no embedded workflow packs")
	}
	return nil
}

func (r *Registry) loadDir(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadDirLocked(dir)
}

func (r *Registry) loadDirLocked(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		p, err := parsePackYAML(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		r.packs[p.ID] = p
		delete(r.builtin, p.ID) // 用户覆盖
	}
	return nil
}

func parsePackYAML(raw []byte) (*Pack, error) {
	var p Pack
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if err := ValidatePack(&p); err != nil {
		return nil, err
	}
	return &p, nil
}
