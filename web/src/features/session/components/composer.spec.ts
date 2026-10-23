import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, type Component } from 'vue'
import { createPinia } from 'pinia'
import PromptInput from './PromptInput.vue'
import InterveneInput from './InterveneInput.vue'
import { useNotificationStore } from '@/stores/notification'

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

/** 每个挂载实例一份独立 pinia：跨用例共享会让 toast 断言互相污染 */
function mount(comp: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(comp, props) })
  // InterveneInput 需要 pinia（运行中粘贴要给一句人话提示，走的是通知 store）。
  // 用**真实 pinia**而不是 mock store：组件只用到 push()，而真实 store 顺带
  // 覆盖了"这条提示确实进了 toast 队列"，mock 反而会把接线问题藏起来。
  const pinia = createPinia()
  app.use(pinia)
  app.mount(host)
  return { host, app, pinia }
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
const menu = (host: HTMLElement) =>
  host.querySelector('[data-testid="composer-frame"]')?.parentElement?.querySelector('[class*="bottom-full"]') ?? null
/** 菜单里的候选按钮（分组标题是 div 不是 button，别把两者混在一个计数里） */
const menuItems = (host: HTMLElement) =>
  menu(host) ? [...menu(host)!.querySelectorAll('button')].map((b) => b.textContent) : []
/** 分组标题（「命令」/「Skills」） */
const groupLabels = (host: HTMLElement) =>
  menu(host)
    ? [...menu(host)!.children].filter((c) => c.tagName === 'DIV').map((c) => c.textContent)
    : []

/**
 * 输入条的「框」（边线 / 底色 / 圆角 / 焦点环）。
 *
 * 为什么从 textarea 挪到外层：动作行现在排在 textarea **下方**（正常流），
 * 两者要看起来是同一个框，所以框的视觉归外层，textarea 自己的边线/底色透明。
 * 判据不变（SPEC §4.4 的外壳规格），只是落点换了 —— 这与"复用全站外壳"不矛盾：
 * 内层的 Textarea 组件本身没有改，只是这里给它叠了 transparent 覆盖。
 */
const frame = (host: HTMLElement) => host.querySelector('[data-testid="composer-frame"]')!

