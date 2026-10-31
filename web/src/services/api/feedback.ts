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
//	POST /api/tasks/:id/preview/capture   采集 console/network（P2；截图已于 2026-10-15 移除）
//	GET  /api/tasks/:id/preview/console|network  Console/网络失败窗口（P2）
//	POST /api/tasks/:id/rewind（scope:file）  文件级回退（P2）
//	POST /api/tasks/:id/push              Evidence Push（P2）

import { request, requestTextMeta } from './client'
import type {
  ApprovalDiffDto,
  ArtifactDto,
  CheckDto,
  ChecksResponseDto,
  ContinueResponseDto,
  EvidenceDto,
  EvidenceScopeDto,
  FeedbackBundleDto,
  FeedbackDiffDto,
  PreviewAttachDto,
  PreviewStatusDto,
  PushResponseDto,
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

/** GET /api/tasks/:id/file 的原始内容 URL（pdf 直接作 iframe src，同源鉴权）。 */
export function taskFileURL(taskId: string, path: string): string {
  const qs = new URLSearchParams({ path })
  return `/api/tasks/${encodeURIComponent(taskId)}/file?${qs}`
}

/**
 * GET /api/tasks/:id/attachments/:hash 的图片 URL（工具结果里的图，如 read_image）。
 *
 * 用 `<img src>` 直接拉而不是先 fetch 成 blob：同源、带 cookie/token 的 GET 就能鉴权，
 * 由浏览器负责缓存与解码（后端对该路径发了 immutable 的 Cache-Control —— 内容寻址
 * 意味着同一 hash 的字节永不变化）。
 *
 * 路径与 fileURL 保持同一风格（裸 `/api/...` 而非 BASE_URL）：两者都由浏览器直接请求，
 * 走的是同源，而 VITE_API_BASE_URL 只服务于 request() 的代码内请求。
 */
export function taskAttachmentURL(taskId: string, hash: string): string {
  return `/api/tasks/${encodeURIComponent(taskId)}/attachments/${encodeURIComponent(hash)}`
}

/**
 * GET /api/tasks/:id/local-image 的 URL：结论正文里引用的**本地图片**。
 *
 * 为什么需要：Agent 刚把文件写到磁盘，结论里就会写
 * `![总览](D:\code\go\pieqi\.tmp-avatar\final\_contact-6up.png)`。浏览器没有 D 盘，
 * 这个 src 会被当同源相对路径 → 拿回 SPA 兜底页 → 永远是一枚破图（2026-10-24 反馈）。
 * 后端只放行该任务项目目录内、扩展名为图片白名单的文件（见 api/local_image.go）。
 */
export function taskLocalImageURL(taskId: string, path: string): string {
  const qs = new URLSearchParams({ path })
  return `/api/tasks/${encodeURIComponent(taskId)}/local-image?${qs}`
}

/** /file 文本载荷：正文 + 磁盘真尺寸 + 是否被服务端截断。 */
export interface TaskFileText {
  text: string
  /** 磁盘上的完整字节数（截断前后都报告真值） */
  size: number
  truncated: boolean
}

/** GET /api/tasks/:id/file 的原始文本（markdown / code 渲染前拉取）。 */
export async function getTaskFileText(taskId: string, path: string): Promise<TaskFileText> {
  const qs = new URLSearchParams({ path })
  return requestTextMeta(`/tasks/${encodeURIComponent(taskId)}/file?${qs}`)
}

/**
 * GET /api/tasks/:id/artifacts：agent 产出的可查看文件清单
 * （html / markdown / image / pdf / code / text）。
 *
 * 与「变更」列表的区别：变更回答"改了什么"，产物回答"有什么能看"。
 * 打开 URL 由服务端算好；前端不自拼。
 */
export async function getTaskArtifacts(taskId: string): Promise<ArtifactDto[]> {
  const res = await request<{ artifacts: ArtifactDto[] }>(
    `/tasks/${encodeURIComponent(taskId)}/artifacts`,
  )
  return res.artifacts ?? []
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

/**
 * 后端 Go 的 **nil 切片会序列化成 `null`**，而这里的语义是"空列表"。
 *
 * 在适配层归一，而不是让每个消费方各防一次：类型声明的是数组，
 * 那适配层就有责任让它**真的是**数组 —— 否则 `outcome.checks.length`
 * 这种在 TS 里完全合法的写法会在运行期直接抛渲染错误。
 * （`getChecks` 早就在用同一个套路，这里补齐另外两处。）
 */
function arr<T>(v: T[] | null | undefined): T[] {
  return Array.isArray(v) ? v : []
}

/** GET /api/tasks/:id/outcome：Task 结构化结果（完成度规则派生，手机端主验收面） */
export async function getOutcome(taskId: string): Promise<TaskOutcomeDto> {
  const dto = await request<TaskOutcomeDto>(`/tasks/${encodeURIComponent(taskId)}/outcome`)
  return { ...dto, checks: arr(dto.checks), issues: arr(dto.issues), rewinds: arr(dto.rewinds) }
}

/** GET /api/tasks/:id/evidence?scope=task|turn：验证证据快照（随取随派生） */
export async function getEvidence(
  taskId: string,
  scope: EvidenceScopeDto = 'task',
  turn?: number,
): Promise<EvidenceDto> {
  const qs = new URLSearchParams({ scope })
  if (turn && turn > 0) qs.set('turn', String(turn))
  const dto = await request<EvidenceDto>(`/tasks/${encodeURIComponent(taskId)}/evidence?${qs}`)
  return {
    ...dto,
    checks: arr(dto.checks),
    diff_brief: arr(dto.diff_brief),
  }
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

// 视觉采集 API（capturePreview / getConsoleSummary / getNetworkSummary）已于 2026-10-15
// 整体移除 —— 对应后端端点在 preview wildcard 里返回 410 Gone（ADR-0012/0013）。
// 保留此注释是为了让"这里本来有东西"可被检索到，避免日后从 git 历史里翻出来当新需求做。

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
