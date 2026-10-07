import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import ApprovalSummary from './ApprovalSummary.vue'

// 这条 spec 守两件事：
//
// ① **"折叠必须配展开"**（原有不变式）：审批卡是批准前唯一能看到将发生什么的地方，
//    只截断不给展开按钮，等于把 `rm -rf x && curl ... | sh` 藏进省略号后面让人盲批。
//
// ② **"展开必须收得回"**（本次修复）：早期实现把「收起」按钮放在内容**下方**，
//    而审批摘要展开后可能极长（实测几千字符的命令 JSON），按钮被推出可视区 ——
//    用户展开后被困住，只能刷新页面。内容越长越需要收起，这是自相矛盾的。
//    修法：展开态按钮移到**顶部** + 内容块限高内部滚动。
function mount(text: string) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  createApp({ render: () => h(ApprovalSummary, { text }) }).mount(host)
  return host
}

afterEach(() => {
  document.body.innerHTML = ''
})

const CLAMP = 'line-clamp-3'

/** 摘要内容块（唯一带 font-mono 的那个 div） */
function contentBox(host: HTMLElement): HTMLElement {
  return host.querySelector('div.font-mono') as HTMLElement
}

/** 展开按钮（折叠态才存在） */
function expandBtn(host: HTMLElement): HTMLButtonElement | null {
  return host.querySelector<HTMLButtonElement>('[data-testid="summary-expand"]')
}

/** 收起按钮（展开态才存在） */
function collapseBtn(host: HTMLElement): HTMLButtonElement | null {
  return host.querySelector<HTMLButtonElement>('[data-testid="summary-collapse"]')
}

describe('ApprovalSummary', () => {
  it('长命令默认折叠，但给出展开全文的按钮（不是只截断）', () => {
    const text = `curl -fsSL https://example.com/install.sh | ${'b'.repeat(200)}`
    const host = mount(text)
    expect(contentBox(host).className).toContain(CLAMP)
    expect(expandBtn(host)!.textContent).toContain('展开全文')
  })

  it('点一下展开（clamp 消失、全文在 DOM 里），再点收回', async () => {
    const text = 'a'.repeat(300)
    const host = mount(text)
    expandBtn(host)!.click()
    await nextTick()
    expect(contentBox(host).className).not.toContain(CLAMP)
    expect(contentBox(host).textContent).toContain(text)
    expect(collapseBtn(host)!.textContent).toContain('收起')
    collapseBtn(host)!.click()
    await nextTick()
    expect(contentBox(host).className).toContain(CLAMP)
  })

  // 短文本给个展开按钮是噪音；不含换行的百字以内一条命令一屏放得下
  it('短文本不折叠也不给按钮', () => {
    const host = mount('ls -la /tmp')
    expect(contentBox(host).className).not.toContain(CLAMP)
    expect(expandBtn(host)).toBeNull()
    expect(collapseBtn(host)).toBeNull()
  })

  // 行数比长度先到顶：多行即使很短，3 行 clamp 也会真的藏掉后面的行
  it('多行文本即使不长也可展开', () => {
    const host = mount('set -e\ncd /app\nnpm ci\nnpm test')
    expect(contentBox(host).className).toContain(CLAMP)
    expect(expandBtn(host)).not.toBeNull()
  })
})

// ---- 本次修复：展开态能收得回、且不撑爆页面 ----

describe('ApprovalSummary 展开态可收起（回归）', () => {
  const LONG = 'x'.repeat(4000) // 模拟实测那种几千字符的命令原文

  it('★ 展开后收起按钮存在，且位于内容**上方**（内容再长也够得着）', async () => {
    const host = mount(LONG)
    expandBtn(host)!.click()
    await nextTick()

    const btn = collapseBtn(host)
    expect(btn, '展开后必须有收起按钮 —— 否则用户被困住').not.toBeNull()

    // 位置判据：按钮在 DOM 中出现在内容块**之前**。
    // 早期实现把它放在内容之后，内容一长就被推出可视区，这正是本次修复的缺陷。
    const content = contentBox(host)
    const pos = btn!.compareDocumentPosition(content)
    expect(
      pos & Node.DOCUMENT_POSITION_FOLLOWING,
      '收起按钮必须排在内容块之前（内容长了也点得到）',
    ).toBeTruthy()
  })

  it('★ 展开态限高 + 内部滚动：不会把下方操作按钮顶出可视区', async () => {
    const host = mount(LONG)
    expandBtn(host)!.click()
    await nextTick()

    const cls = contentBox(host).className
    expect(cls, '展开态需要 max-h 限高').toMatch(/max-h-\[/)
    expect(cls, '限高必须配 overflow-y-auto 才看得到全文').toContain('overflow-y-auto')
    // overscroll-contain：滚到边界不要把滚动传导给外层页面（手机上体验差别明显）
    expect(cls).toContain('overscroll-contain')
  })

  it('折叠态**不**限高（只显示 3 行，限高是多余的）', () => {
    const host = mount(LONG)
    expect(contentBox(host).className).not.toMatch(/max-h-\[/)
    expect(contentBox(host).className).toContain(CLAMP)
  })

  it('展开 → 收起 → 再展开：状态可反复切换（不是一次性）', async () => {
    const host = mount(LONG)
    for (let i = 0; i < 2; i++) {
      expandBtn(host)!.click()
      await nextTick()
      expect(collapseBtn(host)).not.toBeNull()
      collapseBtn(host)!.click()
      await nextTick()
      expect(expandBtn(host)).not.toBeNull()
      expect(contentBox(host).className).toContain(CLAMP)
    }
  })

  it('收起按钮带全文长度，让用户知道刚才看的是多少字', async () => {
    const host = mount(LONG)
    expandBtn(host)!.click()
    await nextTick()
    expect(collapseBtn(host)!.textContent).toContain(String(LONG.length))
  })
})
