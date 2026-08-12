package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const mapMax = 50

// sessionMapEntry 映射条目
type sessionMapEntry struct {
	Index    int    `json:"index"`
	Filename string `json:"filename"`
}

// sessionMap 单 identity 的逻辑→物理映射
type sessionMap struct {
	file    string           // data/mappings/{identity}.json
	entries []sessionMapEntry // ordered by Index
}

func loadSessionMap(mapFile string) *sessionMap {
	m := &sessionMap{file: mapFile}
	data, err := os.ReadFile(mapFile)
	if err != nil {
		return m
	}
	json.Unmarshal(data, &m.entries)
	// ensure sorted
	sort.Slice(m.entries, func(i, j int) bool { return m.entries[i].Index < m.entries[j].Index })
	return m
}

// resolve 按逻辑编号查找物理文件名
func (m *sessionMap) resolve(index int) (string, bool) {
	for _, e := range m.entries {
		if e.Index == index {
			return e.Filename, true
		}
	}
	return "", false
}

// assign 为新文件分配逻辑编号；返回被驱逐的文件名（如有）
func (m *sessionMap) assign(filename string, sessionsDir string) (index int, evicted string) {
	// 找空位
	used := make(map[int]bool, len(m.entries))
	for _, e := range m.entries {
		used[e.Index] = true
	}
	for i := 1; i <= mapMax; i++ {
		if !used[i] {
			m.entries = append(m.entries, sessionMapEntry{Index: i, Filename: filename})
			m.save()
			return i, ""
		}
	}
	// 满 → 驱逐最旧（按文件 mtime）
	oldest, oldestIdx := -1, -1
	var oldestTime time.Time
	for i, e := range m.entries {
		info, err := os.Stat(filepath.Join(sessionsDir, e.Filename+".json"))
		if err != nil {
			// file gone, evict immediately
			evicted = e.Filename
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			m.entries = append(m.entries, sessionMapEntry{Index: e.Index, Filename: filename})
			m.save()
			return e.Index, evicted
		}
		if oldestIdx < 0 || info.ModTime().Before(oldestTime) {
			oldest, oldestIdx = e.Index, i
			oldestTime = info.ModTime()
		}
	}
	evicted = m.entries[oldestIdx].Filename
	m.entries[oldestIdx] = sessionMapEntry{Index: oldest, Filename: filename}
	m.save()
	return oldest, evicted
}

// list 返回所有映射条目（文件存在才保留）
func (m *sessionMap) list(sessionsDir string) []sessionMapEntry {
	valid := make([]sessionMapEntry, 0, len(m.entries))
	for _, e := range m.entries {
		if _, err := os.Stat(filepath.Join(sessionsDir, e.Filename+".json")); err == nil {
			valid = append(valid, e)
		}
	}
	m.entries = valid
	m.save()
	return m.entries
}

// latest 返回最新文件的条目
func (m *sessionMap) latest(sessionsDir string) (sessionMapEntry, time.Time, bool) {
	var best sessionMapEntry
	var bestTime time.Time
	found := false
	for _, e := range m.entries {
		fp := filepath.Join(sessionsDir, e.Filename+".json")
		info, err := os.Stat(fp)
		if err != nil {
			continue
		}
		if !found || info.ModTime().After(bestTime) {
			best = e
			bestTime = info.ModTime()
			found = true
		}
	}
	return best, bestTime, found
}

func (m *sessionMap) save() {
	data, _ := json.Marshal(m.entries)
	os.MkdirAll(filepath.Dir(m.file), 0755)
	os.WriteFile(m.file, data, 0644)
}