describe('PromptInput（输入条外壳）', () => {
  it('框的外观落在外层 composer-frame：surface-subtle / --radius-sm / 3px ring / hover', () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    const cls = frame(host).className
    for (const t of [
      'bg-surface-subtle',
      'rounded-[var(--radius-sm)]',
      'focus-within:ring-[3px]',
      'focus-within:ring-accent/40',
      'hover:border-border-strong',
    ])
      expect(cls, `缺少 ${t}`).toContain(t)
    // 旧的裸 textarea 写法（bg-background + rounded-lg + text-muted/60）不得残留
    expect(ta(host).className).not.toContain('bg-background')
    expect(ta(host).className).not.toContain('placeholder:text-muted/60')
    // textarea 自身不得再画一遍边线与底色（否则框里出现"第二个框"）
    for (const t of ['bg-transparent', 'border-transparent', 'rounded-none'])
      expect(ta(host).className, `textarea 缺少 ${t}`).toContain(t)
    // 占位符仍是全站同一套
    expect(ta(host).className).toContain('placeholder:text-text-tertiary')
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

// ---- 模型选择器：在框内动作行里，向上弹层，会话级切换 ----
// agent 下发的是**不透明串**（dsh 是 JSON.stringify([provider,model])），前端只搬运。
const MODELS = [
  { value: '["magpie","workbuddy/x"]', name: 'X', group: 'magpie' },
  { value: '["magpie","workbuddy/y"]', name: 'Y', group: 'magpie', description: '更强但更贵' },
]

/** 打开弹层（点触发器） */
async function openPicker(host: HTMLElement) {
  ;(host.querySelector('[data-testid="model-picker-trigger"]') as HTMLButtonElement).click()
  await nextTick()
  await nextTick()
}

/**
 * 模型选择器的**触发器**（在输入条动作行里）。
 *
 * ⚠️ 弹层已经 Teleport 到 body（2026-10-23），**不在 host 里** —— 见 pickerPopup 的注释。
 */
const pickerTrigger = (host: HTMLElement) => host.querySelector('[data-testid="model-picker-trigger"]')

/**
 * 模型选择器的**弹层**。
 *
 * 它被 Teleport 到 `document.body`，所以从 host 里查不到 —— 这不是"没渲染"，
 * 而是刻意的结构：弹层必须离开输入栏的层叠上下文（`composer-bar` 有
 * `backdrop-filter`，会自成层叠上下文，把弹层困在里面 ⇒ 上半截被时间线/消息盖住）。
 * jsdom 量不出层叠，但能钉住"它在 body 下、不在输入条子树里"这个结构。
 */
const pickerPopup = () => document.querySelector('[data-testid="model-picker-popup"]')

describe('InterveneInput', () => {
  it('宽度口径交给 ContentColumn（唯一一处定义），外条仍满宽承载 border-top —— AC-R8-01', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true })
    // 内层不再是「自己 mx-auto + max-w-3xl」——那正是 5 处硬编码各自漂移的来源。
    // 现在宽度由 ContentColumn 统一给（宽屏左对齐 + 可调，窄屏居中 max-w-3xl），
    // 断言改为"接线正确"：内层挂在 ContentColumn 里。
    const col = host.querySelector('[data-testid="content-column"]')
    expect(col).not.toBeNull()
    const inner = host.querySelector('[data-testid="composer-inner"]')!
    expect(col!.contains(inner)).toBe(true)
    // 外条负责满宽 border-top / 底色
    expect(host.firstElementChild!.className).toContain('border-t')
    // 输入框这一处**不该**再长出拖拽手柄：手柄只在时间线上出现一次，
    // 同一动作给两个入口会让用户拖了输入框却看不到正文变化（它在最下面）。
    expect(host.querySelector('[data-testid="content-resizer"]')).toBeNull()
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
    // 模型已改为**会话级切换**（独立 emit），send 只带文本 + 图片 ——
    // 「这一轮用哪个模型」不再随发送走，见 ModelSwitchNote 的注释。
    // 图片是第二个参数（无图时为空数组）。
    expect(onSend).toHaveBeenCalledWith('补充一句', [])
  })

  it('canSend=false 时不可发送（决策横幅未就绪）', async () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: false })
    await type(host, '文字')
    expect((host.querySelector('button[aria-label="发送"]') as HTMLButtonElement).disabled).toBe(true)
  })

  it('选择器在框内动作行、与发送按钮同一行（#lead 插槽），且在 textarea **下方**', async () => {
  const { host } = mount(InterveneInput, { canCancel: false, canSend: true, models: MODELS })
  const trigger = host.querySelector('[data-testid="model-picker-trigger"]')!
  const send = host.querySelector('button[aria-label="发送"]')!
  // 结构判据：与发送按钮共用 PromptInput 的动作行
  expect(trigger.closest('[data-testid="composer-actions"]')).toBe(send.closest('[data-testid="composer-actions"]'))
  // ★ "文字被压住"的结构性免疫：动作行与 textarea 是同一框里的**兄弟节点**（后者在前），
  //   而不是浮在 textarea 上的一层。
  const row = host.querySelector('[data-testid="composer-actions"]')!
  expect(row.parentElement).toBe(ta(host).parentElement)
  expect(row.className).not.toContain('absolute')
  const kids = [...row.parentElement!.children]
  expect(kids.indexOf(ta(host))).toBeLessThan(kids.indexOf(row))
  // 有 lead 时该行必须两端对齐，否则模型选择器会被 justify-end 推到右边和发送按钮挤一起
  expect(row.className).toContain('justify-between')
  // 触发器要自己把事件打开（动作行本身不铺满，但插槽内容必须可点）
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

it('弹层 Teleport 到 body（脱离输入栏的层叠上下文），由触发器 rect 算 fixed 定位', async () => {
  /**
   * 2026-10-23：这条用例原来断言的是 `bottom-full`（贴触发器往上弹的 absolute 定位）。
   * 那个形态的问题不是"方向不对"，而是**弹层活在输入栏的层叠上下文里**：
   * `composer-bar` 有 `backdrop-filter`（backdrop-blur），会自成层叠上下文，
   * 弹层 `z-50` 只在那条输入栏内部有效 —— 一旦主体里元素盖上来，表现是
   * "只看到露出输入框上方的一条缝"、点选项点到后面的消息。
   * 所以判据从"定位类名"换成"结构 + 定位方式"：必须在 body 下、在输入条之外、
   * 而且是 `fixed`（absolute 意味着又贴回了某个祖先）。
   */
  const { host } = mount(InterveneInput, { canCancel: false, canSend: true, models: MODELS })
  expect(pickerPopup()).toBeNull()
  await openPicker(host)
  const popup = pickerPopup()!
  // ① 不在输入条子树里（层的判据）
  expect(host.contains(popup), '弹层还挂在输入条子树里：会被 compose-bar 的层叠上下文困住').toBe(false)
  // ② 在 body 下（Teleport 的目标）
  expect(popup.closest('body')).toBe(document.body)
  // ③ fixed 定位（位置由触发器 rect 算，见 ModelPicker 的 measure()）
  expect(popup.className).toContain('fixed')
  // ④ 层级要盖过一切页面内容（底栏 / 侧栏 / 时间线）
  expect(popup.className).toMatch(/z-\[(100|\d{3,})\]/)
})

it('移动端：高度由"触发器上方可用空间"夹取 + 内部滚动 + 底部安全区内边距 + 每项 44px 命中区', async () => {
  const { host } = mount(InterveneInput, { canCancel: false, canSend: true, models: MODELS })
  await openPicker(host)
  const popup = pickerPopup()!
  const list = popup.querySelector('[role="listbox"]') as HTMLElement
  // ① 限高落在**弹层容器**（maxHeight 由 measure() 按触发器上方空间算，内联 style），
  //    列表自己只负责滚动 —— 旧实现把 `max-h-[min(60dvh,20rem)]` 写死在列表上，
  //    小屏上弹层顶会被裁到视口外（实测 -287px），首项直接看不见。
  //
  //    ⚠️ jsdom 里 `getBoundingClientRect()` 恒为 0，measure() 会主动放弃定位
  //    （见它的 `if (!r.width && !r.height) return`）—— 所以这里量不到 max-height。
  //    内联定位的真实数值归 e2e/layout（真浏览器，三视口）。这里只钉结构。
  const style = popup.getAttribute('style') ?? ''
  if (style) expect(style).toContain('max-height')
  expect(list.className).toContain('overflow-y-auto')
  expect(list.className).toContain('overscroll-contain') // 滚到底不带动背后的时间线
  expect(list.className).toContain('min-h-0') // 可收缩，否则内容把弹层撑破
  // ② iPhone 底部横条 / Home 指示器不压住最后一项
  expect(list.className).toContain('pb-[env(safe-area-inset-bottom)]')
  // ③ 触控最小命中区 44px
  for (const opt of popup.querySelectorAll('[role="option"]'))
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
  expect(pickerPopup()!.textContent).toContain('magpie') // 分组标题
  expect(pickerPopup()!.textContent).toContain('更强但更贵') // 选项说明

  const opt = [...pickerPopup()!.querySelectorAll('[role="option"]')].find(
    (o) => o.textContent!.includes('Y'),
  ) as HTMLButtonElement
  opt.click()
  await nextTick()
  expect(onSwitch).toHaveBeenCalledWith(MODELS[1].value)
  // 选中即关闭弹层（点完还杵着会挡住继续看时间线）
  expect(pickerPopup()).toBeNull()
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
  ;(pickerPopup()!.querySelector('[data-testid="model-picker-default"]') as HTMLButtonElement).click()
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
  expect(pickerTrigger(host)).not.toBeNull()
  await openPicker(host)
  expect(pickerPopup()!.querySelector('[data-testid="model-picker-empty"]')!.textContent).toContain('不提供可选模型')
  // 弹层外另有一句说明（弹层关着时它才有出处）
  expect(host.querySelector('[data-testid="composer-model-unsupported"]')).not.toBeNull()
})

it('弹层开着时锁住页面滚动，收起后解除（滚动不逃逸到正文）', async () => {
  const { host, app } = mount(InterveneInput, { canCancel: false, canSend: true, models: MODELS })
  await openPicker(host)
  expect(document.documentElement.classList.contains('overflow-hidden'), '弹层开着却没锁页面滚动').toBe(true)
  // 选中即收起 —— 锁也必须一起解除，否则整页从此滚不动
  ;(pickerPopup()!.querySelector('[role="option"]') as HTMLButtonElement).click()
  await nextTick()
  expect(pickerPopup()).toBeNull()
  expect(document.documentElement.classList.contains('overflow-hidden'), '弹层已收起但页面仍被锁住').toBe(false)
  app.unmount()
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

// ---- 焦点：点过框内的控件之后，接着敲的字必须进输入框 ----
//
// 实测过的真实流程（375×667 / 1440×900 都一样）：写一半 → 点模型选择器 → 关掉/选完 →
// 继续敲字 ⇒ **一个字都没进输入框**。原因是 mousedown 在 textarea 上先触发 blur、
// 焦点落到刚被点的 <button> 上，之后的键盘事件就与 textarea 无关了。
//
// jsdom 不实现真实的焦点转移（mousedown 默认行为），所以这里无法"复现 bug 再修"，
// 能守的是**机制本身**：这些控件必须声明 `@mousedown.prevent`（阻止焦点被抢），
// 点击行为本身仍要正常（不能在 prevent 的同时把 click 弄丢）。
describe('框内控件不抢输入框焦点（mousedown.prevent）', () => {
  // ⚠️ 用 `querySelector` 拿的是**第一个**匹配；同一份 DOM 里只有一个输入条，
  //    但"发送/中止"是 v-if/v-else 二选一 —— 两个态都要覆盖，所以分别挂载着查。
  const controls = (host: HTMLElement) =>
    [
      host.querySelector('[data-testid="model-picker-trigger"]'),
      host.querySelector('[data-testid="composer-add-image"]'),
      host.querySelector('button[aria-label="发送"]'),
      host.querySelector('button[aria-label="中止"]'),
    ].filter(Boolean) as HTMLElement[]

  it('「中止」按钮同样不抢焦点（运行中用户看着流式输出）', () => {
    const { host } = mount(InterveneInput, { canCancel: true, canSend: false })
    const cancel = host.querySelector('button[aria-label="中止"]')!
    const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true })
    cancel.dispatchEvent(ev)
    expect(ev.defaultPrevented, '中止按钮没拦 mousedown：点它会抢走输入框焦点').toBe(true)
  })

  it('模型选择器触发器 / 加图按钮 / 发送按钮都拦下 mousedown，点击照常生效', async () => {
    const onPick = vi.fn()
    const { host } = mount(InterveneInput, {
      canCancel: false,
      canSend: true,
      models: MODELS,
      canAttachImages: true,
      'onSwitch-model': onPick,
      'onUpdate:model': onPick,
    })
    for (const el of controls(host)) {
      // dispatchEvent 返回 false ⇔ preventDefault 被调用过
      const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true })
      el.dispatchEvent(ev)
      expect(ev.defaultPrevented, `${el.getAttribute('data-testid') || el.getAttribute('aria-label')} 没拦 mousedown：点它会抢走输入框焦点`).toBe(true)
    }
    // 弹层里的「Agent 默认」也一样：选完要能接着写
    await openPicker(host)
    const def = pickerPopup()!.querySelector('[data-testid="model-picker-default"]')!
    const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true })
    def.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
    ;(def as HTMLButtonElement).click()
    expect(onPick).toHaveBeenCalledWith('')
  })
})

