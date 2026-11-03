// Package sqlite 是 ADR-0022 的默认 Store 实现（纯 Go modernc.org/sqlite，无 CGO）。
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"pieqi/internal/store"

	_ "modernc.org/sqlite"
)

// DB 实现 store.Store。
type DB struct {
	mu sync.Mutex
	db *sql.DB
	// path 库文件绝对路径（测试/诊断用）。
	path string
}

// Open 打开（或创建）dataDir 下的 pieqi.db，并执行 schema。
//
// 约定：库文件 = <dataDir>/pieqi.db。生产 dataDir 即 tasks 目录
// （main 传入 ~/.pieqi/tasks）；测试传入 t.TempDir()。同目录下遗留的
// *.json 由 MigrateJSONTasks 导入。
func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir store dir: %w", err)
	}
	path := filepath.Join(dataDir, "pieqi.db")
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// 单写者 + TaskStore 已有大锁；限制连接避免锁升级死锁。
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("pragma: %w", err)
	}
	s := &DB{db: sqlDB, path: path}
	if err := s.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return s, nil
}

// Path 返回库文件路径。
func (s *DB) Path() string { return s.path }

// Close 关闭库。
func (s *DB) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *DB) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  requirement_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT '',
  project_path TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_requirement ON tasks(requirement_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);

CREATE TABLE IF NOT EXISTS requirements (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  project_path TEXT NOT NULL DEFAULT '',
  graph_version INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_requirements_status ON requirements(status);

CREATE TABLE IF NOT EXISTS acceptance (
  requirement_id TEXT NOT NULL,
  id TEXT NOT NULL,
  text TEXT NOT NULL,
  verifiable INTEGER NOT NULL DEFAULT 1,
  deferred INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (requirement_id, id),
  FOREIGN KEY (requirement_id) REFERENCES requirements(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS issues (
  requirement_id TEXT NOT NULL,
  id TEXT NOT NULL,
  status TEXT NOT NULL,
  severity TEXT NOT NULL,
  fix_task_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  payload BLOB NOT NULL,
  PRIMARY KEY (requirement_id, id),
  FOREIGN KEY (requirement_id) REFERENCES requirements(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	return nil
}

var _ store.Store = (*DB)(nil)
