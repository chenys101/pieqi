package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"pieqi/internal/model"
)

// UpsertTask 插入或覆盖整份 Task（payload = 完整 JSON；索引列冗余便于查询）。
func (s *DB) UpsertTask(t *model.Task) error {
	if t == nil || t.ID == "" {
		return fmt.Errorf("upsert task: missing id")
	}
	payload, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("upsert task: store closed")
	}
	_, err = s.db.Exec(`
INSERT INTO tasks (id, requirement_id, status, role, project_path, created_at, updated_at, payload)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  requirement_id=excluded.requirement_id,
  status=excluded.status,
  role=excluded.role,
  project_path=excluded.project_path,
  created_at=excluded.created_at,
  updated_at=excluded.updated_at,
  payload=excluded.payload
`, t.ID, t.RequirementID, string(t.Status), string(t.Role), t.ProjectPath,
		t.CreatedAt.UnixNano(), t.UpdatedAt.UnixNano(), payload)
	if err != nil {
		return fmt.Errorf("upsert task %s: %w", t.ID, err)
	}
	return nil
}

// GetTask 按 id 取 Task。
func (s *DB) GetTask(id string) (*model.Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, fmt.Errorf("get task: store closed")
	}
	var payload []byte
	err := s.db.QueryRow(`SELECT payload FROM tasks WHERE id = ?`, id).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	t, err := decodeTask(payload)
	if err != nil {
		return nil, false, err
	}
	return t, true, nil
}

// ListTasks 返回全部任务（无序；调用方排序）。
func (s *DB) ListTasks() ([]*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("list tasks: store closed")
	}
	rows, err := s.db.Query(`SELECT payload FROM tasks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Task
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		t, err := decodeTask(payload)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTask 删除任务行。
func (s *DB) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("delete task: store closed")
	}
	res, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}

func decodeTask(payload []byte) (*model.Task, error) {
	var t model.Task
	if err := json.Unmarshal(payload, &t); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	if t.ID == "" {
		return nil, fmt.Errorf("decode task: missing id")
	}
	return &t, nil
}