/** 带 #actions 的 PromptInput（创建/发送按钮在框内**动作行**上） */
const WithActions = defineComponent({
  render: () =>
    h(
      PromptInput,
      { modelValue: 'x' },
      { actions: () => h('button', { class: 'pointer-events-auto', 'aria-label': '创建任务' }, '飞') },
    ),
})

/** 带 #lead 的 PromptInput（模型选择器在动作行**左侧**，与发送按钮同一行） */
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

/**
 * 动作行在**框内**、但在 textarea **下面**（正常流）—— 2026-10-19 的结构。
 *
 * 这一组断言守的是"文字永远不会被压住"这个**结构性**保证：
 * 旧形态里动作行 `absolute` 浮在 textarea 上，靠 padding 让位，两边数字必须手算对齐，
 * 一旦模型名变长/按钮变大就重新压到文字上（用户反馈的问题）。
 * 现在的判据不是"偏移量调对了"，而是"动作行根本不在 textarea 的盒子里"。
 */
describe('动作行在框内、textarea 下方（PC 与移动端同一套 DOM）', () => {
  it('动作行与 textarea 同属 composer-frame，且**不是**绝对定位', () => {
    const { host } = mount(WithActions)
    const actions = host.querySelector('[data-testid="composer-actions"]')!
    const t = ta(host)
    expect(actions.parentElement).toBe(frame(host))
    expect(t.parentElement).toBe(frame(host))
    // 旧形态的全部痕迹都不允许复活：绝对定位、让位 padding、右上角的 relative 容器
    expect(actions.className).not.toContain('absolute')
    expect(actions.className).not.toContain('bottom-')
    expect(actions.className).not.toContain('inset-x-0')
    expect(t.className).not.toContain('pb-[4.375rem]')
    // 整行可点（动作按钮自己再打开 pointer-events）—— 保持原来的插槽约定
    expect(actions.className).not.toContain('pointer-events-none')
  })

  it('没有 #lead / #actions 时不渲染动作行（空框不留一块空行）', () => {
    const { host } = mount(PromptInput, { modelValue: '' })
    expect(host.querySelector('[data-testid="composer-actions"]')).toBeNull()
  })

  // 按钮外框到框的**右沿与下沿都是 12px**：行在流里，横向靠 px-3、纵向靠 pb-3。
  // 这条能过，是因为"下沿溢出被吃掉"那个老坑已经随绝对定位一起去掉了。
  it('按钮右/下留白同值：行 px-3 + pb-3，行宽 = 框宽', () => {
    const { host } = mount(WithLead)
    const row = host.querySelector('[data-testid="composer-actions"]')!
    expect(row.className).toContain('px-3')
    expect(row.className).toContain('pb-3')
    expect(row.className).toContain('w-full')
    // 行内换行而不是压缩：窄屏下宁可让控件换到下一行，也不许把模型名挤没
    expect(row.className).toContain('flex-wrap')
  })

  it('只有 #lead（模型选择器）时同样两端对齐，且与动作按钮同处一行', () => {
    const { host } = mount(WithLead)
    const row = host.querySelector('[data-testid="composer-actions"]')!
    expect(row.className).toContain('justify-between')
    const lead = host.querySelector('[data-testid="lead-btn"]')!
    expect(lead.parentElement).toBe(row)
    expect(row.children.length).toBe(2)
  })

  it('详情页发送按钮同样在框内动作行（与新建任务页一致，不按断点分两套）', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true })
    const send = host.querySelector('button[aria-label="发送"]')!
    expect(send.parentElement!.getAttribute('data-testid')).toBe('composer-actions')
    expect(send.querySelector('svg')).not.toBeNull()
  })
})

