// Package core 的工具调用连接器：把 AgentAdapter 的 OnToolCallUpdate 回调接到任务事件流，
// 映射为 Phase 1 的 EventToolUse / EventToolResult（M3 落地）。
//
// 设计要点（同 WireContentDelta / WirePermission 的连接器范式：core→agent 单向，一个 wire 绑一个 taskID）：
//   - IsNew=true（ACP ToolCall，工具调用开始）→ 追加 EventToolUse{ToolName:Title, ToolUseID:ToolCallID}
//   - IsNew=false（ACP ToolCallUpdate，状态变更）：
//   - status=completed → 追加 EventToolResult{...}
//   - status=failed    → 追加 EventToolResult{IsError:true}
//   - 其他状态（pending/in_progress 等）→ 忽略
//   - ★ 同一个 ToolCallID 的"后到的标题/入参"**合并进已有那条**，不再追加空壳
//     （mergeToolUseIntoTask；2026-10-21 修，判据见该函数注释）
//   - 经 store.Update 追加事件 + Publish task_updated（工具事件离散，全量重绘合适，不用 delta）。
//
// 注：ToolCallUpdateInfo.RawInput/RawOutput 由 ACP 适配器填充（取自 ACP RawInput/RawOutput），
// 经 mapToolCallEvent 映射到 EventToolUse.Input（原样 JSON）与 EventToolResult.Result（转 string），
// 行为与 Phase 1 一致；ToolName 在 ToolCallUpdate 缺 Title 时回查同 ToolUseID 的最后一个
// EventToolUse 补全。
//
// 依赖方向：core → agent（单向），无循环。
package core

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// ToolCallHandle WireToolCall 返回的句柄，Unwire 可拆卸回调。幂等。
type ToolCallHandle struct {
	adapter agent.AgentAdapter
	// wire 供 Unwire 前落盘残留缓冲（见 Unwire 的"先 Flush 再拆"说明）。
	wire *toolCallWire
}

// Unwire 注销回调（置空），断开 adapter 到事件流的工具调用推送。幂等。
// 任务结束/取消时调用，避免后续误触发的工具事件再写入已终态 task。
//
// ⚠️ 与 DeltaHandle.Unwire 同一纪律：**先 Flush 再拆回调**。合并窗口内的待落盘事件
// 还没进 store，直接拆会丢掉最后一段工具事件（表现：时间线末尾少一条工具卡片，
// 而磁盘上也确实没有 —— 不是渲染问题，是真丢了）。
func (h *ToolCallHandle) Unwire() {
	if h == nil {
		return
	}
	if h.wire != nil {
		h.wire.Flush()
		h.wire = nil
	}
	if h.adapter == nil {
		return
	}
	h.adapter.OnToolCallUpdate(nil)
	h.adapter = nil
}

// Flush 立刻把合并窗口内待落盘的工具事件写进 store（幂等；无待落盘内容时是空操作）。
//
// 与 DeltaHandle.Flush 同一用途：Unwire 内部会先调它，调用方一般不需要手动调；
// 暴露出来主要是给测试与"想在某个边界立刻可见"的场景。
func (h *ToolCallHandle) Flush() {
	if h == nil || h.wire == nil {
		return
	}
	h.wire.Flush()
}

// WireToolCall 把一个 AgentAdapter 的 OnToolCallUpdate 回调接到任务事件流。
//
// 注册后，adapter 每产出一个工具调用更新（ACP ToolCall/ToolCallUpdate），回调内：
//  1. 更新先进**合并缓冲**（不立刻落盘，见 FlushToolCallWindow 的代价说明）；
//  2. 窗口到期或 Flush 时，**一次** store.Update 依次施加整批更新
//     （映射为 EventToolUse/EventToolResult，Seq 自增），再 Publish 一次 task_updated
//     （全量，前端按 Seq 渲染，行为与 Phase 1 一致）。
//
// 返回 *ToolCallHandle，调用方在任务结束/取消时 Unwire 拆卸（会先 Flush）。
func WireToolCall(adapter agent.AgentAdapter, bus *EventBus, store *TaskStore, taskID string) *ToolCallHandle {
	w := &toolCallWire{bus: bus, store: store, taskID: taskID, flushWindow: FlushToolCallWindow}
	adapter.OnToolCallUpdate(w.onUpdate)
	return &ToolCallHandle{adapter: adapter, wire: w}
}

