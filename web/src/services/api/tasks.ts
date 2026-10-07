// Task API（协议见 docs/frontend-v2/baseline.md §2，不修改后端）

import { request, adaptTask } from './client'
import type { TaskGroupDto, TaskDto, InterveneRequestDto } from '@/types/api'
import type { Task, TaskGroup, TaskStatus } from '@/types/task'
import { groupKey } from '@/utils/format'
import { timestamp } from '@/utils/date'

/**
 * GET /api/tasks → 项目分组（保持后端顺序，组内按活跃时间倒序）
 *
 * 后端返回的是轻量视图（TaskSummaryDto，**不含 events**）——
 * 列表只需元数据，事件流按需由 getTaskDto 拉取。
 */
export async function getTasks(): Promise<{ tasks: Task[]; groups: TaskGroup[] }> {
  const data = await request<{ projects: TaskGroupDto[] }>('/tasks')
  const tasks: Task[] = []
  const groupMap = new Map<string, TaskGroup>()

  for (const g of data.projects ?? []) {
    // 轻量 DTO 缺 events 字段，adaptTask 只读元数据，可直接适配
    const adapted = (g.tasks ?? []).map(adaptTask)
    // 组内按 updated_at 倒序：最近活跃的排最前（与 V1 行为一致）
    adapted.sort((a, b) => timestamp(b.updatedAt) - timestamp(a.updatedAt))
    tasks.push(...adapted)

    const key = groupKey(g.project_path)
    const counts = { pending: 0, running: 0, waiting_input: 0, completed: 0, failed: 0, cancelled: 0 } as Record<TaskStatus, number>
    for (const t of adapted) counts[t.status]++
    groupMap.set(key, {
      key,
      projectId: g.project_id || g.project_path,
      projectPath: g.project_path,
      tasks: adapted,
      counts,
    })
  }
  return { tasks, groups: [...groupMap.values()] }
}

/** GET /api/tasks/:id */
export async function getTask(id: string): Promise<Task> {
  const dto = await request<TaskDto>(`/tasks/${encodeURIComponent(id)}`)
  return adaptTask(dto)
}

/** GET /api/tasks/:id 原始 DTO（Session 页需要 events，DTO 层保留） */
export async function getTaskDto(id: string): Promise<TaskDto> {
  return request<TaskDto>(`/tasks/${encodeURIComponent(id)}`)
}

/**
 * POST /api/tasks：创建成功返回完整 DTO（含预置 user 事件）。
 *
 * agent 为 agent 业务名（claude / qoder，见 GET /api/agents）；空串 = 后端默认（Claude Code）。
 * 后端会校验：未在可选目录里的 agent 直接 400，不会静默换成别的 agent。
 *
 * model 为模型选择值（见 GET /api/agents/{agent}/models）；空串 = 用 agent 自己的默认。
 * ⚠️ 它是**不透明串**，必须原样取自清单、原样回传，前端不得解析或拼接 ——
 * 自己拼的取值会让 agent 在建会话时报未知模型（任务直接失败）。
 */
export async function createTask(projectPath: string, prompt: string, agent?: string, model?: string): Promise<TaskDto> {
  return request<TaskDto>('/tasks', {
    method: 'POST',
    body: { project_path: projectPath, prompt, agent: agent || undefined, model: model || undefined },
  })
}

export interface IntervenePayload {
  kind: 'decision' | 'append_prompt'
  decisionId?: string
  /**
   * approve=只批这一次；approve_session=批这一次并把同 kind 的操作记入本会话免审；
   * deny=拒绝。可用动作由后端 Decision.options 声明（不是所有路径都支持 approve_session）。
   */
  choice?: 'approve' | 'approve_session' | 'deny'
  text?: string
  /**
   * 本轮要用的模型（不透明串，见 GET /api/agents/{agent}/models）；不传 = 沿用会话当前。
   * 只在续问（终态 Resume）这一轮生效 —— 那是唯一"起新一轮"的 append_prompt。
   */
  model?: string
}

/** POST /api/tasks/:id/intervene：决策 / 追加 prompt / 终态续问 */
export async function intervene(taskId: string, p: IntervenePayload): Promise<void> {
  const body: InterveneRequestDto = {
    kind: p.kind,
    decision_id: p.decisionId,
    choice: p.choice,
    text: p.text,
    model: p.model || undefined,
  }
  await request(`/tasks/${encodeURIComponent(taskId)}/intervene`, { method: 'POST', body })
}

/** POST /api/tasks/:id/cancel */
export async function cancelTask(taskId: string): Promise<void> {
  await request(`/tasks/${encodeURIComponent(taskId)}/cancel`, { method: 'POST', body: {} })
}

/** DELETE /api/tasks/:id（运行中先取消） */
export async function deleteTask(taskId: string): Promise<void> {
  await request(`/tasks/${encodeURIComponent(taskId)}`, { method: 'DELETE' })
}
