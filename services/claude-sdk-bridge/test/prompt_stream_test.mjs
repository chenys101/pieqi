// test/prompt_stream_test.mjs —— 验证 promptStream 到底把什么交给了 SDK。
//
// ★ 这是本特性最容易被漏掉的一段：前面的 image_blocks_test.mjs 只证明
//   buildImageBlocks() 返回的块**形状**对；但块是否真的被 yield 给 SDK 的
//   query({prompt: ...})，取决于 promptStream 有没有用它。
//   只测前者会出现"块造得漂漂亮亮、却仍在 yield item.text"的假绿灯 ——
//   那正是本次要修的原始缺陷（旧代码就是 content: item.text）。
//
// 做法：不起真 SDK（沙箱内 Node spawn 子进程会 EPERM，且也没必要）。
//   直接 new 一个 SessionRuntime 会调 _start() 去 spawn —— 所以这里不 new，
//   而是构造一个**最小假对象**，把 promptStream 挂上去调用（它只用到
//   this.closed / _nextPrompt / currentClientRef / turnInFlight）。
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
function eqJson(name, got, want) {
  const a = JSON.stringify(got), b = JSON.stringify(want);
  check(name, a === b, `got ${a}, want ${b}`);
}

/**
 * 造一个只够跑 promptStream 的假 runtime：
 * 直接复用 SessionRuntime.prototype.promptStream，但绕开 constructor（它会 spawn SDK）。
 */
function streamFor(items) {
  const fake = {
    closed: false,
    currentClientRef: null,
    turnInFlight: false,
    _items: [...items],
    _nextPrompt() {
      return Promise.resolve(this._items.shift());
    },
  };
  return SessionRuntime.prototype.promptStream.call(fake);
}

async function collect(gen) {
  const out = [];
  for await (const msg of gen) out.push(msg);
  return out;
}

const B64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg==";

console.log("promptStream: 纯文本轮 —— content 仍是字符串（不改动既有路径）");
{
  const msgs = await collect(streamFor([{ text: "你好", ref: "r1", blocks: null }]));
  check("yield 了 1 条", msgs.length === 1, JSON.stringify(msgs));
  eqJson("形态与改动前一致", msgs[0], {
    type: "user",
    message: { role: "user", content: "你好" },
    parent_tool_use_id: null,
  });
}

console.log("\npromptStream: 带图轮 —— content 必须是**块数组**（本次修复的核心）");
{
  const blocks = [
    { type: "text", text: "这张图里是什么" },
    { type: "image", source: { type: "base64", media_type: "image/png", data: B64 } },
  ];
  const msgs = await collect(streamFor([{ text: "这张图里是什么", ref: "r2", blocks }]));
  check("yield 了 1 条", msgs.length === 1, JSON.stringify(msgs));
  const content = msgs[0]?.message?.content;
  check("content 是数组（不是字符串）", Array.isArray(content), `typeof=${typeof content}`);
  eqJson("content 就是那些块（文本在前、图紧随）", content, blocks);
  check(
    "image block 的 media_type 在 source 里",
    content?.[1]?.source?.media_type === "image/png",
    JSON.stringify(content?.[1]),
  );
  // 这条断言直指原始缺陷：若 promptStream 又退回 item.text，content 会变成字符串，
  // 图被静默丢掉而报文看起来"成功"。
  check("content 不等于纯文本（图没被丢掉）", content !== "这张图里是什么");
}

console.log("\npromptStream: 只发图（blocks 无文本块）");
{
  const blocks = [{ type: "image", source: { type: "base64", media_type: "image/jpeg", data: B64 } }];
  const msgs = await collect(streamFor([{ text: "", ref: "r3", blocks }]));
  const content = msgs[0]?.message?.content;
  check("content 是数组", Array.isArray(content));
  check("只有 1 块且是 image", content?.length === 1 && content[0].type === "image", JSON.stringify(content));
}

console.log("\npromptStream: 多轮按队列顺序处理");
{
  const msgs = await collect(streamFor([
    { text: "第一轮", ref: "r1", blocks: null },
    { text: "第二轮", ref: "r2", blocks: [{ type: "image", source: { type: "base64", media_type: "image/png", data: B64 } }] },
  ]));
  check("yield 了 2 条", msgs.length === 2, JSON.stringify(msgs.map((m) => m.message.content)));
  check("第一条是纯文本字符串", typeof msgs[0].message.content === "string");
  check("第二条是数组", Array.isArray(msgs[1].message.content));
}

console.log("\npromptStream: closed 后立即收尾（不挂死）");
{
  const fake = {
    closed: true,
    currentClientRef: null,
    turnInFlight: false,
    _nextPrompt: () => Promise.resolve({ text: "x", ref: "r", blocks: null }),
  };
  const msgs = await collect(SessionRuntime.prototype.promptStream.call(fake));
  check("closed=true 时不产出任何消息", msgs.length === 0, JSON.stringify(msgs));
}

console.log("\npushPrompt: blocks 必须被原样记住（否则 promptStream 拿不到）");
{
  const fake = {
    closed: false,
    queue: [],
    waiters: [],
    _setState() {},
  };
  const ok = SessionRuntime.prototype.pushPrompt.call(fake, "文本", "ref-1", [{ type: "image" }]);
  check("pushPrompt 返回 true", ok === true);
  check("入队 1 条", fake.queue.length === 1, JSON.stringify(fake.queue));
  check("blocks 被记住", Array.isArray(fake.queue[0].blocks) && fake.queue[0].blocks.length === 1, JSON.stringify(fake.queue[0]));
  check("clientRef 被记住", fake.queue[0].ref === "ref-1");

  // 不带 blocks（纯文本）时应为 null，让 promptStream 走字符串分支
  SessionRuntime.prototype.pushPrompt.call(fake, "纯文本", "ref-2");
  check("不传 blocks 时为 null", fake.queue[1].blocks === null, JSON.stringify(fake.queue[1]));
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
