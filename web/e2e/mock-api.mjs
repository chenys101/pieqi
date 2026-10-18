#!/usr/bin/env node
// e2e 专用的**夹具后端**：一个只有 GET 的假 pieqi API。
//
// 为什么要有它（而不是 page.route 拦截）：
//   1. 真机验收要在**真实浏览器**里跑，page.route 拦截的仍是真实网络栈，
//      但一旦某个请求没被拦（新接口、WebSocket 升级），就会打到 :3000 的**现场**——
//      现场有真任务、真 agent 进程，被 e2e 触发一次干预就是不可逆的生产事故。
//      baseURL 指向本服务，从端口层面就断开了这条可能。
//   2. 夹具数据只有一份（fixtures/api-payloads.mjs），mock 与用例共用。
//
// 立下的规矩：
//   · 只实现 GET；**任何写操作一律 405** —— 用例若试图干预，立刻红，而不是改到谁的数据。
//   · WebSocket 升级一律拒绝 —— 让用例显式走"无实时通道"的降级路径（前端有兜底），
//     避免 e2e 与 WS 推送时序纠缠导致 flaky。
//   · 数据只读、不落盘：**不碰 ~/.pieqi**（隔离原则：验证绝不拿现场当试验田）。

import { createServer } from 'node:http'
import { fileURLToPath } from 'node:url'
import {
  agentsResponse,
  authStatusResponse,
  botsResponse,
  feedbackResponse,
  larkRegStatusResponse,
  settingsResponse,
  tasks,
  tasksResponse,
  tunnelStatusResponse,
} from './fixtures/api-payloads.mjs'

const PORT = Number(process.env.E2E_MOCK_PORT || 3101)
const HOST = '127.0.0.1'

function json(res, body, status = 200) {
  const buf = Buffer.from(JSON.stringify(body))
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': buf.length,
    'Cache-Control': 'no-store',
    // vite 的 /api 代理会透传这些头；CORS 给全，方便单独打开 mock 排查
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Headers': '*',
    'Access-Control-Allow-Methods': 'GET,OPTIONS',
  })
  res.end(buf)
}

/** /api/tasks/:id/... —— 路径分段匹配，不做正则大杂烩（漏一条就是打到真后端） */
function route(url) {
  const p = url.pathname.replace(/\/+$/, '')

  if (p === '/api/tasks') return { body: tasksResponse() }
  if (p === '/api/agents') return { body: agentsResponse }
  if (p === '/api/agents/claude/models' || p === '/api/agents/qoder/models')
    // 200 + 空 models = "该 agent 不支持选模型"，前端隐藏下拉框（不是错误）
    return { body: { agent: p.split('/')[3], models: [] } }
  if (p === '/api/settings') return { body: settingsResponse }
  if (p === '/api/auth/status') return { body: authStatusResponse }
  if (p === '/api/bots') return { body: botsResponse }
  if (p === '/api/larkreg/status') return { body: larkRegStatusResponse }
  if (p === '/api/tunnel/status') return { body: tunnelStatusResponse }
  if (p === '/api/skills' || p === '/api/commands') return { body: { skills: [], commands: [] } }
  if (p === '/api/diagnostics/export') return { status: 404, body: { error: 'e2e mock: not implemented' } }

  const task = /^\/api\/tasks\/([^/]+)(\/.*)?$/.exec(p)
  if (task) {
    const id = decodeURIComponent(task[1])
    const rest = task[2] ?? ''
    const t = tasks.find((x) => x.id === id)
    if (!t) return { status: 404, body: { error: `task ${id} not found` } }
    if (rest === '') return { body: t }
    if (rest === '/feedback') return { body: feedbackResponse(id) }
    if (rest === '/capabilities') return { body: { images: true } }
    if (rest === '/checks') return { body: { checks: [] } }
    if (rest === '/evidence') return { body: { scope: 'task', at: t.updated_at } }
    if (rest === '/outcome')
      return {
        body: {
          task_id: id,
          completeness: t.status === 'completed' ? 'complete' : 'incomplete',
          summary: t.title ?? '',
          issues: t.error ? [{ severity: 'blocker', text: t.error }] : [],
        },
      }
    if (rest.startsWith('/preview/')) return { status: 410, body: { error: 'visual capture removed (ADR-0013)' } }
    return { status: 404, body: { error: `e2e mock: unknown subpath ${rest}` } }
  }

  return { status: 404, body: { error: `e2e mock: unknown path ${p}` } }
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://${HOST}:${PORT}`)

  if (req.method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Headers': '*',
      'Access-Control-Allow-Methods': 'GET,OPTIONS',
    })
    return res.end()
  }

  if (req.method !== 'GET') {
    // 写操作在这个后端里**不存在**。e2e 若要验证干预流程，应该走"断言请求被发出"
    // 而不是"让 mock 真的改数据"——后者会把夹具与断言的因果悄悄换掉。
    return json(res, { error: `e2e mock is read-only (${req.method} ${url.pathname})` }, 405)
  }

  // 健康检查：Playwright 的 webServer 以此判断"起来了"
  if (url.pathname === '/__health') return json(res, { ok: true, tasks: tasks.length })

  const { status, body } = route(url)
  json(res, body, status)
})

// WebSocket 升级：显式拒绝。前端 WebSocketClient 会走重连退避，页面照常渲染。
server.on('upgrade', (req, socket) => {
  socket.destroy()
})

server.listen(PORT, HOST, () => {
  // 这行是 webServer 的 ready 判据之外的第二重证据（stdout 落在 playwright 的日志里）
  console.log(`[e2e-mock] listening on http://${HOST}:${PORT} (${tasks.length} fixture tasks)`)
})