// toolCallWire 持有 OnToolCallUpdate 回调所需的依赖。taskID 固定（一个 wire 对应一个 task）。
type toolCallWire struct {
	bus    *EventBus
	store  *TaskStore
	taskID string

	// flushWindow 落盘合并窗口（见 FlushToolCallWindow）。<=0 时不排定时器，
	// 由调用方显式 Flush（仅测试/特殊场景用，生产恒为 FlushToolCallWindow）。
	flushWindow time.Duration

	// mu 保护下面的待落盘队列。onUpdate 由 agent 适配器的 goroutine 调用，
	// Flush 可能由任务结束路径（另一个 goroutine）调用，两者会并发。
	//
	// ⚠️ 锁顺序：**mu 先于 store.mu**。持 mu 期间会调用 store.Update（拿 store.mu），
	// 所以任何需要同时拿两把锁的路径都必须按这个顺序，否则与关闭路径（onAgentSessionClosed）
	// 互等死锁。
	mu sync.Mutex
	// pending 待落盘的工具调用更新，按到达顺序保存。
	//
	// 为什么能合并：这些更新各自独立的 mutator 体只是"补全已有那条 / 追加一条"，
	// 而它们都作用于同一个 task 的事件流。合并后一次 Update 依次施加，结果与逐条
	// 落盘完全一致 —— 只有写盘次数变了。**顺序不能乱**：tool_use 必须先于对应
	// tool_result 落下，否则 lookupToolName 回查不到工具名。
	pending []agent.ToolCallUpdateInfo
	// flushTimer 合并窗口的定时器；非 nil 表示已排定一次落盘。
	flushTimer *time.Timer

	// flushN 累计**实际落盘次数**（测试与诊断用）。
	//
	// 与 contentDeltaWire.flushN 同一理由：只看最终内容正确，合并完全失效
	// （退化成每个更新落盘一次）的回归也能通过测试 —— 而写放大正是本次要修的东西。
	flushN atomic.Int64
}

// flushCount 返回累计落盘次数（测试与诊断用）。
func (w *toolCallWire) flushCount() int64 { return w.flushN.Load() }

// FlushToolCallWindow 是工具调用事件的**落盘**合并窗口。
//
// 为什么需要（2026-10-22 性能诊断）：`TaskStore.Update` 的写锁**贯穿到 fsync+rename**，
// 且 `persist` 每次 marshal 整个 task 含全部 events —— 代价是 **O(事件总数) 每次**。
// 实测 2950 事件 / 2.03MB 的任务单次 Update = **20.6ms**。而一条 agent 工具通知原先
// 最多触发 2 次 Update（backfill + 主 Update），真实任务有 2886 条工具事件 ⇒
// 每任务累计 2 分钟以上的**全库串行阻塞**：审批、流式 delta、WS 快照全部排队，
// 表现就是「网页卡着不动，刷新之后才有审核弹出」。
//
// 与 FlushDeltaWindow 取同一个值（200ms）的理由也一样：它只影响**落盘**，不影响推送语义
// 的实时性——只是把"每条工具事件一次全量序列化"压成"每 200ms 一次批量"。
// 真正影响观感的是"工具卡片什么时候出现"，而 200ms 在人的感知阈值以下。
//
// 正确性由 Flush 保证：任务结束/取消时必定落盘，不依赖窗口到期。
const FlushToolCallWindow = 200 * time.Millisecond

