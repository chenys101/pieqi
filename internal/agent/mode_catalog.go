package agent

import (
	"sync"
)

// mode_catalog.go：ACP SessionMode 清单的旁路缓存。
//
// 与模型清单不同，mode 多数 agent **可以**在 manifest.DefaultModes 里写死
// （Cursor 的 agent/plan/ask 是静态事实）；但 qoder / dsh / codebuddy 是否下发
// modes、有哪些 id，只有 session/new 才知道。所以：
//
//   - DefaultModes：建任务前立刻可显示（Cursor）
//   - 本缓存：探测 / 真会话协商到之后记住，下次 GET /api/agents 就能带上
//
// 不单独起探测进程——挂在 probeAgentModels / SyncModesFromAdapter 上捎带写入。

var (
	modeCatalogMu    sync.Mutex
	modeCatalogCache = map[string][]ModeOption{}
)

// RememberAgentModes 记下某 agent 最近一次协商到的模式清单（覆盖写）。
// 空清单不写：避免一次失败探测把已有的好清单抹掉。
func RememberAgentModes(agentName string, modes []ModeOption) {
	if agentName == "" || len(modes) == 0 {
		return
	}
	cp := append([]ModeOption(nil), modes...)
	modeCatalogMu.Lock()
	modeCatalogCache[agentName] = cp
	modeCatalogMu.Unlock()
}

// PeekAgentModes 读缓存（不触发探测）。没有返回 nil。
func PeekAgentModes(agentName string) []ModeOption {
	modeCatalogMu.Lock()
	defer modeCatalogMu.Unlock()
	src := modeCatalogCache[agentName]
	if len(src) == 0 {
		return nil
	}
	return append([]ModeOption(nil), src...)
}

// ResolveAgentModes 合并 DefaultModes 与缓存：有缓存用缓存（真协商优先），否则 DefaultModes。
func ResolveAgentModes(agentName string, defaults []ModeOption) []ModeOption {
	if cached := PeekAgentModes(agentName); len(cached) > 0 {
		return cached
	}
	if len(defaults) == 0 {
		return nil
	}
	return append([]ModeOption(nil), defaults...)
}
