// test/image_blocks_test.mjs — POST /prompt 的 images 校验与 content block 组装的纯单元测试。
//
// 不需要 SDK、不起桥（index.js 在 import 时就会 startServer，所以逻辑拆到了 src/images.js）。
// 同 tool_kind_test.mjs 的理由：让"改一行图片规则"能毫秒级回归，而不是去跑 5 分钟端到端。
//
// 被它守住的缺陷：桥的 /prompt 此前只收 text（session.js 的 content: item.text），
// 图片在 Go→桥这一层就被丢掉，claude agent 因此恒被判定为"不支持收图"。
import {
  IMAGE_MIME_TYPES,
  MAX_PROMPT_IMAGES,
  MAX_IMAGE_BYTES,
  buildImageBlocks,
  decodedBase64Length,
  isValidBase64,
} from "../src/images.js";

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
function eqJson(name, got, want) {
  const a = JSON.stringify(got), b = JSON.stringify(want);
  check(name, a === b, `got ${a}, want ${b}`);
}

// 8 字节的合法 base64（"AQIDBAUGBwg=" 解出 1..8）
const B64_8 = "AQIDBAUGBwg=";

console.log("buildImageBlocks: 纯文本轮（不产生 blocks，调用方走原路径）");
eq("images 为空数组 → blocks 为 null", buildImageBlocks("hi", []).blocks, null);
eq("images 未传 → blocks 为 null", buildImageBlocks("hi", undefined).blocks, null);

console.log("\nbuildImageBlocks: 带图轮的块序与形态（决定性契约）");
{
  const r = buildImageBlocks("这张图里是什么", [{ data: B64_8, mime_type: "image/png" }]);
  eq("无错误", r.error, undefined);
  eq("块数 = 1 文本 + 1 图", r.blocks.length, 2);
  eqJson("第 1 块是文本（文本在前）", r.blocks[0], { type: "text", text: "这张图里是什么" });
  // source.media_type 的位置是最容易写错的地方：写到顶层会让 SDK 收到缺 media_type 的
  // source，而本地 schema 恰恰会拒这种形态。
  eqJson("第 2 块是 image block，media_type 在 source 里", r.blocks[1], {
    type: "image",
    source: { type: "base64", media_type: "image/png", data: B64_8 },
  });
}

console.log("\nbuildImageBlocks: 只发图（空文本）不塞空 TextBlock");
{
  const r = buildImageBlocks("   ", [{ data: B64_8, mime_type: "image/png" }]);
  eq("无错误", r.error, undefined);
  eq("只有 1 块（不产生空文本块）", r.blocks.length, 1);
  eq("那块是 image", r.blocks[0].type, "image");
}

console.log("\nbuildImageBlocks: 多图保持顺序");
{
  const r = buildImageBlocks("看图", [
    { data: B64_8, mime_type: "image/jpeg" },
    { data: B64_8, mime_type: "image/webp" },
  ]);
  eq("块数 = 1 + 2", r.blocks.length, 3);
  eq("图 1 是 jpeg", r.blocks[1].source.media_type, "image/jpeg");
  eq("图 2 是 webp", r.blocks[2].source.media_type, "image/webp");
}

console.log("\nbuildImageBlocks: 非法输入必须被拒（且错误信息点明原因）");
{
  const tooMany = Array.from({ length: MAX_PROMPT_IMAGES + 1 }, () => ({ data: B64_8, mime_type: "image/png" }));
  check("超张数被拒", !!buildImageBlocks("x", tooMany).error);
  eq("超张数无 blocks", buildImageBlocks("x", tooMany).blocks, undefined);

  const cases = [
    ["空 data", { data: "", mime_type: "image/png" }, "图片数据为空"],
    ["非法 base64", { data: "not-base64!", mime_type: "image/png" }, "不是合法的 base64"],
    ["data URL 前缀", { data: "data:image/png;base64," + B64_8, mime_type: "image/png" }, "纯 base64"],
    ["不支持的 MIME", { data: B64_8, mime_type: "image/bmp" }, "不支持的图片类型"],
    ["空 MIME", { data: B64_8, mime_type: "" }, "不支持的图片类型"],
  ];
  for (const [name, img, wantSub] of cases) {
    const r = buildImageBlocks("x", [img]);
    check(`${name} 被拒`, !!r.error, JSON.stringify(r));
    check(`${name} 错误信息含「${wantSub}」`, (r.error ?? "").includes(wantSub), r.error);
  }
}

console.log("\nbuildImageBlocks: 大小上限按**解码后**字节数判（与 Go 侧一致）");
{
  // 构造刚好超限：解码后 MAX_IMAGE_BYTES+3 字节
  const overLen = Math.ceil((MAX_IMAGE_BYTES + 3) / 3) * 4;
  const over = "A".repeat(overLen);
  const r = buildImageBlocks("x", [{ data: over, mime_type: "image/png" }]);
  check("超限被拒", !!r.error, JSON.stringify(r).slice(0, 120));
  check("错误信息说图片过大", (r.error ?? "").includes("过大"), r.error);
  // 正好不小于上限要放行（边界不能提前一格拒绝）。
  // ⚠️ base64 长度必是 4 的倍数 ⇒ 解码长度只能是 3 的倍数，而 5 MiB=5242880 不是 3 的倍数，
  // 所以"正好等于上限"这个字节数在 base64 里**取不到**。测试要落在真正可达的边界上：
  // 取不超过上限的最大 3 的倍数（5242878），它必须是放行的。
  const largestOk = Math.floor(MAX_IMAGE_BYTES / 3) * 3; // 5242878
  const okLen = (largestOk / 3) * 4;
  eq("边界值解码长度 = 不超过上限的最大 3 的倍数", decodedBase64Length("A".repeat(okLen)), largestOk);
  check("该值确实 ≤ 上限", largestOk <= MAX_IMAGE_BYTES);
  const ok = buildImageBlocks("x", [{ data: "A".repeat(okLen), mime_type: "image/png" }]);
  eq("贴着上限的合法图放行", ok.error, undefined);
  // 再往上跨一个 4 字符块就必须被拒（证明上限真的在起作用，不是永真）
  const overEdge = buildImageBlocks("x", [{ data: "A".repeat(okLen + 4), mime_type: "image/png" }]);
  check("再大一个块就被拒", !!overEdge.error, JSON.stringify(overEdge).slice(0, 120));
}

console.log("\n常量与 Go 侧逐字一致（跨进程契约：任一侧改了另一侧不改，表现就是「换个 agent 就发不出图」）");
eq("MIME 白名单 4 种", IMAGE_MIME_TYPES.size, 4);
for (const m of ["image/png", "image/jpeg", "image/webp", "image/gif"]) {
  check(`白名单含 ${m}`, IMAGE_MIME_TYPES.has(m));
}
eq("张数上限 8", MAX_PROMPT_IMAGES, 8);
eq("单张上限 5 MiB", MAX_IMAGE_BYTES, 5 * 1024 * 1024);

console.log("\nisValidBase64 / decodedBase64Length 边界");
eq("解码长度：4 字符无 padding → 3", decodedBase64Length("AAAA"), 3);
eq("解码长度：4 字符 1 个 padding → 2", decodedBase64Length("AAA="), 2);
eq("解码长度：4 字符 2 个 padding → 1", decodedBase64Length("AA=="), 1);
check("长度不是 4 的倍数 → 非法", !isValidBase64("AAA"));
check("padding 在中间 → 非法", !isValidBase64("AA=A"));
check("非法字符 → 非法", !isValidBase64("AA*A"));
check("3 个 padding → 非法", !isValidBase64("A==="));

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
