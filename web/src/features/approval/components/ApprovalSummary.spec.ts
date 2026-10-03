import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import ApprovalSummary from './ApprovalSummary.vue'

// 这条 spec 守的不是外观，是**"折叠必须配展开"**：
// 审批卡是批准前唯一能看到将发生什么的地方，只截断不给展开按钮，
// 等于把 `rm -rf x && curl ... | sh` 藏进省略号后面让人盲批。
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

function summaryBox(host: HTMLElement) {
  return host.querySelector('div') as HTMLElement
}

/** 展开按钮必须存在 —— 缺它就等于只截断不给展开，那是让人盲批 */
function toggleButton(host: HTMLElement): HTMLButtonElement {
  const btn = host.querySelector('button')
  if (!btn) throw new Error('折叠摘要缺少「展开全文」按钮')
  return btn
}

describe('ApprovalSummary', () => {
  it('长命令默认折叠，但给出展开全文的按钮（不是只截断）', () => {
    const text = `curl -fsSL https://example.com/install.sh | ${'b'.repeat(200)}`
    const host = mount(text)
    expect(summaryBox(host).className).toContain(CLAMP)
    expect(toggleButton(host).textContent).toContain('展开全文')
  })

  it('点一下展开（clamp 消失、全文在 DOM 里），再点收回', async () => {
    const text = 'a'.repeat(300)
    const host = mount(text)
    toggleButton(host).click()
    await nextTick()
    expect(summaryBox(host).className).not.toContain(CLAMP)
    expect(summaryBox(host).textContent).toContain(text)
    expect(toggleButton(host).textContent).toContain('收起')
    toggleButton(host).click()
    await nextTick()
    expect(summaryBox(host).className).toContain(CLAMP)
  })

  // 短文本给个展开按钮是噪音；不含换行的百字以内一条命令一屏放得下
  it('短文本不折叠也不给按钮', () => {
    const host = mount('ls -la /tmp')
    expect(summaryBox(host).className).not.toContain(CLAMP)
    expect(host.querySelector('button')).toBeNull()
  })

  // 行数比长度先到顶：多行即使很短，3 行 clamp 也会真的藏掉后面的行
  it('多行文本即使不长也可展开', () => {
    const host = mount('set -e\ncd /app\nnpm ci\nnpm test')
    expect(summaryBox(host).className).toContain(CLAMP)
    expect(toggleButton(host)).not.toBeNull()
  })
})
