// file_changes_cache.go：TaskEvent → FileChange 派生结果的短缓存。
//
// /file、/artifacts、以及同任务上连点查看都会反复 DeriveFileChanges。
// 事实源仍是事件流（ADR-0001），缓存键 = (taskID, len(events), lastSeq)：
// 事件一变就失效，不会把旧清单当成新真相。
package api

import (
	"pieqi/internal/core"
	"pieqi/internal/model"
)

type fileChangesEntry struct {
	eventLen int
	lastSeq  int
	changes  []core.FileChange
}

// 挂在 Server 上：进程内、按任务。超过上限时整表丢掉只留当前条目（简单有界）。
const fileChangesCacheCap = 64

func (s *Server) cachedFileChanges(task *model.Task) []core.FileChange {
	if task == nil {
		return nil
	}
	eventLen := len(task.Events)
	lastSeq := 0
	if eventLen > 0 {
		lastSeq = task.Events[eventLen-1].Seq
	}

	s.fcMu.Lock()
	defer s.fcMu.Unlock()
	if s.fcCache == nil {
		s.fcCache = make(map[string]fileChangesEntry, 8)
	}
	if e, ok := s.fcCache[task.ID]; ok && e.eventLen == eventLen && e.lastSeq == lastSeq {
		return e.changes
	}
	changes := core.DeriveFileChanges(task.Events, task.WorktreePath, nil)
	s.fcCache[task.ID] = fileChangesEntry{eventLen: eventLen, lastSeq: lastSeq, changes: changes}
	if len(s.fcCache) > fileChangesCacheCap {
		s.fcCache = map[string]fileChangesEntry{task.ID: s.fcCache[task.ID]}
	}
	return changes
}