// ---- 图片附件入口：显隐由**会话能力**决定 ----

describe('InterveneInput 图片入口', () => {
  it('能力为真时出现加图按钮，且在 #lead 行内', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    const add = host.querySelector('[data-testid="composer-add-image"]')
    expect(add).not.toBeNull()
    // 与发送按钮同一行（嵌在框内），不是另起一行
    expect(add!.closest('[data-testid="composer-actions"]')).toBe(
      host.querySelector('button[aria-label="发送"]')!.closest('[data-testid="composer-actions"]'),
    )
  })

  // 摆一个按了必然失败的按钮，比没有这个功能更糟：用户会反复试，
  // 而每次都以一句 agent 报错收场。
  it('能力为假（或未声明）时整个入口不渲染', () => {
    for (const props of [{ canAttachImages: false }, { canAttachImages: undefined }]) {
      const { host } = mount(InterveneInput, { canCancel: false, canSend: true, ...props })
      expect(host.querySelector('[data-testid="composer-add-image"]')).toBeNull()
    }
  })

  // 运行中输入走的是纯文本 stdin 注入通道，收不了图 —— 与后端口径一致。
  it('运行中（canCancel）不渲染图片入口', () => {
    const { host } = mount(InterveneInput, { canCancel: true, canSend: false, canAttachImages: true })
    expect(host.querySelector('[data-testid="composer-add-image"]')).toBeNull()
  })

  it('file input 的 accept 与后端白名单一致', () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    const input = host.querySelector('[data-testid="composer-image-input"]') as HTMLInputElement
    expect(input).not.toBeNull()
    expect(input.accept).toBe('image/png,image/jpeg,image/webp,image/gif')
    expect(input.multiple).toBe(true)
  })
})

