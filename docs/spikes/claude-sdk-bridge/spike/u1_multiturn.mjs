// U1 spike：同进程多轮流式 + turn 边界
// 验证点：
//   1. 单条长生命周期 input async generator 能否跨多轮（streaming input 保活）
//   2. 靠 result 消息判 turn 边界（每轮恰好一条 result）
//   3. CLI 子进程在两轮之间不退出（同进程，快照进程树对比，带进程名）
//   4. 增量文本（includePartialMessages → stream_event deltas）与完整正文都拿到
//   5. 上下文跨轮保留（秘密词记忆）
import { query } from "@anthropic-ai/claude-agent-sdk";
import { mkdirSync } from "node:fs";
import { execSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const cwd = path.join(__dirname, "..", ".spike-cwd");
mkdirSync(cwd, { recursive: true });

// 进程树快照（带进程名），如 ["36408:claude.exe","38376:node.exe"]
function descendants(pid) {
  const cmd = `powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { $_.ParentProcessId -eq ${pid} } | ForEach-Object { \\"$($_.ProcessId):$($_.Name)\\" }"`;
  let out = "";
  try { out = execSync(cmd, { encoding: "utf8" }); } catch { return []; }
  const kids = out.trim().split(/\r?\n/).filter(Boolean);
  const result = [];
  for (const line of kids) {
    const m = line.match(/^(\d+):(.+)$/);
    if (!m) continue;
    result.push(line);
    result.push(...descendants(parseInt(m[1])));
  }
  return result;
}

// 多轮输入队列
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
  console.log("[U1] TIMEOUT");
  try { q.close(); } catch {}
  process.exit(2);
}, 180_000);

const turns = [];
let curPartial = "";
let curDeltaCount = 0;
let sessionId = null;
const pidSnapshots = {};

const q = query({
  prompt: gen(),
  options: {
    cwd,
    permissionMode: "bypassPermissions",
    allowDangerouslySkipPermissions: true,
    includePartialMessages: true, // 开启增量流式事件
    maxTurns: 10,
  },
});

console.log(`[U1] node pid=${process.pid} cwd=${cwd}`);
push("记住一个秘密词：banana。只回复 ok 两个字，不要解释。");

try {
  for await (const msg of q) {
    if (msg.type === "stream_event") {
      const e = msg.event;
      // 增量文本流（includePartialMessages 开启后才发）
      if (e.type === "content_block_delta" && e.delta?.type === "text_delta") {
        curPartial += e.delta.text;
        curDeltaCount += 1;
      }
    } else if (msg.type === "assistant") {
      // 完整 assistant 消息（按内容块发；作为正文兜底与校验）
      for (const block of msg.message?.content ?? []) {
        if (block.type === "text" && block.text) curPartial += `[assist:${block.text}]`;
      }
    } else if (msg.type === "result") {
      const idx = turns.length;
      turns.push({ idx, subtype: msg.subtype, is_error: msg.is_error, partial: curPartial, deltas: curDeltaCount });
      sessionId = msg.session_id;
      console.log(`[U1] turn#${idx} result subtype=${msg.subtype} is_error=${msg.is_error} delta_events=${curDeltaCount} text_len=${curPartial.length}`);
      curPartial = "";
      curDeltaCount = 0;
      if (idx === 0) {
        pidSnapshots.afterTurn1 = descendants(process.pid);
        console.log(`[U1] after turn1 tree: [${pidSnapshots.afterTurn1.join(" , ")}]`);
        push("秘密词是什么？只回复这个词，不要解释。");
      } else if (idx === 1) {
        pidSnapshots.afterTurn2 = descendants(process.pid);
        console.log(`[U1] after turn2 tree: [${pidSnapshots.afterTurn2.join(" , ")}]`);
        break;
      }
    }
  }
} catch (e) {
  console.log(`[U1] stream error: ${e?.message ?? e}`);
}

open = false;
q.close();
clearTimeout(watchdog);

// --- 汇总判定 ---
console.log("\n=== U1 结果 ===");
console.log(`turns=${turns.length} sessionId=${sessionId}`);
const t1 = turns[0]?.partial ?? "";
const t2 = turns[1]?.partial ?? "";
console.log(`turn1: ${JSON.stringify(t1)}`);
console.log(`turn2: ${JSON.stringify(t2)}`);

// 同进程判定：核心 claude 进程（非 node/临时 helper）在两轮间 PID 是否稳定
const coreOf = (arr) => arr.filter((x) => !/node\.exe$/i.test(x) && !/cmd\.exe$/i.test(x));
const core1 = coreOf(pidSnapshots.afterTurn1 ?? []);
const core2 = coreOf(pidSnapshots.afterTurn2 ?? []);
console.log(`core (non-node) after turn1: [${core1.join(" , ")}]`);
console.log(`core (non-node) after turn2: [${core2.join(" , ")}]`);
const sameProcess = core1.length > 0 && core2.length > 0 && core1.join("") === core2.join("");

console.log(`context preserved (banana in turn2): ${/banana/i.test(t2)}`);
console.log(`incremental deltas received turn0: ${turns[0]?.deltas ?? 0}`);
console.log(`same CLI process across turns: ${sameProcess}`);

const pass =
  turns.length === 2 &&
  turns.every((t) => t.subtype === "success" && !t.is_error) &&
  /banana/i.test(t2) &&
  sameProcess &&
  (turns[0]?.deltas ?? 0) > 0;
console.log(`\nU1 ${pass ? "PASS" : "FAIL"}`);
process.exit(pass ? 0 : 1);
