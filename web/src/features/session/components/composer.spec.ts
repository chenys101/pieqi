import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, type Component } from 'vue'
import PromptInput from './PromptInput.vue'
import InterveneInput from './InterveneInput.vue'

// T2 出口条件：输入条与正文左边界同线 + 全站外壳规格（AC-R8-01~08）+ 斜杠补全/双态按钮不回退（AC-R8-09）。
// 补全源用 mock：这些断言与网络无关，别让数据源失败把 R8 的判据一起拖黑。
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    completions: {
      commands: [
        { name: 'help', description: '显示帮助' },
        { name: 'hello', description: '打个招呼' },
      ],
      skills: [{ name: 'heal', description: '修复' }],
    },
  }),
}))

function mount(comp: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props) })
  app.mount(host)
  return { host, app }
}

afterEach(() => {
  document.body.innerHTML = ''
})

const ta = (host: HTMLElement) => host.querySelector('textarea')!

/** 模拟一次真实输入：值是新的、光标在末尾 —— 两者必须同源（见 detectQuery 注释） */
async function type(host: HTMLElement, value: string) {
  const t = ta(host)
  t.value = value
  t.setSelectionRange(value.length, value.length)
  t.dispatchEvent(new Event('input'))
  await nextTick()
  await nextTick()
}

/** 补全菜单容器（贴输入框上方那块）；未弹出时为 null */
const menu = (host: HTMLElement) => host.querySelector('[class*="bottom-full"]')
/** 菜单里的候选按钮（分组标题是 div 不是 button，别把两者混在一个计数里） */
const menuItems = (host: HTMLElement) =>
  menu(host) ? [...menu(host)!.querySelectorAll('button')].map((b) => b.textContent) : []
/** 分组标题（「命令」/「Skills」） */
const groupLabels = (host: HTMLElement) =>
  menu(host)
    ? [...menu(host)!.children].filter((c) => c.tagName === 'DIV').map((c) => c.textContent)
    : []

describe('PromptInput（输入条外壳）', () => {
  it('复用 Textarea 外壳：surface-subtle / --radius-sm / 3px ring / text-tertiary / hover', () => {
    const cls = ta(mount(PromptInput, { modelValue: '' }).host).className
    for (const t of [
      'bg-surface-subtle',
      'rounded-[var(--radius-sm)]',
      'focus-visible:ring-[3px]',
      'focus-visible:ring-accent/40',
      'placeholder:text-text-tertiary',
      'hover:border-border-strong',
    ])
      expect(cls, `缺少 ${t}`).toContain(t)
    // 旧的裸 textarea 写法（bg-background + rounded-lg + text-muted/60）不得残留
    expect(cls).not.toContain('bg-background')
    expect(cls).not.toContain('placeholder:text-muted/60')
  })

  it('输入条不给自己拖高（拖高会把时间线顶走）', () => {
    expect(ta(mount(PromptInput, { modelValue: '' }).host).className).toContain('resize-none')
    expect(ta(mount(PromptInput, { modelValue: '' }).host).className).not.toContain('resize-y')
  })

  it('disabled 透传到原生元素', () => {
    expect(ta(mount(PromptInput, { modelValue: '', disabled: true }).host).disabled).toBe(true)
  })

  it('Ctrl+Enter 提交', () => {
    const onSubmit = vi.fn()
    const { host } = mount(PromptInput, { modelValue: 'x', onSubmit })
    ta(host).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true }))
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })
})

describe('PromptInput（斜杠补全）', () => {
  it('输入 / 弹出 Commands + Skills 分组', async () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    expect(menu(host)).toBeNull()
    await type(host, '/')
    expect(groupLabels(host)).toEqual(['命令', 'Skills'])
    expect(menuItems(host)).toHaveLength(3)
  })

  it('查询词按**当前**光标计算，不慢一个字符', async () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    await type(host, '/hel')
    expect(menuItems(host)).toHaveLength(2)
    // 若 detectQuery 读的是 props.modelValue（要等父组件重渲染），这里会仍停在 /hel = 2 条
    await type(host, '/hell')
    expect(menuItems(host)).toHaveLength(1)
    expect(menuItems(host)[0]).toMatch(/^\/hello/)
  })

  it('↑↓ + Enter 插入候选，并在其后留空格', async () => {
    const onUpdate = vi.fn()
    const { host } = mount(PromptInput, { modelValue: '', 'onUpdate:modelValue': onUpdate })
    await type(host, '/hell')
    ta(host).dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    ta(host).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    expect(onUpdate).toHaveBeenCalledWith('/hello ')
  })

  it('Escape 关闭菜单', async () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    await type(host, '/')
    expect(menuItems(host).length).toBeGreaterThan(0)
    ta(host).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()
    expect(menuItems(host)).toHaveLength(0)
  })

  it('无匹配（空格后）不弹菜单', async () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    await type(host, '/hel x')
    expect(menuItems(host)).toHaveLength(0)
  })
})

