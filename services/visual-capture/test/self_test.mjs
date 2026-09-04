// self_test.mjs visual-capture 服务自测（p2-design.md §12 P2-a）。
// 前置：npx playwright-core install chromium（部署前置，缺失时 SKIP）。
// 覆盖：/health 探活、/v1/capture 截图 + console error/warn + network 4xx 采集、
// 非 127.0.0.1 URL 拒绝（安全约束）。
import assert from "node:assert";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { chromium } from "playwright-core";

// --- 前置检查：Chromium 可用性（不可用 → skip 全部） ---
let chromiumOK = true;
try {
  const b = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
  await b.close();
} catch {
  chromiumOK = false;
}

// --- 测试用靶子页面：注入 console.error / warn + 一个 404 请求 ---
const target = createServer((req, res) => {
  if (req.url.startsWith("/missing")) {
    res.writeHead(404);
    res.end("nope");
    return;
  }
  res.writeHead(200, { "content-type": "text/html" });
  res.end(`<!doctype html><html><body><h1>hello visual</h1>
<script>
  console.error("boom");
  console.warn("careful");
  fetch("/missing.json").catch(()=>{});
</script></body></html>`);
});
await new Promise((r) => target.listen(0, "127.0.0.1", r));
const targetURL = `http://127.0.0.1:${target.address().port}/`;

if (!chromiumOK) {
  console.log("SKIP: chromium not installed (run: npx playwright-core install chromium)");
  target.close();
  process.exit(0);
}

// --- 起 visual-capture 服务子进程（随机端口，避免与开发机常驻实例冲突） ---
const svcPort = 20000 + Math.floor(Math.random() * 20000);
const svcURL = `http://127.0.0.1:${svcPort}`;
const svc = spawn(process.execPath, ["src/index.js"], {
  cwd: new URL("..", import.meta.url).pathname,
  env: { ...process.env, VISUAL_PORT: String(svcPort), VISUAL_HOST: "127.0.0.1" },
  stdio: ["ignore", "pipe", "inherit"],
});
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let failures = 0;
try {
  // 等 /health 就绪（最多 15s）
  let health = false;
  for (let i = 0; i < 75; i++) {
    try {
      const r = await fetch(`${svcURL}/health`);
      if (r.ok) { health = true; break; }
    } catch { /* retry */ }
    await sleep(200);
  }
  assert.ok(health, "/health must be ready within 15s");

  // capture：截图 + console + network
  const cap = await fetch(`${svcURL}/v1/capture`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ url: targetURL }),
  });
  assert.equal(cap.status, 200, "capture must return 200");
  const data = await cap.json();
  assert.ok(data.png_base64?.length > 100, "png must be non-trivial");
  const png = Buffer.from(data.png_base64, "base64");
  assert.equal(png[0], 0x89, "PNG magic byte");

  const errLevels = data.console.filter((e) => e.level === "error").map((e) => e.text);
  const warnLevels = data.console.filter((e) => e.level === "warn").map((e) => e.text);
  assert.ok(errLevels.includes("boom"), `console error captured: ${JSON.stringify(data.console)}`);
  assert.ok(warnLevels.includes("careful"), `console warn captured: ${JSON.stringify(data.console)}`);

  const notFound = data.network.filter((n) => n.status === 404);
  assert.ok(notFound.length >= 1 && notFound[0].url.includes("/missing.json"), `network 404 captured: ${JSON.stringify(data.network)}`);

  // 安全：非 127.0.0.1 URL → 400
  const bad = await fetch(`${svcURL}/v1/capture`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ url: "https://example.com/" }),
  });
  assert.equal(bad.status, 400, "external URL must be rejected");
  console.log("visual-capture self-test: all passed");
} catch (e) {
  failures++;
  console.error("FAIL:", e.message);
} finally {
  svc.kill("SIGTERM");
  target.close();
}
process.exit(failures ? 1 : 0);
