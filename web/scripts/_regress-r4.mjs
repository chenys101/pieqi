// R4 运行时回归（AC-R4-02 / AC-R4-04）：真 SW 环境（vite preview = localhost）。
//   AC-R4-02：预置旧版本缓存 → SW activate 后旧缓存键被删除；
//   AC-R4-04：断网（context.setOffline）后 reload，页面壳仍可用（network-first 回退缓存）。
// 剧本对齐真实用户流：首次访问装 SW → **再来一次受控导航**（此时 '/' 才会进
// shell 缓存）→ 之后才谈得上离线可用。靶点 = web 生产产物。
import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const { chromium } = require('playwright')
import { spawn } from 'node:child_process'

const PORT = 4199
const BASE = `http://127.0.0.1:${PORT}`
const CHROME = process.env.CHROME ?? 'C:/Program Files/Google/Chrome/Application/chrome.exe'

const preview = spawn(
  process.execPath,
  ['./node_modules/vite/bin/vite.js', 'preview', '--port', String(PORT), '--strictPort'],
  { cwd: process.cwd(), stdio: 'ignore' },
)
await new Promise((r) => setTimeout(r, 2500))

let ok = true
try {
  const browser = await chromium.launch({ executablePath: CHROME, headless: true })
  const context = await browser.newContext()
  const page = await context.newPage()

  // ① 首次访问：装 SW
  await page.goto(BASE, { waitUntil: 'networkidle', timeout: 20000 })
  await page.waitForFunction(() => navigator.serviceWorker.ready, { timeout: 15000 })

  // ② 受控导航：此时 SW 拦截 '/' → network-first 写入 shell 缓存（离线的前提）
  await page.reload({ waitUntil: 'networkidle', timeout: 20000 })
  await page.waitForTimeout(500)

  // ③ 造旧版本缓存 → 重注册触发新一轮 activate → 清理。
  // ⚠️ 同 URL 重注册对浏览器是"同一份字节" → 可能复用仍存活的旧 worker、
  //    不触发新 install/activate → 用带 query 的 script URL 强制一次真更新。
  await page.evaluate(async () => {
    const c = await caches.open('pieqi-shell-v-old-fake')
    await c.put('/', new Response('<html>old</html>', { headers: { 'Content-Type': 'text/html' } }))
    const regs = await navigator.serviceWorker.getRegistrations()
    await Promise.all(regs.map((r) => r.unregister()))
    await navigator.serviceWorker.register('/sw.js?force-update=1')
  })
  // 等新 SW 真正 activated（注册 settle + 安装完成需要真实时间；waitForFunction
  // 的快照可能落在旧 worker 上）—— 用轮询状态 + 缓冲，与手动验证一致
  await page
    .waitForFunction(
      async () => {
        const regs = await navigator.serviceWorker.getRegistrations()
        const reg = regs[0]
        return !!reg && reg.active?.state === 'activated' && !reg.installing && !reg.waiting && reg.active.scriptURL.includes('force-update')
      },
      { timeout: 15000, polling: 250 },
    )
    .catch(() => {})
  await page.waitForTimeout(1500) // waitUntil 里的异步清理落地
  const keys = await page.evaluate(async () => (await caches.keys()).join(','))
  console.log('缓存键:', keys)
  if (keys.includes('v-old-fake')) {
    console.log('✗ AC-R4-02 失败：旧缓存未被清理')
    ok = false
  } else {
    console.log('AC-R4-02 OK：旧缓存已清理')
  }

  // ④ AC-R4-04：再来一次受控导航（确保 '/' 在当前 SW 下已进 shell 缓存）→ 断网 reload
  await page.reload({ waitUntil: 'networkidle', timeout: 20000 }).catch(() => {})
  await page.waitForTimeout(800)
  await context.setOffline(true)
  await page.reload({ waitUntil: 'load', timeout: 15000 }).catch(() => {})
  await page.waitForTimeout(1200)
  const offlineOk = await page.evaluate(() => {
    const app = document.querySelector('#app')
    return !!app && app.children.length > 0 && document.body.innerHTML.length > 500
  })
  console.log(offlineOk ? 'AC-R4-04 OK：离线壳可加载' : '✗ AC-R4-04 失败：离线空白')
  if (!offlineOk) ok = false

  await browser.close()
} catch (e) {
  console.log('✗ 异常:', String(e).split('\n')[0])
  ok = false
} finally {
  preview.kill()
}
console.log(ok ? 'R4 RUNTIME OK' : 'R4 RUNTIME FAIL')
process.exit(ok ? 0 : 1)
