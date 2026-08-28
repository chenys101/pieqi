// U2 spike：权限挂起与释放
// 验证点：
//   1. canUseTool 返回挂起 Promise 时：进程存活、无新 result 到达（turn 被阻塞）
//   2. 审批 allow（resolve {behavior:'allow'}）后：工具执行、turn 完成
//   3. 挂起干净释放：下一轮纯文本不再触发 canUseTool，不被上一轮挂起阻塞
import { query } from "@anthropic-ai/claude-agent-sdk";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const cwd = path.join(__dirname, "..", ".spike-cwd");
mkdirSync(cwd, { recursive: true });

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

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const waitFor = async (fn, timeoutMs = 60_000, label = "") => {
  const t0 = Date.now();
  while (!fn()) {
    if (Date.now() - t0 > timeoutMs) throw new Error(`waitFor timeout: ${label}`);
    await sleep(50);
  }
};

const pending = new Map(); // requestId|toolUseID -> resolve
const canUseToolCalls = [];
let approvalError = null;

const watchdog = setTimeout(() => {
  console.log("[U2] TIMEOUT");
  try { q.close(); } catch {}
  process.exit(2);
}, 240_000);

const q = query({
  prompt: gen(),
  options: {
    cwd,
    permissionMode: "default",
    tools: { type: "preset", preset: "claude_code" },
    // 显式清空 allow/deny 并强制 shell 工具走 ask 路径（否则可能被默认规则直接放行）
    settings: {
      permissions: {
        allow: [],
        deny: [],
        ask: ["Bash", "PowerShell"],
      },
    },
    includePartialMessages: true,
    maxTurns: 6,
    canUseTool: async (toolName, input, { toolUseID, requestId }) => {
      canUseToolCalls.push({ toolName, toolUseID, requestId });
      console.log(`[U2] canUseTool called: ${toolName} requestId=${requestId} toolUseID=${toolUseID}`);
      return new Promise((resolve) => {
        pending.set(requestId ?? toolUseID, resolve);
      });
    },
  },
});

const results = [];
let curText = "";
let sessionId = null;

// 并发"审批"任务：模拟用户在 IM/PWA 上看到审批卡片后点允许
const approvalTask = (async () => {
  try {
    await waitFor(() => pending.size > 0, 60_000, "wait canUseTool");
    const beforeResolve = results.length;
    console.log("[U2] tool suspended; process alive, waiting 3s to confirm no turn end arrives...");
    await sleep(3000);
    const duringResolve = results.length;
    const blocked = beforeResolve === duringResolve;
    console.log(`[U2] suspension verified (no result during 3s): ${blocked}`);
    // 审批：allow
    for (const resolve of pending.values()) resolve({ behavior: "allow" });
    pending.clear();
    console.log("[U2] approved allow, tool should execute");
  } catch (e) {
    approvalError = e;
  }
})();

push("运行 Bash 命令 `echo SPIKE_OK`，然后告诉我命令输出是什么。");

try {
  for await (const msg of q) {
    if (msg.type === "stream_event") {
      const e = msg.event;
      if (e.type === "content_block_delta" && e.delta?.type === "text_delta") {
        curText += e.delta.text;
      }
    } else if (msg.type === "assistant") {
      for (const block of msg.message?.content ?? []) {
        if (block.type === "text" && block.text) curText += block.text;
        if (block.type === "tool_use") console.log(`[U2] tool_use: ${block.name} id=${block.id}`);
      }
    } else if (msg.type === "system") {
      console.log(`[U2] system msg subtype=${msg.subtype ?? msg.type}`);
    } else if (msg.type === "result") {
      results.push({ subtype: msg.subtype, is_error: msg.is_error, text: curText });
      sessionId = msg.session_id;
      console.log(`[U2] turn#${results.length - 1} result subtype=${msg.subtype} is_error=${msg.is_error} text_len=${curText.length}`);
      curText = "";

      const turnIdx = results.length - 1;
      if (turnIdx === 0) {
        // 下一轮纯文本：验证挂起不泄漏到下一轮
        push("很好。现在只回复：DONE。");
      } else if (turnIdx === 1) {
        break;
      }
    }
  }
} catch (e) {
  console.log(`[U2] stream error: ${e?.message ?? e}`);
}

await approvalTask;
open = false;
q.close();
clearTimeout(watchdog);

// --- 汇总判定 ---
console.log("\n=== U2 结果 ===");
console.log(`canUseTool calls: ${JSON.stringify(canUseToolCalls.map((c) => c.toolName))}`);
console.log(`turn0 text: ${JSON.stringify(results[0]?.text)}`);
console.log(`turn1 text: ${JSON.stringify(results[1]?.text)}`);
console.log(`pending size after all turns: ${pending.size}`);
console.log(`approvalError: ${approvalError?.message ?? "none"}`);

const toolExecuted = /SPIKE_OK/.test(results[0]?.text ?? "");
const nextTurnClean = results[1]?.subtype === "success" && !results[1]?.is_error && /DONE/.test(results[1]?.text ?? "");
const noLeak = pending.size === 0;

const pass =
  !approvalError &&
  canUseToolCalls.length === 1 &&
  toolExecuted &&
  nextTurnClean &&
  noLeak &&
  results.length === 2;
console.log(`\nU2 ${pass ? "PASS" : "FAIL"}`);
process.exit(pass ? 0 : 1);
