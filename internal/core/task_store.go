package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TaskStore 任务的内存索引 + 文件持久化。
//
// 一任务一文件 data/tasks/<id>.json，每次状态变更加 os.Rename 原子写。
// 启动时遍历 data/tasks/ 重建索引；waiting_input 的孤儿任务（claude 子进程已死）
// 标记 failed，保留 worktree 供手动 resume。
type TaskStore struct {
	mu       sync.RWMutex
	tasksDir string
	tasks    map[string]*model.Task
	// eventRetention 单任务事件保留上限（0 = 全部保留，见 AppendEvent）。
	// 与 tasks 共用 mu：它在 Update 的 mutator 内被读取。
	eventRetention int
	// interruptedByRestart 是「因计划内自重启而被打断、待自动接续」的会话 id 集合。
	//
	// 与"进程崩了"必须区分（这正是本次事故的根因）：崩了是意外，标 failed 合理；
	// 自重启是**调用方主动发起**的计划内动作，把它当故障会让用户看到一批
	// 莫名其妙的 failed，还得人工 resume。由 SetInterruptedByRestart 在
	// store 构造后、服务开始服务前注入（见 main 的接线顺序）。
	interruptedByRestart map[string]bool
	// badFiles 是启动时读不出来的任务文件（见 load / BadTaskFile）。
	//
	// 记下来而不是就地吞掉：坏文件是**数据丢失的现场**，必须能在日志与诊断导出里
	// 看到"丢了几个、坏在哪、证据留在哪"。原先裸 continue 让这一切无声无息。
	badFiles []BadTaskFile

	// attachments 工具结果图片的落盘存储（见 attachments.go）。
	//
	// nil = 未接线：此时工具结果里的图**不落盘**，事件上只留空的 Images。
	// 这与"未接线时别的反馈能力降级"同一口径（api.Server 的 nil-safe 注入），
	// 让只用到事件流的测试不必为此建目录。
	attachments *AttachmentStore
	// logger 只用于附件落盘失败这类**本来会被静默吞掉**的诊断（见 saveToolImages）。
	logger *zap.Logger
}

// SetAttachments 注入图片附件存储（main 启动时接线；未接线时工具图片不落盘）。
func (s *TaskStore) SetAttachments(a *AttachmentStore, logger *zap.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachments = a
	s.logger = logger
}

// Attachments 返回已注入的附件存储（未接线时 nil）。
func (s *TaskStore) Attachments() *AttachmentStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attachments
}

// NewTaskStore 创建并从磁盘恢复任务索引。
//
// interrupted 是「因计划内自重启而被打断、待自动接续」的会话 id（可空）。
// 它必须**在 load 之前**就位：load 是唯一决定这些任务是变成 failed 还是
// 保留待接续的地方，晚一步注入就没机会干预了。因此由调用方在构造时传入，
// 而不是提供事后 Setter（见 main 里 ResolveRestartHandoff 的取值顺序）。
//
// 变参而非显式第二参数：调用点绝大多数是测试里的 NewTaskStore(dir)，
// 它们与"自重启接续"毫无关系；为了一个只在生产启动路径用到的参数去改
// 三十多处测试，只会让噪音淹没真实改动。生产侧显式传 interruptedIDs。
func NewTaskStore(tasksDir string, interrupted ...string) (*TaskStore, error) {
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir tasks dir: %w", err)
	}
	s := &TaskStore{
		tasksDir: tasksDir,
		tasks:    make(map[string]*model.Task),
	}
	if len(interrupted) > 0 {
		s.interruptedByRestart = make(map[string]bool, len(interrupted))
		for _, id := range interrupted {
			s.interruptedByRestart[id] = true
		}
	}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("load tasks: %w", err)
	}
	return s, nil
}

// InterruptedByRestart 报告该任务是否因计划内自重启被打断而保留了可接续状态。
func (s *TaskStore) InterruptedByRestart(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.interruptedByRestart[id]
}

