// Package agent: manager.go 实现 AgentManager——承担 TaskRunner 的 agent 调度职责
// （多会话生命周期 / 每项目并发上限 / ACP 不可用时透明回退），让调用方（core 的 Wire*
// 连接器）只依赖 AgentAdapter 接口，底层 ACP / claude -p 切换对调用方无感。
//
// 设计要点与依赖方向：
//   - 不 import pieqi/internal/core：AgentManager 只做 adapter 生命周期与并发控制，
//     事件路由（EventBus/TaskStore/IM 通知）由 core 侧 Wire* 连接器在拿到的 adapter 上
//     注册回调完成。保持 core→agent 单向依赖——agent 若反向 import core 会形成循环依赖。
//   - 工厂可注入：primary/fallback 为 adapterFactory，测试用 fake 替换；生产路径由
//     NewAgentManager 按 cfg.UseACP 构建（true→ACP 主 + Print 回退；false→仅 Print）。
//   - 透明回退（Task 4.3）：primary 工厂失败、或其 NewSession 失败时，自动切到 fallback
//     工厂，并经 onFallback hook 异步通知调用方记录回退事件（不阻塞 Open）。
//   - 每项目并发上限：用 agent 包内本地定义的 semaphore（与 core 包的互不相干）按
//     projectID 限流；MaxConcurrent<=0 时不限制。
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"pieqi/internal/config"

	"go.uber.org/zap"
)

// AgentKind 标识 adapter 底层类型（诊断/回退事件记录用）。
type AgentKind string

const (
	AgentKindACP   AgentKind = "acp"
	AgentKindPrint AgentKind = "print"
)

// ManagerConfig AgentManager 配置。
type ManagerConfig struct {
	UseACP        bool             // true=优先 ACP（失败回退 PrintAgent）；false=直接用 PrintAgent
	MaxConcurrent int              // 每项目并发上限；<=0 不限制
	ACPConfig     config.ACPConfig // ACP 工厂参数（UseACP=true 时用）
	PrintConfig   PrintConfig      // PrintAgent 工厂参数
	IdleTimeout   time.Duration    // ACP 会话空闲回收阈值：轮间保活、超过该时长无对话优雅关闭（避免孤儿进程）；<=0 禁用
}

// adapterFactory 创建一个 AgentAdapter + 它的 Kind。可注入（测试用）。
type adapterFactory func() (AgentAdapter, AgentKind, error)

// AgentManager 管理多个 task 的 agent 会话：按 projectID 限流、ACP/Print 工厂切换与
// 透明回退、Run/Cancel/Close 生命周期。本身不碰 EventBus/TaskStore（由 core 侧 Wire*
// 连接器在 Open 返回的 adapter 上注册回调完成事件路由）。
type AgentManager struct {
	logger    *zap.Logger
	cfg       ManagerConfig
	primary   adapterFactory // 默认按 cfg 构建：UseACP=true→ACP，false→Print
	fallback  adapterFactory // primary 失败时回退；UseACP=true 时为 Print 工厂，否则 nil
	onFallback func(taskID string, primaryErr error) // 回退事件回调（可选，调用方注入记录回退事件）

	// onSessionClosed 会话关闭回调（可选，调用方注入清理会话级资源，如 TaskRunner 的 wires）。
	// 在 Close 的 closeOnce 内触发，恰好一次；reaper 空闲回收也会走 Close 触发。
	onSessionClosed func(taskID string)

	mu       sync.Mutex
	sessions map[string]*managedSession // taskID -> session
	projSems sync.Map                    // projectID -> *semaphore

	reaperMu   sync.Mutex
	reaperStop chan struct{} // StartReaper 的停止信号；nil=未启动
}

