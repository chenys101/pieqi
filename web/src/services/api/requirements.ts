import { request } from './client'
import type {
  ConfirmResult,
  ProposedTaskNode,
  Requirement,
  RequirementOutcome,
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
}): Promise<Requirement> {
  return request<Requirement>('/requirements', { method: 'POST', body })
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

/** 启动 agent 拆分；完成后提案出现在 getProposal。agent/model 与建任务同口径。 */
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

export async function discardProposal(id: string): Promise<void> {
  await request(`/requirements/${encodeURIComponent(id)}/proposal`, { method: 'DELETE' })
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

/** 含 planned，再按 requirement_id 过滤（列表默认排除 planned）。 */
export async function listTasksForRequirement(requirementId: string): Promise<Task[]> {
  const data = await request<{ projects: TaskGroupDto[] }>('/tasks?include_planned=1')
  const out: Task[] = []
  for (const g of data.projects ?? []) {
    for (const t of g.tasks ?? []) {
      if (t.requirement_id === requirementId) {
        out.push(adaptTask(t as TaskDto))
      }
    }
  }
  return out
}