// Create 新建一个 pending 任务并持久化。
func (s *TaskStore) Create(t *model.Task) (*model.Task, error) {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.ClaudeSessionID == "" {
		t.ClaudeSessionID = uuid.New().String()
	}
	now := time.Now()
	t.Status = model.TaskPending
	t.CreatedAt = now
	t.UpdatedAt = now

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = t

	if err := s.persist(t); err != nil {
		delete(s.tasks, t.ID)
		return nil, err
	}
	return t, nil
}

// snapshot 返回可交给调用方的任务副本：结构体连同 **Events 元素数组**一起拷贝。
//
// 为什么连元素数组也要拷，而不是 `cp := *t` 了事：Events 是**唯一**会被原地改写
// 的切片 —— agent_stream / task_runner 的批量合并都直接写最后一个元素
// （`last.Text += text`、`last.At = now`）。浅拷贝会让副本与活体共享底层数组，
// 于是这条真实路径上会出问题：取快照 → 释放锁 → （前端/WS 序列化这份快照）
// 与（流式 delta 原地改写同一个元素）并发。Go 的 string 头是两个机器字、非原子写，
// 撕裂读轻则让前端看到截断/串味的文本，重则按一个「指针是旧的、长度是新的」的
// 头去读内存 —— 这属于数据竞争（未定义行为），不是观感问题。
//
// 只拷元素数组、不深拷 string/RawMessage 字节：那些是不可变的，共享没有风险；
// 写方 `last.Text += text` 是**换一个新 string 写回字段**，不是改旧 string 的字节。
// 代价按元素算（TaskEvent ≈ 128B，190 个事件 ≈ 24KB），相对紧随其后的整份
// JSON 序列化可以忽略。
func snapshot(t *model.Task) *model.Task {
	cp := *t
	cp.Events = slices.Clone(t.Events)
	return &cp
}

// Get 返回任务副本（调用方可安全修改）。
func (s *TaskStore) Get(id string) (*model.Task, bool) {
	s.mu.RLock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.RUnlock()
		return nil, false
	}
	cp := snapshot(t)
	s.mu.RUnlock()
	return cp, true
}

// ListSummaryEvents 返回全部任务副本 + 各自的事件总数（同序），按 CreatedAt 升序。
//
// 给列表/快照视图用：那里**不需要**事件内容，只需要条数（TaskSummary.EventCount）。
// 与 List 的唯一区别是**不克隆 Events 切片**（见下方 2026-10-22 的性能判据）。
//
// 为什么值得单开一个方法，而不是让调用方自己 len(t.Events)：
// 事件总数必须在**持锁时**读取才有意义 —— 异步合并会原地改写最后一个元素、
// 释放锁后还可能追加，锁外读会读到别的时刻的条数。这里在锁内数完，一次锁穿越。
//
// 判据（实测 2026-10-22）：89 个任务共 **3.9 万条事件 / 磁盘 33.9MB**，单个任务
// 事件占其 JSON 的 **83%**（2120 条里 834 条 thinking、881 条 tool_use）。而
// `GET /api/tasks` 与 WS 首帧只下发 3.3MB 的轻量视图 —— 为得出一个 `int` 把 33.9MB
// 的 TaskEvent 结构体全拷一遍，纯属白付，且每个流式 delta 都会再付一次
// （TaskRunner 每轮在持写锁时 persist，列表/快照线程等这把锁）。
func (s *TaskStore) ListSummaryEvents() ([]*model.Task, []int) {
	s.mu.RLock()
	// 任务与计数**成对**收集后一次性排序：分别排两个切片会让 Counts 与 Tasks 错位
	// （Create 时两个切片同序，但排序比较键不同就会出现"计数跟着别的任务走"）。
	pairs := make([]summaryPair, 0, len(s.tasks))
	for _, t := range s.tasks {
		cp := *t // 不克隆 Events：本函数的契约是"调用方不读事件内容"
		cp.Events = nil
		pairs = append(pairs, summaryPair{task: &cp, count: len(t.Events)})
	}
	s.mu.RUnlock()
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].task.CreatedAt.Before(pairs[j].task.CreatedAt) })
	out := make([]*model.Task, len(pairs))
	counts := make([]int, len(pairs))
	for i, p := range pairs {
		out[i], counts[i] = p.task, p.count
	}
	return out, counts
}

