// Feedback API（p0-design.md §5）：
//
//	GET  /api/tasks/:id/feedback          反馈总览
//	GET  /api/tasks/:id/feedback/diff     单文件 Diff（turn 省略 = Baseline 累计）
//	POST /api/tasks/:id/rewind            代码回退
//	POST /api/tasks/:id/preview/start|stop / GET status

import { request } from './client'
import type {
  FeedbackBundleDto,
  FeedbackDiffDto,
  PreviewStatusDto,
  RewindResponseDto,
} from '@/types/api'

/** GET /api/tasks/:id/feedback：总览（turns/changes/cumulative/checkpoints/preview） */
export async function getFeedback(taskId: string): Promise<FeedbackBundleDto> {
  return request<FeedbackBundleDto>(`/tasks/${encodeURIComponent(taskId)}/feedback`)
}

/** GET /api/tasks/:id/feedback/diff：单文件 diff（turn 省略 = Baseline 累计） */
export async function getFeedbackDiff(
  taskId: string,
  path: string,
  turn?: number,
): Promise<FeedbackDiffDto> {
  const qs = new URLSearchParams({ path })
  if (turn && turn > 0) qs.set('turn', String(turn))
  return request<FeedbackDiffDto>(`/tasks/${encodeURIComponent(taskId)}/feedback/diff?${qs}`)
}

/** POST /api/tasks/:id/rewind：把 Agent 触碰过的文件恢复到 Turn N 开始之前 */
export async function rewindToTurn(taskId: string, toTurn: number): Promise<RewindResponseDto> {
  return request<RewindResponseDto>(`/tasks/${encodeURIComponent(taskId)}/rewind`, {
    method: 'POST',
    body: { to_turn: toTurn, scope: 'code' },
  })
}

/** POST /api/tasks/:id/preview/start（异步启动，立即返回 starting） */
export async function startPreview(taskId: string): Promise<void> {
  await request(`/tasks/${encodeURIComponent(taskId)}/preview/start`, { method: 'POST', body: {} })
}

/** POST /api/tasks/:id/preview/stop */
export async function stopPreview(taskId: string): Promise<void> {
  await request(`/tasks/${encodeURIComponent(taskId)}/preview/stop`, { method: 'POST', body: {} })
}

/** GET /api/tasks/:id/preview/status */
export async function getPreviewStatus(taskId: string): Promise<PreviewStatusDto> {
  return request<PreviewStatusDto>(`/tasks/${encodeURIComponent(taskId)}/preview/status`)
}
