// test/image_http_probe.mjs —— 端到端探测：真起桥服务，POST /prompt 带图，
// 验证桥**接受**（202）并对非法载荷给出 400。
//
// 为什么需要它（而不是只跑单测）：单测覆盖 buildImageBlocks / promptStream 两个纯函数，
// 但"HTTP 层有没有把 images 取出来传给 pushPrompt"是另一段代码（index.js 的路由分支）。
// 这段此前完全没被任何测试覆盖 —— 而它恰恰是"图在哪儿丢的"最可能的落点。
//
// ⚠️ 本探测**不**验证"SDK 真的把图发给模型了"：那需要 spawn claude 子进程，
//    在沙箱内会 EPERM（详见 AGENTS.md 的沙箱边界）。本探测到 HTTP → pushPrompt 为止。
//
// 用法：node test/image_http_probe.mjs
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";

const PORT = 18899;
const BASE = `http://127.0.0.1:${PORT}`;
let failed = 0;
let passed = 0;
function check(name, cond, detail = "") {
  if (cond) {
    passed++;
    console.log(`  ok   ${name}`);
  } else {
    failed++;
    console.log(`  FAIL ${name}${detail ? " — " + detail : ""}`);
  }
}

const B64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg==";

const child = spawn(process.execPath, ["src/index.js"], {
  env: { ...process.env, BRIDGE_PORT: String(PORT) },
  stdio: ["ignore", "pipe", "pipe"],
});
let serverLog = "";
child.stdout.on("data", (d) => (serverLog += d));
child.stderr.on("data", (d) => (serverLog += d));
// 提前退出要能看出来（否则下面会等到超时，报错也看不出原因）
child.on("exit", (code) => {
  if (code !== 0 && code !== null) console.log(`[bridge exited code=${code}]\n${serverLog}`);
});

async function waitHealth(timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(`${BASE}/v1/health`);
      if (r.ok) return true;
    } catch {
      /* 还没起来 */
    }
    await sleep(150);
  }
  return false;
}

async function post(path, body) {
  const r = await fetch(`${BASE}${path}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  let payload = null;
  try {
    payload = await r.json();
  } catch {
    /* 无 body */
  }
  return { status: r.status, body: payload };
}

try {
  const up = await waitHealth();
  if (!up) {
    console.log(`FAIL 桥没起来：\n${serverLog}`);
    process.exit(1);
  }
  console.log("桥已就绪，开始 HTTP 层探测\n");

  // 建会话（真建会 spawn SDK 子进程；沙箱内会失败，但 HTTP 层仍会返回 201 或错误）
  const created = await post("/v1/sessions", { cwd: process.cwd() });
  console.log(`  (POST /v1/sessions → ${created.status})`);
  const sid = created.body?.id;

  if (!sid) {
    // 会话建不起来（沙箱 spawn EPERM）时，无法测 /prompt 的接受路径。
    // 这时**如实退出为跳过**，不伪装成通过。
    console.log("\nSKIP 无法创建会话（SDK 需 spawn 子进程，沙箱内 EPERM）——");
    console.log("     /prompt 的成功路径未验证；仅纯函数层被单测覆盖。");
    console.log(`     桥日志片段：${serverLog.slice(0, 300)}`);
    process.exit(0);
  }

  // 1) 合法带图 → 202
  const ok = await post(`/v1/sessions/${sid}/prompt`, {
    text: "这张图里是什么",
    clientRef: "r1",
    images: [{ data: B64, mime_type: "image/png" }],
  });
  check("合法带图 → 202 接受", ok.status === 202, `status=${ok.status} body=${JSON.stringify(ok.body)}`);

  // 2) 非法载荷 → 400（证明 HTTP 层真的把 images 交给了校验，而不是忽略掉）
  const cases = [
    ["空 data", [{ data: "", mime_type: "image/png" }]],
    ["非法 base64", [{ data: "!!!", mime_type: "image/png" }]],
    ["data URL 前缀", [{ data: "data:image/png;base64," + B64, mime_type: "image/png" }]],
    ["不支持的 MIME", [{ data: B64, mime_type: "image/bmp" }]],
    ["超张数", Array.from({ length: 9 }, () => ({ data: B64, mime_type: "image/png" }))],
  ];
  for (const [name, images] of cases) {
    const r = await post(`/v1/sessions/${sid}/prompt`, { text: "x", clientRef: "r2", images });
    check(`${name} → 400`, r.status === 400, `status=${r.status} body=${JSON.stringify(r.body)}`);
  }

  // 3) 只发图（空文本）→ 202（合法用法）
  const onlyImg = await post(`/v1/sessions/${sid}/prompt`, {
    text: "",
    clientRef: "r3",
    images: [{ data: B64, mime_type: "image/png" }],
  });
  check("空文本 + 有图 → 202", onlyImg.status === 202, `status=${onlyImg.status}`);

  // 4) 纯文本空 → 仍然 400（原有约束不能被放宽）
  const emptyText = await post(`/v1/sessions/${sid}/prompt`, { text: "", clientRef: "r4" });
  check("空文本无图 → 400（原约束保持）", emptyText.status === 400, `status=${emptyText.status}`);
} finally {
  child.kill();
  await sleep(200);
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
