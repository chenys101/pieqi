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
  agentModelsResponse,
  agentsResponse,
  authStatusResponse,
  botsResponse,
  feedbackResponse,
  larkRegStatusResponse,
  LONG_TASK_ID,
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

/**
 * /api/tasks/:id/... —— 路径分段匹配，不做正则大杂烩（漏一条就是打到真后端）
 *
 * `soloId`（来自请求头 `x-e2e-solo: 1`，见 server 里那处注释）：任务清单只回这一条，
 * 用来复现"只有一个在途任务 ⇒ 头部不渲染「其他任务」按钮"的生产形态。
 */
function route(url, soloId = '') {
  const p = url.pathname.replace(/\/+$/, '')

  if (p === '/api/tasks') return { body: tasksResponse(soloId) }
  if (p === '/api/agents') return { body: agentsResponse }
  // 模型清单：对**每个** agent 都下发夹具目录里那份（查不到就给空清单）。
  //
  // ⚠️ 早先这里对 claude / qoder 一律回空清单，而 claude 又是默认 agent ——
  // 于是「输入框里的模型选择器」在**所有** e2e 场景里都只有「Agent 默认」的形态，
  // 用户实际看到的「选了具体模型之后的长名字」从没被验过。
  //
  // ⚠️ 这里刻意**不按 agent 名去掉清单**（哪怕 claude 目录里是 `model_selectable:false`）：
  // 夹具页的会话场景直接挂 InterveneInput，父组件不在（生产里是 SessionPage），
  // 上一轮选过的模型不会自己回来 —— 要让"触发器带长名字"这个真实形态出现，
  // 只能靠用户自己点开选择器。若 claude 回空清单，那个点开动作就没有可选项，
  // 验收里最挤的那个形态（宽触发器 + 加图按钮 + 发送按钮同行）永远不会被渲染。
  {
    const m = /^\/api\/agents\/([^/]+)\/models$/.exec(p)
    if (m) {
      const agent = decodeURIComponent(m[1])
      const models = agentModelsResponse[agent]?.models ?? agentModelsResponse.dsh.models
      return { body: { agent, models } }
    }
  }
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
    // 会话能力（图片入站通路）。字段名与后端一致：`image_prompt`（见 types/api.ts 的
    // TaskCapabilitiesDto 与 SessionPage 的 canAttachImages）。
    //
    // ⚠️ 早先这里回的是 `{ images: true }` —— 字段名拼错，前端读 `image_prompt` 拿到
    // undefined ⇒ 加图入口在所有 e2e 场景里**永远不渲染**，而"入口没出现"看起来
    // 与"这个 agent 不收图"一模一样，于是这个功能零覆盖（2026-10-24 发现并改正）。
    // 夹具模型的配置是 ImagePrompt: true（internal/agent/manifest.go），所以这里给 true。
    if (rest === '/capabilities') return { body: { image_prompt: true } }
    // 截断标题生成后会补发一条"标题变了"的事件（生产里由 runner 经 WS 推送）。
    // 新建任务页也订阅它，所以夹具必须回一个**形状正确**的响应，否则控制台会冒 404。
    if (rest === '/history') return { body: { events: [] } }
    if (rest === '/checks') return { body: { checks: [] } }
    // 产物清单（ADR-0018）：前端「变更」Tab 的文件行会打开它。
    // 夹具里没有产物 ⇒ 空数组；**必须实现**，否则 404 会被 layout 用例的
    // 「控制台不得有错误」判据抓成整片红（看起来像页面回归，实际是夹具缺口）。
    if (rest === '/artifacts') return { body: { artifacts: [] } }
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

/** 写请求的流水账（只记不放行；读回与清空见 /__writes）。 */
const recordedWrites = []

/**
 * 夹具**时序/故障**开关 —— 专为"异步竞态"这类用例。
 *
 * 为什么需要（2026-10-24，新建任务页的预选项目）：`GET /api/settings`（默认工作目录）
 * 与 `GET /api/tasks`（历史项目清单）是两个请求，谁先到不确定。真实缺陷恰恰只在一侧
 * 先到的那一瞬间暴露（"配了默认目录却不生效"只在列表比设置先到时出现），而本地跑
 * 一百次也复现不了 —— 除非能**指定**先后。于是给夹具一个开关，用例把某一侧摁住。
 *
 *   · `_delay=<path>:<ms>`  该路径的响应延后 ms 再发（可重复，按出现顺序生效）
 *   · `_fail=<path>`        该路径回 500（模拟"这条数据拉不到"）
 *
 * 两者都**只影响读**，且每个用例自己设、自己擦（`DELETE /__faults` 或下次设置覆盖），
 * 不改变任何夹具数据本身。
 */
let faults = { delay: {}, fail: new Set() }

function parseFaultQuery(url) {
  const delays = {}
  for (const v of url.searchParams.getAll('_delay')) {
    const i = v.lastIndexOf(':')
    if (i > 0) delays[v.slice(0, i)] = Number(v.slice(i + 1)) || 0
  }
  const fail = new Set(url.searchParams.getAll('_fail'))
  faults = { delay: delays, fail }
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

  /**
   * 写操作：**记录但不改数据**。
   *
   * 这是"真点击用例"的前提。三条判据：
   *
   * ① 必须能观测：点「允许一次」/「发送」之后，用例要能证明**请求真的发出去了**。
   *    没有这条通道，就只能断言"按钮被点到了"——而那证明不了整条链通。
   * ② **不能改数据**：mock 一旦真的改夹具，"页面为什么变成这样"就有了两个来源
   *    （我们的点击 / mock 的副作用），断言与因果会被悄悄换掉。
   *    所以这里只记账，状态推进由用例自己驱动 store（与生产里"WS 推回快照"等价）。
   * ③ 记账要能被读回与清空：`GET /__writes` 取、`DELETE /__writes` 清。
   *    用例之间必须互不干扰，否则"上一条用例的请求"会让下一条假绿。
   */
  //
  // ⚠️ 顺序陷阱（实测踩过）：`/__writes` 的 GET 必须排在 DELETE 之后判断，
  //    或者像这里一样**先看 method 再看路径**。写成"路径命中就 return"会让
  //    `DELETE /__writes` 也走 GET 分支 —— 永远返回旧的流水账、永远清不掉，
  //    而 beforeEach 里那句"清空后读回为 0"会一直失败，报的却是
  //    "有别的用例在并发写"（完全指错方向）。
  if (url.pathname === '/__writes' && req.method === 'GET') {
    return json(res, { writes: recordedWrites })
  }

  // 清空流水账（用例 beforeEach 用）。
  if (url.pathname === '/__writes' && req.method === 'DELETE') {
    recordedWrites.length = 0
    return json(res, { ok: true, cleared: true })
  }

  // ⚠️ 夹具路由表的**快照**：让 `vite --mode e2e` 的 `router.push('/tasks/new?project=…')`
  // 这类**不在夹具里的路径**也能落到夹具页（否则夹具路由表没有该路径 ⇒
  // vue-router 报"No match"、页面停在原地，看起来像"点了没反应"）。
  // 只做服务端重定向，不改变任何夹具数据；用于复现"从详情页带项目进新建页"的落点。
  if (url.pathname === '/__fixture' && req.method === 'GET') {
    const target = url.searchParams.get('to') || '/'
    res.writeHead(302, { Location: target, 'Cache-Control': 'no-store' })
    return res.end()
  }

  // 夹具故障/时序开关：设置见 parseFaultQuery，清空走 DELETE /__faults
  if (url.pathname === '/__faults' && req.method === 'DELETE') {
    faults = { delay: {}, fail: new Set() }
    return json(res, { ok: true, cleared: true })
  }
  if (url.pathname === '/__faults') {
    parseFaultQuery(url)
    return json(res, { ok: true, delay: faults.delay, fail: [...faults.fail] })
  }

  if (req.method !== 'GET' && req.method !== 'DELETE') {
    const chunks = []
    req.on('data', (c) => chunks.push(c))
    req.on('end', () => {
      const raw = Buffer.concat(chunks).toString('utf8')
      let body = null
      try {
        body = raw ? JSON.parse(raw) : null
      } catch {
        body = { __unparsed: raw.slice(0, 2000) }
      }
      recordedWrites.push({ method: req.method, path: url.pathname, body, at: Date.now() })
      // 200 + 一个中性载荷：调用方（intervene / cancel / ...）只关心"成功"。
      // 刻意不回夹具的新状态 —— 见上面判据 ②。
      json(res, { ok: true, recorded: true })
    })
    return
  }

  // 健康检查：Playwright 的 webServer 以此判断"起来了"
  if (url.pathname === '/__health') return json(res, { ok: true, tasks: tasks.length })

  // 故障/时序开关（见 faults 的注释）：只对**读**生效，且不改数据
  const failFor = faults.fail.has(url.pathname)
  const delayMs = faults.delay[url.pathname] || 0
  /**
   * `x-e2e-solo: 1` —— 让 `GET /api/tasks` 只回当前这一条任务。
   *
   * 为什么必须从**服务端**这一层收窄：应用启动时 `setupProviders()` 会自己
   * `loadTasks()` 覆盖夹具在 beforeEnter 里灌的 store。只在夹具页 seed 一条任务，
   * 会被这次请求换回全量清单，「其他任务」按钮又出现了 —— 用例测的就不是出问题的布局。
   */
  const soloId = req.headers['x-e2e-solo'] === '1' ? LONG_TASK_ID : ''
  const send = () => {
    if (failFor) return json(res, { error: 'e2e mock: fault injected' }, 500)
    const { status, body } = route(url, soloId)
    json(res, body, status)
  }
  if (delayMs > 0) setTimeout(send, delayMs)
  else send()
})

// WebSocket 升级：显式拒绝。前端 WebSocketClient 会走重连退避，页面照常渲染。
server.on('upgrade', (req, socket) => {
  socket.destroy()
})

server.listen(PORT, HOST, () => {
  // 这行是 webServer 的 ready 判据之外的第二重证据（stdout 落在 playwright 的日志里）
  console.log(`[e2e-mock] listening on http://${HOST}:${PORT} (${tasks.length} fixture tasks)`)
})