// ---- 截图粘贴：飞书/企微截图后 Ctrl+V ----
//
// 这一段原本紧跟上一个 describe（结构上是“平铺”的，不是被包在它里面）——
// 2026-10-19 加"焦点"用例时误把多出的一个 `})` 留在了旧位置，导致**整个文件**
// Transform 失败（esbuild: Unexpected end of file）、composer 相关用例一条都没跑。
// 教训：vitest 里"某个 spec 文件解析失败"不会让其它文件红，很容易被当成"全绿"。
//
// ⚠️ 这里只能合成 ClipboardEvent（jsdom 没有真实剪贴板），所以守的是**接线**：
// paste 事件是否抵达 ImagePicker、图是否进了缩略图条、运行中是否给了人话解释。
// "真机截图确实能贴上"必须过 web 的自测脚本（AGENTS.md 的 UI 验收纪律）。

/** 造一个真实可用的 ClipboardEvent —— jsdom 的构造器不吃 clipboardData，只能 defineProperty */
function pasteEvent(dt: unknown): Event {
  const e = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(e, 'clipboardData', { value: dt })
  return e
}

/** 纯 items 形态（截图工具的真实形态，见 imageAttach.spec.ts 的同名说明） */
function itemsClipboard(...files: File[]) {
  return {
    files: [] as unknown as FileList,
    items: files.map((f) => ({ kind: 'file', type: f.type, getAsFile: () => f })),
  }
}

