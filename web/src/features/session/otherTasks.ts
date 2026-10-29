// 详情页「其他运行中任务」的取数规则（唯一事实源）。
//
// 为什么要单独抽出来：这是**排序 + 截断**两条业务规则，不是排版细节。
//   · 待决策优先：用户点开任务列表的第一诉求是"哪个在等我"，而不是"哪个最新"；
//   · 同档按 updatedAt 倒序：刚有动作的更可能是用户想去看的那个；
//   · 最多 3 个：顶栏/下拉的空间预算（更多走「查看全部」）。
// 组件只负责画，规则集中在这里，单测不用挂 Vue。
import type { Task, TaskStatus } from '@/types/task'

/** 视为"还在跑"的状态（包含等待决策 —— 它同样占着一个在途任务位）。 */
const ACTIVE_STATUSES: TaskStatus[] = ['waiting_input', 'running', 'pending']

/** 状态优先级：越小越靠前。未列出的终态不会进列表。 */
const STATUS_RANK: Partial<Record<TaskStatus, number>> = {
  waiting_input: 0,
  running: 1,
  pending: 2,
}

function timestamp(iso: string | undefined): number {
  if (!iso) return 0
  const n = Date.parse(iso)
  return Number.isFinite(n) ? n : 0
}

/**
 * 当前任务之外、仍在途的任务，按「待决策 → 运行中 → 待运行」排序；
 * 同档按 updatedAt 倒序。返回值是新数组，不修改入参。
 */
export function otherRunningTasks(tasks: readonly Task[], currentTaskId: string): Task[] {
  return tasks
    .filter((t) => t.id !== currentTaskId && ACTIVE_STATUSES.includes(t.status))
    .sort((a, b) => {
      const rank = (STATUS_RANK[a.status] ?? 99) - (STATUS_RANK[b.status] ?? 99)
      return rank !== 0 ? rank : timestamp(b.updatedAt) - timestamp(a.updatedAt)
    })
}
