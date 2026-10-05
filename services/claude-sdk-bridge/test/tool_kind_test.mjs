// test/tool_kind_test.mjs — ToolKind 映射的纯单元测试（不需要 SDK / 不起桥）
//
// 为什么单独一个文件：bridge_self_test.mjs 是端到端自测，要拉起真实 Agent SDK 会话、
// 有 5 分钟 watchdog，不适合当"改一行映射就跑一次"的回归。这个文件只 import
// src/session.js 里的导出纯函数，毫秒级跑完。
//
// 被它守住的缺陷：桥此前对所有工具恒发 toolKind:""，而 pieqi 的免审名单（L0/L1 自动
// 放行）与审批卡风险分级都按 ToolKind 匹配 —— 空 kind 经 RiskOfKind 兜底落 L2，
// 于是连 Edit 都要人工点，卡片还一律显示成"执行命令"。
import { toolKindOf } from "../src/session.js";

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
function eq(name, got, want) {
  check(name, got === want, `got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
}

// pieqi 侧 riskLevelKinds 认识的 kind：read/search/fetch/think(L0)、edit/move(L1)、
// execute/switch_mode/other(L2)、delete(L3)。映射值必须落在这个集合里，
// 否则等于发了个 pieqi 不认识的 kind（会被当 L2，静默降级成"必须人工审批"）。
const KNOWN_KINDS = new Set([
  "read", "search", "fetch", "think",
  "edit", "move",
  "execute", "switch_mode", "other",
  "delete",
]);

console.log("toolKindOf: 变更类工具");
eq("Edit → edit（L1 免审）", toolKindOf("Edit"), "edit");
eq("Write → edit", toolKindOf("Write"), "edit");
eq("MultiEdit → edit", toolKindOf("MultiEdit"), "edit");
eq("NotebookEdit → edit", toolKindOf("NotebookEdit"), "edit");
eq("Delete → delete（L3 强制人工）", toolKindOf("Delete"), "delete");
eq("Move → move", toolKindOf("Move"), "move");
eq("Rename → move", toolKindOf("Rename"), "move");

console.log("toolKindOf: 命令执行");
eq("Bash → execute", toolKindOf("Bash"), "execute");
eq("PowerShell → execute", toolKindOf("PowerShell"), "execute");

console.log("toolKindOf: 只读/派生");
eq("Read → read", toolKindOf("Read"), "read");
eq("Grep → search", toolKindOf("Grep"), "search");
eq("Glob → search", toolKindOf("Glob"), "search");
eq("WebFetch → fetch", toolKindOf("WebFetch"), "fetch");
eq("WebSearch → fetch", toolKindOf("WebSearch"), "fetch");
eq("Task → think", toolKindOf("Task"), "think");
eq("Agent → think", toolKindOf("Agent"), "think");

console.log("toolKindOf: 未知工具");
// 未知工具必须返回 ""（→ pieqi 侧 RiskOfKind 兜底 L2），**不能**猜成 read：
// 猜成 read 等于让一个没枚举过的工具静默通过。
eq("未知工具 → 空串", toolKindOf("SomeBrandNewTool"), "");
eq("空名 → 空串", toolKindOf(""), "");
eq("undefined → 空串", toolKindOf(undefined), "");

console.log("映射值必须都是 pieqi 认识的 kind");
for (const name of [
  "Edit", "Write", "MultiEdit", "NotebookEdit", "Delete", "Move", "Rename",
  "Bash", "PowerShell", "Read", "Grep", "Glob", "WebFetch", "WebSearch",
  "Task", "Agent",
]) {
  const k = toolKindOf(name);
  check(`${name} → ${JSON.stringify(k)} 在已知集合内`, KNOWN_KINDS.has(k));
}

// AUTO_ALLOW_TOOLS 里的工具（桥内直接放行、不发 permission_needed）如果**同时**
// 被映射成 L1/L0，说明两处判据打架：桥放行了一批，pieqi 又以为要审。这里只做提示，
// 不作为失败 —— 桥内放行是"更早放行"，语义上不冲突，但值得知道重叠范围。
console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
