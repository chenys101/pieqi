/** 短路径且未挂需求时可升格（PE 入口拍板）。 */
export function canPromoteToRequirement(task: { requirementId?: string } | null | undefined): boolean {
  if (!task) return false
  return !String(task.requirementId || '').trim()
}
