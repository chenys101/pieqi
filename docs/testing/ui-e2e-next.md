# UI 真机验收：下个版本待办（2026-10-18 记）

> 背景与用法见 `docs/testing/ui-e2e.md`（本文件只记**还没做的事**）。
> 当前已落地：`web/e2e/` 两层（layout 数值断言 6 例 + visual 视觉基线 12 例），
> 全量 18 例连跑 4 次全绿。下面按性价比排序。

---

## P0 · 修「发送按钮被底栏盖住」（本轮测试发现的唯一真实可用性缺陷）

**现象**：矮视口下输入区装不下，`button[aria-label="发送"]` 的中心与四角被
`BottomNav` 的 `RouterLink`（`a.relative.flex`）命中 —— Playwright 的 `click()`
会直接报 "element intercepts pointer events"，**用户点不动发送按钮**。

**实测数字**（`sessionTimeline` / `composerBar` 的 getBoundingClientRect）：

| 视口 | 主区可用高 | 输入区高 | 结果 |
|---|---|---|---|
| 375×667（老 iPhone） | ~141px | 170px | 发送按钮被盖 |
| 1440×900（笔记本） | ~141px | 170px | 发送按钮被盖 |
| 375×844（竖屏手机） | ~318px | 170px | 可用 |

输入区 170px 的构成：`PromptInput` 的 `rows=2` textarea（实测约 149px）
+ `pb-[4.375rem]`（70px 让位行）。

**状态：实现已改（2026-10-18，并行会话），⚠️ 基线待更新。**

采用方向 1：`rows` 改 `1`（运行中保留 `2`）+ `autoResize` + `maxHeight=66`，
并给 `composer-bar` 加 `shrink-0`（硬约束：不加的话矮视口下被压掉的恰是发送按钮那一段）。
改动文件：`Textarea.vue` / `InterveneInput.vue` / `PromptInput.vue`。

> ⚠️ **验收未完成**：`e2e/baseline/layout-baseline.json` 里 `composerBar.height` 仍是
> 旧值 `170`、`composerInner.height` 仍是 `149`。按注释里的实测（66px ⇒
> composer-bar 74px / composer-inner 53px），几何断言**现在必然红**。
> 补最后一步：确认数字后跑 `npm run test:e2e:update-baseline`（视觉层同跑
> `update-snapshots`），再跑一遍全量确认 18 例全绿。

**改完的验收**：现有 layout 用例里 `composerSend` 的可操作性断言会立刻验证；
另建议补一条"点击发送后断言请求已发出"（见 P1）。

---

## ~~P0 · 给 Dashboard 建基线~~ ✅ 已完成（2026-10-18，提交 `eb73424`）

Dashboard 是首页，"待审批组 + 本周概览"是最高频首屏，此前**零基线**。
现在 `mode=dashboard` 挂真实 `DashboardPage`；layout 加专属断言三支
（待审批块存在 / 首张卡「允许一次」一屏内可见 / 本周概览渲染），
visual 加 6 张基线。全量 **27 例**（layout 9 + visual 18）连跑 2 次全绿。

顺带修了两处夹具缺陷 —— 都是"基线守住了一个假状态"的来源：
- completed 任务缺 `finished_at`（`insight.ts:97` 的判据是 `status==='completed' &&
  finishedAt`）⇒ 概览显示「完成任务 0」，而夹具里明明有 8 个 completed；
- `auth/status` 返回 `bound:false` ⇒ `loadBootContext` **异步**置横幅，
  把全屏下移 12~36px。夹具在 `beforeEnter` 里清空 banner 没用（之后又被设回来），
  改成 mock 回 `bound:true` 从源头消除。

**过程中踩到的坑（值得记）**：断言写成
`locator('[data-testid=...]').or(getByText('本周'))` 会被 strict mode 拒绝
（"resolved to 3 elements"——头部那句「…· 本周 8 个任务已完成」也含"本周"），
而报错文本很像"元素没渲染"，极易误判成功能坏了。测"某区块在不在"必须用唯一锚点。

---

## ~~P1 · 补「真点击」用例~~ ✅ 已完成（2026-10-18，`e2e/interaction/`）

新增第三层 `interaction`（4 例）。全量 **31 例**（layout 9 + visual 18 + interaction 4）全绿。
与前两层的判据不同：前两层量"渲染结果"，这一层量"**行为**"—— 按钮看得见、点得到，
证明不了 onClick 绑没绑、参数对不对、请求发没发。