describe('InterveneInput', () => {
  it('内层与正文同宽（max-w-3xl）且水平居中 —— AC-R8-01 的结构前提', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true })
    const inner = host.firstElementChild!.firstElementChild!
    expect(inner.className).toContain('mx-auto')
    expect(inner.className).toContain('max-w-3xl')
    expect(inner.className).toContain('w-full')
    // 外条负责满宽 border-top / 底色，内层只管对齐
    expect(host.firstElementChild!.className).toContain('border-t')
  })

  it('双态按钮：视觉 36 但命中区外扩到 ≥44（after:-inset-1 = 4px×2）', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true })
    const send = host.querySelector('button[aria-label="发送"]')!
    for (const t of ['relative', 'after:absolute', 'after:-inset-1', 'after:content-[\'\']', 'rounded-[var(--radius-sm)]', 'focus-visible:ring-[3px]'])
      expect(send.className, `缺少 ${t}`).toContain(t)

    document.body.innerHTML = ''
    const c = mount(InterveneInput, { canCancel: true, canSend: false })
    const cancel = c.host.querySelector('button[aria-label="中止"]')!
    expect(cancel.className).toContain('after:-inset-1')
    expect(c.host.querySelector('button[aria-label="发送"]')).toBeNull()
  })

  it('运行中：textarea disabled 且只给「中止」', () => {
    const onCancel = vi.fn()
    const { host } = mount(InterveneInput, { canCancel: true, canSend: false, onCancel })
    expect(ta(host).disabled).toBe(true)
    ;(host.querySelector('button[aria-label="中止"]') as HTMLButtonElement).click()
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('终态：空文本不可发送，有文本才 emit send 并清空', async () => {
    const onSend = vi.fn()
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, onSend })
    const send = () => host.querySelector('button[aria-label="发送"]') as HTMLButtonElement
    expect(send().disabled).toBe(true)

    await type(host, '补充一句')
    expect(send().disabled).toBe(false)
    send().click()
    // 模型已改为**会话级切换**（独立 emit），send 只带文本 ——
    // 「这一轮用哪个模型」不再随发送走，见 ModelSwitchNote 的注释
    expect(onSend).toHaveBeenCalledWith('补充一句')
  })

  it('canSend=false 时不可发送（决策横幅未就绪）', async () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: false })
    await type(host, '文字')
    expect((host.querySelector('button[aria-label="发送"]') as HTMLButtonElement).disabled).toBe(true)
  })

// ---- 模型选择器：嵌在输入框内，自下而上弹层，会话级切换 ----
// agent 下发的是**不透明串**（dsh 是 JSON.stringify([provider,model])），前端只搬运。
const MODELS = [
  { value: '["magpie","workbuddy/x"]', name: 'X', group: 'magpie' },
  { value: '["magpie","workbuddy/y"]', name: 'Y', group: 'magpie', description: '更强但更贵' },
]

/** 打开弹层（点触发器） */
async function openPicker(host: HTMLElement) {
  ;(host.querySelector('[data-testid="model-picker-trigger"]') as HTMLButtonElement).click()
  await nextTick()
}
const pickerPopup = (host: HTMLElement) => host.querySelector('[data-testid="model-picker-popup"]')

it('选择器嵌在输入框内、与发送按钮同一行（#lead 插槽）', async () => {
  const { host } = mount(InterveneInput, { canCancel: false, canSend: true, models: MODELS })
  const trigger = host.querySelector('[data-testid="model-picker-trigger"]')!
  const send = host.querySelector('button[aria-label="发送"]')!
  // 结构判据：与发送按钮共用 PromptInput 的底部动作行 —— 即"嵌在框内"
  expect(trigger.closest('[data-testid="composer-actions"]')).toBe(send.closest('[data-testid="composer-actions"]'))
  // 有 lead 时该行必须两端对齐，否则模型选择器会被 justify-end 推到右边和发送按钮挤一起
  const row = host.querySelector('[data-testid="composer-actions"]')!
  expect(row.className).toContain('justify-between')
  // 整行 pointer-events-none，所以触发器要自己把事件打开，否则点不动
  expect(trigger.closest('[data-testid="model-picker"]')!.className).toContain('pointer-events-auto')
})

