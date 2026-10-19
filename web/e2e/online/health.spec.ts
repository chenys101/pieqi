// 线上复量 · R4：静态资源与 SPA 兜底健康（**纯 HTTP，不开浏览器**）。
//
// 为什么先做这一条（见 docs/testing/ui-e2e-online.md §8）：
//   · 它是唯一不需要浏览器的用例 —— 部署刚完成、CDN 还没热时就能跑；
//   · 它能立刻抓到"二进制换新了但前端资源 404"这类问题 —— 那种故障下
//     页面会白屏，但 `GET /api/tasks` 可能照样 200，光看接口是看不出来的。
//
// ★ 隔离判据：本项目的线上用例**只读**。这里全部是 GET。
//   能力上也不需要写 —— 内网访问线上实例时读写鉴权强度相同（auth.go:11），
//   一次误调就是真实副作用，所以本层设计成"物理上只发 GET"。
//
// 运行：ONLINE_BASE_URL=http://127.0.0.1:3000 npx playwright test --project=online
import { test, expect } from '@playwright/test'

test('R4 · 首页返回 SPA 骨架（不是空壳/错误页）', async ({ request }) => {
  const res = await request.get('/')
  expect(res.status(), '首页不是 200：可能是二进制没起来、或反向代理配置错了').toBe(200)

  const html = await res.text()
  // SPA 挂载点：缺了它页面一定白屏，而 HTTP 状态仍是 200 —— 这正是要抓的"假健康"。
  expect(html, '返回了 200 但没有 #app 挂载点（拿到的是别的页面？）').toContain('id="app"')
  // 入口脚本：同样是"状态 200 但页面起不来"的来源。
  expect(html, 'index.html 里没有 module 入口脚本').toMatch(/<script[^>]+type="module"/)
})

test('R4 · 前端资源可访问（从 index.html 里解析真实资源名）', async ({ request }) => {
  const html = await (await request.get('/')).text()

  // 先找 assets/*.js（vite 产物名带 hash，不能写死）
  const m = /<script[^>]+src="([^"]+\.js)"/.exec(html)
  expect(m, 'index.html 里解析不到 js 资源路径').not.toBeNull()
  const src = m![1]

  const res = await request.get(src)
  expect(res.status(), `入口资源 ${src} 不是 200：二进制里的前端产物与 index.html 不匹配（典型的"前端没 build 就编了 Go"）`).toBe(200)

  // 内容类型也要对：返回 200 但内容是 HTML（SPA fallback 兜底）说明文件真的不存在，
  // 只是被 NoRoute 接住了 —— 这是本项目 registerStatic 的已知行为，必须在这里暴露。
  const ct = res.headers()['content-type'] ?? ''
  expect(ct, `入口资源 ${src} 返回的是 ${ct}（不是 JS）—— 文件不存在，被 SPA fallback 兜成了 index.html`).toContain('javascript')
})

test('R4 · 接口鉴权通路正常（内网放行应返回 200）', async ({ request }) => {
  const res = await request.get('/api/tasks')
  expect(res.status(), 'GET /api/tasks 不是 200：鉴权通路有问题（或服务没起来）').toBe(200)

  const body = await res.json()
  expect(body, '/api/tasks 没有 projects 字段').toHaveProperty('projects')
  expect(Array.isArray(body.projects), 'projects 不是数组').toBe(true)
})

test('R4 · 未命中接口返回 404 JSON，而不是被 SPA fallback 兜成 HTML', async ({ request }) => {
  const res = await request.get('/api/__definitely_not_a_route__')
  expect(res.status(), '未命中的 /api 路径应返回 404（registerStatic 对 /api/ 前缀不回退 SPA）').toBe(404)

  // 这条判据的价值：如果它返回了 200 + HTML，说明 NoRoute 的 /api/ 前缀判断被破坏，
  // 前端调用任何不存在的接口都会拿到一个 HTML，然后 JSON.parse 失败 ——
  // 那时排查方向会完全错（看起来像"接口返回了乱码"）。
  const ct = res.headers()['content-type'] ?? ''
  expect(ct, '未命中的 /api 返回了非 JSON 内容：SPA fallback 的前缀判断被破坏').toContain('json')
})

test('R4 · 页面不得整页横向溢出（纯 HTTP 拿不到，这里只校验视口 meta）', async ({ request }) => {
  // 说明：横向溢出必须靠真实浏览器量（layout 层做）。线上层先守住**能导致溢出的
  // 那个前置条件** —— viewport meta 存在且带 width=device-width。
  // 缺了它，移动端会按 980px 渲染整页，横向溢出必现，而桌面上看不出来。
  const html = await (await request.get('/')).text()
  expect(html, '缺少 viewport meta：移动端会按 980px 渲染，横向溢出必现').toMatch(
    /<meta[^>]+name="viewport"[^>]+width=device-width/,
  )
})
