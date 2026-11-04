/**
 * 新建需求入口目标（ADR-0021 双入口约定）。
 *
 * 与 AppSidebar / NewTaskPage 同口径：有 project_path 则带 `?project=`，
 * 空路径仍可进新建页（query 省略），由新建页自身校验。
 */
export type NewRequirementTarget =
  | { path: '/requirements/new'; query: { project: string } }
  | '/requirements/new'

export function newRequirementTarget(
  projectPath: string | undefined | null,
): NewRequirementTarget {
  const p = (projectPath ?? '').trim()
  return p ? { path: '/requirements/new', query: { project: p } } : '/requirements/new'
}
