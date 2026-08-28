// test/bridge_self_test.mjs — bridge 集成自测（一次性验证脚本，非正式测试框架）
//
// 覆盖全端点（multi-agent.md §5.3）：
//   create → SSE 事件流 → 多轮流式（上下文跨轮）→ 权限挂起/释放 → cancel 后可用
//   → close 后拒绝 → resume 重建上下文
// 每轮 /prompt 带 clientRef，turn_end 按 clientRef 精确关联。
import { spawn } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SRC = path.join(__dirname, "..", "src", "index.js");
const TEST_CWD = mkdtempSync(path.join(tmpdir(), "bridge-self-"));
const WATCHDOG_MS = 300_000;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function waitFor(fn, label, timeoutMs = 60_000) {
  const t0 = Date.now();
  while (!fn()) {
    if (Date.now() - t0 > timeoutMs) throw new Error(`timeout waiting: ${label}`);
    await sleep(50);
  }
}

// 起桥：BRIDGE_PORT=0 → 系统分配端口，从 stdout 解析
const child = spawn(process.execPath, [SRC], {
  env: { ...process.env, BRIDGE_PORT: "0", BRIDGE_HOST: "127.0.0.1" },
  stdio: ["ignore", "pipe", "inherit"],
});
let BASE = null;
child.stdout.on("data", (d) => {
  const m = d.toString().match(/listening on http:\/\/[^:]+:(\d+)/);
  if (m) BASE = `http://127.0.0.1:${m[1]}`;
});

async function post(p, body) {
  const res = await fetch(BASE + p, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
  return { status: res.status, body: await res.json().catch(() => ({})) };
}
async function del(p) {
  const res = await fetch(BASE + p, { method: "DELETE" });
  return { status: res.status, body: await res.json().catch(() => ({})) };
}

// SSE 读取：逐帧解析 event/data，推到 events 数组
function openSSE(p, events) {
  (async () => {
    try {
      const res = await fetch(BASE + p);
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        let idx;
        while ((idx = buf.indexOf("\n\n")) !== -1) {
          const frame = buf.slice(0, idx);
          buf = buf.slice(idx + 2);
          parseFrame(frame, events);
        }
      }
    } catch {
      /* 连接关闭 */
    }
  })();
}
function parseFrame(frame, events) {
  let event = "message";
  const dataLines = [];
  for (const line of frame.split("\n")) {
    if (line.startsWith("event:")) event = line.slice(6).trim();
    else if (line.startsWith("data:")) dataLines.push(line.slice(5).trim());
  }
  if (dataLines.length) {
    try {
      const data = JSON.parse(dataLines.join("\n"));
      events.push({ kind: event, ...data });
    } catch {
      /* 忽略坏帧 */
    }
  }
}

async function waitForEvent(events, kind, pred = () => true, timeoutMs = 60_000) {
  const t0 = Date.now();
  while (true) {
    const found = events.find((e) => e.kind === kind && pred(e));
    if (found) return found;
    if (Date.now() - t0 > timeoutMs) throw new Error(`timeout waiting event ${kind}`);
    await sleep(50);
  }
}
const textOf = (events, turnSeq) =>
  events
    .filter((e) => e.kind === "text_delta" && e.turnSeq === turnSeq)
    .map((e) => e.text)
    .join("");

const results = [];
function check(name, ok, detail = "") {
  results.push({ name, ok });
  console.log(`  ${ok ? "PASS" : "FAIL"}  ${name}${detail ? ` — ${detail}` : ""}`);
}

const watchdog = setTimeout(() => {
  console.log("\n  SELF-TEST WATCHDOG TIMEOUT");
  child.kill();
  process.exit(2);
}, WATCHDOG_MS);

let refN = 0;
const newRef = () => `r${++refN}`;