// onUpdate OnToolCallUpdate 回调实现：把更新攒进合并缓冲，窗口到期后批量落盘 + 推送。
//
// ⚠️ **这里不再直接 store.Update**（2026-10-22 性能修复）。原因见 FlushToolCallWindow：
// 单次 Update 是 20.6ms 且持全局写锁，逐条落盘会让审批/流式/快照全部排队。
//
// 顺序语义不变：pending 按到达顺序保存，Flush 时**一次 Update 依次施加**，
// 与逐条落盘的结果逐字节一致（只少了几千次 fsync）。
func (w *toolCallWire) onUpdate(info agent.ToolCallUpdateInfo) {
	w.mu.Lock()
	w.pending = append(w.pending, info)
	if w.flushTimer == nil {
		w.flushTimer = time.AfterFunc(w.flushWindow, w.Flush)
	}
	w.mu.Unlock()
}

// Flush 把缓冲里的工具调用更新批量落盘并推送。幂等；无待落盘内容时是空操作。
//
// 由三条路径调用：窗口定时器到期、ToolCallHandle.Unwire（任务结束）、测试直接调用。
//
// ⚠️ **锁顺序**（违反即静默死锁）：
//  1. 持 w.mu 调 store.Update（即 w.mu → store.mu，与关闭路径同向）；
//  2. **Publish 必须在放掉 store 写锁之后**。理由不是洁癖：ws.go 的 writeSnapshot
//     在持有 bus.mu.RLock 的同时调 store.List()（要 store.mu）；若在持 store.mu 时
//     Publish（要 bus.mu），两边互等，表现为流式彻底停住而日志全干净。
func (w *toolCallWire) Flush() {
	w.mu.Lock()
	if w.flushTimer != nil {
		w.flushTimer.Stop()
		w.flushTimer = nil
	}
	infos := w.pending
	w.pending = nil
	if len(infos) == 0 {
		w.mu.Unlock()
		return
	}
	w.flushN.Add(1)

	// 锁内只写内存；store.Update 返回的快照带出锁外再 Publish。
	//
	// ⚠️ imageSink 必须在 Update **之前**取（在写锁外拿一次 RLock 快照出附件依赖）：
	// mutator 运行在 store 写锁内，若在 mutator 里再取读锁就是同一把 RWMutex
	// 上的自死锁 —— 2026-10-24 线上事故：第一张真实工具图到达时 /api/tasks
	// 全库冻结（详见 imageSink 与 agent_toolcall_deadlock_test.go 的注释）。
	sink := w.store.imageSink()
	updated, err := w.store.Update(w.taskID, func(t *model.Task) bool {
		changed := false
		for _, info := range infos {
			if applyToolCallUpdate(w.store, sink, t, info) {
				changed = true
			}
		}
		return changed
	})
	w.mu.Unlock()

	if err != nil || updated == nil {
		return
	}
	// 工具事件是离散的，全量重绘合适（前端按 Seq 增量渲染），不用 delta。
	// 一次批量只推一次 —— 中途的中间态对前端没有意义（它按 Seq 渲染最终集合）。
	w.bus.Publish(Event{Type: "task_updated", TaskID: w.taskID, Task: updated})
}