type summaryPair struct {
	task  *model.Task
	count int
}

// List 返回全部任务副本（含事件）**浅拷贝**，按 CreatedAt 升序。
func (s *TaskStore) List() []*model.Task {
	s.mu.RLock()
	out := make([]*model.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, snapshot(t))
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Update 用 mutator 修改任务并持久化，返回更新后的副本。
// 若 mutator 返回 false 表示无变更，跳过持久化。
//
// **返回契约**：改动一旦在内存生效就一定会返回快照，即便落盘失败 —— 落盘失败时
// 返回 (已生效的快照, 非 nil error)。调用方**必须**先看 error 再决定要不要用这个
// 快照，但**不能**把 error 当成「任务不存在」：两者语义完全不同（见下）。
//
// ⚠️ 写锁**贯穿到落盘结束**，不能只护住内存改动：
//  1. 同一个任务会被多处并发 Update —— 流式文本增量（agent_stream.appendTextDelta，
//     每个 delta 一次）、ACP 权限回调（agent_perm）、用户干预（RecordIntervention）。
//     提前放锁的话，两个 goroutine 会同时操作同一个 `<id>.json.tmp`：一个还在
//     os.WriteFile（句柄未关，Windows 的 Go 默认 share mode 不含 FILE_SHARE_DELETE），
//     另一个已经 os.Rename → ERROR_SHARING_VIOLATION
//     「The process cannot access the file because it is being used by another process.」
//     生产上表现为间歇性 `record intervention failed`。
//  2. **落盘顺序必须等于变更顺序**。快照是浅拷贝，两个快照的 Rename
//     先后不受控，旧快照可能后落地，把刚追加的事件**写回退**掉（实测磁盘事件数
//     少于成功 Update 次数）。锁内 marshal 同时保证了快照的一致性（见 snapshot）。
//
// mutator 只允许改内存字段，**不得**回调 store 的任何取锁方法（Update/Get/List/
// Delete，以及Attachments/EventRetention/BadFiles/InterruptedByRestart/imageSink
// 等一切会取 s.mu 的访问器）—— 那会在持写锁时自锁。注意 RWMutex **不可重入**：
// 持写锁时再取读锁（RLock）同样永久死锁，2026-10-24 线上事故正是
// saveToolImages 在 mutator 里 RLock 引爆的（见 agent_toolcall.go 的 imageSink）。
func (s *TaskStore) Update(id string, mutator func(*model.Task) bool) (*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if !mutator(t) {
		return snapshot(t), nil
	}
	t.UpdatedAt = time.Now()
	cp := snapshot(t)
	if err := s.persist(cp); err != nil {
		// 内存已生效、磁盘没跟上。**必须**连同快照一起返回错误，而不是返回 nil：
		// 返回 nil 会让调用方把「落盘失败」误判成「任务不存在」——
		// agent_perm.setWaitingApproval 正是这么被坑的：err != nil 被当成
		// 「task 不存在或已终态」，于是审批卡不展示、直接静默 Deny，同时内存里
		// 留一个永远没人能 Resolve 的 waiting_input。
		return cp, err
	}
	return cp, nil
}

// SetEventRetention 设置单任务事件保留上限（0 = 全部保留）。
// 由全局偏好（Settings.EventRetention）驱动；运行期可改，只影响之后产生的事件。
func (s *TaskStore) SetEventRetention(n int) {
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	s.eventRetention = n
	s.mu.Unlock()
}

// EventRetention 返回当前上限。
func (s *TaskStore) EventRetention() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eventRetention
}