| 用例 | 断言要点 |
|---|---|
| 「允许一次」→ 发请求 | 点前 0 请求；点后 POST intervene，`kind=decision`/`choice=approve`/带 `decision_id` |
| **L3 二次确认** | 第一下**不发请求**（负向，安全关键）；出现「确认允许」；再点才发 |
| 「加载更早的 N 轮」 | 点前 `turn-group-1` 不存在；点后挂载且总 Turn 数增加；**点一次不得到顶**（12 轮下一次点击后第 1 轮仍不在 DOM，负向）；连点到顶后才出现第 1 轮、按钮消失 |
| 输入 → 「发送」 | 点前 0 请求；点后 `kind=append_prompt` 且 `text` 与输入**逐字一致** |

**观测手段**：mock 后端新增 `/__writes` 流水账 —— **记录写请求但不改数据**。
判据见 `mock-api.mjs`：一旦 mock 真的改夹具，"页面为什么变成这样"就有两个来源
（点击 / mock 副作用），断言与因果会被悄悄换掉。状态推进由用例自己驱动 store，
与生产里"WS 推回快照"等价。

### 本批踩到的四个坑（都写进了代码注释）

1. **`approval-list` 的直接子节点就是卡片**（实测 childCount=2）。写成 `> div > div`
   会选中卡片内部子块，点击目标整个错位 —— 表现为"点了 L3 却发出 `decision_id=dec-l2`"。
2. **跨元素的正则匹配不可靠**：卡片文本由多个子元素拼成，`hasText: /^需要授权\s*L2/`
   直接超时。改用 `filter({ has: getByText('L2 · 执行命令', { exact: true }) })`。
3. **`DELETE /__writes` 走进了 GET 分支**：路由先判路径再判 method，于是"清空"永远
   返回旧流水账、永远清不掉，`beforeEach` 报的却是"有别的用例在并发写"（指错方向）。
   改为**先判 method 再判路径**，并让清空用 `expect.poll` 收敛。
4. **`InterveneInput` 的 `onSend` 只 `emit('send')`**：真正发请求的是父组件（生产里是
   SessionPage）。夹具页不接这个事件，"点发送"就静默无效，表现只是超时。已补上。

**变异验证**：把 `ApprovalCard.act()` 的二次确认条件临时短路 → L3 用例立刻变红
（`waitFor` 超时）；恢复后全绿。证明这条负向断言守的是真约束。

---

## P1 · 线上复量接线（AGENTS.md 第 2 条纪律的后半段）

当前 `baseURL` 虽然是参数化的，但夹具页 `/__e2e/detail` **只在 dev 入口存在**，
所以这套用例**不能**直接指向线上。部署后复量需要另写一组只读用例：

- 只打真实页面（`/dashboard`、`/sessions/<真实任务 id>`），不依赖夹具；
- **只读**：不点审批、不提交、不发任何写请求（线上没有 405 兜底）；
- 断言的指标用"相对基线放宽的阈值"（线上数据量不同，DOM 节点数不可直接比），
  但**横向溢出、元素遮挡、主行动按钮可见**这三条与数据量无关，可以一字不改地复用到线上。

⚠️ 线上复量必须用独立凭据与只读身份，并且**先在小流量上跑一次**再接入日常流程。

---

## P2 · 扩展场景与视口

- 场景：设置页（`/settings`）、新建任务页（`/tasks/new`）—— 两者都含表单，
  是"标签挤压 / 按钮换行 / 输入框被键盘顶走"的高发区。
- 视口：补 320×568（最小 iPhone）与 1024×768（iPad 横屏）；桌面补 1920×1080。
  ⚠️ 每加一个视口都要**重拍全部视觉基线**（`npm run test:e2e:update-snapshots`）。

---

## ~~P1 · 设置页 + 新建任务页场景~~ ✅ 已完成（2026-10-18，`7a58ab0` / `804d514`）

两页此前**零 e2e 覆盖**，现在各 3 视口 layout + 6 张 visual 基线。
全量 **58 例**（layout 18 + visual 36 + interaction 4）连跑 2 次全绿。

断言都按"用户能不能用"倒推，不是"控件在不在"：

| 页面 | 关键断言 |
|---|---|
| 设置页 | 四组标题齐全；**settings 驱动的开关必须可用**；L2/L3 恒为「不可配置」 |
| 新建任务页 | Agent 已选默认值；空输入时创建按钮 **disabled**（负向）；填完变可用 |

### 两条"断言空转"的教训（都做过探针验证）

