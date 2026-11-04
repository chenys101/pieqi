// 该审必见（V2.0.1 P1 / ac2）：决策出现时的视口锚定与对照日志。
//
// 判据见 docs/notes/approval-miss-and-soft-complete.md：
//   后端已 approval shown / CurrentDecision ≠ 用户已看见可点入口。
//   当前会话页必须把 Banner 滚进视口；移动端若停在「变更反馈」整屏，必须切回时间线。

/** 决策出现时：若还在反馈整屏，强制回时间线（否则 Banner 被 v-show 藏掉）。 */
export function paneForDecision(
  current: 'timeline' | 'feedback',
  hasDecision: boolean,
): 'timeline' | 'feedback' {
  return hasDecision ? 'timeline' : current
}

/**
 * 把决策入口滚进最近可视区。
 *
 * `block: 'nearest'`：已经在视口内时不瞎滚；被遮挡时才动。
 * 不传 el / 环境无 scrollIntoView 时静默 —— 单测与 SSR 友好。
 */
export function anchorDecisionEntry(el: Element | null | undefined): void {
  if (!el || typeof (el as HTMLElement).scrollIntoView !== 'function') return
  ;(el as HTMLElement).scrollIntoView({ block: 'nearest', behavior: 'smooth' })
}

/**
 * 可选对照日志：前端「已渲染决策卡」↔ 后端 `approval shown`。
 * 非 AC，仅便于以后告警；用 console.info，不接 toast。
 */
export function logDecisionRendered(info: {
  id: string
  taskId: string
  tool?: string
  kind?: string
}): void {
  // 对照后端 agent_perm.go 的 "approval shown (waiting for human decision)"
  console.info('[pieqi] decision_rendered', {
    decision_id: info.id,
    task_id: info.taskId,
    tool: info.tool ?? '',
    kind: info.kind ?? '',
  })
}