// AppendEvent 追加一条任务事件：统一分配**单调** Seq，并按上限裁剪最旧的事件。
//
// ⚠️ 调用方**必须**已经持有 s.mu —— 它只为 `TaskStore.Update` 的 mutator 设计
// （mutator 在锁内运行，所以这里读 s.eventRetention 是安全的）。
// 单独调用而不持锁会与外部的 Update 竞争。
//
// 为什么裁剪放在这里而不是各自 append 后再收拾：这是**唯一**的追加入口，
// 保留上限才不会在某个新写的路径上被漏掉（漏掉的表现是内存缓慢上涨，
// 而长任务是这个产品里最正常的用法）。
func (s *TaskStore) AppendEvent(t *model.Task, ev model.TaskEvent) {
	if t.NextEventSeq <= 0 {
		// 旧任务（本次改动前落盘）没有计数器：用最后一个事件的 Seq 推起点。
		// 不能用 len(Events)+1 —— 若该任务此前已被裁过，len 会偏小，
		// 于是整条序列从中间开始重复。lastEventSeq 取的是**实际序号**。
		t.NextEventSeq = lastEventSeq(t.Events) + 1
		if t.NextEventSeq <= 0 {
			t.NextEventSeq = 1
		}
	}
	ev.Seq = t.NextEventSeq
	t.NextEventSeq++
	t.Events = append(t.Events, ev)

	if s.eventRetention > 0 && len(t.Events) > s.eventRetention {
		drop := len(t.Events) - s.eventRetention
		kept := make([]model.TaskEvent, s.eventRetention)
		copy(kept, t.Events[drop:])
		// 换成新底层数组，而不是 t.Events[drop:] —— 后者会让被裁掉的部分
		// 一直挂在同一个数组上（append 只是移动切片的起点，容量不释放），
		// 那正是这个开关要解决的问题。
		t.Events = kept
	}
}

// lastEventSeq 返回事件流里最大的 Seq（空则 0）。
func lastEventSeq(events []model.TaskEvent) int {
	last := 0
	for _, e := range events {
		if e.Seq > last {
			last = e.Seq
		}
	}
	return last
}