1. **设置页**：第一版写成"存在任意一个可用开关"—— 实测**空转**。
   用 `page.route` 模拟 `getSettings` 失败后，6 个开关里仍有 1 个可用
   （「减弱动效」是本地偏好，不依赖该请求），所以加载失败也能通过。
   改为"前两个可用开关必须可见"（组① 的 L0/L1）后，同一探针立刻变红。
   > 通用判据：**降级态的断言必须证明它真的能红**，否则它守的是空气。
2. **新建任务页**：`data-testid="agent-select"` 落在 `Select` 的**外层 div** 上
   （内部才是原生 `<select>`），直接取 `inputValue` 会报
   "Node is not an <input>, <textarea> or <select>" —— 错误信息很容易被误读成
   "选择器没渲染"。要再往下点一层。

### 视觉基线里发现的真实问题（未修，待定）

`newtask-desktop-light.png` 里，右下角浮着一条「正文已铺满。把右侧竖线左右拖动可调宽度，
双击复位。」的提示条，**压在「项目」行上**。它是 `ContentColumn` 的拖宽提示，
在新建任务页这种没有正文列的场景里没有意义，却会遮挡表单。
建议：该提示只在**会话详情页**（真的能拖宽的地方）出现。

---

## ~~P2 · 工具语义表~~ ✅ 已完成（2026-10-18，`caf0295`）

三个 Agent 的高级能力（goal / todo / plan / subagent / jobs）全是 tool_call，
此前一律渲染成「🔧 工具名 + 参数」的通用卡 —— 用户在一堆 BASH/READ 里看不出
"目标变了""计划更新了"。现在认得出的走语义卡：
`◎ 目标 …`、`☑ 计划清单 3 项 · 2 完成`、`▤ 方案 第一步…`。

**两条判据（写死在代码注释里）**：

1. **本表只管展示，绝不参与审批。** 审批依据是 `riskLevelKinds`（按 ACP ToolKind），
   而 `create_goal` / `todo_write` 在 `toolkind_map.go` 里**刻意留在 other（L2 强制人工）**。
   把语义表接到放行逻辑上 = 让"卡片画得好"变成"权限更宽"。
2. **认不出就不猜。** 表外工具一律走通用卡，不做启发式归类。
   单测钉了 `get_goals_for_project` / `taskkill` / `replan_file` / `todoist_sync`
   必须返回 undefined；变异验证（改成按子串猜 goal）后测试立刻变红。

### ⚠️ 本批发现的"基线假象"（必须记）

`--update-snapshots` 在图片**匹配**时**不重写文件**。改完夹具跑"重拍基线"看着
全绿，但截图字节一字未变 —— 一直是旧图（旧图与当时页面确实一致，因为语义卡
当时还没进夹具）。于是出现「断言通过、截图里看不到新功能」的假象。

**判据**：改了夹具里的可见内容后，要确认基线文件的**时间戳/字节真的变了**，
不能只看 `N passed`。数据变了的信号是字节数变化（detail desktop 59k → 70k）。

---

## P2 · 让 LCP 真正可用

现状：多数场景 `lcpMs: null`（拿不到 Largest Contentful Paint 条目），断言自动跳过 ——
首屏代价目前主要靠 DOM 节点数与几何断言兜底。**不要把它解读成"首屏很快"。**

排查方向：`PerformanceObserver` 是否需要在 `page.addInitScript` 里提前注册
（现在是在页面加载后读 `performance.getEntriesByType`，若条目被早期清理就拿不到）。

---

## P3 · 已知限制（不是待办，是必须知道的边界）

1. **跨机/跨通道基线会失配**：Windows+Chrome 生成的基线放到 Linux+Chromium CI 会全红。
   换环境必须重拍；`E2E_CHANNEL` 与 `maxDiffPixelRatio` 的调整都要同步重拍。
2. **全局横幅会让整屏下移 12~36px**：`App.vue` 的横幅由 auth 状态驱动，
   夹具页已显式清空 `appStore.banner`，但真实页面在两种状态下不可比。
3. **`src/main.ts` 一旦改写法要重新确认夹具入口**：`e2eEntryPlugin` 靠
   `index.html` 里的 `/src/main.ts` 字样替换入口，改不到就抛错（刻意响亮）。
4. **改 `tailwind.config.ts` 或夹具后必须重启 dev server**：
   `reuseExistingServer` 会复用旧进程，而 Tailwind 的内容扫描不会重扫 ——
   表现为"我的样式没生效"，且不报错。
