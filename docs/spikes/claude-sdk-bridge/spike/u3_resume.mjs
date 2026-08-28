// U3 spike：CLI 子进程崩溃后 resume
// 验证点：
//   1. turn1 拿到 session_id
//   2. 杀掉 CLI 子进程（模拟崩溃）后，原 query 流如何收尾（Error / error result / 挂住）
//   3. 新 query 用 resume:session_id 重建上下文（秘密词记忆），证明崩溃后可恢复
import { query } from "@anthropic-ai/claude-agent-sdk";
import { mkdirSync } from "node:fs";
import { execSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const cwd = path.join(__dirname, "..", ".spike-cwd");
mkdirSync(cwd, { recursive: true });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const waitFor = async (fn, timeoutMs = 90_000, label = "") => {
  const t0 = Date.now();
  while (!fn()) {
    if (Date.now() - t0 > timeoutMs) throw new Error(`waitFor timeout: ${label}`);
    await sleep(50);
  }
};

function childPids(pid) {
  const out = execSync(
    `powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { $_.ParentProcessId -eq ${pid} } | Select-Object -ExpandProperty ProcessId"`,
    { encoding: "utf8" }
  );
  return out.trim().split(/\s+/).filter(Boolean).map(Number);
}
function killTree(pid) {
  execSync(`taskkill /PID ${pid} /T /F`, { encoding: "utf8" });
}

const queue = [];
const waiters = [];
function push(text) {
  queue.push(text);
  if (waiters.length) waiters.shift()(queue.shift());
}
function next() {
  return queue.length ? Promise.resolve(queue.shift()) : new Promise((res) => waiters.push(res));
}
let open = true;
async function* gen() {
  while (open) {
    const text = await next();
    yield { type: "user", message: { role: "user", content: text }, parent_tool_use_id: null };
  }
}

const watchdog = setTimeout(() => {
  console.log("[U3] TIMEOUT");
  try { q.close(); } catch {}
  process.exit(2);
}, 240_000);

const q = query({
  prompt: gen(),
  options: {
    cwd,
    permissionMode: "bypassPermissions",
    allowDangerouslySkipPermissions: true,
    includePartialMessages: true,
    maxTurns: 5,
  },
});

const results = [];
let curText = "";
let sessionId = null;
let streamResult = null; // {ended:true} | {error} | null(未收尾)
let streamDoneResolve;
const streamDone = new Promise((r) => (streamDoneResolve = r));

(async () => {
  try {
    for await (const msg of q) {
      if (msg.type === "stream_event") {
        const e = msg.event;
        if (e.type === "content_block_delta" && e.delta?.type === "text_delta") curText += e.delta.text;
      } else if (msg.type === "assistant") {
        for (const block of msg.message?.content ?? []) {
          if (block.type === "text" && block.text) curText += block.text;
        }
      } else if (msg.type === "result") {
        results.push({ subtype: msg.subtype, is_error: msg.is_error, text: curText });
        sessionId = msg.session_id;
        console.log(`[U3] turn#${results.length - 1} result subtype=${msg.subtype} is_error=${msg.is_error}`);
        curText = "";
      }
    }
    streamResult = { ended: true };
  } catch (e) {
    streamResult = { error: e?.message ?? String(e) };
  }
  streamDoneResolve();
})();

console.log(`[U3] node pid=${process.pid}`);
push("记住一个秘密词：banana。只回复 ok 两个字，不要解释。");

try {
  await waitFor(() => results.length >= 1, 90_000, "turn1 result");
  console.log(`[U3] turn1 text: ${JSON.stringify(results[0].text)}`);
  console.log(`[U3] session_id=${sessionId}`);

  // 模拟崩溃：杀掉 CLI 子进程树
  const kids = childPids(process.pid);
  console.log(`[U3] direct children of node: [${kids.join(",")}]`);
  if (!kids.length) throw new Error("no child process found to kill");
  for (const k of kids) {
    console.log(`[U3] killing child tree ${k}`);
    try { killTree(k); } catch (e) { console.log(`[U3] kill ${k}: ${e.message}`); }
  }

  // 观察原流收尾（最多等 15s；挂住本身也是发现）
  await Promise.race([streamDone, sleep(15_000)]);
  console.log(`[U3] original stream after kill: ${JSON.stringify(streamResult)}`);
} catch (e) {
  console.log(`[U3] main error: ${e.message}`);
}

open = false;
try { q.close(); } catch {}
clearTimeout(watchdog);

// --- resume 重建上下文 ---
console.log("\n[U3] resuming session...");
let resumeText = "";
let resumeError = null;
try {
  const rq = query({
    prompt: "秘密词是什么？只回复这个词，不要解释。",
    options: { cwd, resume: sessionId, permissionMode: "bypassPermissions", allowDangerouslySkipPermissions: true, includePartialMessages: true, maxTurns: 3 },
  });
  for await (const msg of rq) {
    if (msg.type === "stream_event" && msg.event.type === "content_block_delta" && msg.event.delta?.type === "text_delta") {
      resumeText += msg.event.delta.text;
    } else if (msg.type === "assistant") {
      for (const block of msg.message?.content ?? []) {
        if (block.type === "text" && block.text) resumeText += block.text;
      }
    }
  }
  rq.close();
} catch (e) {
  resumeError = e?.message ?? String(e);
}

console.log("\n=== U3 结果 ===");
console.log(`original stream outcome: ${JSON.stringify(streamResult)}`);
console.log(`resume error: ${resumeError ?? "none"}`);
console.log(`resumed answer: ${JSON.stringify(resumeText)}`);

const resumeOk = !!sessionId && !resumeError && /banana/i.test(resumeText);
console.log(`context reconstructed after crash (banana): ${resumeOk}`);
console.log(`\nU3 ${resumeOk ? "PASS" : "FAIL"}`);
process.exit(resumeOk ? 0 : 1);