// applyToolCallUpdate 把**一条** ToolCallUpdateInfo 施加到 t 上，返回是否改动了 t。
//
// 这是原先 onUpdate 里两段 Update 的合并体（2026-10-22）。合并是**纯等价变换**：
// 原先 `backfillExistingToolUse` 的 mutator 体与随后主 Update 的 mutator 体各自调用
// 同一批 helper，作用于同一个 Task —— 拆成两次写只是让同一批数据多落一次盘。
//
// 顺序不能调换，两步是"先补全已有的那条，再决定要不要追加新的"：
//  1. 入参/工具名**后到**：补全已落盘的那条 tool_use，而不是再追加一条空壳
//     （2026-10-21 修复的"审批卡上只有工具名、没有命令"，见 mergeToolUseIntoTask）；
//  2. 映射为事件并追加；若这次是 IsNew 但已有同 id 的 tool_use，同样只合并不追加。
func applyToolCallUpdate(store *TaskStore, sink imageSink, t *model.Task, info agent.ToolCallUpdateInfo) bool {
	changed := false

	// ① 补全已有那条：mergeToolUseIntoTask 改 Input/ToolName，attachToolDescription 补说明。
	// 两者都只对"已有同 id 的 tool_use"生效；都不命中时 changed 保持 false。
	if mergeToolUseIntoTask(t, info) {
		changed = true
	}
	if attachToolDescription(t, info) {
		changed = true
	}

	// ② 映射为事件。非完成态的中间状态更新不产生事件。
	ev, ok := mapToolCallEvent(info)
	if !ok {
		// 中间态（pending / in_progress）不落事件是**设计**，但如果它带着图，
		// 图片就再也回不来了（同一调用后面不会重发 Content）。必须可见。
			if len(info.Images) > 0 {
				sink.warnDropped(t.ID, info, "update status produced no event: "+info.Status)
			}
		return changed
	}
	ev.At = time.Now()
	// ToolCallUpdate 缺 Title 时，回查同 ToolUseID 的最后一个 tool_use 补全工具名。
	if ev.Type == model.EventToolResult && ev.ToolName == "" {
		if name := lookupToolName(t.Events, info.ToolCallID); name != "" {
			ev.ToolName = name
		}
	}
	// 同一个 id 已经有 tool_use、但这次更新把标题/入参补齐了：合并进那一条，
	// **不再追加**第二条只有工具名的空卡。
	if ev.Type == model.EventToolUse && mergeToolUseIntoTask(t, info) {
		// 合并分支会提前 return —— 若这次还带着图，它会**悄悄丢掉**。
		// 理论开工通知不带图（图在结果里），但"理论"不是判据：打一条告警，
		// 让"图片在哪个分支消失"这件事下次一眼可见，而不是再反推一晚。
		if len(info.Images) > 0 {
			sink.warnDropped(t.ID, info, "merged into existing tool_use")
		}
		return true
	}
	// 结果里的图片：落在 tool_result 事件上（见 attachToolImages 的说明）。
	//
	// 放在 append 之前算，是因为要把它一并写进**这一条**事件的 Images；
	// 落盘失败不阻断事件本身（工具确实跑完了，结果文本才是主体），
	// 只是这条事件不带图 —— 与"图太大被拒"同一处理口径，不制造假事实。
	if len(info.Images) > 0 && ev.Type == model.EventToolResult {
		ev.Images = sink.saveImages(info.Images)
	}
	store.AppendEvent(t, ev)
	return true
}

// imageSink 是"把工具图落盘/告警"所需依赖的**锁外快照**（附件存储 + logger）。
//
// 为什么要有这个类型：这两件事都发生在 store.Update 的 mutator 里，也就是
// **持有 store 写锁**的代码段内。而取依赖本身（读 s.attachments / s.logger 字段）
// 按惯例要拿 s.mu.RLock —— 同一把 RWMutex 上持写锁再取读锁，Go 的 RWMutex
// 不可重入，必然永久自锁。2026-10-24 线上事故：中转层修好后第一张真实工具图
// 到达，saveToolImages 在 mutator 里 RLock，/api/tasks 与 WS 快照全库冻结。
//
// 纪律：imageSink() 只能在**不持 store 写锁**的语境调用（典型：Flush 在
// Update 之前取一次），sink 本身是不可变值，可安全带进 mutator。
// 回归测试：agent_toolcall_deadlock_test.go（修复前在该处 3s 看门狗必红）。
type imageSink struct {
	att *AttachmentStore
	log *zap.Logger
}

// imageSink 在锁外语境下快照附件依赖（未接线时 att 为 nil，saveImages 会打 WARN）。
func (s *TaskStore) imageSink() imageSink {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return imageSink{att: s.attachments, log: s.logger}
}

// warnDropped 记录"解析到了图，却没进事件"这一事实。
//
// 为什么值得单独一个函数：这条链路的失败**天生是静默的**（工具结果照常有、
// 只是没有图），而三种失败方式（未接线、合入已有 tool_use、中间态不落事件）
// 各在一处、表现完全相同。统一打一条带原因的 WARN，线上只需一条 grep 就能定位。
func (sink imageSink) warnDropped(taskID string, info agent.ToolCallUpdateInfo, reason string) {
	if sink.log == nil {
		return
	}
	sink.log.Warn("attachment images dropped",
		zap.String("task", taskID),
		zap.String("tool_call_id", info.ToolCallID),
		zap.String("tool", info.Title),
		zap.Int("images", len(info.Images)),
		zap.String("reason", reason))
}