try {
  // 0. 等桥起来 + health
  await waitFor(() => BASE !== null, "bridge start", 30_000);
  const health = await fetch(BASE + "/v1/health").then((r) => r.json());
  check("health ok", health.ok === true);

  // 1. 创建会话 + 连 SSE
  const created = await post("/v1/sessions", { cwd: TEST_CWD });
  check("create session 201", created.status === 201 && !!created.body.id, created.body.id);
  const sid = created.body.id;
  const events = [];
  openSSE(`/v1/sessions/${sid}/events`, events);
  await sleep(300);

  // 2. 第一轮：记秘密词
  const ref1 = newRef();
  const p1 = await post(`/v1/sessions/${sid}/prompt`, { text: "记住一个秘密词：banana。只回复 ok 两个字，不要解释。", clientRef: ref1 });
  check("prompt 1 -> 202", p1.status === 202);
  const te1 = await waitForEvent(events, "turn_end", (e) => e.clientRef === ref1 && !e.isError, 90_000);
  check("turn1 text_delta streamed", events.some((e) => e.kind === "text_delta" && e.turnSeq === 1));
  const resumeId = te1.turn?.resumeId;
  check("turn1 resumeId captured", !!resumeId, resumeId);

  // 3. 第二轮：问秘密词（上下文跨轮 = 同进程保活）
  const ref2 = newRef();
  await post(`/v1/sessions/${sid}/prompt`, { text: "秘密词是什么？只回复这个词，不要解释。", clientRef: ref2 });
  await waitForEvent(events, "turn_end", (e) => e.clientRef === ref2 && !e.isError, 90_000);
  const turn2Text = textOf(events, 2);
  check("turn2 context preserved (banana)", /banana/i.test(turn2Text), JSON.stringify(turn2Text.slice(0, 40)));

  // 3.5 只读工具自动放行：Read 不产生 permission_needed（P5 修复），轮直接跑完无需审批
  const readFile = path.join(TEST_CWD, "hello.txt");
  writeFileSync(readFile, "SPIKE_READ_OK\n");
  const refRead = newRef();
  await post(`/v1/sessions/${sid}/prompt`, { text: "用 Read 工具读取当前目录下的 hello.txt，然后只回复文件里的内容，不要解释。", clientRef: refRead });
  await waitForEvent(events, "turn_end", (e) => e.clientRef === refRead && !e.isError, 90_000);
  // 只断言关键行为：读工具轮全程无 permission_needed（是否真的读到内容取决于模型，不纳入断言）。
  const readNoPerm = !events.some((e) => e.kind === "permission_needed" && e.turnSeq === 3);
  const turnReadText = textOf(events, 3);
  check("read-only tool auto-allowed (no permission_needed)", readNoPerm, JSON.stringify(turnReadText.slice(0, 40)));

  // 4. 权限挂起/释放：让模型跑 shell（变更类工具仍走审批）
  const ref3 = newRef();
  await post(`/v1/sessions/${sid}/prompt`, { text: "运行 shell 命令 `echo SPIKE_OK`，然后把输出告诉我。", clientRef: ref3 });
  const perm = await waitForEvent(events, "permission_needed", (e) => e.turnSeq === 4, 90_000);
  check("permission_needed raised", !!perm.reqId, `tool=${perm.toolName} reqId=${perm.reqId}`);
  await sleep(2000);
  check("turn blocked while pending", !events.some((e) => e.kind === "turn_end" && e.clientRef === ref3));
  const resp = await post(`/v1/sessions/${sid}/permissions/${perm.reqId}`, { allow: true });
  check("permission approve -> resolved", resp.status === 200 && resp.body.resolved === true);
  await waitForEvent(events, "turn_end", (e) => e.clientRef === ref3, 90_000);
  const turn4Text = textOf(events, 4);
  check("tool executed after approve (SPIKE_OK)", /SPIKE_OK/.test(turn4Text), JSON.stringify(turn4Text.slice(0, 60)));

  // 5. cancel：长诗轮被中断（合成 turn_end），随后会话仍可用
  const ref4 = newRef();
  await post(`/v1/sessions/${sid}/prompt`, { text: "请写一首很长很长的诗，至少要 500 字。", clientRef: ref4 });
  await sleep(800);
  const cancel = await post(`/v1/sessions/${sid}/cancel`, {});
  check("cancel -> 202", cancel.status === 202);
  const teCancelled = await waitForEvent(events, "turn_end", (e) => e.clientRef === ref4, 30_000);
  check("cancelled turn ends (synthetic)", teCancelled.subtype === "cancelled" || teCancelled.isError);
  const ref5 = newRef();
  await post(`/v1/sessions/${sid}/prompt`, { text: "只回复 OK。", clientRef: ref5 });
  const teAfter = await waitForEvent(events, "turn_end", (e) => e.clientRef === ref5 && !e.isError, 90_000);
  const tAfter = textOf(events, teAfter.turnSeq);
  check("session usable after cancel (OK)", /OK/i.test(tAfter), JSON.stringify(tAfter.slice(0, 30)));

  // 6. close：DELETE 后 prompt 拒绝
  const closeRes = await del(`/v1/sessions/${sid}`);
  check("close session", closeRes.status === 200 && closeRes.body.closed === true);
  const pAfter = await post(`/v1/sessions/${sid}/prompt`, { text: "hi" });
  check("prompt after close -> 409", pAfter.status === 409);

  // 7. resume：新会话带 sdkSessionId 重建上下文
  const created2 = await post("/v1/sessions", { cwd: TEST_CWD, resumeSdkSessionId: resumeId });
  check("resume create 201", created2.status === 201, created2.body.id);
  const sid2 = created2.body.id;
  const events2 = [];
  openSSE(`/v1/sessions/${sid2}/events`, events2);
  await sleep(300);
  const ref6 = newRef();
  await post(`/v1/sessions/${sid2}/prompt`, { text: "秘密词是什么？只回复这个词，不要解释。", clientRef: ref6 });
  const rte = await waitForEvent(events2, "turn_end", (e) => e.clientRef === ref6 && !e.isError, 120_000);
  const rText = textOf(events2, rte.turnSeq);
  check("resume reconstructs context (banana)", /banana/i.test(rText), JSON.stringify(rText.slice(0, 40)));
  await del(`/v1/sessions/${sid2}`);

  // 8. token 鉴权：BRIDGE_TOKEN 非空时业务路由 401，health 保持开放，带 token 放行
  const tokenChild = spawn(process.execPath, [SRC], {
    env: { ...process.env, BRIDGE_PORT: "0", BRIDGE_HOST: "127.0.0.1", BRIDGE_TOKEN: "sekrit" },
    stdio: ["ignore", "pipe", "inherit"],
  });
  let tokenBase = null;
  tokenChild.stdout.on("data", (d) => {
    const m = d.toString().match(/listening on http:\/\/[^:]+:(\d+)/);
    if (m) tokenBase = `http://127.0.0.1:${m[1]}`;
  });
  await waitFor(() => tokenBase !== null, "token bridge up", 30_000);
  const tokHealth = await fetch(tokenBase + "/v1/health");
  check("token bridge health open (no token)", tokHealth.status === 200);
  const tokPost = await fetch(tokenBase + "/v1/sessions", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ cwd: TEST_CWD }),
  });
  check("create without token -> 401", tokPost.status === 401);
  const tokPostOk = await fetch(tokenBase + "/v1/sessions", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: "Bearer sekrit" },
    body: JSON.stringify({ cwd: TEST_CWD }),
  });
  check("create with token -> 201", tokPostOk.status === 201);
  const tokBad = await fetch(tokenBase + "/v1/sessions", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: "Bearer wrong" },
    body: JSON.stringify({ cwd: TEST_CWD }),
  });
  check("create with wrong token -> 401", tokBad.status === 401);
  if (tokPostOk.status === 201) await del(`/v1/sessions/${tokPostOk.body.id}`);
  tokenChild.kill();
  await sleep(200);
} catch (e) {
  console.log(`\n  SELF-TEST ERROR: ${e?.message ?? e}`);
  results.push({ name: `uncaught: ${e?.message}`, ok: false });
} finally {
  clearTimeout(watchdog);
  child.kill();
  await sleep(300);
}

const failed = results.filter((r) => !r.ok).length;
console.log(`\n=== bridge self-test: ${results.length - failed}/${results.length} passed ===`);
process.exit(failed ? 1 : 0);
