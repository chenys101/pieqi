package sqlite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"pieqi/internal/model"
)

// JSONMigrateResult 一次 JSON→SQLite 迁移的摘要（ADR-0022：不得静默丢任务）。
type JSONMigrateResult struct {
	Imported   int
	SkippedDB  int // 库中已有同 id，保留库内、JSON 仍归档
	BadFiles   []BadJSONFile
	ArchivedTo string // 成功导入后 JSON 挪到的目录（相对或绝对）
}

// BadJSONFile 无法导入的遗留任务文件。
type BadJSONFile struct {
	Name   string
	Reason string
	KeptAs string
}

const metaJSONMigrated = "json_tasks_migrated_at"

// MigrateJSONTasks 扫描 dir 下 *.json，导入 tasks 表后归档到 dir/migrated-json/。
//
// 幂等：meta 已标记且无新 json 时直接返回；有残留 json 则继续尝试导入。
// 库中已有同 id：不覆盖（SQLite 为真相），仍把 JSON 归档以免双真相。
func (s *DB) MigrateJSONTasks(dir string) (*JSONMigrateResult, error) {
	res := &JSONMigrateResult{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return nil, err
	}

	var jsonNames []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		jsonNames = append(jsonNames, e.Name())
	}
	if len(jsonNames) == 0 {
		return res, nil
	}

	archiveDir := filepath.Join(dir, "migrated-json")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir migrated-json: %w", err)
	}
	res.ArchivedTo = archiveDir

	for _, name := range jsonNames {
		full := filepath.Join(dir, name)
		data, err := os.ReadFile(full)
		if err != nil {
			res.BadFiles = append(res.BadFiles, BadJSONFile{Name: name, Reason: fmt.Sprintf("读失败：%v", err)})
			continue
		}
		var t model.Task
		if err := json.Unmarshal(data, &t); err != nil {
			kept := quarantine(full, name, archiveDir)
			res.BadFiles = append(res.BadFiles, BadJSONFile{
				Name: name, Reason: describeBadJSON(data), KeptAs: kept,
			})
			continue
		}
		if t.ID == "" {
			kept := quarantine(full, name, archiveDir)
			res.BadFiles = append(res.BadFiles, BadJSONFile{
				Name: name, Reason: "缺少 id 字段", KeptAs: kept,
			})
			continue
		}

		_, exists, err := s.GetTask(t.ID)
		if err != nil {
			return res, err
		}
		if exists {
			res.SkippedDB++
		} else {
			if err := s.UpsertTask(&t); err != nil {
				return res, fmt.Errorf("import %s: %w", name, err)
			}
			res.Imported++
		}
		dest := filepath.Join(archiveDir, name)
		if err := os.Rename(full, dest); err != nil {
			// 归档失败不回滚已导入行，但必须上报，避免双真相残留。
			return res, fmt.Errorf("archive %s: %w", name, err)
		}
	}

	s.mu.Lock()
	_, _ = s.db.Exec(`INSERT INTO meta(key,value) VALUES(?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		metaJSONMigrated, time.Now().UTC().Format(time.RFC3339Nano))
	s.mu.Unlock()
	return res, nil
}

func quarantine(full, name, archiveDir string) string {
	stamp := time.Now().Format("20060102-150405")
	kept := filepath.Join(archiveDir, name+".bad-"+stamp)
	_ = os.Rename(full, kept)
	return kept
}

func describeBadJSON(data []byte) string {
	switch {
	case len(data) == 0:
		return "文件为空（0 字节）"
	case len(bytes.Trim(data, "\x00")) == 0:
		return fmt.Sprintf("%d 字节但全为 0x00（掉电/蓝屏留下的空块特征）", len(data))
	case len(bytes.TrimSpace(data)) == 0:
		return "文件只有空白字符"
	}
	return "JSON 解析失败"
}