// Delete 删除任务记录与磁盘文件。
func (s *TaskStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	delete(s.tasks, id)

	// 与 Create/Update 一样把文件操作放在锁内：否则某个已在途的快照可能在
	// 删除之后才落地，凭空把刚删掉的文件写回来（内存里已经没有这条任务，
	// 磁盘上却多出一个孤儿 .json，重启时被 load() 当作真实任务捞回来）。
	path := s.path(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// load 启动恢复：遍历 tasksDir 重建索引，孤儿 waiting_input 标 failed。
func (s *TaskStore) load() error {
	entries, err := os.ReadDir(s.tasksDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		full := filepath.Join(s.tasksDir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			// 读不到就跳过：可能是并发写期间的瞬时占用（rename 窗口）。
			// 但要**留痕**，否则一个持续读不出来的文件等于静默丢任务。
			s.recordBadTaskFile(e.Name(), fmt.Sprintf("读文件失败：%v", err))
			continue
		}
		var t model.Task
		if json.Unmarshal(data, &t) != nil {
			// ★ 解析失败**不能静默跳过**（2026-10-13 修）。
			//
			// 原先这里是裸 `continue`：文件坏了就当作"没有这个任务"，不打日志、
			// 不计数、不保留 —— 用户看到的是"任务凭空消失"，而日志里一个字都没有。
			// 最可能的坏法正是**全长零字节**：rename 已改目录项、数据还在页缓存时
			// 断电（与 magpie issue #1505 同一失效模式，见 persist 的说明），
			// 而零字节 JSON 必然解析失败。
			//
			// 现在的处置（对齐 magpie internal/lastgood 的"a file that is there but
			// not valid is unknown, never empty"）：
			//   1. 把坏文件**另存**为 <name>.bad-<时间>，绝不就地丢弃
			//      —— 它是唯一可能事后恢复的证据；
			//   2. 记进 badFiles，由调用方在启动日志里显式报出来。
			// 仍然跳过（不阻塞启动）：一个坏文件不该让整个服务起不来。
			s.recordBadTaskFile(e.Name(), describeBadJSON(data))
			s.quarantineBadTaskFile(full, e.Name())
			continue
		}
		if t.ID == "" {
			s.recordBadTaskFile(e.Name(), "缺少 id 字段")
			s.quarantineBadTaskFile(full, e.Name())
			continue
		}
		// 进程重启后 claude 子进程已死。
		// - running：进程被杀，标 failed。
		// - waiting_input(approval, 路径 A)：进程挂在 hook channel，channel 已丢无法恢复，标 failed。
		// - waiting_input(choice, 路径 B)：进程本就 end_turn 退出（合法持久态），保留 waiting_input，
		//   用户隔天仍可选项触发 Resume 续跑。
		//
		// ⚠️ 不变量：**waiting_input(approval) 必须有一个活着的 owner**。
		// 卡片是"进程挂在 channel 上等人答"的可视化，owner 一死卡片就是遗物。
		// 违反它的表现是「卡片可点，点了必然 409」（2026-10-13 线上事故）：
		// 交接单的 affected 列表是**发起重启那一刻**的快照，而审批卡可能在此之后才
		// 产生（该次实测：快照 12:53:32.860，卡片 12:53:36.360，相差 3.5s）——
		// 于是这张卡既不在接续名单里、也没人清算，磁盘上留下一个 status=waiting_input
		// 且 CurrentDecision 非空的**僵尸态**。所以下面标 failed 时必须一并清空
		// CurrentDecision，让 UI 不再渲染那张卡。
		//
		// 例外（本次自重启事故的修复）：若这张任务是**计划内自重启**打断的
		// （id 在 interruptedByRestart 里），就**保留 running**，交由启动流程
		// 自动 resume（见 TaskRunner.ResumeInterrupted）。
		// 依据：自重启是调用方主动发起的动作，不是故障。按故障处理会让用户
		// 看到一批莫名的 failed 并被迫人工 resume —— 那正是"老是中断"的体感来源。
		// 保留 running 也符合既有语义：Resume() 明确接受 running（排队续跑）。
		if s.interruptedByRestart[t.ID] {
			// 保留待接续 —— 但**只保留接续逻辑认得的形状**。
			// ResumeInterrupted 只会处理 running / waiting_input；其余状态（终态、
			// pending）它第一关就 continue 掉，不会有人来收尾。所以这里必须替它
			// 把"没有 owner 的审批卡"清掉，否则那张卡会穿过整个接续流程留在盘上，
			// 前端照样渲染、点了照样 409（与上面同一个不变量）。
			if t.CurrentDecision != nil && t.CurrentDecision.Kind != model.DecisionKindChoice &&
				t.Status != model.TaskWaitingInput && t.Status != model.TaskRunning {
				t.CurrentDecision = nil
				t.UpdatedAt = time.Now()
				_ = s.persist(&t)
			}
			s.tasks[t.ID] = &t
			continue
		}
		if t.Status == model.TaskRunning {
			t.Status = model.TaskFailed
			if t.Error == "" {
				t.Error = "进程被重启打断"
			}
			now := time.Now()
			t.FinishedAt = &now
			t.UpdatedAt = now
			_ = s.persist(&t) // 尽力持久化修正
		} else if t.Status == model.TaskWaitingInput {
			if t.CurrentDecision != nil && t.CurrentDecision.Kind == model.DecisionKindChoice {
				// 路径 B：保留 waiting_input，可继续选
			} else {
				t.Status = model.TaskFailed
				if t.Error == "" {
					t.Error = "进程被重启打断"
				}
				// **必须同时清掉那张审批卡**（本次事故的修复，见下方长注释）。
				// 留着它 = 前端照常渲染出一张可点的卡，而后端 pending 表里
				// 没有任何东西能应答 —— 用户点「批准」只会拿到 409。
				t.CurrentDecision = nil
				now := time.Now()
				t.FinishedAt = &now
				t.UpdatedAt = now
				_ = s.persist(&t)
			}
		}
		s.tasks[t.ID] = &t
	}
	return nil
}

// renameAttempts persist 里改名的尝试次数（含首次）。
// 依据见 rename_windows.go：Windows 上改名会被杀软/索引器瞬时挡住，
// 实测隔 1ms 重试即成功，所以给 5 次、退避 2/4/6/8ms（最坏多花 20ms）。
const renameAttempts = 5

// renameBackoffUnit 重试的退避单位：第 n 次重试前等 n * 该值。
const renameBackoffUnit = 2 * time.Millisecond

// renameWithRetry 对改名做瞬时错误重试；非瞬时错误立即上报，不做无谓等待。
// 抽成独立函数（而不是内联进 persist）是为了让「重试几次、什么错误不该重试」
// 能被确定性测试覆盖 —— 见 rename_windows_test.go。
func renameWithRetry(rename func() error) error {
	var err error
	for attempt := 0; attempt < renameAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * renameBackoffUnit)
		}
		if err = rename(); err == nil {
			return nil
		}
		if !isTransientRenameErr(err) {
			return err
		}
	}
	return err
}