// managedSession 一个 task 的 agent 会话（不导出）。
//
// runMu 保护 running/runCancel，使 Run 与 Cancel/Close 之间串行化对"当前是否在跑一轮
// prompt"的读写。sem 在 Open 成功时 acquire，Close 时 release；全失败路径由 Open 释放。
type managedSession struct {
	taskID    string
	projectID string
	adapter   AgentAdapter
	kind      AgentKind
	sessionID string
	fellBack  bool
	sem       *semaphore

	// lastActivity 最近一次 Run（prompt turn）开始/结束的时间；空闲回收器据此判断会话是否闲置。
	lastActivity time.Time

	runMu     sync.Mutex
	running   bool
	runCancel context.CancelFunc // Run 期间设置，Cancel/Close 经它中断 SendPrompt

	closeOnce sync.Once // 守护 Close 幂等
}

// NewAgentManager 创建 AgentManager。
// logger 为 nil 时用 zap.NewNop()。primary/fallback 工厂按 cfg 构建：
//   - UseACP=true：primary=NewACPAgent（KindACP），fallback=NewPrintAgent（KindPrint）
//   - UseACP=false：primary=NewPrintAgent（KindPrint），fallback=nil
func NewAgentManager(cfg ManagerConfig, logger *zap.Logger) *AgentManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	m := &AgentManager{
		logger:   logger,
		cfg:      cfg,
		sessions: make(map[string]*managedSession),
	}
	if cfg.UseACP {
		m.primary = func() (AgentAdapter, AgentKind, error) {
			return NewACPAgent(cfg.ACPConfig, logger), AgentKindACP, nil
		}
		m.fallback = func() (AgentAdapter, AgentKind, error) {
			return NewPrintAgent(cfg.PrintConfig, logger), AgentKindPrint, nil
		}
	} else {
		m.primary = func() (AgentAdapter, AgentKind, error) {
			return NewPrintAgent(cfg.PrintConfig, logger), AgentKindPrint, nil
		}
		m.fallback = nil
	}
	return m
}

// SetFallbackHook 注入回退事件回调（可选）。发生透明回退时异步调用，参数为触发回退的
// primary 失败错误，供调用方记录回退事件/告警。
func (m *AgentManager) SetFallbackHook(fn func(taskID string, primaryErr error)) {
	m.onFallback = fn
}

