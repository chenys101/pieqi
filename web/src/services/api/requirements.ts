import { request } from './client'
import type {
  AcceptanceItem,
  ConfirmResult,
  PlanDraft,
  PlanView,
  ProposedTaskNode,
  Requirement,
  RequirementOutcome,
  SuggestOutcome,
} from '@/types/requirement'
import type { TaskDto, TaskGroupDto } from '@/types/api'
import { adaptTask } from './client'
import type { Task } from '@/types/task'

export async function listRequirements(): Promise<Requirement[]> {
  const data = await request<{ requirements: Requirement[] }>('/requirements')
  return data.requirements ?? []
}

export async function getRequirement(id: string): Promise<Requirement> {
  return request<Requirement>(`/requirements/${encodeURIComponent(id)}`)
}

export async function createRequirement(body: {
  title: string
  body?: string
  project_path: string
  acceptance?: Array<{ id: string; text: string; verifiable?: boolean }>
  path?: 's0' | 's1' | 's2'
  workflow_id?: string
}): Promise<Requirement> {
  return request<Requirement>('/requirements', { method: 'POST', body })
}

/** 把孤儿 Task 挂到需求下（ADR-0021 Promote；禁止自动调用）。 */
export async function promoteTaskToRequirement(
  requirementId: string,
  taskId: string,
): Promise<Task> {
  const dto = await request<TaskDto>(
    `/requirements/${encodeURIComponent(requirementId)}/promote`,
    { method: 'POST', body: { task_id: taskId } },
  )
  return adaptTask(dto)
}

export async function patchRequirement(
  id: string,
  body: {
    workflow_id?: string
    path?: 's0' | 's1' | 's2'
    title?: string
    body?: string
    integration_branch?: string
    acceptance?: AcceptanceItem[]
  },
): Promise<Requirement> {
  return request<Requirement>(`/requirements/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body,
  })
}

export async function getRequirementOutcome(id: string): Promise<RequirementOutcome> {
  return request<RequirementOutcome>(`/requirements/${encodeURIComponent(id)}/outcome`)
}

export async function confirmRequirement(
  id: string,
  nodes?: ProposedTaskNode[],
  opts?: { replace?: boolean; force?: boolean },
): Promise<ConfirmResult> {
  const body: { nodes?: ProposedTaskNode[]; replace?: boolean; force?: boolean } = {}
  if (nodes?.length) body.nodes = nodes
  if (opts?.replace) body.replace = true
  if (opts?.force) body.force = true
  return request<ConfirmResult>(`/requirements/${encodeURIComponent(id)}/confirm`, {
    method: 'POST',
    body,
  })
}

export async function getProposal(id: string): Promise<{
  id: string
  requirement_id: string
  nodes: ProposedTaskNode[]
  created_at: string
} | null> {
  try {
    return await request(`/requirements/${encodeURIComponent(id)}/proposal`)
  } catch {
    return null
  }
}

/** 详情页轮询：proposal | blocked | none。 */
export async function getSuggestOutcome(id: string): Promise<SuggestOutcome> {
  return request<SuggestOutcome>(`/requirements/${encodeURIComponent(id)}/suggest-outcome`)
}

/** 启动 agent 拆分；完成后看 getSuggestOutcome。无 branch 时抛 ApiError 409 + code。 */
export async function suggestSplit(
  id: string,
  opts?: { agent?: string; model?: string },
): Promise<{ task: { id: string; status: string }; message?: string }> {
  const body: { agent?: string; model?: string } = {}
  if (opts?.agent) body.agent = opts.agent
  if (opts?.model) body.model = opts.model
  return request(`/requirements/${encodeURIComponent(id)}/suggest`, {
    method: 'POST',
    body,
  })
}

/** 清 proposal + block。 */
export async function discardProposal(id: string): Promise<void> {
  await request(`/requirements/${encodeURIComponent(id)}/suggest-outcome`, { method: 'DELETE' })
}

export async function expandRequirement(
  id: string,
  nodes: ProposedTaskNode[],
): Promise<ConfirmResult> {
  return request<ConfirmResult>(`/requirements/${encodeURIComponent(id)}/expand`, {
    method: 'POST',
    body: { nodes },
  })
}

export async function markRequirementDone(id: string): Promise<Requirement> {
  return request<Requirement>(`/requirements/${encodeURIComponent(id)}/done`, { method: 'POST' })
}

export async function openIssue(
  requirementId: string,
  body: {
    title: string
    detail?: string
    severity?: string
    covers_ac?: string[]
  },
): Promise<{ requirement: Requirement; issue: { id: string }; fix_task?: { id: string } }> {
  return request(`/requirements/${encodeURIComponent(requirementId)}/issues`, {
    method: 'POST',
    body,
  })
}

export async function confirmIssueFix(issueId: string) {
  return request(`/issues/${encodeURIComponent(issueId)}/confirm-fix`, { method: 'POST' })
}

export async function resolveIssue(issueId: string): Promise<Requirement> {
  return request<Requirement>(`/issues/${encodeURIComponent(issueId)}/resolve`, { method: 'POST' })
}

export async function humanVerifyAC(
  requirementId: string,
  acId: string,
  pass: boolean,
  detail?: string,
) {
  return request(
    `/requirements/${encodeURIComponent(requirementId)}/acceptance/${encodeURIComponent(acId)}/verify`,
    { method: 'POST', body: { pass, detail: detail ?? '' } },
  )
}

/** 含 planned；服务端按 requirement_id 过滤（勿拉全库再筛）。 */
export async function listTasksForRequirement(requirementId: string): Promise<Task[]> {
  const q = new URLSearchParams({
    include_planned: '1',
    requirement_id: requirementId,
  })
  const data = await request<{ projects: TaskGroupDto[] }>(`/tasks?${q}`)
  const out: Task[] = []
  for (const g of data.projects ?? []) {
    for (const t of g.tasks ?? []) {
      out.push(adaptTask(t as TaskDto))
    }
  }
  return out
}

/** Plan Engineering（ADR-0023） */
export async function getRequirementPlan(id: string): Promise<PlanView> {
  return request<PlanView>(`/requirements/${encodeURIComponent(id)}/plan`)
}

export async function savePlanDraft(
  id: string,
  draft: Partial<PlanDraft> & {
    stage?: string
    nodes?: PlanDraft['nodes']
    assertions?: PlanDraft['assertions']
  },
): Promise<{ draft: PlanDraft; publish_blocked?: string[] }> {
  return request(`/requirements/${encodeURIComponent(id)}/plan/draft`, {
    method: 'PUT',
    body: draft,
  })
}

export async function publishPlan(
  id: string,
  opts?: { materialize?: boolean; force?: boolean; actor?: string },
): Promise<{ revision: unknown; materialize?: ConfirmResult }> {
  const q = new URLSearchParams()
  if (opts?.materialize) q.set('materialize', '1')
  if (opts?.force) q.set('force', '1')
  const qs = q.toString()
  return request(`/requirements/${encodeURIComponent(id)}/plan/publish${qs ? `?${qs}` : ''}`, {
    method: 'POST',
    body: {
      actor: opts?.actor ?? '',
      materialize: !!opts?.materialize,
      force: !!opts?.force,
    },
  })
}