const pngFile = (name = 'image.png') =>
  new File([new Uint8Array([1, 2, 3, 4])], name, { type: 'image/png' })

/** 等粘贴处理（异步读图 + 渲染）落定 */
async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0))
  await nextTick()
}

describe('截图粘贴进输入框', () => {
  it('在有图入口的输入框里粘贴截图 → 图片进入待发列表', async () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    ta(host).dispatchEvent(pasteEvent(itemsClipboard(pngFile())))
    await settle()
    expect(host.querySelector('[data-testid="composer-images"]')).not.toBeNull()
    const thumb = host.querySelector('[data-testid="composer-images"] img') as HTMLImageElement
    expect(thumb.src.startsWith('data:image/png;base64,')).toBe(true)
  })

  it('粘贴不吞掉文本：不调用 preventDefault（否则普通文本粘贴一起废掉）', async () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    const e = pasteEvent(itemsClipboard(pngFile()))
    ta(host).dispatchEvent(e)
    await settle()
    expect(e.defaultPrevented).toBe(false)
  })

  it('只发一张图不写字也能发送（粘贴是"只发图"最常见的形式）', async () => {
    const sent: { text: string; images: unknown[] }[] = []
    const { host } = mount(InterveneInput, {
      canCancel: false,
      canSend: true,
      canAttachImages: true,
      onSend: (text: string, images: unknown[]) => sent.push({ text, images }),
    })

    ta(host).dispatchEvent(pasteEvent(itemsClipboard(pngFile())))
    await settle()
    ;(host.querySelector('button[aria-label="发送"]') as HTMLButtonElement).click()
    await nextTick()
    expect(sent).toHaveLength(1)
    expect(sent[0].images).toHaveLength(1)
    expect(sent[0].text).toBe('')
  })

  it('过大的粘贴图给出可读报错（不能静默丢弃）', async () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    const big = new File([new Uint8Array(6 * 1024 * 1024)], 'big.png', { type: 'image/png' })
    ta(host).dispatchEvent(pasteEvent(itemsClipboard(big)))
    await settle()
    const err = host.querySelector('[data-testid="composer-paste-error"]')
    expect(err).not.toBeNull()
    expect(err!.textContent).toContain('过大')
  })

  // 运行中输入走纯文本 stdin 通道，收不了图 —— 与 v-if 的判据一致。
  // 但粘贴是**用户主动动作**，静默失败会让他以为图已经贴上去了，必须给一句解释。
  it('运行中粘贴 → 不出现缩略图，但给出解释（不是静默失败）', async () => {
    const { host, pinia } = mount(InterveneInput, {
      canCancel: true,
      canSend: false,
      canAttachImages: true,
    })
    ta(host).dispatchEvent(pasteEvent(itemsClipboard(pngFile())))
    await settle()
    expect(host.querySelector('[data-testid="composer-images"]')).toBeNull()
    const toasts = useNotificationStore(pinia).toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].message).toContain('运行中无法追加图片')
  })

  it('不带图片的文本粘贴不产生任何图片痕迹', async () => {
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    const dt = { files: [] as unknown as FileList, items: [{ kind: 'string', type: 'text/plain', getAsFile: () => null }] }
    ta(host).dispatchEvent(pasteEvent(dt))
    await settle()
    expect(host.querySelector('[data-testid="composer-images"]')).toBeNull()
    expect(host.querySelector('[data-testid="composer-paste-error"]')).toBeNull()
  })
})