// saveImages 把工具结果里的图片落盘，返回可进事件的元数据。
//
// 单张失败**不影响其它张**（理由同 agent.toolCallImages：一张坏图不该让整次调用变无图）。
// 全部失败时返回 nil —— 事件照常追加，只是不带图。
func (sink imageSink) saveImages(images []agent.ImageInput) []model.TaskImage {
	// 依赖已在 imageSink() 里锁外快照（见该类型注释：mutator 锁内禁取 s.mu）。
	att, log := sink.att, sink.log
	if att == nil {
		// 未接线：这不是"正常降级"，而是**部署配置缺失**。原先这里静默返回 nil，
		// 表现是"图片解析成功、事件里却没有图、日志一句没有" —— 2026-10-24 线上
		// 排查绕了整晚的直接原因就是它。
		if log != nil {
			log.Warn("attachment store not wired; tool images dropped",
				zap.Int("images", len(images)))
		}
		return nil
	}
	out := make([]model.TaskImage, 0, len(images))
	for _, img := range images {
		// 文件名不在 ImageInput 里（那是"发出去"的结构，没有名字的概念）；
		// 展示用的名字与大小由附件层按字节算，不信任上游自报。
		hash, bytes, err := att.Save(img.Data, img.MimeType, "")
		if err != nil {
			// 静默失败是这里最危险的事：界面只会看起来"没有图"。
			// 日志是唯一的线索，所以一定要打。
			if log != nil {
				log.Warn("attachment save failed",
					zap.String("mime", img.MimeType), zap.Int("b64_len", len(img.Data)), zap.Error(err))
			}
			continue
		}
		out = append(out, model.TaskImage{
			MimeType: img.MimeType,
			Bytes:    bytes,
			Hash:     hash,
			Stored:   true,
		})
	}
	if len(out) == 0 {
		return nil
	}
	if log != nil {
		log.Debug("attachment saved", zap.Int("count", len(out)), zap.String("hash", out[0].Hash), zap.Int("bytes", out[0].Bytes))
	}
	return out
}

// mergeToolUseIntoTask 把一次"新工具调用"更新合并进已有的同名工具事件；返回 true 表示已处理。
//
// 判据（全部满足才合并）：
//   - 已有同 ToolCallID 的 tool_use 事件；
//   - 已有那条**入参或工具名是空的**（已完整的说明它才是权威的那条，不能覆盖）；
//   - 这次带来的入参/工具名**非空**。
//
// 为什么必须有这一步（2026-10-21 线上复现）：
// dsh 的 tool_call 通知**先发一条没有任何入参的**（标题也可能缺），入参要么随后的
// tool_call_update 带、要么根本不带（pwsh 有完整 command，edit/read 这些有时只有空壳）。
// 旧实现 `IsNew ⇒ AppendEvent` 无条件追加，于是同一调用落了两条 tool_use：
// 后一条入参为空、只有工具名。时间线渲染出来就是"有个 pwsh，但看不见命令"，
// 而审批卡当时拿到的也正是这条空壳 —— 用户看到的就是"审批只展示工具名"。
//
// 事件是**事实源**，所以只能补全不能删；补全对派生层（FileChange、Check）是安全的：
// 它们都按 (ToolName, Input) 取值，多一个字段只会让它们第一次真正取到值。
func mergeToolUseIntoTask(t *model.Task, info agent.ToolCallUpdateInfo) bool {
	if info.ToolCallID == "" || (len(info.RawInput) == 0 && info.Title == "") {
		return false
	}
	for i := len(t.Events) - 1; i >= 0; i-- {
		ev := &t.Events[i]
		if ev.Type != model.EventToolUse || ev.ToolUseID != info.ToolCallID {
			continue
		}
		changed := false
		if len(ev.Input) == 0 && len(info.RawInput) > 0 {
			ev.Input = info.RawInput
			changed = true
		}
		if ev.ToolName == "" && info.Title != "" {
			ev.ToolName = info.Title
			changed = true
		}
		if changed {
			t.UpdatedAt = time.Now()
		}
		return true
	}
	return false
}