// SetOnSessionClosed 注入会话关闭回调（可选）。会话被 Close（含 reaper 空闲回收、Cancel、
// 服务器关停 CloseAll）关闭时触发，参数为 taskID。调用方用它清理会话级资源——
// TaskRunner 借此 Unwire 三个 wire handle，避免 reaper 关会话后 wires 残留对已关 adapter 的引用。
// 幂等：同一会话的 closeOnce 只触发一次。
func (m *AgentManager) SetOnSessionClosed(fn func(taskID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSessionClosed = fn
}

// Open 为 task 创建 agent 会话：取项目并发槽 → 调 primary 工厂创建 adapter 并 NewSession
// （失败时按 4.3 透明回退到 fallback）→ 登记会话。返回的 adapter 由调用方注册回调。
//
// cfg.Cwd 用于 worktree；cfg 整体透传给 NewSession（含 ResumeFrom 续问字段）。
// 已存在同 taskID 的会话返回错误。回退时 fellBack=true。
func (m *AgentManager) Open(ctx context.Context, taskID, projectID string, cfg SessionConfig) (AgentAdapter, bool, error) {
	m.mu.Lock()
	if _, exists := m.sessions[taskID]; exists {
		m.mu.Unlock()
		return nil, false, fmt.Errorf("agent: session already open for task %s", taskID)
	}
	m.mu.Unlock()

	// 取项目并发槽（MaxConcurrent<=0 时 sem=nil，acquire/release 为 no-op）。
	sem := m.projectSem(projectID)
	sem.acquire()

	att, err := m.createAdapterWithFallback(ctx, cfg)
	if err != nil {
		// 全失败：释放并发槽，不登记会话。
		sem.release()
		return nil, false, err
	}

	// 回退事件回调（异步，不阻塞 Open）。
	if att.fellBack && m.onFallback != nil {
		go m.onFallback(taskID, att.primaryErr)
	}

	sess := &managedSession{
		taskID:    taskID,
		projectID: projectID,
		adapter:   att.adapter,
		kind:      att.kind,
		sessionID: att.sessionID,
		fellBack:  att.fellBack,
		sem:       sem,
	}

	m.mu.Lock()
	// 竞态兜底：另一 goroutine 可能在释放 mu 期间登记了同 taskID。清理本侧资源。
	if _, exists := m.sessions[taskID]; exists {
		m.mu.Unlock()
		_ = att.adapter.Close(ctx)
		sem.release()
		return nil, false, fmt.Errorf("agent: session already open for task %s", taskID)
	}
	m.sessions[taskID] = sess
	m.mu.Unlock()

	return att.adapter, att.fellBack, nil
}

// adapterAttempt createAdapterWithFallback 的结果。
type adapterAttempt struct {
	adapter    AgentAdapter
	kind       AgentKind
	sessionID  string
	fellBack   bool
	primaryErr error // 触发回退的 primary 失败错误；未回退时为 nil
}

// createAdapterWithFallback 调 primary 工厂创建 adapter 并 NewSession；若 primary 工厂失败
// 或其 NewSession 失败，且有 fallback 工厂，则回退到 fallback。
//
// cfg 整体透传给 primary/fallback 的 NewSession（含 ResumeFrom 续问字段，回退路径也支持 resume）。
// 返回 att（含 adapter/kind/sessionID/fellBack/primaryErr）与 err（全失败时非 nil）。
// 调用方负责在 err != nil 时释放并发槽（本方法不碰 sem，避免双重释放）；在 err == nil 时
// 登记 session。primary NewSession 失败时先 Close primary adapter 再回退，防资源泄漏。
func (m *AgentManager) createAdapterWithFallback(ctx context.Context, cfg SessionConfig) (adapterAttempt, error) {
	primaryAdapter, primaryKind, perr := m.primary()
	if perr == nil {
		sid, nerr := primaryAdapter.NewSession(ctx, cfg)
		if nerr == nil {
			return adapterAttempt{adapter: primaryAdapter, kind: primaryKind, sessionID: sid}, nil
		}
		// primary NewSession 失败：关闭 primary adapter，转入下面的回退逻辑（统一用 perr 记 primary 失败原因）。
		_ = primaryAdapter.Close(ctx)
		perr = fmt.Errorf("agent: primary new session: %w", nerr)
	}

	// 到这里 perr != nil（primary 工厂失败 或 其 NewSession 失败）。
	if m.fallback == nil {
		return adapterAttempt{primaryErr: perr}, perr
	}

	fbAdapter, fbKind, ferr := m.fallback()
	if ferr != nil {
		return adapterAttempt{primaryErr: perr}, fmt.Errorf("agent: primary failed (%v); fallback factory: %w", perr, ferr)
	}
	fbSid, ferr2 := fbAdapter.NewSession(ctx, cfg)
	if ferr2 != nil {
		_ = fbAdapter.Close(ctx)
		return adapterAttempt{primaryErr: perr}, fmt.Errorf("agent: primary failed (%v); fallback new session: %w", perr, ferr2)
	}
	return adapterAttempt{adapter: fbAdapter, kind: fbKind, sessionID: fbSid, fellBack: true, primaryErr: perr}, nil
}

// Run 对已 Open 的 task 发送一轮 prompt。同一 task 同时只允许一个 Run（并发第二个返回错误）。
// 内部为该轮派生 cancelable ctx，Cancel/Close 经它中断 SendPrompt。
func (m *AgentManager) Run(ctx context.Context, taskID, prompt string) error {
	m.mu.Lock()
	sess, ok := m.sessions[taskID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("agent: no session for task %s", taskID)
	}

	sess.runMu.Lock()
	if sess.running {
		sess.runMu.Unlock()
		return fmt.Errorf("agent: prompt already running for task %s", taskID)
	}
	sess.lastActivity = time.Now() // 轮开始：重置空闲计时
	runCtx, cancel := context.WithCancel(ctx)
	sess.runCancel = cancel
	sess.running = true
	sess.runMu.Unlock()

	err := sess.adapter.SendPrompt(runCtx, sess.sessionID, prompt)

	sess.runMu.Lock()
	sess.runCancel = nil
	sess.running = false
	sess.lastActivity = time.Now() // 轮结束：空闲计时重新起算
	sess.runMu.Unlock()
	cancel() // 释放 runCtx 资源（已 cancel 时为 no-op）
	return err
}

// Cancel 取消 task 正在进行的 prompt turn：先经 runCancel 中断 SendPrompt 的 ctx，
// 再调 adapter.Cancel 协作取消（ACP 发 session/cancel；PrintAgent 杀进程）。
// 无 session 或无 running turn 时 no-op 返回 nil（便于调用方无脑调）。
func (m *AgentManager) Cancel(ctx context.Context, taskID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[taskID]
	m.mu.Unlock()
	if !ok {
		return nil
	}

	sess.runMu.Lock()
	runCancel := sess.runCancel
	sess.runMu.Unlock()
	if runCancel != nil {
		runCancel()
	}
	return sess.adapter.Cancel(ctx, sess.sessionID)
}

// Close 关闭 task 的会话：从登记表移除、中断 running turn、关 adapter、释放并发槽。幂等。
// 无 session 时 no-op 返回 nil。会话关闭后触发 onSessionClosed 钩子（TaskRunner 清 wires）。
func (m *AgentManager) Close(taskID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[taskID]
	if ok {
		delete(m.sessions, taskID)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}

	sess.closeOnce.Do(func() {
		sess.runMu.Lock()
		runCancel := sess.runCancel
		sess.runMu.Unlock()
		if runCancel != nil {
			runCancel()
		}
		_ = sess.adapter.Close(context.Background())
		sess.sem.release()
		// 通知外部清理会话级资源（wires）；在 closeOnce 内触发，恰好一次
		m.mu.Lock()
		fn := m.onSessionClosed
		m.mu.Unlock()
		if fn != nil {
			fn(taskID)
		}
	})
	return nil
}

// CloseAll 关闭所有会话。幂等。
func (m *AgentManager) CloseAll() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Close(id)
	}
	return nil
}