describe('粘贴提示的可发现性', () => {
  // 功能做在 paste 上（零点击），所以必须有字告诉用户"这里能贴图"。
  // 判据是输入形态（有精确指针=桌面端）而不是设备名 —— jsdom 的 matchMedia
  // 默认无匹配，所以这里显式桩成桌面端/触屏两种。
  const origMatchMedia = window.matchMedia
  afterEach(() => {
    window.matchMedia = origMatchMedia
  })

  function stubPointer(hover: boolean) {
    window.matchMedia = ((q: string) => ({
      matches: hover,
      media: q,
      addEventListener() {},
      removeEventListener() {},
    })) as unknown as typeof window.matchMedia
  }

  it('桌面端显示「Ctrl+V 粘贴」提示', () => {
    stubPointer(true)
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    expect(host.querySelector('[data-testid="composer-paste-hint"]')).not.toBeNull()
  })

  it('触屏端不显示该提示（手机上没有这条路，提示只是噪音）', () => {
    stubPointer(false)
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    expect(host.querySelector('[data-testid="composer-paste-hint"]')).toBeNull()
  })

  it('提示只是提示：触屏端粘贴依然能收到图（别把它当功能开关）', async () => {
    stubPointer(false)
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    ta(host).dispatchEvent(pasteEvent(itemsClipboard(pngFile())))
    await settle()
    expect(host.querySelector('[data-testid="composer-images"]')).not.toBeNull()
  })

  it('已有图时收起提示（说明已完成使命，不再占地方）', async () => {
    stubPointer(true)
    const { host } = mount(InterveneInput, { canCancel: false, canSend: true, canAttachImages: true })
    ta(host).dispatchEvent(pasteEvent(itemsClipboard(pngFile())))
    await settle()
    expect(host.querySelector('[data-testid="composer-paste-hint"]')).toBeNull()
  })
})
