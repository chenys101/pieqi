// 事件序号连续性追踪（gap detection）。
//
// 为什么需要它：服务端 EventBus 只在**自己缓冲满**时置 dropped 标记并补发 snapshot
// （见 internal/core/event_bus.go）。而客户端 socket 断开时是另一回事 —— 服务端已经
// Unsubscribe，事件投给了不存在的订阅者，既不置标记也不补发。加上 task_delta 是纯
// 增量（载荷里没有状态可对账），客户端**完全无从察觉**自己漏了内容。
//
// 后端为此给每条事件分配了全局单调 seq（core.Event.Seq）。本模块做一件事：
// 在**同一条连接内**发现序号断裂 —— 把"静默丢失"变成"可发现"。
//
// ⚠️ 关键约束（第一版实现踩过的坑，务必先读懂再改）：
//
// **seq 的生命周期是"服务端进程"，不是"客户端会话"**。EventBus.seq 是内存计数器
// （internal/core/event_bus.go），服务端一重启就归零。所以：
//
//   - 快照报的 seq 是"服务端此刻已分配到第几号"，**不是**"你已收到第几号"
//   - 客户端断线跨过一次服务端重启时，重连后收到的事件 seq 会**小于**断线前的
//     快照 seq（从 1 重新开始）
//
// 第一版把这两者混成一个游标：先 observeSnapshot(467) 把游标推到 467，随后
// observe(1) 因 "1 <= 467" 被判为"乱序"而**不更新游标** ⇒ 游标永远卡在 467，
// 1..467 之间的任何空洞再也检测不出来。这是**静默失效**：不报错、测试也不红，
// 但补拉再也不会被触发。
//
// 现在的模型：**快照建立新基线，其余事件只在"同一基线内"比较**。
//   - 收到快照 ⇒ 重设基线为快照 seq（视为该基线的起点，不判定）
//   - 收到事件 ⇒ 与本基线内的上一条比较；严格 +1 才算连续
//   - 小于等于上一条 ⇒ 视为基线重置（服务端重启）或重放，**重设基线**而非忽略
//
// 判定规则（宁可漏报，不可误报 —— 漏报只是回到"刷新页面才好"，误报则是无谓的
// 全量补拉，会打断正在逐字渲染的时间线）：
//   - seq 缺失（旧服务端 / 未填）→ 不判定
//   - 同基线内 seq == last + 1 → 连续，推进
//   - 同基线内 seq > last + 1 → **有洞**，报告
//   - seq <= last → 视为基线重置（重启）或重放，重设基线，不报告
export class SeqTracker {
  /** 当前基线内最后一条已收事件的 seq（null = 尚未建立基线） */
  private last: number | null = null

  /** 记录一条**事件**消息，返回是否检测到序号断裂 */
  observe(seq: number | undefined): boolean {
    if (typeof seq !== 'number' || seq <= 0) return false // 未知：不判定
    if (this.last === null) {
      this.last = seq
      return false
    }
    if (seq <= this.last) {
      // 服务端重启（计数器归零）或重连后重放旧事件：重设基线。
      // **不能只 return false 了事** —— 那会把游标永远钉在高位（见文件头注释）。
      this.last = seq
      return false
    }
    const gap = seq > this.last + 1
    this.last = seq
    return gap
  }

  /**
   * 用快照携带的服务端序号**建立新基线**，返回是否检测到断裂。
   *
   * 快照是"服务端此刻已分配到第几号"的权威读数，因此它总是重设基线（不判定）——
   * 本地游标在断线期间必然过期，以它为准只会得出错误结论，而"断裂"这件事本身
   * 已由连接层的重连钩子（onReconnected）无条件覆盖：重连即补拉，不依赖这个判定。
   */
  observeSnapshot(seq: number | undefined): boolean {
    if (typeof seq !== 'number' || seq <= 0) return false // 未知：不判定
    this.last = seq
    return false
  }

  /** 重置（换连接/登出时） */
  reset(): void {
    this.last = null
  }

  /** 当前游标（测试与诊断用） */
  get lastSeq(): number | null {
    return this.last
  }
}