it('触发器显示会话当前模型的人读名（不是不透明串）', () => {
  const { host } = mount(InterveneInput, {
    canCancel: false,
    canSend: true,
    models: MODELS,
    currentModel: MODELS[0].value,
  })
  const trigger = host.querySelector('[data-testid="model-picker-trigger"]')!
  expect(trigger.textContent).toContain('X')
  expect(trigger.textContent).not.toContain('magpie')
  expect(trigger.getAttribute('aria-label')).toContain('X')
})

it('弹层自下而上弹出（bottom-full）且贴着输入框上沿', async () => {
  const { host } = mount(InterveneInput, { canCancel: false, canSend: true, models: MODELS })
  expect(pickerPopup(host)).toBeNull()
  await openPicker(host)
  const popup = pickerPopup(host)!
  expect(popup.className).toContain('bottom-full') // 向上展开，不是向下
  expect(popup.className).toContain('mb-1') // 与触发器之间留缝
})

it('移动端：限高 60dvh + 内部滚动 + 底部安全区内边距 + 每项 44px 命中区', async () => {
  const { host } = mount(InterveneInput, { canCancel: false, canSend: true, models: MODELS })
  await openPicker(host)
  const list = pickerPopup(host)!.querySelector('[role="listbox"]') as HTMLElement
  // ① 小屏上不能盖满整屏：限高后内部滚动
  expect(list.className).toContain('max-h-[min(60dvh,20rem)]')
  expect(list.className).toContain('overflow-y-auto')
  expect(list.className).toContain('overscroll-contain') // 滚到底不带动背后的时间线
  // ② iPhone 底部横条 / Home 指示器不压住最后一项
  expect(list.className).toContain('pb-[env(safe-area-inset-bottom)]')
  // ③ 触控最小命中区 44px
  for (const opt of pickerPopup(host)!.querySelectorAll('[role="option"]'))
    expect(opt.className, '选项触控区不足 44px').toContain('min-h-11')
})

it('清单按分组渲染；选中某项 emit 的是那个不透明串（原样搬运，不解析）', async () => {
  const onSwitch = vi.fn()
  const { host } = mount(InterveneInput, {
    canCancel: false,
    canSend: true,
    models: MODELS,
    onSwitchModel: onSwitch,
  })
  await openPicker(host)
  expect(pickerPopup(host)!.textContent).toContain('magpie') // 分组标题
  expect(pickerPopup(host)!.textContent).toContain('更强但更贵') // 选项说明

  const opt = [...pickerPopup(host)!.querySelectorAll('[role="option"]')].find(
    (o) => o.textContent!.includes('Y'),
  ) as HTMLButtonElement
  opt.click()
  await nextTick()
  expect(onSwitch).toHaveBeenCalledWith(MODELS[1].value)
  // 选中即关闭弹层（点完还杵着会挡住继续看时间线）
  expect(pickerPopup(host)).toBeNull()
})

it('「Agent 默认」= 清空选择（emit 空串，交还 agent 路由）', async () => {
  const onSwitch = vi.fn()
  const { host } = mount(InterveneInput, {
    canCancel: false,
    canSend: true,
    models: MODELS,
    currentModel: MODELS[0].value,
    onSwitchModel: onSwitch,
  })
  await openPicker(host)
  ;(host.querySelector('[data-testid="model-picker-default"]') as HTMLButtonElement).click()
  await nextTick()
  expect(onSwitch).toHaveBeenCalledWith('')
})

it('该 agent 没有清单：触发器仍在，弹层里说明原因（不是静默消失）', async () => {
  const { host } = mount(InterveneInput, {
    canCancel: false,
    canSend: true,
    models: [],
    modelsLoaded: true,
  })
  // 触发器必须还在：整个藏起来会让用户以为功能不存在
  expect(host.querySelector('[data-testid="model-picker-trigger"]')).not.toBeNull()
  await openPicker(host)
  expect(host.querySelector('[data-testid="model-picker-empty"]')!.textContent).toContain('不提供可选模型')
  // 弹层外另有一句说明（弹层关着时它才有出处）
  expect(host.querySelector('[data-testid="composer-model-unsupported"]')).not.toBeNull()
})

it('运行中禁用切换（这一轮的模型已定死），不摆一个按了没用的开关', () => {
  const { host } = mount(InterveneInput, {
    canCancel: true,
    canSend: false,
    models: MODELS,
    switchDisabled: true,
  })
  const trigger = host.querySelector('[data-testid="model-picker-trigger"]') as HTMLButtonElement
  expect(trigger.disabled).toBe(true)
})
})

/** 带 #actions 的 PromptInput（动作按钮嵌在输入框内右下角） */
const WithActions = defineComponent({
  render: () =>
    h(
      PromptInput,
      { modelValue: 'x' },
      { actions: () => h('button', { class: 'pointer-events-auto', 'aria-label': '创建任务' }, '飞') },
    ),
})