// session 返回 taskID 对应的会话（无则 nil）。只读引用，调用方需自行同步（runMu/mu）。
func (m *AgentManager) session(taskID string) *managedSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[taskID]
}

// StartReaper 启动后台空闲回收 goroutine：每 interval 扫一次，关闭空闲超过 cfg.IdleTimeout
// 的会话（ACP 保活会话的寿命上限，避免孤儿进程累积）。running 中的会话跳过。
// cfg.IdleTimeout<=0 或 interval<=0 时 no-op。幂等：重复调用复用同一 goroutine。
// 返回后调用方应配合 StopReaper（如服务器关停）停止回收。
func (m *AgentManager) StartReaper(interval time.Duration) {
	if m.cfg.IdleTimeout <= 0 || interval <= 0 {
		return
	}
	m.reaperMu.Lock()
	if m.reaperStop != nil {
		m.reaperMu.Unlock()
		return // 已在跑
	}
	stop := make(chan struct{})
	m.reaperStop = stop
	m.reaperMu.Unlock()

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				m.CloseIdle(now)
			}
		}
	}()
}

// StopReaper 停止空闲回收 goroutine（幂等）。
func (m *AgentManager) StopReaper() {
	m.reaperMu.Lock()
	stop := m.reaperStop
	m.reaperStop = nil
	m.reaperMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// CloseIdle 关闭所有空闲超过 cfg.IdleTimeout 的会话（now 为当前时间；running 中的跳过）。
// 空闲判定：running==false 且 now-lastActivity >= IdleTimeout。
func (m *AgentManager) CloseIdle(now time.Time) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		sess := m.session(id)
		if sess == nil {
			continue // 已被并发 Close
		}
		sess.runMu.Lock()
		idle := !sess.running && now.Sub(sess.lastActivity) >= m.cfg.IdleTimeout
		sess.runMu.Unlock()
		if idle {
			_ = m.Close(id)
		}
	}
}