// persist 原子落盘：写 tmp → **刷盘** → rename。
//
// 调用方**必须**持有 s.mu（Create/Update/Delete 都在锁内调用；load 只在
// NewTaskStore 里单线程跑）。tmp 名带 pid：进程内已由写锁串行化，这个后缀防的是
// 「同一 data 目录被两个 pieqi 实例同时打开」—— 旧实例没退干净是这里的常见情形，
// 跨进程撞在同一个 tmp 上照样是 sharing violation。
//
// 改名带重试：**这不是补救并发的补丁，而是另一个独立成因**。写锁把同进程的
// 并发写串行化之后，单线程纯循环 400 次仍会失败 3~5 次（ERROR_ACCESS_DENIED，
// temp 与非 temp 目录都有），来源于杀软/索引器对刚落盘的目标文件的瞬时持有；
// 不加这层重试，生产上照样会间歇性丢一条干预/事件。
//
// ⚠️ 为什么必须在 rename **之前** fsync（2026-10-13 补）：
//
// rename 只是一个目录项改动，它**到盘的时刻由 OS 自行安排，而它背后的数据更晚**。
// 于是存在一个致命的中间态：目录项已指向新文件，而新文件的数据块还留在页缓存里。
// 此刻断电/蓝屏 ⇒ 得到一个**长度正确、内容全是 0** 的文件。
//
// 这不是理论担忧：magpie 记录过一模一样的事故（issue #1505，其 internal/steady
// 包的注释）—— 机器在 write 与 rename 之间down掉，`logins.json` 变成"全长零字节"，
// 程序读出"零个账号"，**下一次写入把这个状态固化**，账号就此丢失。
//
// pieqi 的暴露面完全相同，且后果更隐蔽：坏掉的 `<id>.json` 会被 load 静默跳过
// （见 load 里对 Unmarshal 失败的处理），用户看到的是"任务凭空消失"，而日志里
// 一个字都没有。
//
// 所以顺序写死为 write → Sync → close → rename：Sync 保证数据先落盘，
// 之后 rename 才可能把目录项指过去。宁可每次多花一次 fsync 的代价
// （本地 SSD 上亚毫秒级，而这是**事件流落盘**的必经路径），也不要一个
// "看起来正常、实际全零"的任务文件。
func (s *TaskStore) persist(t *model.Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	path := s.path(t.ID)
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())

	// 手写而不是 os.WriteFile：后者内部 create→write→close 一气呵成，
	// 拿不到中间的文件句柄，也就没机会在 close 前 Sync。
	if err := writeFileSynced(tmp, data, 0644); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	errRename := renameWithRetry(func() error { return os.Rename(tmp, path) })
	if errRename != nil {
		// 落盘失败别把 tmp 留在目录里（load 只认 *.json，无害但会越积越多）。
		_ = os.Remove(tmp)
	}
	return errRename
}

