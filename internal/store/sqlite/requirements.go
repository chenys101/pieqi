package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"pieqi/internal/model"
)

// UpsertRequirement 写入需求；同步刷新 acceptance / issues 子表（先删后插）。
func (s *DB) UpsertRequirement(r *model.Requirement) error {
	if r == nil || r.ID == "" {
		return fmt.Errorf("upsert requirement: missing id")
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal requirement: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("upsert requirement: store closed")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`
INSERT INTO requirements (id, status, project_path, graph_version, created_at, updated_at, payload)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  status=excluded.status,
  project_path=excluded.project_path,
  graph_version=excluded.graph_version,
  created_at=excluded.created_at,
  updated_at=excluded.updated_at,
  payload=excluded.payload
`, r.ID, string(r.Status), r.ProjectPath, r.GraphVersion,
		r.CreatedAt.UnixNano(), r.UpdatedAt.UnixNano(), payload)
	if err != nil {
		return fmt.Errorf("upsert requirement: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM acceptance WHERE requirement_id = ?`, r.ID); err != nil {
		return err
	}
	for _, ac := range r.Acceptance {
		verifiable, deferred := 0, 0
		if ac.Verifiable {
			verifiable = 1
		}
		if ac.Deferred {
			deferred = 1
		}
		_, err = tx.Exec(`
INSERT INTO acceptance (requirement_id, id, text, verifiable, deferred)
VALUES (?, ?, ?, ?, ?)`, r.ID, ac.ID, ac.Text, verifiable, deferred)
		if err != nil {
			return fmt.Errorf("upsert acceptance: %w", err)
		}
	}

	if _, err := tx.Exec(`DELETE FROM issues WHERE requirement_id = ?`, r.ID); err != nil {
		return err
	}
	for i := range r.Issues {
		iss := &r.Issues[i]
		ipayload, err := json.Marshal(iss)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`
INSERT INTO issues (requirement_id, id, status, severity, fix_task_id, created_at, updated_at, payload)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, iss.ID, string(iss.Status), string(iss.Severity), iss.FixTaskID,
			iss.CreatedAt.UnixNano(), iss.UpdatedAt.UnixNano(), ipayload)
		if err != nil {
			return fmt.Errorf("upsert issue: %w", err)
		}
	}

	return tx.Commit()
}

// GetRequirement 按 id 取需求（从 payload 还原，含 AC / Issues / Events）。
func (s *DB) GetRequirement(id string) (*model.Requirement, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, false, fmt.Errorf("get requirement: store closed")
	}
	var payload []byte
	err := s.db.QueryRow(`SELECT payload FROM requirements WHERE id = ?`, id).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	r, err := decodeRequirement(payload)
	if err != nil {
		return nil, false, err
	}
	return r, true, nil
}

// ListRequirements 返回全部需求。
func (s *DB) ListRequirements() ([]*model.Requirement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, fmt.Errorf("list requirements: store closed")
	}
	rows, err := s.db.Query(`SELECT payload FROM requirements`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Requirement
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		r, err := decodeRequirement(payload)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRequirement 删除需求及其 acceptance / issues（FK CASCADE）。
func (s *DB) DeleteRequirement(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("delete requirement: store closed")
	}
	res, err := s.db.Exec(`DELETE FROM requirements WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("requirement not found: %s", id)
	}
	return nil
}

func decodeRequirement(payload []byte) (*model.Requirement, error) {
	var r model.Requirement
	if err := json.Unmarshal(payload, &r); err != nil {
		return nil, fmt.Errorf("decode requirement: %w", err)
	}
	if r.ID == "" {
		return nil, fmt.Errorf("decode requirement: missing id")
	}
	return &r, nil
}
