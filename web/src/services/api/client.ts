// HTTP 统一请求层（方案 §32）：
// base URL / JSON / 错误 / 鉴权头 / 超时，全部在此收敛。
// 组件与 Store 禁止直接 fetch（方案 §33）。

import type { TaskDto } from '@/types/api'
import { truncateTitle } from '@/utils/format'
import type { Task } from '@/types/task'

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api'
const TIMEOUT_MS = 30_000

// ---------- 鉴权上下文（迁移自 V1 auth.js） ----------

const TOKEN_KEY = 'tunnel_token'
const OPENID_KEY = 'feishu_openid'

/** 隧道 token：sessionStorage（刷新/PWA 启动不丢）> URL ?token= */
export function tunnelToken(): string {
  const cached = sessionStorage.getItem(TOKEN_KEY)
  if (cached) return cached
  const url = new URLSearchParams(location.search).get('token')
  if (url) {
    sessionStorage.setItem(TOKEN_KEY, url)
    return url
  }
  return ''
}

/** 飞书 OpenID：sessionStorage（SSO/JSSDK 落地）> URL ?openid=（仅调试） */
export function feishuOpenId(): string {
  const cached = sessionStorage.getItem(OPENID_KEY)
  if (cached) return cached
  const url = new URLSearchParams(location.search).get('openid')
  if (url) {
    sessionStorage.setItem(OPENID_KEY, url)
    return url
  }
  return ''
}

export function setOpenId(openid: string): void {
  if (openid) sessionStorage.setItem(OPENID_KEY, openid)
}

/** 所有 API 请求携带的鉴权头 */
export function authHeaders(): Record<string, string> {
  const h: Record<string, string> = { 'Content-Type': 'application/json' }
  const openid = feishuOpenId()
  if (openid) h['X-Feishu-Openid'] = openid
  const tok = tunnelToken()
  if (tok) h['Authorization'] = `Bearer ${tok}`
  return h
}

// ---------- 401 全局提示（由 providers 注入，避免循环依赖） ----------

let unauthorizedHandler: (() => void) | null = null
export function onUnauthorized(handler: () => void): void {
  unauthorizedHandler = handler
}

// ---------- 请求核心 ----------

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'DELETE' | 'PUT' | 'PATCH'
  body?: unknown
  /** 401 时是否触发全局提示（auth/status 自身轮询不再重复提示） */
  quiet401?: boolean
}

/** 统一请求入口：JSON 序列化 / 超时 / 错误归一为 ApiError */
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS)
  let res: Response
  try {
    res = await fetch(`${BASE_URL}${path}`, {
      method: opts.method ?? 'GET',
      headers: authHeaders(),
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: controller.signal,
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw new ApiError(`请求超时: ${path}`, 0)
    }
    throw new ApiError(`网络错误: ${path}`, 0)
  } finally {
    clearTimeout(timer)
  }

  if (res.status === 401 && !opts.quiet401) unauthorizedHandler?.()

  const text = await res.text()
  let json: Record<string, unknown> = {}
  try {
    json = text ? (JSON.parse(text) as Record<string, unknown>) : {}
  } catch {
    json = { error: text }
  }
  if (!res.ok) {
    throw new ApiError(String(json.error ?? `${path}: ${res.status}`), res.status)
  }
  return json as T
}

/** 原始文本请求（markdown 等非 JSON 内容：不走 JSON 解析，直接返回文本）。 */
export async function requestText(path: string): Promise<string> {
  let res: Response
  try {
    res = await fetch(`${BASE_URL}${path}`, { headers: authHeaders() })
  } catch {
    throw new ApiError(`网络错误: ${path}`, 0)
  }
  if (res.status === 401) unauthorizedHandler?.()
  const text = await res.text()
  if (!res.ok) throw new ApiError(`请求失败: ${path}`, res.status)
  return text
}

/**
 * 二进制请求（导出诊断包等）。
 *
 * 单独一个入口而不是让调用方直接 fetch：导出接口同样要带鉴权头，
 * 而 `<a href>` 下载**不会**带上 Authorization —— 那样只会拿到 401，
 * 却表现为"下载了一个空文件"。这里统一取回 Blob 再由调用方落地。
 */
export async function requestBlob(path: string): Promise<{ blob: Blob; filename: string }> {
  let res: Response
  try {
    res = await fetch(`${BASE_URL}${path}`, { headers: authHeaders() })
  } catch {
    throw new ApiError(`网络错误: ${path}`, 0)
  }
  if (res.status === 401) unauthorizedHandler?.()
  if (!res.ok) {
    let msg = `${path}: ${res.status}`
    try {
      const j = (await res.json()) as { error?: string }
      if (j?.error) msg = j.error
    } catch {
      // 非 JSON 错误体：保留状态码文案
    }
    throw new ApiError(msg, res.status)
  }
  const cd = res.headers.get('Content-Disposition') ?? ''
  const m = /filename="?([^";]+)"?/.exec(cd)
  return { blob: await res.blob(), filename: m?.[1] ?? 'download' }
}

// ---------- Task DTO → 前端模型 Adapter（方案 §55） ----------

const DEFAULT_AGENT = 'claude-code'

/** 后端 DTO → 前端领域模型：字段命名 / 兜底逻辑收敛在此 */
export function adaptTask(dto: TaskDto): Task {
  return {
    id: dto.id,
    title: dto.title || truncateTitle(dto.prompt),
    prompt: dto.prompt,
    project: dto.project_id || dto.project_path,
    projectPath: dto.project_path,
    status: dto.status,
    agent: DEFAULT_AGENT,
    sessionId: dto.claude_session_id || dto.acp_session_id || dto.id,
    decision: dto.current_decision
      ? {
          id: dto.current_decision.id,
          taskId: dto.id,
          kind: dto.current_decision.kind || 'approval',
          tool: dto.current_decision.tool_name,
          risk: dto.current_decision.risk,
          summary: dto.current_decision.summary,
          options: dto.current_decision.options ?? [],
          createdAt: dto.current_decision.created_at,
        }
      : undefined,
    output: dto.output,
    error: dto.error,
    createdAt: dto.created_at,
    updatedAt: dto.updated_at,
    startedAt: dto.started_at,
    finishedAt: dto.finished_at,
    diffStat: dto.diff_stat
      ? {
          files: dto.diff_stat.files,
          additions: dto.diff_stat.additions,
          deletions: dto.diff_stat.deletions,
          capturedAt: dto.diff_stat.captured_at,
        }
      : undefined,
  }
}
