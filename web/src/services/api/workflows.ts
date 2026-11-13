import { request } from './client'

export interface WorkflowGates {
  max_nodes: number
  max_parallel_implement: number
  forbid_fe_be_hard_split: boolean
  require_ac_evidence_to_done: boolean
  path_bias?: string
  require_brief_in_prompt?: boolean
}

export interface WorkflowUIStep {
  id: string
  label: string
}

export interface WorkflowSummary {
  id: string
  version: string
  label: string
  short_label?: string
  description: string
  tags?: string[]
  ui_steps?: WorkflowUIStep[]
  gates: WorkflowGates
  builtin: boolean
  is_default?: boolean
}

export interface WorkflowDetail extends WorkflowSummary {
  split_instructions?: string
  brief_header?: string
  dod_implement?: string
  dod_test?: string
}

export async function listWorkflows(): Promise<{
  workflows: WorkflowSummary[]
  default_id: string
  user_dir: string
}> {
  const data = await request<{
    workflows: WorkflowSummary[]
    default_id: string
    user_dir: string
  }>('/workflows')
  return {
    workflows: data.workflows ?? [],
    default_id: data.default_id ?? '',
    user_dir: data.user_dir ?? '',
  }
}

export async function getWorkflow(id: string): Promise<WorkflowDetail> {
  return request<WorkflowDetail>(`/workflows/${encodeURIComponent(id)}`)
}

export async function reloadWorkflows(): Promise<{
  workflows: WorkflowSummary[]
  default_id: string
  user_dir: string
}> {
  return request('/workflows/reload', { method: 'POST', body: {} })
}

export async function setDefaultWorkflow(id: string): Promise<{ default_id: string }> {
  return request('/workflows/default', { method: 'POST', body: { id } })
}

export async function getReviewCapacity(): Promise<{
  high_stakes_pending: number
  blocked: boolean
}> {
  return request('/requirements/capacity')
}
