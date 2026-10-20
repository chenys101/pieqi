import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import Input from './Input.vue'
import Textarea from './Textarea.vue'
import Select from './Select.vue'

// T1 出口条件：三件组件的「底色 / 圆角 / 焦点 ring / hover / placeholder 对比度 / 命中区」
// 必须在 class 层可判定 —— 这几项是 R8 的病因所在，不是外观偏好。
// 用 jsdom mount 而非 renderToString：需要断言 disabled / aria-invalid 的**真值**，
// 而 class 字符串里也含 "disabled:" "invalid" 等子串，纯文本匹配会自欺。
function mount(comp: Component, props: Record<string, unknown> = {}, slots?: Record<string, () => unknown>) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props, slots) })
  app.mount(host)
  return { host, app }
}

afterEach(() => {
  document.body.innerHTML = ''
})

/** 所有 form 控件共用的外壳规格（SPEC §4.4 + 原型 .dlg-input/.dlg-select 的合规实现） */
const SHELL = [
  'bg-surface-subtle', // 不是 bg-background —— 后者是 canvas，深色下会让输入框反向凹陷
  'rounded-[var(--radius-sm)]', // 6px；锚到 token 而非 rounded-md
  'focus-visible:ring-[3px]', // SPEC §7：3px
  'focus-visible:ring-accent/40',
  'focus-visible:outline-none', // 是"用 ring 替代"，不是裸删焦点态
  'hover:border-border-strong',
]

// 只有文本类控件有 placeholder；select 没有这个概念，别把它塞进通用断言
const PLACEHOLDER = 'placeholder:text-text-tertiary' // 4.8:1 达标 AA（text-muted/60 约 3:1 不达标）

describe('Input', () => {
  it('外壳规格齐全（底色 / 圆角 / 3px ring / placeholder 对比度 / hover）', () => {
    const { host } = mount(Input, { modelValue: '' })
    const cls = host.querySelector('input')!.className
    for (const t of [...SHELL, PLACEHOLDER]) expect(cls, `缺少 ${t}`).toContain(t)
  })

  it('高度：md 默认 36px，sm 为 32px 紧凑', () => {
    expect(mount(Input).host.querySelector('input')!.className).toContain('h-9')
    expect(mount(Input, { size: 'sm' }).host.querySelector('input')!.className).toContain('h-8')
  })

  it('invalid：边框转 error 且 aria-invalid=true；非 invalid 时不渲染该属性', () => {
    const bad = mount(Input, { invalid: true }).host.querySelector('input')!
    expect(bad.className).toContain('border-error')
    expect(bad.getAttribute('aria-invalid')).toBe('true')
    expect(mount(Input).host.querySelector('input')!.getAttribute('aria-invalid')).toBeNull()
  })

  it('disabled 透传到原生元素', () => {
    expect(mount(Input, { disabled: true }).host.querySelector('input')!.disabled).toBe(true)
  })

  it('type / modelValue 透传', () => {
    const i = mount(Input, { type: 'email', modelValue: 'a@b.com' }).host.querySelector('input')!
    expect(i.type).toBe('email')
    expect(i.value).toBe('a@b.com')
  })

  it('输入时 emit update:modelValue', () => {
    const onUpdate = vi.fn()
    const i = mount(Input, { modelValue: '', 'onUpdate:modelValue': onUpdate }).host.querySelector('input')!
    i.value = 'hi'
    i.dispatchEvent(new Event('input'))
    expect(onUpdate).toHaveBeenCalledWith('hi')
  })
})

