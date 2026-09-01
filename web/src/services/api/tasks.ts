// Task API（协议见 docs/frontend-v2/baseline.md §2，不修改后端）

import { request, adaptTask } from './client'
import type { TaskGroupDto, TaskDto, InterveneRequestDto } from '@/types/api'
import type { Task, TaskGroup, TaskStatus } from '@/types/task'
import { groupKey } from '@/utils/format'
import { timestamp } from '@/utils/date'

/** GET /api/tasks → 项目分组（保持后端顺序，组内按活跃时间倒序） */
export async function getTasks(): Promise<{ tasks: Task[]; groups: TaskGroup[] }> {
  const data = await request<{ projects: TaskGroupDto[] }>('/tasks')
  const tasks: Task[] = []
  const groupMap = new Map<string, TaskGroup>()

  for (const g of data.projects ?? []) {
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

/** POST /api/tasks：创建成功返回完整 DTO（含预置 user 事件） */
export async function createTask(projectPath: string, prompt: string): Promise<TaskDto> {
  return request<TaskDto>('/tasks', {
    method: 'POST',
    body: { project_path: projectPath, prompt },
  })
}

export interface IntervenePayload {
  kind: 'decision' | 'append_prompt'
  decisionId?: string
  choice?: 'approve' | 'deny'
  text?: string
}

/** POST /api/tasks/:id/intervene：决策 / 追加 prompt / 终态续问 */
export async function intervene(taskId: string, p: IntervenePayload): Promise<void> {
  const body: InterveneRequestDto = {
    kind: p.kind,
    decision_id: p.decisionId,
    choice: p.choice,
    text: p.text,
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
