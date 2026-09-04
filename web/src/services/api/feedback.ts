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
//	POST /api/tasks/:id/preview/screenshots  截图（P2；GET = 列表，GET :id.png = 文件）
//	GET  /api/tasks/:id/preview/console|network  Console/网络失败窗口（P2）
//	POST /api/tasks/:id/rewind（scope:file）  文件级回退（P2）
//	POST /api/tasks/:id/push              Evidence Push（P2）

import { request } from './client'
import type {
  ApprovalDiffDto,
  CheckDto,
  ChecksResponseDto,
  ConsoleSummaryDto,
  ContinueResponseDto,
  EvidenceDto,
  EvidenceScopeDto,
  FeedbackBundleDto,
  FeedbackDiffDto,
  NetworkSummaryDto,
  PreviewAttachDto,
  PreviewStatusDto,
  PushResponseDto,
  RewindResponseDto,
  ScreenshotDto,
  ScreenshotsResponseDto,
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

// ---------- Feedback P2（p2-design.md §9） ----------

/** POST /api/tasks/:id/preview/screenshots：对运行中 preview 截图（preview 非 running → 409） */
export async function captureScreenshot(taskId: string, fullPage = false): Promise<ScreenshotDto> {
  return request<ScreenshotDto>(`/tasks/${encodeURIComponent(taskId)}/preview/screenshots`, {
    method: 'POST',
    body: { full_page: fullPage },
  })
}

/** GET /api/tasks/:id/preview/screenshots：截图列表（时间倒序） */
export async function listScreenshots(taskId: string): Promise<ScreenshotDto[]> {
  const res = await request<ScreenshotsResponseDto>(
    `/tasks/${encodeURIComponent(taskId)}/preview/screenshots`,
  )
  return res.screenshots ?? []
}

/** GET /api/tasks/:id/preview/console：console error/warn 窗口（since 增量游标可选） */
export async function getConsoleSummary(taskId: string, since?: string): Promise<ConsoleSummaryDto> {
  const qs = since ? `?since=${encodeURIComponent(since)}` : ''
  return request<ConsoleSummaryDto>(`/tasks/${encodeURIComponent(taskId)}/preview/console${qs}`)
}

/** GET /api/tasks/:id/preview/network：失败请求窗口（4xx/5xx/failed） */
export async function getNetworkSummary(taskId: string, since?: string): Promise<NetworkSummaryDto> {
  const qs = since ? `?since=${encodeURIComponent(since)}` : ''
  return request<NetworkSummaryDto>(`/tasks/${encodeURIComponent(taskId)}/preview/network${qs}`)
}

/**
 * POST /api/tasks/:id/rewind（scope:file，P2 §7）：单文件回退到 Turn N 开始之前。
 * 不影响其他文件；rewind 事件入 Timeline。
 */
export async function rewindFileToTurn(
  taskId: string,
  toTurn: number,
  path: string,
  verify = false,
): Promise<RewindResponseDto> {
  return request<RewindResponseDto>(`/tasks/${encodeURIComponent(taskId)}/rewind`, {
    method: 'POST',
    body: { to_turn: toTurn, scope: 'file', path, verify },
  })
}

/**
 * POST /api/tasks/:id/push：把 Outcome/Evidence 推送到来源渠道（手动补充入口；
 * 终态自动推送由后端 WatchBus 完成）。
 */
export async function pushToChannel(
  taskId: string,
  kind: 'outcome' | 'evidence' = 'outcome',
  instruction?: string,
): Promise<PushResponseDto> {
  const body: Record<string, unknown> = { kind }
  if (instruction) body.instruction = instruction
  return request<PushResponseDto>(`/tasks/${encodeURIComponent(taskId)}/push`, {
    method: 'POST',
    body,
  })
}