describe('Textarea', () => {
  it('外壳规格与 Input 一致', () => {
    const cls = mount(Textarea, { modelValue: '' }).host.querySelector('textarea')!.className
    for (const t of [...SHELL, PLACEHOLDER]) expect(cls, `缺少 ${t}`).toContain(t)
  })

  it('固定高度模式用 rows（默认 3）且可纵向 resize', () => {
    const t = mount(Textarea).host.querySelector('textarea')!
    expect(t.getAttribute('rows')).toBe('3')
    expect(t.className).toContain('resize-y')
  })

  // ⚠️ 2026-10-18 判据变更（修「发送按钮被底栏盖住」时改的，见 docs/testing/ui-e2e-next.md）：
  //
  // 以前 autoResize 会**去掉** rows，让高度完全由内容决定。现在 rows 仍在，语义变成
  // 「量不到内容时的兜底」—— resize() 的空框分支要靠它撑出稳定起步高度
  // （见 Textarea 里 minHeight 的注释：空框量到的是**单行**高度，
  // 直接拿它当高度会让空框缩成一行，而输入条需要一个稳定的初始高度）。
  //
  // 所以断言的是「rows 保留 + 不可手拖 + 内容不溢出」；高度由内容决定这件事
  // 由 autoResize + maxHeight 的组合保证（浏览器行为，jsdom 量不出来 ——
  // 真正的验证在 e2e/layout 的 composerBar 尺寸断言里）。
  it('autoResize：rows 作为兜底保留、禁用手动 resize、内容不溢出', () => {
    const t = mount(Textarea, { autoResize: true, rows: 3 }).host.querySelector('textarea')!
    expect(t.getAttribute('rows')).toBe('3')
    expect(t.className).toContain('resize-none')
    expect(t.className).toContain('overflow-hidden')
  })

  it('invalid 转 error 边框', () => {
    const t = mount(Textarea, { invalid: true }).host.querySelector('textarea')!
    expect(t.className).toContain('border-error')
    expect(t.getAttribute('aria-invalid')).toBe('true')
  })

  /**
   * 内容删光后高度必须退回 `auto`（= 回到 rows 那一档），而不是钉在"上一次多行"
   * 算出来的像素值上。
   *
   * 旧 resize() 遇到空值时只记 minHeight 就 return，`style.height='auto'` 虽然设过，
   * 但**下一次**输入又把它写成数字、再删光时不会清回来 —— 表现是输入框停在
   * 144px 的大空块上。修法就是空值分支只 return，不写回数字。
   *
   * jsdom 量不出真实高度，但 `style.height` 是**显式赋值**与否的直接证据。
   */
  it('autoResize：内容清空后不得残留显式高度（会钉在旧的多行高度上）', async () => {
    const wrap = mount(Textarea, { autoResize: true, maxHeight: 66 }, undefined)
    const t = wrap.host.querySelector('textarea') as HTMLTextAreaElement
    const type = async (v: string) => {
      t.value = v
      t.dispatchEvent(new Event('input'))
      await nextTick()
    }
    await type('第一行\n第二行\n第三行')
    await type('')
    expect(t.style.height, '清空后仍残留显式高度：输入框会停在多行时的大空块上').toBe('auto')
  })
})

describe('Select', () => {
  it('复用 Input 外壳，且剥掉原生外观（否则箭头与 Input 语言不一致）', () => {
    const { host } = mount(Select, { modelValue: 'a' }, { default: () => [h('option', { value: 'a' }, 'A')] })
    const cls = host.querySelector('select')!.className
    expect(cls).toContain('appearance-none')
    for (const t of SHELL) expect(cls, `缺少 ${t}`).toContain(t)
  })

  it('自绘 chevron 在右侧且不拦截点击', () => {
    const { host } = mount(Select, {}, { default: () => [h('option', { value: 'a' }, 'A')] })
    const svg = host.querySelector('svg')!
    expect(svg).toBeTruthy()
    expect(svg.className.baseVal || svg.getAttribute('class')).toContain('pointer-events-none')
    expect(svg.getAttribute('aria-hidden')).toBe('true')
  })

  it('插槽选项透传，切换时 emit update:modelValue', () => {
    const onUpdate = vi.fn()
    const { host } = mount(
      Select,
      { modelValue: 'a', 'onUpdate:modelValue': onUpdate },
      { default: () => [h('option', { value: 'a' }, 'A'), h('option', { value: 'b' }, 'B')] },
    )
    const sel = host.querySelector('select')!
    expect(host.querySelectorAll('option')).toHaveLength(2)
    sel.value = 'b'
    sel.dispatchEvent(new Event('change'))
    expect(onUpdate).toHaveBeenCalledWith('b')
  })

  it('disabled / invalid 生效', () => {
    const { host } = mount(Select, { disabled: true, invalid: true })
    const sel = host.querySelector('select')!
    expect(sel.disabled).toBe(true)
    expect(sel.getAttribute('aria-invalid')).toBe('true')
    expect(sel.className).toContain('border-error')
  })
})
