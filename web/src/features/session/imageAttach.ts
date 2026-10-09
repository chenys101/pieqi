// 图片附件：本地选图 → 校验/压缩 → base64（纯 base64，不带 data: 前缀）。
//
// 三条纪律（都来自后端的硬约束，见 internal/agent/acp.go 的 validateImage）：
//   1. **只接受四种 MIME**（png/jpeg/webp/gif）—— 别的类型 agent 会拒收，
//      与其发出去再拿到一句协议级 invalid，不如选图时就拦掉。
//   2. **发出去的是纯 base64**，不是 data URL。data URL 是浏览器读文件最顺手的
//      形态（FileReader.readAsDataURL），而这个前缀恰恰是后端专门写了一条错误
//      来拦的误用 —— 所以在这里就剥掉。
//   3. **压缩在客户端做**：后端上限是解码后 5MB，而手机原图动辄 4~8MB。
//      不压缩的话"选了张照片就发不出去"，而用户完全不知道为什么。

/** 后端认可的图片类型（与 agent.imageMimeWhitelist 一一对应）。 */
export const ALLOWED_IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/webp', 'image/gif'] as const

/** 一轮最多几张（与后端 maxPromptImages 一致）。 */
export const MAX_IMAGES = 8

/** 单张解码后上限（与后端 maxImageBytes 一致，5 MiB）。 */
export const MAX_IMAGE_BYTES = 5 * 1024 * 1024

/**
 * 压缩的触发阈值：超过它就重编码。
 *
 * 取 1MB 而不是等到上限 5MB：留出余量给"压缩后仍偏大"的情况，
 * 也避免每次都动大图（重编码是有代价的，能不动就不动）。
 */
const COMPRESS_THRESHOLD = 1024 * 1024

/** 压缩后的最长边（px）。1536 是视觉模型常用的输入边长，再大基本是浪费 token。 */
const COMPRESS_MAX_EDGE = 1536

/** 压缩后的 JPEG 质量。0.85 在肉眼与体积之间比较平衡。 */
const COMPRESS_QUALITY = 0.85

/** 一张待发图片：预览用 dataUrl（本地），发送用 base64（不含前缀）。 */
export interface PendingImage {
  /** 本地唯一 id（列表 key 与删除用；不用文件名，重名会串） */
  id: string
  /** 预览用（data URL，仅存在于浏览器内存，绝不发给后端） */
  previewUrl: string
  /** 发送用：**纯 base64** */
  data: string
  /** MIME 类型 */
  mimeType: string
  /** 解码后字节数（展示与上限判定） */
  bytes: number
  /** 原始文件名（仅供展示） */
  name: string
}

/** 选图/压缩的结果：成功给图片，失败给可读原因（给用户看，不是给日志看）。 */
export type ImageLoadResult = { ok: true; image: PendingImage } | { ok: false; error: string }

/** 人类可读的文件大小 */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

/**
 * 把 File 读成一张可发送的图片：校验类型 → （必要时）压缩 → 输出纯 base64。
 *
 * 失败一律返回可读原因而不是抛异常：调用方（输入框）要把它显示给用户，
 * 而"选了张 HEIC 照片"这种失败是完全正常的用户操作，不该走异常通道。
 */
export async function loadImageFile(file: File): Promise<ImageLoadResult> {
  const type = (file.type || '').toLowerCase()
  if (!(ALLOWED_IMAGE_TYPES as readonly string[]).includes(type)) {
    return {
      ok: false,
      error: `不支持 ${type || file.name} 格式，仅支持 PNG / JPEG / WebP / GIF`,
    }
  }

  let blob: Blob = file
  // 小图直接用原文件；大图先压（见 COMPRESS_THRESHOLD 的理由）。
  if (file.size > COMPRESS_THRESHOLD) {
    const compressed = await compressImage(file)
    // 压缩失败不阻断：退回原图，让后端的尺寸校验给出结论（它才是权威）。
    if (compressed) blob = compressed
  }

  const buf = await readArrayBuffer(blob)
  if (buf.byteLength > MAX_IMAGE_BYTES) {
    return {
      ok: false,
      error: `图片过大（${formatBytes(buf.byteLength)}），上限 ${formatBytes(MAX_IMAGE_BYTES)}`,
    }
  }

  const mimeType = (blob.type || type).toLowerCase()
  return {
    ok: true,
    image: {
      id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      previewUrl: `data:${mimeType};base64,${toBase64(buf)}`,
      data: toBase64(buf),
      mimeType,
      bytes: buf.byteLength,
      name: file.name,
    },
  }
}

/**
 * 用 canvas 把图缩到 COMPRESS_MAX_EDGE 以内并重编码为 JPEG。
 *
 * 只在"确实需要"时调用（超过阈值）。失败返回 null 让调用方退回原图 ——
 * 某些浏览器/格式组合下 canvas 会抛（如 GIF 动图、超大尺寸触发内存上限），
 * 而那不该让整个选择操作失败。
 *
 * 输出 JPEG 而不是保留原格式：PNG 重编码往往**更大**（照片类内容尤其），
 * 而这里的唯一目的就是压体积。
 */
async function compressImage(file: File): Promise<Blob | null> {
  try {
    const bitmap = await createImageBitmap(file)
    const scale = Math.min(1, COMPRESS_MAX_EDGE / Math.max(bitmap.width, bitmap.height))
    const w = Math.max(1, Math.round(bitmap.width * scale))
    const h = Math.max(1, Math.round(bitmap.height * scale))

    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.drawImage(bitmap, 0, 0, w, h)
    // 释放解码后的位图（大图不解绑会一直占着内存直到 GC）
    bitmap.close?.()

    return await new Promise<Blob | null>((resolve) => {
      canvas.toBlob((b) => resolve(b), 'image/jpeg', COMPRESS_QUALITY)
    })
  } catch {
    return null
  }
}

/**
 * Blob/File → ArrayBuffer，兼容没有 `Blob.arrayBuffer()` 的环境。
 *
 * 为什么不能直接 `await blob.arrayBuffer()`：它对**旧 Safari（<14）与 jsdom**
 * 都不存在。前者是真机用户会踩的（那一代 iPhone 上的 PWA 恰好是本项目的主要
 * 使用场景之一），后者会让单测整片报 "blob.arrayBuffer is not a function" ——
 * 一个纯环境差异伪装成功能坏了。FileReader 到处都有，用它兜底。
 */
export function readArrayBuffer(blob: Blob): Promise<ArrayBuffer> {
  if (typeof blob.arrayBuffer === 'function') return blob.arrayBuffer()
  return new Promise((resolve, reject) => {
    const fr = new FileReader()
    fr.onload = () => resolve(fr.result as ArrayBuffer)
    fr.onerror = () => reject(fr.error ?? new Error('读取图片失败'))
    fr.readAsArrayBuffer(blob)
  })
}

/**
 * ArrayBuffer → 纯 base64（**不带** data: 前缀）。
 *
 * 不用 FileReader.readAsDataURL：它给的是 data URL，得再手工剥前缀；
 * 而剥前缀这步正是最容易漏的地方（漏了后端会明确报错，见 agent.validateImage）。
 * 分块转换是为了避免 String.fromCharCode(...bigArray) 的参数个数上限
 * （几 MB 的图会直接 RangeError: too many arguments）。
 */
export function toBase64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  const chunk = 0x8000
  let binary = ''
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}
