// 图片附件逻辑的单测：校验 / base64 形状 / 上限。
//
// ⚠️ 这里测不到"真机能选中图、能显示缩略图"—— 那需要真实浏览器与文件选择器，
// 必须过 web/scripts/self-test.ps1（见 AGENTS.md 的 UI 验收纪律）。
// 本文件守的是**与后端的契约**：发出去的 base64 必须是纯的、类型必须是白名单内的。
import { describe, expect, it } from 'vitest'
import {
  ALLOWED_IMAGE_TYPES,
  MAX_IMAGES,
  MAX_IMAGE_BYTES,
  formatBytes,
  loadImageFile,
  toBase64,
} from './imageAttach'

describe('toBase64', () => {
  it('输出纯 base64，**不带** data: 前缀', () => {
    const buf = new TextEncoder().encode('pieqi').buffer as ArrayBuffer
    const b64 = toBase64(buf)
    expect(b64).toBe('cGllcWk=')
    // 这条是本文件最重要的断言：带前缀会被后端明确拒绝
    // （见 internal/agent/acp.go 的 validateImage 专门为它写了一条错误）。
    expect(b64).not.toContain('data:')
    expect(b64.startsWith('data:')).toBe(false)
  })

  it('能处理超过 String.fromCharCode 参数上限的数据量（分块转换）', () => {
    // 100KB：一次性展开会 RangeError（参数个数上限约 65535）
    const big = new Uint8Array(100 * 1024)
    for (let i = 0; i < big.length; i++) big[i] = i % 256
    const b64 = toBase64(big.buffer)
    expect(b64.length).toBeGreaterThan(0)
    // 反向校验：解码回来必须等长等值（证明分块拼接没丢字节）
    const back = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
    expect(back.length).toBe(big.length)
    expect(back[0]).toBe(big[0])
    expect(back[big.length - 1]).toBe(big[big.length - 1])
  })

  it('空数据得到空串（不抛）', () => {
    expect(toBase64(new ArrayBuffer(0))).toBe('')
  })
})

describe('常量与后端一致', () => {
  // 这几条是**跨进程契约**：前端放行的必须正好是后端认可的集合。
  // 任何一侧改了而另一侧没改，表现都是"选图后发送失败"，且错误信息离根因很远。
  it('类型白名单是后端认可的四种', () => {
    expect([...ALLOWED_IMAGE_TYPES]).toEqual(['image/png', 'image/jpeg', 'image/webp', 'image/gif'])
  })

  it('张数上限与后端 maxPromptImages 一致', () => {
    expect(MAX_IMAGES).toBe(8)
  })

  it('单张上限与后端 maxImageBytes 一致（5 MiB）', () => {
    expect(MAX_IMAGE_BYTES).toBe(5 * 1024 * 1024)
  })
})

describe('formatBytes', () => {
  it('分档显示', () => {
    expect(formatBytes(500)).toBe('500 B')
    expect(formatBytes(2048)).toBe('2 KB')
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MB')
  })
})

describe('loadImageFile', () => {
  it('拒绝白名单外的类型，并给出可读原因', async () => {
    const f = new File([new Uint8Array([1, 2, 3])], 'photo.heic', { type: 'image/heic' })
    const r = await loadImageFile(f)
    expect(r.ok).toBe(false)
    if (!r.ok) {
      expect(r.error).toContain('不支持')
      expect(r.error).toContain('PNG')
    }
  })

  it('空 type 的类型也被拒（不能因为 type 为空就放行）', async () => {
    const f = new File([new Uint8Array([1])], 'x', { type: '' })
    const r = await loadImageFile(f)
    expect(r.ok).toBe(false)
  })

  it('接受的类型产出纯 base64 与正确 mime', async () => {
    // jsdom 的 File.arrayBuffer 可用；blob.type 会保留构造时的 type。
    const f = new File([new Uint8Array([1, 2, 3, 4])], 'a.png', { type: 'image/png' })
    const r = await loadImageFile(f)
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.image.mimeType).toBe('image/png')
      expect(r.image.data).not.toContain('data:')
      expect(r.image.previewUrl.startsWith('data:image/png;base64,')).toBe(true)
      expect(r.image.bytes).toBe(4)
      expect(r.image.id).toBeTruthy()
    }
  })

  it('小文件不触发压缩路径（原样输出，字节数不变）', async () => {
    // 小于压缩阈值：应直接读原文件。jsdom 没有 createImageBitmap，
    // 若代码在小文件上也走压缩，这里会因压缩失败而 fallback —— 但那会掩盖
    // "大图才压缩"这个意图。用字节数精确相等来钉住"没被重编码"。
    const data = new Uint8Array([9, 8, 7, 6, 5])
    const f = new File([data], 'small.jpg', { type: 'image/jpeg' })
    const r = await loadImageFile(f)
    expect(r.ok).toBe(true)
    if (r.ok) expect(r.image.bytes).toBe(5)
  })
})
