package core

import (
	"fmt"
	"sync"
	"time"

	"pieqi/internal/model"
)

// HookService 管理 PreToolUse hook 的待决策注册与阻塞等待。
//
// 流程（见 plan 附录 A.6）：
//  1. bridge pre-tool-use 子进程从 stdin 读 hook payload -> POST /internal/hook
//  2. RegisterPending 把 task 置 waiting_input，返回一个 decision channel
//  3. TaskRunner.Intervene(decision) -> Resolve 写 choice 到 channel
//  4. hook 子进程拿到 choice -> 输出 permissionDecision allow/deny -> exit 0
type HookService struct {
	mu       sync.Mutex
	pending  map[string]*pendingDecision // taskID -> 待决策
	timeout  time.Duration
	onPending func(taskID, toolUseID, toolName, summary string) // 状态转换回调（TaskRunner 注入）
}

type pendingDecision struct {
	taskID     string
	toolUseID  string
	toolName   string
	summary    string
	choiceCh   chan string // "approve" | "deny"
	registered chan struct{}
}

// NewHookService 创建 hook 服务。timeout 为等待决策上限，超时返回 deny。
func NewHookService(timeout time.Duration) *HookService {
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	return &HookService{
		pending: make(map[string]*pendingDecision),
		timeout: timeout,
	}
}

// SetOnPending 注入待决策状态转换回调（避免与 TaskRunner 循环依赖）。
func (h *HookService) SetOnPending(fn func(taskID, toolUseID, toolName, summary string)) {
	h.onPending = fn
}

// HookPayload hook 子进程 POST 过来的请求体。
type HookPayload struct {
	TaskID    string `json:"task_id"`
	ToolName  string `json:"tool_name"`
	ToolUseID string `json:"tool_use_id"`
	Summary   string `json:"summary"`
}

// HookResult hook 子进程拿到的决策。
type HookResult struct {
	PermissionDecision string `json:"permission_decision"` // "allow" | "deny"
	Reason             string `json:"reason"`
}

// RegisterPending 注册一个待决策，阻塞等待 Intervene 或超时。
// 由 HTTP /internal/hook handler 调用。
func (h *HookService) RegisterPending(p HookPayload) HookResult {
	pd := &pendingDecision{
		taskID:     p.TaskID,
		toolUseID:  p.ToolUseID,
		toolName:   p.ToolName,
		summary:    p.Summary,
		choiceCh:   make(chan string, 1),
		registered: make(chan struct{}),
	}

	h.mu.Lock()
	if _, exists := h.pending[p.TaskID]; exists {
		h.mu.Unlock()
		return HookResult{PermissionDecision: "deny", Reason: "task already has a pending decision"}
	}
	h.pending[p.TaskID] = pd
	h.mu.Unlock()

	// 触发 task -> waiting_input 状态转换（由 TaskRunner 注入）
	if h.onPending != nil {
		h.onPending(p.TaskID, p.ToolUseID, p.ToolName, p.Summary)
	}

	timer := time.NewTimer(h.timeout)
	defer timer.Stop()

	select {
	case choice := <-pd.choiceCh:
		if choice == "approve" {
			return HookResult{PermissionDecision: "allow", Reason: "user approved"}
		}
		return HookResult{PermissionDecision: "deny", Reason: "user denied"}
	case <-timer.C:
		h.mu.Lock()
		delete(h.pending, p.TaskID)
		h.mu.Unlock()
		return HookResult{PermissionDecision: "deny", Reason: "decision timeout"}
	}
}

// Resolve 投递用户决策。返回 error 若任务无待决策。
// 由 TaskRunner.Intervene 调用。
func (h *HookService) Resolve(taskID, decisionID, choice string) error {
	h.mu.Lock()
	pd, ok := h.pending[taskID]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("no pending decision for task %s", taskID)
	}
	delete(h.pending, taskID)
	h.mu.Unlock()

	if decisionID != "" && pd.toolUseID != "" && decisionID != pd.toolUseID {
		// decisionID 不匹配仍放行（取最新意图），但记日志可加
	}
	pd.choiceCh <- choice
	return nil
}

// HasPending 判断任务是否有待决策（测试/查询用）。
func (h *HookService) HasPending(taskID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.pending[taskID]
	return ok
}

// PendingDecision 返回任务的待决策信息（供 API 展示）。
func (h *HookService) PendingDecision(taskID string) (toolName, summary, toolUseID string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pd, exists := h.pending[taskID]
	if !exists {
		return "", "", "", false
	}
	return pd.toolName, pd.summary, pd.toolUseID, true
}

// 静默引用 model 包以保留语义（PendingDecision 等未来扩展用）
var _ = model.TaskWaitingInput
