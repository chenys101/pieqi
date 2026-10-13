// src/images.js —— POST /prompt 的 images 入参校验与 content block 组装。
//
// 为什么单独一个模块：index.js 在 import 时就会 startServer，任何 import 它的测试
// 都会拉起真服务。把纯函数拆出来才能毫秒级单测（同 toolKindOf 的理由）。
//
// ★ 这里的四条规则与 Go 侧 internal/agent/acp.go 的 imageMimeWhitelist /
//   maxPromptImages / maxImageBytes **必须逐字一致**：两条传输（ACP 与桥）对
//   同一个「可发什么图」问题只能有一个答案。任一侧放宽，表现都是
//   "换个 agent 就发不出去了"，而错误信息离根因很远。

/** 与 agent.imageMimeWhitelist 一致。 */
export const IMAGE_MIME_TYPES = new Set([
  "image/png", "image/jpeg", "image/webp", "image/gif",
]);

/** 与 agent.maxPromptImages 一致。 */
export const MAX_PROMPT_IMAGES = 8;

/** 与 agent.maxImageBytes 一致（5 MiB，**解码后**字节数）。 */
export const MAX_IMAGE_BYTES = 5 * 1024 * 1024;

/**
 * 严格 base64 判据（字符集 + padding 位置 + 长度同余），与 Go 侧 decodedBase64Len 等价。
 * 不用 Buffer.from(data,'base64') 直接解：那要为每张图分配一份完整副本，而这里只想
 * 知道"合不合法、多大"。
 */
export function isValidBase64(s) {
  if (typeof s !== "string" || s.length % 4 !== 0) return false;
  let padding = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === "=") {
      if (i < s.length - 2) return false; // padding 只允许出现在末尾
      padding++;
      continue;
    }
    if (padding > 0) return false; // padding 之后不能还有数据
    const ok =
      (c >= "A" && c <= "Z") || (c >= "a" && c <= "z") ||
      (c >= "0" && c <= "9") || c === "+" || c === "/";
    if (!ok) return false;
  }
  return padding <= 2;
}

/** 由 base64 长度推解码后字节数（4 字符 → 3 字节，再减 padding）。 */
export function decodedBase64Length(s) {
  let padding = 0;
  for (let i = s.length - 1; i >= 0 && s[i] === "="; i--) padding++;
  return (s.length / 4) * 3 - padding;
}

/**
 * 把请求里的 images 校验并组装成 SDK 的 content blocks（文本在前、图片紧随）。
 *
 * 返回 { blocks } 或 { error }；images 为空时返回 { blocks: null }（调用方走纯文本路径）。
 *
 * 顺序「文本在前、图片紧随」与 ACP 侧 buildPromptBlocks 的选择一致：人写提示词的
 * 习惯是先说要什么再给素材；反过来会让语境变成"先看到图，再看到问题"。
 */
export function buildImageBlocks(text, images) {
  if (!Array.isArray(images) || images.length === 0) return { blocks: null };
  if (images.length > MAX_PROMPT_IMAGES) {
    return { error: `一轮最多 ${MAX_PROMPT_IMAGES} 张图片（收到 ${images.length} 张）` };
  }
  const blocks = [];
  // 空文本时不放 TextBlock："只发图"是完全合理的用法（让 agent 描述图片），
  // 塞一个空文本块反而可能被对端判为 invalid。
  if (typeof text === "string" && text.trim() !== "") {
    blocks.push({ type: "text", text });
  }
  for (let i = 0; i < images.length; i++) {
    const img = images[i] ?? {};
    const mime = typeof img.mime_type === "string" ? img.mime_type.toLowerCase().trim() : "";
    const data = typeof img.data === "string" ? img.data : "";
    if (!IMAGE_MIME_TYPES.has(mime)) {
      return { error: `第 ${i + 1} 张图片: 不支持的图片类型 "${mime}"（仅支持 image/png、image/jpeg、image/webp、image/gif）` };
    }
    if (data === "") {
      return { error: `第 ${i + 1} 张图片: 图片数据为空` };
    }
    if (data.startsWith("data:")) {
      // 最常见的误用：把 data URL整个塞进来。点明它，别让人对着"图片损坏"猜半天。
      return { error: `第 ${i + 1} 张图片: 图片数据必须是纯 base64，不能带 data:image/...;base64, 前缀` };
    }
    if (!isValidBase64(data)) {
      return { error: `第 ${i + 1} 张图片: 图片数据不是合法的 base64` };
    }
    const decoded = decodedBase64Length(data);
    if (decoded > MAX_IMAGE_BYTES) {
      return { error: `第 ${i + 1} 张图片: 图片过大（${decoded} 字节，上限 ${MAX_IMAGE_BYTES} 字节）` };
    }
    // Anthropic Messages API 的 image block 形态。
    // 注意 media_type 在 source 里（不是顶层），写错位置会让 SDK 收到一个
    // 缺 media_type 的 source —— 那正是本地 schema 会拒绝的形态。
    blocks.push({ type: "image", source: { type: "base64", media_type: mime, data } });
  }
  if (blocks.length === 0) return { error: "prompt 内容为空（既无文本也无图片）" };
  return { blocks };
}
