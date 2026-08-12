package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"pieqi/internal/model"
)

// PendingRequest 等待审批的 Claude 操作
type PendingRequest struct {
	Prompt    string
	SessionID string
	Msg       model.Message
	CreatedAt time.Time
}

// ApprovalGate 审批状态机。纯状态，无外部依赖。
//
// 接口:
//   Check(identity) → blocked bool      — 是否有 pending 阻塞新消息
//   IsBypass(identity) → bool           — 是否在免审窗口内
//   SetPending(identity, req)           — 存入待审批请求
//   Approve(identity) → *PendingRequest — 取出 pending + 标记免审
//   Deny(identity) → bool               — 清除 pending，不标记免审
type ApprovalGate struct {
	mu              sync.Mutex
	pendingRequests map[string]*PendingRequest
	approvedUntil   map[string]time.Time
	approvalsPath   string // data/approvals.json
	window          time.Duration
}

// NewApprovalGate 创建审批状态机，window 为免审时长（0 = 默认 30min）
func NewApprovalGate(approvalsPath string, window time.Duration) *ApprovalGate {
	if window <= 0 {
		window = 30 * time.Minute
	}
	g := &ApprovalGate{
		pendingRequests: make(map[string]*PendingRequest),
		approvedUntil:   make(map[string]time.Time),
		approvalsPath:   approvalsPath,
		window:          window,
	}
	g.load()
	return g
}

// Check 检查是否有待审批请求阻塞新消息
func (g *ApprovalGate) Check(identity string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.pendingRequests[identity]
	return ok
}

// IsBypass 检查 identity 是否在免审窗口内（自动清理已过期）
func (g *ApprovalGate) IsBypass(identity string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.approvedUntil[identity]
	if !ok {
		return false
	}
	if time.Now().After(at) {
		delete(g.approvedUntil, identity)
		g.save()
		return false
	}
	return true
}

// SetPending 存入待审批请求
func (g *ApprovalGate) SetPending(identity string, req *PendingRequest) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pendingRequests[identity] = req
}

// Approve 取出 pending 请求，标记 identity 在 window 内免审，持久化
func (g *ApprovalGate) Approve(identity string) *PendingRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	req, ok := g.pendingRequests[identity]
	if !ok {
		return nil
	}
	delete(g.pendingRequests, identity)
	g.approvedUntil[identity] = time.Now().Add(g.window)
	g.save()
	return req
}

// Deny 清除 pending，不标记免审
func (g *ApprovalGate) Deny(identity string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.pendingRequests[identity]
	delete(g.pendingRequests, identity)
	return ok
}

// load 从文件恢复已批准的 identity
func (g *ApprovalGate) load() {
	data, err := os.ReadFile(g.approvalsPath)
	if err != nil {
		return
	}
	var raw map[string]time.Time
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	now := time.Now()
	for k, v := range raw {
		if now.Before(v) {
			g.approvedUntil[k] = v
		}
	}
}

// ResetForTest 清除所有内存状态（测试用，避免生产环境残留数据干扰）
func (g *ApprovalGate) ResetForTest() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pendingRequests = make(map[string]*PendingRequest)
	g.approvedUntil = make(map[string]time.Time)
}

// save 持久化已批准列表
func (g *ApprovalGate) save() {
	os.MkdirAll(filepath.Dir(g.approvalsPath), 0755)
	now := time.Now()
	clean := make(map[string]time.Time)
	for k, v := range g.approvedUntil {
		if now.Before(v) {
			clean[k] = v
		}
	}
	data, _ := json.Marshal(clean)
	os.WriteFile(g.approvalsPath, data, 0644)
}