// writeFileSynced 写文件并确保内容**真的落盘**（close 之前 fsync）。
//
// 抽成独立函数是为了让"必须先 Sync 再 rename"这条纪律有一个可测试的落点
// —— 见 task_store_sync_test.go。
func writeFileSynced(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	// Sync 失败**必须**当作写入失败：它意味着数据可能没落盘，
	// 此时若继续 rename，就等于亲手把目录项指向一个内容不可信的文件。
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *TaskStore) path(id string) string {
	return filepath.Join(s.tasksDir, id+".json")
}

// BadTaskFile 描述一个启动时读不出来的任务文件。
//
// 存在的理由：这类失败**必须可见**。原先 load 对坏文件是裸 continue，
// 表现是"任务凭空消失"而日志全干净 —— 排查时无从下手（不知道有几个、坏在哪）。
type BadTaskFile struct {
	// Name 是文件名（<taskid>.json）。
	Name string
	// Reason 是人读的坏因（空的 / 全零 / 解析失败 / 缺 id）。
	Reason string
	// KeptAs 是坏文件被另存的路径（"" = 另存失败，原文件仍在原处）。
	KeptAs string
}

// recordBadTaskFile 记下一个坏任务文件（load 期间单线程调用，无需加锁）。
func (s *TaskStore) recordBadTaskFile(name, reason string) {
	s.badFiles = append(s.badFiles, BadTaskFile{Name: name, Reason: reason})
}

// quarantineBadTaskFile 把坏文件另存为 <name>.bad-<时间>，并让原名消失。
//
// 为什么是"另存 + 移除原名"而不是只打日志：原名留着会让每次启动都重复报同一个错
// （噪音淹没真问题），而直接删掉则丢掉唯一可能事后恢复的证据。
// 另存解决了这个两难 —— 错只报一次，证据还在。
//
// 后缀 `.bad-<时间>` 不含 `.json`，所以不会被 load 再次当成任务文件捡起来。
func (s *TaskStore) quarantineBadTaskFile(full, name string) {
	kept := fmt.Sprintf("%s.bad-%s", full, time.Now().Format("20060102-150405"))
	if err := os.Rename(full, kept); err != nil {
		// 另存失败（权限/占用）不致命：文件仍在原处，只是每次启动会再报一次。
		return
	}
	s.badFiles[len(s.badFiles)-1].KeptAs = kept
}

// BadFiles 返回启动时读不出来的任务文件（供 main 记日志/诊断导出）。
//
// 返回副本：调用方不该改内部切片。
func (s *TaskStore) BadFiles() []BadTaskFile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.badFiles)
}

// describeBadJSON 说明一份解析不了的 JSON 坏在哪，用于日志与诊断。
//
// 特意把"全长零字节"单独识别出来：它是最需要警惕的一种坏法 —— 不是文件写错，
// 而是**掉电/蓝屏**在 rename 与数据落盘之间留下的痕迹（见 persist 的说明），
// 意味着磁盘上可能还有别的文件处于同样状态。含糊地报"解析失败"会让人往
// "数据格式不对"的方向找，而真相在存储层。
func describeBadJSON(b []byte) string {
	switch {
	case len(b) == 0:
		return "文件为空（0 字节）"
	case len(bytes.Trim(b, "\x00")) == 0:
		// 全长但每个字节都是 0x00：掉电特征，与 magpie issue #1505 同一模式。
		return fmt.Sprintf("%d 字节但全为 0x00（掉电/蓝屏留下的空块特征）", len(b))
	case len(bytes.TrimSpace(b)) == 0:
		return "文件只有空白字符"
	}
	return "JSON 解析失败"
}