// Adapter 返回 task 的 adapter（无则 nil）。
func (m *AgentManager) Adapter(taskID string) AgentAdapter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[taskID]; ok {
		return s.adapter
	}
	return nil
}

// SessionID 返回 task 的 sessionID（无则 ""）。
func (m *AgentManager) SessionID(taskID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[taskID]; ok {
		return s.sessionID
	}
	return ""
}

// Kind 返回 task 的 adapter Kind（无则 ""）。
func (m *AgentManager) Kind(taskID string) AgentKind {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[taskID]; ok {
		return s.kind
	}
	return ""
}

// FellBack 返回 task 是否走了回退路径（无则 false）。
func (m *AgentManager) FellBack(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[taskID]; ok {
		return s.fellBack
	}
	return false
}

// projectSem 取（或创建）projectID 对应的并发信号量。MaxConcurrent<=0 返回 nil（不限）。
func (m *AgentManager) projectSem(projectID string) *semaphore {
	if m.cfg.MaxConcurrent <= 0 {
		return nil
	}
	v, _ := m.projSems.LoadOrStore(projectID, newSemaphore(m.cfg.MaxConcurrent))
	return v.(*semaphore)
}

// semaphore agent 包内本地定义的计数信号量（不导出；core 包有自己的实现，互不相干）。
// nil 接收者时 acquire/release 为 no-op，便于 MaxConcurrent<=0（不限）路径无分支调用。
type semaphore struct{ ch chan struct{} }

func newSemaphore(n int) *semaphore { return &semaphore{ch: make(chan struct{}, n)} }

func (s *semaphore) acquire() {
	if s == nil {
		return
	}
	s.ch <- struct{}{}
}

func (s *semaphore) release() {
	if s == nil {
		return
	}
	select {
	case <-s.ch:
	default:
	}
}

// ManagerConfigFromPieqi 由 config.PieqiConfig 构建 ManagerConfig。
//
// 把 PieqiConfig.ACP 映射到 ManagerConfig.UseACP/ACPConfig；PrintConfig.PermissionMode 取自
// PieqiConfig.PermissionMode（PrintAgent 回退路径用）；MaxConcurrent 取自 PieqiConfig.MaxConcurrentPerProject。
//
// 消费方（main/wire 层）用法：
//
//	mgr := agent.NewAgentManager(agent.ManagerConfigFromPieqi(cfg.Pieqi), logger)
//	runner.SetAgentManager(mgr, cfg.Pieqi.ACP.UseACP, cfg.Pieqi.HookTimeout)
//
// 注：AgentType/SpawnCommand/InitTimeout 都在 cfg.ACP 里，原样透传给 ACPAgent。
// PrintConfig.Binary 留空（NewPrintAgent 兜底为 "claude"）；Model 留空（claude 自决，由 ~/.claude 维护）；
// SysPrompt 留空（Phase 1 sysPrompt 经 TaskRunner 注入 worktree settings，不经 PrintConfig）。
func ManagerConfigFromPieqi(pieqi config.PieqiConfig) ManagerConfig {
	return ManagerConfig{
		UseACP:        pieqi.ACP.UseACP,
		MaxConcurrent: pieqi.MaxConcurrentPerProject,
		ACPConfig:     pieqi.ACP,
		IdleTimeout:   pieqi.ACP.IdleTimeout, // ACP 会话空闲回收阈值（轮间保活上限；<=0 禁用）
		PrintConfig: PrintConfig{
			PermissionMode: pieqi.PermissionMode,
		},
	}
}
