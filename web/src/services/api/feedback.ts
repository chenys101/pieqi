// Feedback API（p0-design.md §5 + p1-design.md §11）：
//
//	GET  /api/tasks/:id/feedback          反馈总览
//	GET  /api/tasks/:id/feedback/diff     单文件 Diff（turn 省略 = Baseline 累计）
//	POST /api/tasks/:id/rewind            代码回退（verify=true → 回退后验证）
//	POST /api/tasks/:id/preview/start|stop|restart / GET status|attach
//	GET  /api/tasks/:id/approvals/:decisionId/diff   前瞻性 Diff（P1）
//	GET  /api/tasks/:id/checks            Check 列表（P1）
//	POST /api/tasks/:id/checks/:checkId/rerun        Check 重跑（P1）
//	GET  /api/tasks/:id/outcome           Task 结构化结果（P1）
//	GET  /api/tasks/:id/evidence          验证证据快照（P1）
//	POST /api/tasks/:id/continue          Evidence → Continue（P1）

import { request } from './client'
import type {
  ApprovalDiffDto,
  CheckDto,
  ChecksResponseDto,
  ContinueResponseDto,
  EvidenceDto,
  EvidenceScopeDto,
  FeedbackBundleDto,
  FeedbackDiffDto,
  PreviewAttachDto,
  PreviewStatusDto,
  RewindResponseDto,
  TaskOutcomeDto,
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

/**
 * POST /api/tasks/:id/rewind：把 Agent 触碰过的文件恢复到 Turn N 开始之前。
 * verify=true → 回退后自动重跑目标轮 checks + 重启 preview（Rewind → Verify）。
 */
export async function rewindToTurn(
  taskId: string,
  toTurn: number,
  verify = false,
): Promise<RewindResponseDto> {
  return request<RewindResponseDto>(`/tasks/${encodeURIComponent(taskId)}/rewind`, {
    method: 'POST',
    body: { to_turn: toTurn, scope: 'code', verify },
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

/** POST /api/tasks/:id/preview/restart：停止后重启（Rewind→Verify / 非 HMR 改动手动刷新，P1） */
export async function restartPreview(taskId: string): Promise<void> {
  await request(`/tasks/${encodeURIComponent(taskId)}/preview/restart`, { method: 'POST', body: {} })
}

/** GET /api/tasks/:id/preview/status */
export async function getPreviewStatus(taskId: string): Promise<PreviewStatusDto> {
  return request<PreviewStatusDto>(`/tasks/${encodeURIComponent(taskId)}/preview/status`)
}

/**
 * GET /api/tasks/:id/preview/attach（P1）：外部浏览器可打开的预览外链 + 二维码。
 * 前置：preview 运行中 + 隧道 active（不足 → 409，提示用户先启动）。
 */
export async function getPreviewAttach(taskId: string): Promise<PreviewAttachDto> {
  return request<PreviewAttachDto>(`/tasks/${encodeURIComponent(taskId)}/preview/attach`)
}

// ---------- Feedback P1（p1-design.md §11） ----------

/** GET /api/tasks/:id/approvals/:decisionId/diff：审批前的前瞻性 Diff（无文件语义工具 → 404） */
export async function getApprovalDiff(taskId: string, decisionId: string): Promise<ApprovalDiffDto> {
  return request<ApprovalDiffDto>(
    `/tasks/${encodeURIComponent(taskId)}/approvals/${encodeURIComponent(decisionId)}/diff`,
  )
}

/** GET /api/tasks/:id/checks：Check 列表（agent 事件流复用 + 重跑记录，按开始时间升序） */
export async function getChecks(taskId: string): Promise<CheckDto[]> {
  const res = await request<ChecksResponseDto>(`/tasks/${encodeURIComponent(taskId)}/checks`)
  return res.checks ?? []
}

/** POST /api/tasks/:id/checks/:checkId/rerun：异步重跑，返回 running 态记录 */
export async function rerunCheck(taskId: string, checkId: string): Promise<CheckDto> {
  return request<CheckDto>(`/tasks/${encodeURIComponent(taskId)}/checks/${encodeURIComponent(checkId)}/rerun`, {
    method: 'POST',
    body: {},
  })
}

/** GET /api/tasks/:id/outcome：Task 结构化结果（完成度规则派生，手机端主验收面） */
export async function getOutcome(taskId: string): Promise<TaskOutcomeDto> {
  return request<TaskOutcomeDto>(`/tasks/${encodeURIComponent(taskId)}/outcome`)
}

/** GET /api/tasks/:id/evidence?scope=task|turn：验证证据快照（随取随派生） */
export async function getEvidence(
  taskId: string,
  scope: EvidenceScopeDto = 'task',
  turn?: number,
): Promise<EvidenceDto> {
  const qs = new URLSearchParams({ scope })
  if (turn && turn > 0) qs.set('turn', String(turn))
  return request<EvidenceDto>(`/tasks/${encodeURIComponent(taskId)}/evidence?${qs}`)
}

/** POST /api/tasks/:id/continue：带当前证据续问（后端组装 prompt 走既有 Resume 路径） */
export async function continueWithEvidence(
  taskId: string,
  instruction?: string,
): Promise<ContinueResponseDto> {
  return request<ContinueResponseDto>(`/tasks/${encodeURIComponent(taskId)}/continue`, {
    method: 'POST',
    body: instruction ? { instruction } : {},
  })
}