/** 带 #lead 的 PromptInput（模型选择器嵌在输入框内左下角，与动作按钮同一行） */
const WithLead = defineComponent({
  render: () =>
    h(
      PromptInput,
      { modelValue: 'x' },
      {
        lead: () => h('button', { class: 'pointer-events-auto', 'data-testid': 'lead-btn' }, '模'),
        actions: () => h('button', { class: 'pointer-events-auto', 'aria-label': '发送' }, '飞'),
      },
    ),
})

describe('输入框内嵌动作按钮（PC 与移动端同一套 DOM）', () => {
  it('动作区与 textarea 同父、绝对定位贴右下角', () => {
    const { host } = mount(WithActions)
    const actions = host.querySelector('[data-testid="composer-actions"]')!
    const t = ta(host)
    // "嵌在框内" 的结构性判据：与 textarea 共用那个 relative 容器
    expect(actions.parentElement).toBe(t.parentElement)
    expect(actions.parentElement!.className).toContain('relative')
    // ⚠️ 这里只验证"贴着右下角"这一结构事实，**具体偏移值不在这里钉**：
    // 下沿几何由下方那条几何用例负责（它要求 `bottom-2.5` 且**明确禁止** `bottom-0`）。
    // 早期这里写死 `bottom-0`，与那条用例互相矛盾 —— 41f8d7f 把行从 bottom-0 抬到
    // bottom-2.5（下沿才等于 12px）时只改了代码没改这里，于是同一份断言集里
    // 「必须含 bottom-0」和「必须不含 bottom-0」同时成立，测试永远红。
    for (const cls of ['absolute', 'inset-x-0', 'bottom-', 'justify-end', 'pointer-events-none'])
      expect(actions.className, `缺少 ${cls}`).toContain(cls)
  })

  it('有动作区时 textarea 让出底部空间，没有则不留空 padding', () => {
    expect(ta(mount(WithActions).host).className).toContain('pb-[4.375rem]')
    expect(ta(mount(PromptInput, { modelValue: '' }).host).className).not.toContain('pb-')
  })

  // 按钮外框到输入框**右沿与下沿都必须是 12px**（headless Chrome 实测基准）。
  // 只靠 class 断言守不住几何：曾经 px-3/pb-2/bottom-0 看着"下沿=pb-2=8px"，
  // 实测却是 2px —— 行的 border-box 比让位区矮不了多少，底部 6px 溢到框外被吃掉了，
  // 于是右边 12px、下边 2px，按钮显得"贴底、离右边远"。
  // 所以这里既钉 class，也钉"行必须在框内、且让位高度自洽"这两条推导链。
  it('按钮右/下留白同值：行 px-3 + 行落在框内 + 让位高度自洽', () => {
    const { host } = mount(WithLead)
    const row = host.querySelector('[data-testid="composer-actions"]')!
    const t = ta(host)
    // 横向：行宽 = textarea 宽，行的 px-3(12px) 就是右边距
    expect(row.className).toContain('px-3')
    // 纵向：行**不能**贴 textarea 下沿（bottom-0）——那样 pb-2 会被溢出吃掉，
    // 实测下沿只剩 2px。必须留出 bottom-2.5(10px) 把行抬进框内，下沿才等于 12px。
    expect(row.className).toContain('bottom-2.5')
    expect(row.className).not.toContain('bottom-0')
    expect(row.className).toContain('pb-2')
    // 让位高度绑死：70px = 行占位 44px(按钮 36 + 行 pb 8) + bottom 10px + 余量 16px。
    // 改行的 bottom/pb 或按钮尺寸后必须重新量，否则按钮压住文字。
    expect(t.className).toContain('pb-[4.375rem]')
  })

  it('只有 #lead（模型选择器）时也让出底部空间，且该行两端对齐', () => {
    const { host } = mount(WithLead)
    expect(ta(host).className).toContain('pb-[4.375rem]')
    const row = host.querySelector('[data-testid="composer-actions"]')!
    expect(row.className).toContain('justify-between')
    // 结构判据：lead 与 actions 同处一行
    const lead = host.querySelector('[data-testid="lead-btn"]')!
    expect(lead.parentElement).toBe(row)
    expect(row.children.length).toBe(2)
  })

  it('详情页发送按钮同样嵌在框内（与新建任务页一致，不按断点分两套）', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true })
    const send = host.querySelector('button[aria-label="发送"]')!
    expect(send.parentElement!.getAttribute('data-testid')).toBe('composer-actions')
    expect(send.querySelector('svg')).not.toBeNull()
  })
})
