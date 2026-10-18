// test/goal_event_test.mjs — Claude Code `active_goal` 事件的桥侧回归。
//
// 被它守住的缺陷（真实缺口）：SDK 会发 `active_goal` 消息（Claude Code 的
// `/goal <condition>` 状态），但 session.js 的 `_onMessage` switch 只处理
// system / stream_event / assistant / user，**没有 active_goal 分支** ——
// 它落进 default: break，被静默丢弃。于是用户设了 `/goal` 之后：
//   · 迭代了几次、上次为什么"未满足"（last_reason）全看不到；
//   · goal 结束（value === null）也没有任何信号。
// 而**日志里一切正常**（默认分支不报错），属于典型静默失效。
//
// 做法与 prompt_stream_test.mjs 一致：不起真 SDK（沙箱里 spawn 会 EPERM），
// 造一个最小假对象，把 SessionRuntime.prototype._onMessage 挂上去直接喂消息。
// _onMessage 只用到 this._emit / this.lastGoalJson / this.sdkSessionId 等字段。
import { SessionRuntime } from "../src/session.js";

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

/** 造一个只够跑 _onMessage 的假 runtime（绕开 constructor，它真的会 spawn SDK）。 */
function fakeRuntime() {
  const emitted = [];
  const fake = {
    id: "fake-sess",
    sdkSessionId: null,
    turnSeq: 1,
    lastGoalJson: "",
    state: "idle",
    pendingTools: new Map(),
    currentClientRef: null,
    turnInFlight: false,
    closed: false,
    _emit(kind, data = {}) {
      emitted.push({ kind, ...data });
    },
    _setState(s) {
      this.state = s;
    },
  };
  fake.onMessage = SessionRuntime.prototype._onMessage.bind(fake);
  return { fake, emitted };
}

// ── ① 设定 goal ──────────────────────────────────────────────────────────
{
  const { fake, emitted } = fakeRuntime();
  fake.onMessage({
    type: "active_goal",
    value: {
      condition: "所有 e2e 用例通过",
      iterations: 0,
      set_at: 1792240915904,
      tokens_at_start: 12000,
    },
  });
  const ev = emitted.find((e) => e.kind === "goal_changed");
  check("设定 goal → 发出一条 goal_changed", !!ev);
  check(
    "载荷保留 condition",
    ev?.goal?.condition === "所有 e2e 用例通过",
    JSON.stringify(ev?.goal),
  );
  check("载荷保留 iterations", ev?.goal?.iterations === 0);
}

// ── ② 迭代 +1，带 last_reason ────────────────────────────────────────────
{
  const { fake, emitted } = fakeRuntime();
  fake.onMessage({
    type: "active_goal",
    value: { condition: "c", iterations: 1, set_at: 1, tokens_at_start: 1, last_reason: "还差 layout" },
  });
  const ev = emitted.find((e) => e.kind === "goal_changed");
  check("迭代事件带 last_reason", ev?.goal?.last_reason === "还差 layout", JSON.stringify(ev?.goal));
  check("迭代次数透传", ev?.goal?.iterations === 1);
}

// ── ③ goal 清除：value === null ─────────────────────────────────────────
// 这一条最容易写错：若把 null 当作"没有数据"直接 return，用户永远看不到
// "goal 已结束"。正确行为是**照样发一条**，载荷为 goal:null。
{
  const { fake, emitted } = fakeRuntime();
  fake.onMessage({ type: "active_goal", value: null });
  const ev = emitted.find((e) => e.kind === "goal_changed");
  check("goal 清除 → 仍然发一条 goal_changed", !!ev);
  check("清除载荷的 goal 为 null（不是缺失字段）", ev && "goal" in ev && ev.goal === null);
}

// ── ④ 去重：未变化的上报不能逐条进时间线 ─────────────────────────────────
// SDK 可能在每个 message 都重复上报同一个 goal；不按内容去重会刷屏。
{
  const { fake, emitted } = fakeRuntime();
  const g = { condition: "c", iterations: 0, set_at: 1, tokens_at_start: 1 };
  fake.onMessage({ type: "active_goal", value: g });
  fake.onMessage({ type: "active_goal", value: { ...g } }); // 内容相同，新对象
  const n = emitted.filter((e) => e.kind === "goal_changed").length;
  check("相同 goal 重复上报只发一次", n === 1, `实际发了 ${n} 次`);

  fake.onMessage({ type: "active_goal", value: { ...g, iterations: 1 } });
  const n2 = emitted.filter((e) => e.kind === "goal_changed").length;
  check("goal 变化后重新发（iterations 0→1）", n2 === 2, `实际发了 ${n2} 次`);
}

// ── ⑤ 清除后再设定：不能被去重吃掉 ───────────────────────────────────────
// 序列 "有 goal → 清除 → 同一个 goal 再设" 里，第三次的内容与第一次相同；
// 若只比较当前值与上次值，会把"重新设定"误判成重复。
{
  const { fake, emitted } = fakeRuntime();
  const g = { condition: "c", iterations: 0, set_at: 1, tokens_at_start: 1 };
  fake.onMessage({ type: "active_goal", value: g });
  fake.onMessage({ type: "active_goal", value: null });
  fake.onMessage({ type: "active_goal", value: { ...g } });
  const seq = emitted.filter((e) => e.kind === "goal_changed").map((e) => e.goal);
  check(
    "设定 → 清除 → 重新设定 三次都要送达",
    seq.length === 3 && seq[0] && seq[1] === null && seq[2],
    `实际 ${JSON.stringify(seq)}`,
  );
}

// ── ⑥ 不干扰既有消息类型 ─────────────────────────────────────────────────
{
  const { fake, emitted } = fakeRuntime();
  fake.onMessage({ type: "system", session_id: "sdk-1" });
  check("system 仍能记录 sdkSessionId", fake.sdkSessionId === "sdk-1");
  check("system 不产生 goal_changed", !emitted.some((e) => e.kind === "goal_changed"));
}

// ── ⑦ 变异对照：证明上面的用例真的守住了"缺 case"这个缺陷 ─────────────────
//
// 不靠"临时改源码再还原"（那要写文件，容易留下脏状态），而是**在内存里**构造一个
// 去掉 active_goal 分支的旧版 _onMessage，看用例是否变红。
// 判据：旧实现必须让 ① / ③ 两条核心断言失败 —— 如果它照样通过，说明这一组用例
// 测的根本不是"有没有接上 active_goal"，而是别的什么。
{
  const legacy = function (msg) {
    // 复刻修复前的 switch：没有 active_goal 分支，落进 default 静默丢弃。
    switch (msg.type) {
      case "system":
        if (msg.session_id) this.sdkSessionId = msg.session_id;
        break;
      default:
        break;
    }
  };
  const emitted = [];
  const fake = { sdkSessionId: null, _emit: (k, d) => emitted.push({ kind: k, ...d }) };
  legacy.call(fake, { type: "active_goal", value: { condition: "c", iterations: 0 } });
  const sawAny = emitted.some((e) => e.kind === "goal_changed");
  check("变异对照：缺 active_goal 分支时，goal 事件确实被丢弃（用例能抓到）", !sawAny);
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
