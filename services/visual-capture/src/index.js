// src/index.js
// Pieqi 视觉采集服务（p2-design.md §2，ADR-0006）：
// 私有常驻服务（默认 127.0.0.1:18791），Go 侧 VisualCaptureManager 经 HTTP 调用。
//
// 安全约束：
//   - 只连调用方传入的 127.0.0.1 preview 端口（Go 侧已校验），不接触用户页面之外的东西
//   - 不持有隧道 token（同 Preview 子进程约束）
//   - 浏览器 no-sandbox：无用户数据、无持久 profile，仅内存态采集
//
// API：
//   GET  /health      探活（供 Go Proc 模式 waitHealth）
//   POST /v1/capture  一次采集会话：打开 url → 收集 console(error/warn) +
//                      network(4xx/5xx/failed) → 截图，全部结果一次返回
const { createServer } = require("node:http");
const { chromium } = require("playwright-core");

module.exports = { startServer };

// 直接运行时启动（node src/index.js）
if (require.main === module) {
  startServer({
    port: Number(process.env.VISUAL_PORT || 18791),
    host: process.env.VISUAL_HOST || "127.0.0.1",
  });
}

// 单浏览器长驻：每次 capture 复用 browser，只开新 context（会话隔离，无 cookie 残留）。
let browserPromise = null;

function getBrowser() {
  if (!browserPromise) {
    browserPromise = chromium.launch({
      headless: true,
      args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
    });
    // 崩溃后下次重连（browser close → 置空，重新 launch）
    browserPromise.catch(() => {
      browserPromise = null;
    });
    browserPromise.then((b) => {
      b.on("disconnected", () => {
        browserPromise = null;
      });
    });
  }
  return browserPromise;
}

function startServer({ port, host }) {
  const server = createServer(async (req, res) => {
    if (req.method === "GET" && req.url === "/health") {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ ok: true }));
      return;
    }
    if (req.method === "POST" && req.url === "/v1/capture") {
      await handleCapture(req, res);
      return;
    }
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "not found" }));
  });
  server.listen(port, host);
  // eslint-disable-next-line no-console
  console.log(`visual-capture listening on http://${host}:${port}`);
  return server;
}

// 一次采集会话：
//   1. 打开 url（收集 console error/warn 与 network 4xx/5xx/failed）
//   2. 等 networkidle 或 timeoutMs（默认 8s），给 SPA 渲染与 lazy 请求留时间
//   3. 截图（视口或全页）
async function handleCapture(req, res) {
  const body = await readJSON(req);
  const url = String(body.url || "");
  if (!/^http:\/\/127\.0\.0\.1:\d+\//.test(url)) {
    res.writeHead(400, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "only 127.0.0.1 preview URLs are allowed" }));
    return;
  }
  const fullPage = Boolean(body.full_page);
  const timeoutMs = Math.min(Number(body.timeout_ms) || 8000, 30000);

  const console_ = [];
  const network = [];
  try {
    const browser = await getBrowser();
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    const page = await context.newPage();

    // console：只留 error / warn（设计 §4）
    page.on("console", (msg) => {
      const level = msg.type();
      if (level === "error" || level === "warning" || level === "warn") {
        console_.push({ level: level === "warning" ? "warn" : level, text: msg.text(), at: new Date().toISOString() });
      }
    });
    // network：只留 4xx / 5xx / failed（设计 §5；failed = requestfailed，status=0）
    page.on("response", (resp) => {
      const status = resp.status();
      if (status >= 400) {
        network.push({ url: resp.url(), method: resp.request().method(), status, at: new Date().toISOString() });
      }
    });
    page.on("requestfailed", (req) => {
      network.push({ url: req.url(), method: req.method(), status: 0, at: new Date().toISOString() });
    });

    await page.goto(url, { waitUntil: "domcontentloaded", timeout: timeoutMs });
    try {
      await page.waitForLoadState("networkidle", { timeout: timeoutMs });
    } catch {
      /* networkidle 超时不算失败：SPA 长连接会一直不 idle，继续截图 */
    }
    const png = await page.screenshot({ fullPage });
    await context.close();

    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ png_base64: png.toString("base64"), console: console_, network }));
  } catch (err) {
    res.writeHead(502, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: String(err && err.message ? err.message : err) }));
  }
}

function readJSON(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    req.on("data", (c) => {
      size += c.length;
      if (size > 64 * 1024) {
        reject(new Error("body too large"));
        req.destroy();
        return;
      }
      chunks.push(c);
    });
    req.on("end", () => {
      try {
        resolve(chunks.length ? JSON.parse(Buffer.concat(chunks).toString()) : {});
      } catch (e) {
        reject(e);
      }
    });
    req.on("error", reject);
  });
}