// attachToolDescription 给已有 tool_use 补上"这条调用是干什么的"（description）。
//
// 为什么要单独一步：dsh 的 pwsh 入参里除 command 还有 description（人写的意图说明），
// 但开工通知常常先到、入参后到；而"后到的那份入参"与已落盘的那份可能**只差 description**。
// mergeToolUseIntoTask 出于"不覆盖权威入参"的判据不会动它，于是时间线/审批摘要里
// 就少了那句最能一眼读懂的话。这里只补一个键，不动其它字段。
//
// 只加不覆盖：已有 description 时保持原样（那是 agent 自己给的原文）。
func attachToolDescription(t *model.Task, info agent.ToolCallUpdateInfo) bool {
	if info.ToolCallID == "" || len(info.RawInput) == 0 {
		return false
	}
	var incoming map[string]any
	if json.Unmarshal(info.RawInput, &incoming) != nil {
		return false
	}
	desc, _ := incoming["description"].(string)
	if strings.TrimSpace(desc) == "" {
		return false
	}
	for i := len(t.Events) - 1; i >= 0; i-- {
		ev := &t.Events[i]
		if ev.Type != model.EventToolUse || ev.ToolUseID != info.ToolCallID || len(ev.Input) == 0 {
			continue
		}
		var cur map[string]any
		if json.Unmarshal(ev.Input, &cur) != nil {
			return false
		}
		if s, _ := cur["description"].(string); strings.TrimSpace(s) != "" {
			return false
		}
		cur["description"] = desc
		merged, err := json.Marshal(cur)
		if err != nil {
			return false
		}
		ev.Input = merged
		t.UpdatedAt = time.Now()
		return true
	}
	return false
}

// mapToolCallEvent 把 ToolCallUpdateInfo 映射为 TaskEvent。
// 第二返回值 ok=false 表示该更新不产生事件（如 pending/in_progress 状态变更）。
func mapToolCallEvent(info agent.ToolCallUpdateInfo) (model.TaskEvent, bool) {
	if info.IsNew {
		// ACP ToolCall：工具调用开始 → EventToolUse。
		// Input 取自 RawInput（ACP RawInput，原样 JSON），行为与 Phase 1 一致。
		return model.TaskEvent{
			Type:      model.EventToolUse,
			ToolName:  info.Title,
			ToolUseID: info.ToolCallID,
			Input:     info.RawInput,
		}, true
	}
	switch info.Status {
	case "completed":
		// Result 取自 RawOutput（ACP RawOutput，转 string），为空时留空串。
		return model.TaskEvent{
			Type:      model.EventToolResult,
			ToolName:  info.Title,
			ToolUseID: info.ToolCallID,
			Result:    string(info.RawOutput),
		}, true
	case "failed":
		return model.TaskEvent{
			Type:      model.EventToolResult,
			ToolName:  info.Title,
			ToolUseID: info.ToolCallID,
			IsError:   true,
			Result:    string(info.RawOutput),
		}, true
	default:
		// pending / in_progress 等中间状态变更：忽略（前端按 tool_use/tool_result 离散渲染即可）。
		return model.TaskEvent{}, false
	}
}

// lookupToolName 在事件流中回查同 ToolUseID 的最后一个 tool_use 事件的工具名。
// 供 tool_result 在 ToolCallUpdate 未带 Title 时补全 ToolName（仿 Phase 1 pendingToolUses 映射）。
func lookupToolName(events []model.TaskEvent, toolUseID string) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == model.EventToolUse && events[i].ToolUseID == toolUseID {
			return events[i].ToolName
		}
	}
	return ""
}
