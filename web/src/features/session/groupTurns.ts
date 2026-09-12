// Turn 分组（R3，p0-design §5.1 / AC-R3-01~06）。
//
// **边界真相在后端 `start_event_seq`，不在前端**：Turn = 两条 EventUser 之间
// 的全部事件。事件流里的 `user_message` 有两个来源 —— 真实 EventUser，和
// normalizer 对旧任务「↻ 续问: 」text 的兼容合成（后端不认后者为 Turn）。
// 若按 user_message 切组，旧任务会被放大成一批"没有数字的假分组"。
// 所以：**bundle.turns 为空（含旧任务）→ 返回 [] 走平铺（AC-R3-04）**；
// bundle 已知 Turn 按 seq 落位（seq 从持久化事件 id `${taskId}:${seq}` 解析）；
// 只有「已知边界之后的 user_message」（新一轮真实 EventUser / 乐观插入的本地
// 消息）才开**临时增量组**（info=null）—— 运行中新一轮立即出现（AC-R3-05），
// bundle 追上后自动转正。前导区（首条 EventUser 之前的系统事件）= turn 0。
import type { AgentEvent } from '@/types/event'
import type { TurnInfoDto } from '@/types/api'

export interface TurnGroup {
  /** 1 起步；0 = 前导区（首条用户消息之前的系统事件，仅在有内容时存在） */
  turn: number
  events: AgentEvent[]
  /** 同名 Turn 的 TurnInfo（临时增量组为 null —— 没有第二来源就不编数字） */
  info: TurnInfoDto | null
}

/** 持久化事件 id = `${taskId}:${seq}`；流式增量 / 乐观插入没有 seq → null */
function parseSeq(id: string): number | null {
  const m = /:(\d+)$/.exec(id)
  return m ? Number(m[1]) : null
}

export function groupEventsByTurn(events: AgentEvent[], turns: TurnInfoDto[] | undefined | null): TurnGroup[] {
  const list = [...(turns ?? [])].sort((a, b) => a.start_event_seq - b.start_event_seq)
  if (!list.length) return [] // 无 TurnInfo（旧任务 / 未加载）→ 平铺兜底

  const infoByTurn = new Map(list.map((t) => [t.turn, t]))
  const groups: TurnGroup[] = list.map((t) => ({
    turn: t.turn,
    events: [] as AgentEvent[],
    info: infoByTurn.get(t.turn) ?? null,
  }))
  const provisional: TurnGroup[] = []
  const prelude: AgentEvent[] = []
  const lastStart = list[list.length - 1].start_event_seq

  /** 已开启的临时组；provSeq = 该组首条事件的 seq（乐观插入无 seq → null = 吞掉后续全部） */
  let prov: TurnGroup | null = null
  let provSeq: number | null = -1
  let provCount = 0

  for (const ev of events) {
    const seq = parseSeq(ev.id)
    // 已在临时组地界内（事件按 seq 序到达）：顺流而下
    if (prov && (seq === null || provSeq === null || seq >= provSeq)) {
      prov.events.push(ev)
      continue
    }
    if (ev.type === 'user_message' && (seq === null || seq > lastStart)) {
      provCount += 1
      const turn = list.length + provCount
      prov = { turn, events: [ev], info: infoByTurn.get(turn) ?? null }
      provSeq = seq
      provisional.push(prov)
      continue
    }
    if (seq === null) {
      // 非 user 且无 seq：附到当前最后一组（不丢事件 —— 覆盖率 100%）
      ;(prov ?? groups[groups.length - 1]).events.push(ev)
      continue
    }
    let gi = -1
    for (let i = 0; i < list.length; i++) {
      if (seq >= list[i].start_event_seq) gi = i
      else break
    }
    if (gi === -1) prelude.push(ev)
    else groups[gi].events.push(ev)
  }

  const out: TurnGroup[] = []
  if (prelude.length) out.push({ turn: 0, events: prelude, info: null })
  out.push(...groups, ...provisional)
  return out
}

/** 事件流里的用户消息数 —— 判断 bundle 是否落后于事件流（落后 = 需要重拉） */
export function countUserMessages(events: AgentEvent[]): number {
  return events.reduce((n, ev) => (ev.type === 'user_message' ? n + 1 : n), 0)
}
