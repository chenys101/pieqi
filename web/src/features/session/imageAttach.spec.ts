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
  clipboardImageFiles,
  formatBytes,
  loadClipboardImages,
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

// 加图入口的显隐判据已迁到**服务端目录**（GET /api/agents 的 image_prompt）：
// 能力取决于配置而非 agent 名（claude 走桥有图片通路、配成 print 就没有），
// 所以前端不再持有任何硬编码名单，这里也就不再需要那组用例。
// 覆盖它的地方：internal/agent 的 AvailableAgents 测试（服务端算能力）、
// 以及 web/src/stores/agent.spec.ts（目录字段搬运）。


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

// ---- 剪贴板取图：截图直接粘贴的主路径 ----
//
// ⚠️ 这些用例守的是**取值路径**，不是"真机截图能贴上"—— 后者需要一个真实的
// 剪贴板事件，只能过 web 的自测脚本（见 AGENTS.md 的 UI 验收纪律）。
// 但"读 items 还是读 files"这个判据是纯逻辑，正好是回归价值最高的地方：
// 飞书/企微截图（Windows 走 CF_DIBV5）普遍**只给 items、不给 files**，
// 漏掉 items 这条就等于整个功能不存在，而单测能钉死它。

/** 构造一个「只在 items 里、files 为空」的剪贴板 —— 截图工具的真实形态 */
function clipboardFromItems(...items: File[]): DataTransfer {
  return {
    files: [] as unknown as FileList,
    items: items.map((f) => ({
      kind: 'file',
      type: f.type,
      getAsFile: () => f,
    })),
  } as unknown as DataTransfer
}

/** 构造一个「files 里有」的剪贴板 —— 从资源管理器复制图片文件的形态 */
function clipboardFromFiles(...files: File[]): DataTransfer {
  return {
    files: files as unknown as FileList,
    items: [] as unknown as DataTransferItemList,
  } as unknown as DataTransfer
}

const png = (name = 'image.png', bytes = 8) =>
  new File([new Uint8Array(bytes)], name, { type: 'image/png' })

describe('clipboardImageFiles', () => {
  it('items 里有图、files 为空时也能取到（截图工具的主路径）', () => {
    const f = png()
    const got = clipboardImageFiles(clipboardFromItems(f))
    expect(got).toHaveLength(1)
    expect(got[0].type).toBe('image/png')
  })

  it('files 里有图时能取到（从资源管理器复制文件）', () => {
    const got = clipboardImageFiles(clipboardFromFiles(png()))
    expect(got).toHaveLength(1)
  })

  it('两路同时给同一张图时只算一张（去重，否则会粘出两张）', () => {
    const f = png()
    const dt = {
      files: [f] as unknown as FileList,
      items: [{ kind: 'file', type: f.type, getAsFile: () => f }],
    } as unknown as DataTransfer
    expect(clipboardImageFiles(dt)).toHaveLength(1)
  })

  it('纯文本粘贴得到空数组（不能把文本当图）', () => {
    const dt = {
      files: [] as unknown as FileList,
      items: [{ kind: 'string', type: 'text/plain', getAsFile: () => null }],
    } as unknown as DataTransfer
    expect(clipboardImageFiles(dt)).toEqual([])
  })

  it('null / 空剪贴板不抛', () => {
    expect(clipboardImageFiles(null)).toEqual([])
    expect(clipboardImageFiles({} as DataTransfer)).toEqual([])
  })

  it('空 type 的空壳项被丢弃（有的浏览器会塞无类型项）', () => {
    const junk = new File([new Uint8Array(3)], 'x', { type: '' })
    expect(clipboardImageFiles(clipboardFromItems(junk))).toEqual([])
  })

  it('截图的无名图被赋予可读名（缩略图 title 与报错都引用它）', () => {
    // 截图来源的名字通常是 image.png 或空串，直接显示对用户没有信息量
    expect(clipboardImageFiles(clipboardFromItems(png('')))[0].name).toBe('粘贴的图片')
    expect(clipboardImageFiles(clipboardFromItems(png('image.png')))[0].name).toBe('粘贴的图片')
  })

  it('有真实文件名的图保留原名（用户认得出自己贴的是哪个文件）', () => {
    expect(clipboardImageFiles(clipboardFromItems(png('design-v2.png')))[0].name).toBe('design-v2.png')
  })
})

describe('loadClipboardImages', () => {
  it('产出纯 base64（与选图同一条校验/读取路径）', async () => {
    const r = await loadClipboardImages(clipboardFromItems(png()), MAX_IMAGES)
    expect(r.images).toHaveLength(1)
    expect(r.error).toBeUndefined()
    expect(r.images[0].data).not.toContain('data:')
    expect(r.images[0].previewUrl.startsWith('data:image/png;base64,')).toBe(true)
  })

  it('白名单外的类型被拒并给出原因（HEIC 截图不能静默丢掉）', async () => {
    const bad = new File([new Uint8Array(4)], 'shot.heic', { type: 'image/heic' })
    const r = await loadClipboardImages(clipboardFromItems(bad), MAX_IMAGES)
    expect(r.images).toEqual([])
    expect(r.error).toContain('不支持')
  })

  it('没图时不产生任何错误（正常的文本粘贴不该弹提示）', async () => {
    const dt = { files: [] as unknown as FileList, items: [] } as unknown as DataTransfer
    const r = await loadClipboardImages(dt, MAX_IMAGES)
    expect(r.images).toEqual([])
    expect(r.error).toBeUndefined()
  })

  it('超出剩余额度时**报错**（粘贴被截断必须说出来，不能静默丢）', async () => {
    // 额度只剩 1 张，却贴了 3 张
    const r = await loadClipboardImages(
      clipboardFromItems(png(), png('b.png'), png('c.png')),
      1,
    )
    expect(r.images).toHaveLength(1)
    expect(r.error).toContain(`最多 ${MAX_IMAGES} 张`)
  })

  it('额度为 0（已满 8 张）时不收图且报错', async () => {
    const r = await loadClipboardImages(clipboardFromItems(png()), 0)
    expect(r.images).toEqual([])
    expect(r.error).toContain('不能再加了')
  })
})
