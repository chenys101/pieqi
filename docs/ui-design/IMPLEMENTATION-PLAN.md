# 落地实施方案：从原型到实现

> 输入：`docs/ui-design/prototype/index.html`（设计目标态）+ `UI-DESIGN-SPEC.md`
> 产出：可执行的落地路径、工作量边界、需要拍板的决策
> 日期：2026-09-10

---

## 0. 一句话结论

原型的**视觉与交互可以直接落地**，但有一处**模型级缺口必须先解决**：

> **后端没有「机器人」这个实体。** 现在只有一份**全局飞书应用凭据**（单例），
> 而原型承诺的是「多台机器人 + 角色 + 能力 + 预设提示词」。

所以落地不是"把 HTML 翻译成 Vue"，而是 **先补模型、再补 UI**。

差距分三类：

| 类型 | 含义 | 处理方式 |
|---|---|---|
| **A 纯前端** | 现有接口够用，只缺结构与组件 | 可直接开工 |
| **B 需后端新增** | 接口 / 模型不存在，前端无法对接 | 先定模型，再动 UI |
| **C 演示件** | 原型为演示造的假入口 | **不带走** |

---

## 1. 现状核对（每条都有证据）

### 1.1 设置页

| 原型 | 现状 | 差距 |
|---|---|---|
| 4 组：审批与自动化 / 连接与账号 / 外观与偏好 / 数据与关于 | ✅ **已落地（切片 1 + 3 + 4 + 5）**：`SettingsPage.vue` 已重排为 760px 单列分组卡，**四组全部渲染**；原 6 段里的 PWA 安装按钮按 D5 移除（改情境化提示条 + iOS 静态说明行），其余内容已并入组② | — |

### 1.2 IM 机器人（本轮验收内容）

| 原型 | 现状（证据） | 差距 |
|---|---|---|
| 机器人**列表**（`#agentList`），管理员卡 unset/ready 双态 | 无任何列表。`internal/model/types.go` 只有 `Channel` / `Message`，无 bot 实体 | **B：需新增模型** |
| 一键创建 = 扫码弹层**四态**（gen / scan / busy / dead + 600s 倒计时 + 失效重生成） | `LarkConfigModal.vue:46-81` 只有「点按钮 → 出码 → 轮询」，无 gen / busy / dead，无倒计时、无过期重生成 | **A：改造**（接口够用，`expire_in` 已在 `larkreg.go:114` 返回） |
| 手动配置是**页脚次要入口** | 现为**并列页签**（`LarkConfigModal.vue:133-148`） | A：改造 |
| 外网：入口 `disabled` + **就地**说明原因 | 现为**点开后才整屏 ⚠️**（`LarkConfigModal.vue:124-126`） | A：改造（判定已具备：`larkreg.ts:13-23` 返回 `status===403`） |
| **预设提示词** | 后端无此概念；`/larkreg/start` 不接任何参数（`larkreg.ts:27`、`larkreg.go:86-94`） | **B：需新增存储** |
| 隧道 / API 特权（`.cap-tag`） | 特权实为**人**：IM 命令校验 openid（`bridge.go:272`）；而外网 `POST /api/tunnel/start` 只看飞书 UA、**匿名即可领到 token**（`router.go:170-173`） | **B：需补两层** —— ① 按机器人区分（`Message` 缺 `BotID`）② 关掉匿名 bootstrap → **§4.2 / §4.2.1 已定案** |

### 1.3 主题

| 原型 | 现状 | 差距 |
|---|---|---|
| 三态（跟随系统 / 浅色 / 深色），**浅色优先** | ✅ **已落地（切片 4）**。原状是 `:root`(暗) + `html.light`(亮，**从未被激活**的一段死代码)；现为 `:root`(亮) + `html[data-theme="dark"]`(暗)，三态偏好由 `data-theme-pref` 与生效值 `data-theme` 分离承载 | 语言 / 时区**不做**（无 i18n，时区随服务端本地）|

### 1.4 其余差距

| 项 | 现状 | 差距 |
|---|---|---|
| 侧栏底部 | 「设置」与「连接状态」**两行**（`AppSidebar.vue:156-176`） | ✅ **已落地**：合并为一行 `设置 ● 连接正常` |
| 原生 `confirm()` | 仍在用（`AppSidebar.vue:56`） | ✅ **已清零**（2026-09-11）：侧栏 / `SessionPage` / `TurnCard` 全部改为内联确认条，全仓库已无原生 `confirm()` |
| 组件库 | 8 件：`Modal` / `Button` / `Badge` / `Drawer` / `Card` / `EmptyState` / `Spinner` / `ToastHost` | ✅ **已补齐** `Switch` / `SettingRow` / `SegmentedControl` / `CapTag` / `SetGroup`（另新增 `BottomNav` / `InstallPrompt`） |
| 审批 L0–L3 | 后端只有 `auto_approve_tools`（**工具名列表**，`config.go:212`）与 `permission_mode` | ✅ **已落地（切片 3）**：`internal/core/settings_store.go` 新增风险分级映射；L2 / L3 为硬锁 |
| 免打扰时段 / 事件保留上限 / 诊断日志导出 / 语言 / 时区 | 后端均无（语言时区可纯前端） | ✅ **已落地（切片 5）**，语言 / 时区**不做** |

---

## 2. C 类：明确不带走的原型演示件

1. **工具条「网络」切换**（`#netSwitch`）→ 真实内网判定取 `GET /api/larkreg/status` 的 403
2. **「模拟扫码」假入口** → 真实由 `/poll` 轮询感知
3. 工具条里的**视口 / 主题**切换副本 → 只保留设置页的正式入口
4. `data-soon` 的 **14 处占位按钮** → 逐个决策（真做 / 隐藏），**不允许留"点了没反应"**

---

## 3. 建议的切片顺序

每片**独立可验收**，且都跑一遍原型回归（`_validate.mjs` + `_verify.mjs`）守目标态不退化。

| 切片 | 内容 | 类型 | 依赖 |
|---|---|---|---|
| **0** | 定模型（下面 5 个决策） | B | — |
| **1** ✅ | 设置页骨架 + 基础组件（4 组容器、`SettingRow` / `Switch` / `SetGroup`、只读项） | A | — |
| **2a** ✅ | IM 机器人 — **后端模型层**（`Bot` / `BotStore` / `/api/bots` / `Message.BotID` / Q2 校验） | B | 切片 0 |
| **2b** ✅ | IM 机器人 — **多实例投递与回执路由**（`BotIdentity` / `WebhookMux` / `Bridge` 按 bot id 回执 / 控制器逐台建实例）—— 让 Q2 真正生效 | A+B | 切片 2a |
| **2c** ✅ | IM 机器人 — **前端**（机器人列表接 `/api/bots`、扫码弹层改造） | A | 切片 2b |
| **3** ✅ | 审批与自动化（L0–L3 + 免打扰） | A+B | 切片 0 |
| **4** ✅ | 外观与偏好（**主题三态 + 默认反转**、减弱动效）—— 语言 / 时区**不做**（无 i18n，时区随服务端本地） | A | — |
| **5** ✅ | 数据与关于（保留上限、诊断导出、版本） | A+B | — |
| **6** ✅ | **移动端外壳**（底栏 3 项 + 任务浏览器页 + PWA 情境化安装提示）—— **D5 定案后新增** | A | — |
| ③ ✅ | `/api/tunnel/start` **收口**（从"仅 UA 判定"改为 `ExternalAuth + TunnelOpGate`）—— 见 D3-c | B | 切片 0 |
| **7** ✅ | **变更反馈三档形态**（`1fr 420px ⇄ 1fr 46px` + 46px 贴边条 + 收起 + 四个 Tab + 移动端整屏分段 + 1280 断点）—— **不在原计划内**，见 §7.3 | A | — |
| **8** ⬜ | Timeline 按 Turn 分组 + 本轮变更摘要卡（§7.4 的 C 条）—— **不在原计划内** | A | — |

> **切片 7 / 8 是"计划外产物"**：SPEC 早已定案，但当初排切片时盯的是
> 设置页 / 机器人 / 移动端 / 仪表盘，Session 这一整块**没有进过表**
> （全文搜「变更反馈」在原计划里是 0 处命中）。它们和仪表盘是同一类差距，
> 只是更晚才被发现 —— 因为那两处看上去"都有个能用的东西"。

> 建议**先做切片 1 + 4**：纯前端、无模型依赖，能最快看到"原型长进产品里"，同时验证前端组件库的复用率。
> 切片 2 是价值最高但依赖后端的一片，作为第一个**端到端**切片。
> 切片 6 同样纯前端；与 1 / 4 只有一处交接 —— 设置页「数据与关于」组要放 PWA 的 iOS 说明行。

---

## 4. 决策记录与待定项

### 4.0 已定（2026-09-11）

| # | 决策 | 落地影响 |
|---|---|---|
| **D1** | **复数** —— 机器人是多条记录，不是单例 | 后端新增 bot 存储 + `/api/bots`；UI 按列表渲染 |
| **D2** | **预设提示词跟随 bot 记录** | 需要一套完整的 bot 模型 → 见 §4.1 |
| **D3** | **特权属于人（绑定的飞书账号），机器人只是承载者**；`role != admin` 的机器人收到特权命令回「无权操作」；并把外网隧道启动收紧到需持 token | 改 `bridge.go` 判定 + `Message` 增 `BotID`；`/api/tunnel/start` 并入需 token 的组 → 见 §4.2 / §4.2.1 |
| **D4** | **接受现有用户屏幕变白** | `:root` 改为浅色；暗色移入 `[data-theme="dark"]`；`tokens.css:21` 的 `html.light` 死类删除 |
| **D5** | **底栏本次一起做**（3 项）；PWA 安装改**情境化提示** + iOS 静态说明 | 新增切片 6；设置页不再放常驻安装项 → 见 §4.3 |

> D4 执行细节：现在是 `:root`(暗) + `html.light`(亮，**从未被激活**)。反转后应为
> `:root`(亮) + `[data-theme="dark"]`(暗) + `[data-theme="light"]`(显式亮)。
> 三态偏好（system / light / dark）由 `data-theme-pref` 承载，与生效值 `data-theme` 分离。

### 4.1 D2 展开：Bot 模型（后端新增）

**先把两个概念分开** —— 这是整块落地里最容易混的一处：

| 概念 | 是什么 | 存在哪 |
|---|---|---|
| **飞书应用** | 飞书开放平台上的对象（app_id / app_secret / 事件回调） | 飞书侧 + 本地凭据文件 |
| **机器人** | pieqi 侧的一条**绑定记录**：引用某个渠道应用 + 承载本地属性（角色 / 预设提示词） | `~/.pieqi/bots/`（新增） |

现状是**单例**：`~/.pieqi/lark_credentials.json`（`ChannelConfig`，`larkreg/credentials.go:20-26`）。
复数改造建议拆成两个文件 —— **元数据与密钥分离**：

```
~/.pieqi/bots/
  index.json        # []Bot —— 可安全下发前端
  <bot_id>.json     # ChannelConfig（含 app_secret，0600，永不进 API 响应）
```

`Bot` 字段草案：

| 字段 | 用途 | 备注 |
|---|---|---|
| `id` | 本地稳定 id | 生成 |
| `channel` | lark / wecom / wechat | 创建时定死（**属性，不是分组**） |
| `name` | 显示名（`飞书 · 管理员`） | 可改 |
| `role` | `admin` / `member` | **首个绑定自动 admin**（规则，不是配置项） |
| `sys_prompt` | 预设提示词 | ← **D2 的落点** |
| `app_id` | 渠道侧应用标识 | 可下发 |
| `created_at` | 排序（原型按创建顺序） | — |

接口草案：

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/api/bots` | **ExternalAuth**（外网也要能看） | 列表，不含 secret |
| POST | `/api/bots` | BindOpGate（仅内网） | 创建；扫码 / 手动成功后落记录 |
| PATCH | `/api/bots/:id` | BindOpGate | 改名 / 提示词 / 角色 |
| DELETE | `/api/bots/:id` | BindOpGate | 解绑 |

> ⚠️ **一处必须改的鉴权**：现在 `/api/larkreg` 五个端点 **全部**挂 `BindOpGateMiddleware`
> （仅内网，`router.go:188-196`）。沿用这个策略的话，**手机 / PWA 上永远看不到机器人列表**（外网 403）。
> 所以读接口要挂 `ExternalAuthMiddleware`，写接口保留 `BindOpGate`。

### 4.2 D3 已定（2026-09-11）：特权属于**人**，机器人**承载**

**结论（一句话）**：

> **特权归属于绑定的飞书账号（人）；机器人是承载与转达者。**
> 特权命令通过 ⟺ `发送者 openid == 绑定管理员` **且** `消息由 admin 机器人收到`（两者是「与」）。

→ 实质是 **D3-b 的形态 + D3-a 的归属**：不引入"机器人自己的权限"，
但只要 `role != admin` 的机器人收到特权命令，一律回「无权操作」。
UI 的 `.cap-tag` 保留，语义定为「**承载**管理员能力」。

**依据（四条已核事实）**：

1. 现有「管理员」是**人**：`Binding{OpenID, UserID, Nickname, BoundAt, Active}`，
   注释明写 *exactly one Feishu account*，`Match()` 是 openid 精确匹配（`auth/binding.go:12-27,90-97`）。
2. **IM 隧道命令校验的是发消息的人**：`msg.Channel != lark || !adminBinding.Match(msg.UserID)`
   → 「⛔ 仅绑定的飞书管理员可操作隧道」（`core/bridge.go:270-275`）。**这条保留，是 Q1 的落地。**
3. **HTTP 隧道接口不校验人**：`TunnelOpGateMiddleware` 只看「非内网 + 飞书移动端 UA」
   （`auth/middleware.go:98-119`）；`TunnelManager.Start` 不接收任何身份参数（`auth/tunnel.go:186`）。
4. **客户端身份头不可信**：`X-Feishu-Openid` 由前端自行注入、**可伪造**，所以已被显式弃用
   —— 连 `openid` 都不敢回给外网（`api/binding.go:51-58`：*"knowing the OpenID is sufficient
   to pass it"*）。**任何"HTTP 层再验一次身份"的方案都不能建立在请求头之上。**

**Q2 的落地**：`飞书 · 项目通知`（member）收到「隧道」→ 回「⛔ 无权操作：该命令仅管理员机器人受理」。

> ⚠️ **一处接口缺口（实现前必须知道）**：`model.Message` 只有
> `Channel / ChatID / UserID / UserName / Content / MentionBot / Raw`（`model/types.go:13-21`），
> **没有"这条消息是哪个机器人收到的"**。而 Bridge 用 `receivers []channel.MessageReceiver`
> 聚合多个渠道实例（`bridge.go:39`），**无法区分同一渠道（lark）下的多台机器人** ——
> 这正是多机器人落地时第一块要动的砖。
> 做法：给 `Message` 增 `BotID`（由各 receiver 在投递时填充），判定改为
> `msg.BotID == adminBotID && adminBinding.Match(msg.UserID)`。

### 4.2.1 Q3 的落地（D3-c，已定：**收紧**）

**洞在哪**：`POST /api/tunnel/start` **只挂 `TunnelOpGateMiddleware`**（`router.go:170-173`）——
外部请求只要有**飞书移动端 UA**，**无需任何身份或凭据即可自取一枚隧道 token**。
而这枚 token 恰恰是外网全部 API 的**唯一凭据**。

> **核心论证**：token 本质是**管理员的代理凭据**，它的可信度**完全依赖"只经 IM 交付给管理员"**
> （IM 命令那条路有 `adminBinding.Match` 把关，`bridge.go:272`）。
> `/start` 的匿名 bootstrap 破坏了这一前提 —— 它把"凭据签发"从**身份已验证的通道**
> 挪到了**任何人可伪造的 UA 声明**上。

**修法（最小改动，且不丢功能）**：把 `/start` 从「仅 UA 门」并入 `tunnelMutate` 组
（`ExternalAuth + TunnelOpGate`），并确立 **IM 命令为唯一的"首次启动"路径**。

| 路径 | 改前 | 改后 |
|---|---|---|
| IM 命令「隧道」 | 校验人 → 直接 `TunnelManager.Start()`（`bridge.go:289-291`） | **不变** —— 它就是 bootstrap（不经 HTTP） |
| `POST /api/tunnel/start` | 仅 UA（匿名可发起） | 需**有效 token**（`ExternalAuth`）+ UA |
| `/stop` `/reset` `/renew` | 需 token + UA | 不变 |

- **原注释的死锁担子可以卸掉**：`router.go:163-166` 担心"首启需要 token → 死锁"，
  是因为当时没把 **IM 命令视为 bootstrap** —— 它直接调 `TunnelManager.Start`，根本不走 HTTP。
- **功能无损**：PWA 面板在**已有 token**（从 deep link 进来）时仍可重启 / 停止 / 续期 / 轮换；
  token 已过期时回落 IM 命令 —— IM 侧支持**启动 / 停止 / 续期**三类（`bridge.go:314-344`，无 reset）。
  少的那一个并不缺：`RenewToken` 在「token 已过期、隧道仍活着」时会**签发全新 token**
  （`auth/tunnel.go:396-438`），已覆盖 `/reset` 的换码场景，且域名不变。
- **不需要**再引入配对码 / 身份 cookie：**token 即身份代理**（只经 IM 发给管理员），链路已闭环。

**由此产生的 UI 影响**：设置页「外网隧道」段的**起始动作**（开启隧道）不再是一个按钮，
改为引导语「在飞书里对**管理员机器人**发送「隧道」」——
与本轮 IM 机器人「禁用即就地说明」的原则同源：**不给一个点下去注定失败/不安全的控件。**

**明确不本次做（记为遗留）**：
- 管理员**转移 / 回收**未建模：多机器人下"哪台是 admin"目前只是 `role` 单字段。
- 真正"在 HTTP 层按 openid 校验"需要**不依赖可伪造头**的身份源；飞书 JSAPI 要求固定域名，
  与动态 `trycloudflare` 域名冲突 → 单独的安全议题。

### 4.3 D5 已定（2026-09-11）：底栏本次做，PWA 改情境化

**现状**：

- 移动端**没有底栏** —— `MobileLayout.vue:22-68` 是「TopBar（汉堡）+ 抽屉侧边栏」，
  抽屉里直接装 PC 的 `AppSidebar`；原型的底栏是 **3 项**（首页 / 任务 / 设置，
  `prototype/index.html:1917-1921`），属于**新设计**，不属于设置页这一片。
  ⚠️ 原型里移动端是**直接把侧栏 `display:none`**（`index.html:211`）、改用底栏导航的 ——
  所以底栏是**替代**抽屉，不是补充。
  ⚠️ 原型里 `#page-tasks`（移动端任务浏览页）**唯一入口就是底栏「任务」**
  （`index.html:1257`）—— 底栏不做，这一页就没有落点。
- PWA 安装入口现在是设置页的**独立一段**（`SettingsPage.vue:71-78`）。
- `canInstall` 只在 `beforeinstallprompt` 触发后才是 true（`usePwa.ts:21-25`）
  → 那个按钮**时而出现时而消失**。

**关键判据**：**一个"时有时无"的按钮不该放进设置页** —— 设置项应当稳定；
而且 PWA 安装是**一次性动作**，不是偏好。

| 选项 | 做法 |
|---|---|
| a | 进「外观与偏好」组 |
| b | 进「数据与关于」组（"关于这个 app"） |
| **c**（定案） | **情境化**：`beforeinstallprompt` 触发时出一条可选提示（横幅 / Toast + 安装按钮），设置页不放常驻项；`isStandalone` 时彻底不出现 |
| d | 移动端底栏「更多」菜单里（依赖底栏先落地） |

**定案：c**（情境化提示）—— 设置页**不再放常驻安装项**；
iOS 另在「数据与关于」放一行**纯文字**说明（是说明不是按钮 → 不会时有时无）。
a / b 之所以落选：**换位置不改变不稳定性**，只是把不确定性搬到另一个组里。

**落地要点**（D5 拆成两件，已分别定案）：

| 项 | 做法 | 落点 |
|---|---|---|
| **底栏 3 项**（Q4：本次做） | 替代抽屉（原型移动端已隐藏侧栏）；`#page-tasks` 成为移动端任务页，与侧栏树共用数据源与折叠状态 | `layouts/MobileLayout.vue` + 新增底栏组件 |
| **PWA 提示条** | 仅 `canInstall` 为真时出现，带「安装 / 暂不」；`isStandalone` 时彻底不出现 | 挂在应用外壳（与底栏同层） |
| **iOS 说明行** | 一行静态文字（Safari 分享 → 添加到主屏幕） | 设置页「数据与关于」组 |

> 建议**桌面端不放安装提示**：`beforeinstallprompt` 在桌面也会触发，但场景弱，且会侵入桌面布局。

> 📌 **为什么之前读着不清晰**：D5 把两件互不相干的事压进了一个编号 ——
> **Q4 是结构问题**（移动端导航外框要不要换成底栏，顺带决定 `#page-tasks` 的生死），
> **Q5 是时机问题**（一个由浏览器决定出现与否的按钮，该放在一个应当稳定的页面上吗）。
> 两者唯一的共同点是"都跟移动端入口沾边"。**应当拆开各自决策。**

### 4.4 已落地的切片（2026-09-11）

**切片 1 — 设置页骨架 + 基础组件**

| 文件 | 内容 |
|---|---|
| `styles/tokens.css` | 按 SPEC §3.1/§3.2 重建：三层表面 / 三级边框 / 四级文字 / accent 三态 / 语义色 / **四档阴影**，深浅两套值 |
| `tailwind.config.ts` | 补全色名映射；`darkMode` 绑 `[data-theme="dark"]`；阴影走 token；保留 `elevated` / `muted` 兼容别名，现有页面无需改类名 |
| `components/ui/SetGroup.vue` · `SettingRow.vue` · `Switch.vue` | **新增** 三个基础构件（`SettingRow` 另有 `locked` / `stack` 两个变体） |
| `pages/SettingsPage.vue` | 重排为 760px 单列分组卡 |

**切片 4 — 外观与偏好（主题三态 + 默认反转）**

| 文件 | 内容 |
|---|---|
| `styles/tokens.css` | `:root` 反转为浅色（D4），深色移入 `html[data-theme="dark"]` |
| `composables/useTheme.ts` | **新增**：偏好 / 生效值分离、`localStorage` 持久化、监听系统主题、同步 `theme-color` |
| `composables/useReduceMotion.ts` | **新增**：减弱动效偏好（与主题同构但**不合并** —— 配色与时序是两件独立的事） |
| `styles/index.css` | 减弱动效的全局降速规则：用户偏好与 `prefers-reduced-motion` **两条来源合一** |
| `index.html` | 内联**同步**脚本做首屏防闪（与 `useTheme` 共用 key 与解析规则）；`<html>` 不再写死 `data-theme` |
| `public/manifest.webmanifest` | `theme_color` / `background_color` 由深色改浅色 —— 否则 PWA 启动屏与应用脱节 |
| `components/ui/SegmentedControl.vue` | **新增**：分段单选，`aria-pressed` 为唯一真相 |
| `pages/SettingsPage.vue` | 补组③（主题 + 减弱动效）；语言 / 时区**不做** |

**两个实现要点**（都是踩过才写下的）：

1. **`MediaQueryList` 不是响应式对象。** 若 `computed` 直接读 `mql.matches`，系统主题变化时**不会重算** ——
   「跟随系统」会永远停在首次解析的值上（首版实测：系统切深色，界面纹丝不动）。
   正解：把 `matches` 镜像进一个 `ref`，让 `computed` 依赖它。
2. **`data-theme` 必须始终显式存在**，否则 `dark:` 变体与 `html[data-theme=...]` 规则**全部命中不了** ——
   这正是它此前沦为死代码的根因。所以首屏由一个**内联同步脚本**设置，而不是等 JS 框架启动（那会闪一下）。

验证：`npm run build` 通过（`vue-tsc` + vite）；真机 **22 项断言全绿**（默认浅色 / 三态往返 /
刷新保持 / 系统主题联动 / 显式选择不被系统顶掉 / 减弱动效持久化 / 移动端组头 why 隐藏）。
截图见 `prototype/_shots/21..25-theme-*`。

> ⚠️ 主题偏好存**前端 localStorage**，不进后端：主题是**设备级**偏好，
> 同一用户在手机与电脑上可能想要不同，跨设备同步是反需求。

**切片 2a — IM 机器人（后端模型层，已落地）**

| 文件 | 内容 |
|---|---|
| `internal/model/bot.go` | **新增** `Bot`（ID / Channel / Name / Role / SysPrompt / AppID / CreatedAt）+ `BotRole`（admin / member）。`app_secret` **刻意不在结构里** |
| `internal/core/bots_store.go` | **新增** `BotStore`：`index.json`（元数据，可下发）+ `<id>.json`（凭据，0600）。`List` / `Get` / `AdminBot` / `AdminBotID` / `Create` / `Update(BotPatch)` / `Delete` / `CredsPath` |
| `internal/model/types.go` | `Message` 增 `BotID`（由各 receiver 投递时填充） |
| `internal/core/bridge.go` | **Q2 落地**：`AdminBotResolver` + `EnableBotRouting`；`handleTunnelCommand` 在「人」校验之后再加机器人校验 |
| `internal/api/bots.go` | **新增** `GET` / `POST` / `PATCH :id` / `DELETE :id` |
| `internal/api/router.go` | 注册 bots 路由；抽 `apiAuthMiddleware` 消除重复 |
| `internal/api/larkreg.go` | `start` 收 `{sys_prompt}`；`poll` 追加落 bot 记录（**幂等**，见下） |
| `internal/config/config.go` · `cmd/pieqi/main.go` | `pieqi.bots_dir`（默认 `~/.pieqi/bots`）；`NewBotStore` 接线 + `SetBotStore` + `EnableBotRouting` |

**四条实现判据**（都不是选择，是约束）：

1. **Q2 的判定顺序是「人 → 机器人」**：先验发送者是否为绑定管理员，再验消息落在哪台机器人。
   顺序反了会给出误导性错误（非管理员从 member 机器人发命令，应回「仅绑定」而非「无权操作」）。
2. **`BotID` 为空 = 投递方未标注来源 → 不收紧**。具名机器人实例会填 `BotID`（切片 2b 已落地），
   渠道级单实例（`BotID == ""`）仍按未标注处理 —— 老部署与单机器人行为因此完全不变。
3. **`poll` 必须幂等**：前端会重复轮询，成功态可能被读多次。用 `larkRegState.botID` 做标记，
   否则每轮一次就多一条机器人记录。
4. **`Create` / `Delete` 的失败回滚用整片快照，不能用「截掉最后一个」** —— `sortBots` 会重排，
   截尾会删错元素。（这个 bug 是写测试时发现的。）

**切片 2b — IM 机器人（多实例投递 + 回执路由，已落地）**

让 Q2 **真正生效**的一片：receiver 标注来源机器人，`Bridge` 按 bot id 回执。

| 文件 | 内容 |
|---|---|
| `internal/channel/receiver.go` | **新增**可选能力接口 `BotIdentity{BotID()}`；`Init` 的契约注释说明多实例渠道为何改由分发器承担路由 |
| `internal/channel/lark/lark.go` | `Adapter` 增 `botID`（**不可变**）+ `WithBotID` / `BotID` / `Mode`；webhook 与长连接两条入站路径都填 `Message.BotID`；`Init` 改为空实现；补 `verify_token` 校验 |
| `internal/channel/lark/longconn.go` | 长连接回调标注 `BotID` |
| `internal/channel/lark/webhook_mux.go` | **新增** `WebhookMux`：独占 `/webhook/lark` 与 `/webhook/lark/:bot_id`，按 bot 分发 |
| `internal/core/bridge.go` | `senders` 加锁；新增 `botSenders` / `botChannel` / `botOrder` 与 `SyncBotSenders` / `senderFor` / `BindReceiver`；`reply`、`NotifyOrigin`、`Sender` 全部改走 `senderFor` |
| `cmd/pieqi/larkchannel.go` | 重写：一实例 = 一台机器人，实例集由 `BotStore` 决定；`Rebuild()` / `Apply()`；**差量重建**（构造参数未变的实例原地复用） |
| `internal/api/bots.go` + `internal/api/router.go` | 新增 `botChanged` 钩子（`SetBotChangeHook`）：机器人记录增删后回调控制器 `Rebuild()` |
| `cmd/pieqi/main.go` | 把 `botStore` 传给控制器；接线 `SetBotStore` + `SetBotChangeHook`（钩子签名 `func()`，渠道错误在 main 内记日志） |

**五条实现判据**：

1. **一个 adapter = 一台机器人，`botID` 不可变。** 实例集合变化时重建 adapter，
   不做原地改写 —— 否则"已经发出的消息属于谁"会产生歧义。
2. **路由表必须有锁。** `senders` 从"启动时写一次"变成了"运行期可重建"，
   不加锁就是数据竞争。`senderFor` 的读全部在 `RLock` 下。
3. **回退顺序是「越具体越优先」**：bot id → 渠道名 → 该渠道的管理员机器人 → 注册顺序首台。
   后两步不是凑数：多机器人下没有渠道级实例时，`Sender("lark")` 返回 nil 会让 outcome 推送**静默失效**。
   用 `botOrder` 而不是遍历 map，是为了让回退**确定**。
4. **`Init` 空实现 + `WebhookMux` 独占路由，不是绕路。** 两台机器人天然共享 `/webhook/lark` 前缀，
   gin 不允许同一 pattern 重复注册；而新机器人在**运行期**产生，gin 服务启动后追加路由不是并发安全的。
   路由只在启动时注册一次、实例集合随时可换 —— 这也是**接入方式切换不再需要重启**的来源。
5. **路由常驻后，`longconn` 实例必须在分发器里被拒（404），且 webhook 必须验 `verify_token`。**
   否则长连接部署会凭空多出一个可被伪造的入口（原来不注册路由，所以没这个问题）。
   `verify_token` 仅在**配置了**才校验，保证向后兼容。
6. **增删机器人必须让"被删的那台"真的停下来。** 只改记录不够 —— 被删的机器人若仍持有实例，
   就还在收发消息，而它已从列表里消失（这最容易被误认为"已下线"）。故 `POST`/`DELETE /api/bots`
   落盘后经 `BotChangeHook` 触发 `Rebuild()`。
7. **重建是差量的，不是全量。** 全量重建会 cancel 掉所有长连接 —— 改一台（或删一台）会让**其余机器人
   一起掉线重连**，而绝大多数调用只影响一台。参数未变的实例原地复用，只停"变的"与"消失的"；
   变的那台**必须先停**，否则同一 app 会同时挂两条 wss。

**验证**（`go build ./...` exit=0；`go vet` 干净）：

- **单元测试**：`internal/channel/lark`（7 项：按 bot_id 分发 / 未知 bot 404 / 渠道级路径 / 长连接实例拒 HTTP /
  `Replace` 清空 / `verify_token` 强制与兼容）、`internal/core`（含 9 项回执路由）、`cmd/pieqi`
  （7 项控制器：逐台建实例 / 差量复用 / 删除后实例与路由一起消失 / 渠道级兜底 / 无凭据跳过 / 无凭据不建 / 模式继承）。
- **本机 HTTP 冒烟**（隔离 `config.yaml` + 独立 cwd + `PIEQI_HOME`，端口 3101，22 项断言全绿）：
  空列表 → 建首台自动为 admin → 无凭据时不可寻址(404) → `POST /api/larkreg/config` 后 live(200) →
  错 token 401 / 未知 bot 404 → 加第二台为 member 且 admin 不变、**第一台不被打断** → 删除后 404 →
  **末台删除后渠道级单实例自动回归**（`/webhook/lark` 复通）。
- 「删除-回退」环路重复 5 轮稳定通过（首次偶发的连接重置是回执同步调飞书 API 经代理抖动所致，**非崩溃**：网关无 Recovery，但进程存活）。

> **范围边界（明确不做）**：`Task.OriginBotID` 与 `PushRegistry` 的按机器人路由**未做**。
> 原因：`task.OriginChannel` 在**生产代码里没有任何写入点**（IM 在 Pieqi 模式下不创建任务，
> 任务从 PWA 建），所以回推/推送这条路径当前是死的 —— 为一个没有生产者的字段加 bot 维度，
> 只会得到"看起来完备"的空壳。等 PWA 侧真的能指定来源机器人时再一并做。
> 当前 `NotifyOrigin` 按渠道名回退到管理员机器人，行为是**确定**的。

> **运行时提示**：切片 2b **只动后端**（无前端改动），故 `npm run build` 不必要，
> 但必须**重新编译 Go** 才会生效 —— `pieqi.exe` 是预编译二进制，跑着旧实例时
> 新接口（如 `/api/bots`、`/webhook/lark/:bot_id`）会返回 404。

**③ `/api/tunnel/start` 收口（D3-c，已落地）**

把 `/api/tunnel/start` 从 `tunnelRead`（仅 UA 判定）移进 `tunnelMutate`
（`ExternalAuthMiddleware + TunnelOpGateMiddleware`）。

| 文件 | 内容 |
|---|---|
| `internal/api/router.go` | `/start` 移入 `tunnelMutate` 组，并写明 **D3-c 的判据注释**（真正的引导入口是 IM 命令，匿名 UA 门就是那个洞） |
| `internal/api/router_tunnel_bootstrap_test.go` | **断言反转**：`TestTunnelStart_RequiresToken`（`lark_mobile_no_token_401` / `lark_mobile_with_token_200` / `non_lark_ua_403`）。原来断言"匿名可 200"，那正是漏洞本身 |

**切片 2c — IM 机器人（前端，已落地）**

| 文件 | 内容 |
|---|---|
| `web/src/types/api.ts` | `BotRoleDto` / `BotDto` / `BotsResponseDto` |
| `web/src/services/api/bots.ts` | **新增** `listBots` / `createBot` / `updateBot` / `deleteBot` / `channelLabel` |
| `web/src/services/api/larkreg.ts` | `start` 返回 `{qrUrl, expireIn}`；`poll` 返回 botId；`saveLarkConfig` 接受预设提示词 |
| `web/src/features/settings/components/RobotList.vue` | **新增**：管理员卡（unset / ready 双态，单 `data-state` 驱动）+ 成员列表 + 内联改名 / 解绑确认 + 「继续创建」入口；内网不可用时内联说明 |
| `web/src/features/settings/components/RobotCreateModal.vue` | **新增**：四态（gen / scan / busy / dead，单 `data-stage` 驱动）+ 模式切换（扫码 / 手动）；**扫码态没有创建按钮**（动作发生在用户手机的飞书上）+ `expire_in` 倒计时 |
| `web/src/components/ui/CapTag.vue` | **新增**：能力标记（warning 槽位）—— 权限与状态是两套词汇，不复用状态徽章 |
| `internal/api/larkreg.go` | `configUpdate` 收 `SysPrompt`；**新增 `upsertBotForApp`**（幂等键 = `(channel, app_id)`，新建时写**自己的**凭据文件，失败回滚） |
| `internal/core/bots_store.go` | **新增** `FindByAppID` —— 手动配置路径的幂等查找 |
| `cmd/pieqi/larkchannel.go` | **修掉一个潜在的数据损坏 bug**：`Apply` 曾把新凭据写进**管理员机器人**的凭据文件。配第二台时会覆盖第一台。已整块移除 —— 凭据只由各自的 upsert / poll 路径写入自己的文件 |

> **为什么 `Apply` 那个 bug 一定要现在修**：它在单机器人时代完全看不出来（管理员就是唯一那台），
> 一旦"多机器人"落地就从"潜在"变成"必然"，而且症状是**第一台静默失效** —— 最难查的那一类。

**切片 3 — 审批与自动化（已落地）**

| 文件 | 内容 |
|---|---|
| `internal/core/settings_store.go` | **新增**：`RiskLevel`（L0–L3）+ `riskLevelKinds` 映射 + `Settings` + `DefaultSettings()` + `AutoApproveTools()` + `InDND(t)`（**跨零点**）+ `SettingsStore`（RWMutex + 原子 tmp/rename）+ `Patch` / `OnChange` / `RiskMatrix` |
| `internal/api/settings.go` | **新增** `GET` / `PATCH /api/settings`（DTO 投影，与 `/api` 同鉴权） |
| `internal/core/task_runner.go` | `autoApprove` 改为 `RWMutex` 保护 + `SetAutoApproveTools` / `SetDNDChecker` / `inDND`；`notifyWaitingInput` 在免打扰时段**只压推送、不压决策** |
| `web/src/services/api/settings.ts` | **新增** |
| `web/src/pages/SettingsPage.vue` | 补组①：L0 / L1 可配开关 + **L2 / L3 锁定行（不是"默认关"，是根本没有这个字段）** + 免打扰时段 |

**两条实现判据**：

1. **L2 / L3 是硬锁，不是默认值。** 做成"默认关闭的开关"等于把"能把自己配进坑里"做成了选项。
   `riskLevelKinds` 的 L2 还**刻意包含 `other`** —— 否则未分类工具会绕过白名单，白名单就成了摆设。
2. **免打扰压的是推送，不是决策。** 决策照常产出、照常入队，只是不打断人 ——
   否则"安静时段"会变成"任务卡死时段"。`InDND` 必须处理**跨零点**（22:00–08:00）。

**切片 5 — 数据与关于（已落地）**

| 文件 | 内容 |
|---|---|
| `internal/core/task_store.go` | **新增** `SetEventRetention` / `EventRetention` / `AppendEvent`（**单调 `NextEventSeq`** + 裁剪 + 重建底层数组）/ `lastEventSeq`；`Task` 增 `NextEventSeq` |
| `internal/core/agent_stream.go` · `agent_toolcall.go` · `internal/api/feedback.go` | 事件追加**统一收口**到 `store.AppendEvent` |
| `internal/logging/logfile.go` | **新增** `DailyFileSink`（按天轮转）+ `NewLogger`（console + 日文件，sink 失败降级为仅 console）+ `BuildDiagnosticsZip`（按文件名取窗口；空目录也产出合法 zip） |
| `internal/api/diagnostics.go` | **新增** `GET /api/diagnostics/export`（zip；meta 含 go_version / os / arch / uptime / tasks / bots） |
| `cmd/pieqi/main.go` | 日志切到 `logging.NewLogger`；接线 `SettingsStore`（初始化 `SetAutoApproveTools` / `SetDNDChecker` / `SetEventRetention`，并订阅 `OnChange` 双向同步）、`SetSettingsStore` / `SetLogDir` |
| `web/src/services/api/client.ts` | **新增** `requestBlob` —— 诊断导出要带 `Authorization`，`<a href>` 带不了 |
| `web/src/pages/SettingsPage.vue` | 补组④：事件保留上限下拉 + 诊断导出 + iOS 静态说明行 + 版本 |

> **`AppendEvent` 为什么必须单调**：原来 `ev.Seq = len(events) + 1`，一旦裁剪过，
> 后面的序号会**回退** —— 客户端按 Seq 去重/续传时会漏消息。故 `Task` 自带 `NextEventSeq`，
> 老任务从 `lastEventSeq` 推导起点。

**切片 6 — 移动端外壳（已落地）**

| 文件 | 内容 |
|---|---|
| `web/src/components/ui/BottomNav.vue` | **新增** 底栏 3 项（首页 / 任务 / 设置），**全部是页面切换**；首页有待审批角标、设置有断连角标（见下） |
| `web/src/components/ui/InstallPrompt.vue` | **新增** PWA 情境化提示条（`canInstall` 才存在，带「安装 / 暂不」） |
| `web/src/layouts/MobileLayout.vue` | **重写**：删掉 TopBar / 汉堡 / 抽屉，改为「内容区 + 提示条 + 底栏」。底栏是**替代**抽屉，不是补充 |
| `web/src/stores/taskTree.ts` | **新增** 折叠状态（`groupOpen` / `openProjects`）—— 一份状态两个容器 |
| `web/src/pages/TasksPage.vue` | **重写**为任务浏览器页（项目手风琴 + 搜索 + 状态筛选 + 渐进披露） |
| `web/src/features/task/components/TaskBrowserGroup.vue` | **新增** 项目手风琴（含 `grid-template-rows: 0fr→1fr` 折叠动画） |
| `web/src/features/task/types.ts` | **新增** `BrowserGroup` 视图模型（页面与组件共用，避免两份结构漂移） |
| `web/src/composables/usePwa.ts` | 改为**模块级单例**（见下） |
| `web/src/utils/format.ts` | **新增** `STATUS_DOT` —— 状态点色值从侧栏提出来共用 |
| `web/src/layouts/AppSidebar.vue` | 折叠态改用 `taskTree`；删除改用内联确认条；**移除已失效的 `navigate` emit** |
| `web/src/stores/app.ts` | 移除 `mobileNavOpen`（抽屉不存在了） |
| `web/src/features/task/components/TaskFilterBar.vue` | **删除**：属于被取代的那版列表页，其中项目下拉更是 SPEC §6.1 明令不做的 |

**四条实现判据**：

1. **底栏替代抽屉，所以移动端不再渲染 `AppSidebar`。** 原来"抽屉里装 PC 侧栏"把一整套常驻导航树
   塞进 80vw —— 侧栏的高亮依赖"一直在那儿"，抽屉一收起就不知道自己在哪了。
2. **`usePwa` 必须是单例。** `beforeinstallprompt` 是一次性事件且可能早于 Vue 挂载就派发；
   在组件里现注册监听 → 组件挂载晚于事件 → **永远收不到**。这正是 D5 记录的那颗
   "时有时无的按钮"的一部分成因：每次调用 `usePwa()` 都新建一份 ref 和一个监听器。
3. **折叠状态的优先级是「手动 > 筛选自动 > 默认收起」。** 原型的写法
   （`open = filtering ? list.length > 0 : openProjects[name]`）会让筛选期间点项目头**毫无反应** ——
   一个能点的开关点了没反应，比不显示更糟。手动状态存在 `taskTree.openProjects` 里（跨容器共享），
   "筛选自动展开"是浏览器页本地的派生行为，不写回 store。
4. **移走 TopBar 不能静默丢掉断连信号。** 侧栏是 SPEC 规定的"唯一一处连接状态"，
   移动端侧栏不存在，那一处也就没了 —— 于是底栏「设置」项上挂一个断连点**不构成第二处**，
   它是移动端的唯一一处。同理，待审批角标不能因为侧栏隐藏就一起消失。

**验证**：`go build ./...` exit=0；`go test ./internal/... ./cmd/...` 全绿
（唯一失败项 `TestRunACP_PersistsRealSessionID` 是 Windows TempDir 清理的已知偶发，
单独复跑 3/3 通过，非回归）；`npm run build`（vue-tsc + vite）通过；`vitest run` 44 项全绿。
**真机冒烟**（Chrome 414×860，对真实二进制跑，**移动端 27 项 / 桌面端 7 项全绿**）：
底栏恰好 3 项且移动端不渲染侧栏 → 任务页手风琴默认收起、点击展开（高度 1→120，`aria-expanded` 同步）
→ 筛选后无匹配项目置灰且 `disabled`、不从列表消失、出现「清除筛选」并隐藏「展开全部」
→ 设置页可达且高亮 → 未触发 `beforeinstallprompt` 时提示条**不存在** → 无 JS / HTTP 错误。

---

## 5. 切片 2 的文件级清单（IM 机器人）

### 前端

| 文件 | 动作 |
|---|---|
| `pages/SettingsPage.vue` | 重写为 4 组；「连接与账号」内落 IM 机器人组 |
| `features/settings/components/RobotList.vue` | **新增**：管理员卡（unset / ready 双态）+ 继续创建入口 |
| `features/settings/components/RobotCreateModal.vue` | **新增**（改造自 `LarkConfigModal.vue`）：四态 + 模式切换（扫码 / 手动）+ 预设提示词 |
| `features/settings/components/LarkConfigModal.vue` | 拆分：配置表单下沉为模态内的「手动」模式 |
| `components/ui/CapTag.vue` | **新增**（`Switch` / `SettingRow` 已随切片 1 落地） |
| `services/api/larkreg.ts` | 扩展：`start` 带预设提示词；新增 bots 接口 |
| `layouts/AppSidebar.vue` | 底部两行合一；去 `confirm()`（切片 1 可顺手做） |

### 后端

| 文件 | 动作 |
|---|---|
| `internal/model/bot.go` | **新增** `Bot` 实体（字段见 §4.1） |
| `internal/api/bots.go` | **新增** `/api/bots`：GET 走 `ExternalAuth`；POST / PATCH / DELETE 走 `BindOpGate` |
| `internal/core/bots_store.go` | **新增** 落盘：`~/.pieqi/bots/index.json` + `<id>.json`（0600，原子 rename，照 `larkreg/credentials.go` 的写法）。<br>⚠️ **落点是 `core` 而非 `api`**（原稿写在 `api`）：`core/bridge.go` 需要查「管理员机器人 id」才能实现 Q2，而 `api` 依赖 `core`，反向依赖成环。与 `TaskStore` / `FeedbackStore` 同层才正确。 |
| `internal/api/larkreg.go` | `start` 接受可选 body `{sys_prompt}`；`poll` 成功后**追加**写 bot 记录 + per-bot 凭据。<br>⚠️ **单例凭据照旧写**（不"改成" per-bot）：`main.go:loadLarkChannelConfig` 仍靠它做渠道 bootstrap，停写会让重启后飞书渠道直接失效。单例 = 当前生效渠道凭据，`bots/` = 多机器人注册表，两者并存，收敛留后续切片。 |
| `internal/model/types.go` | `Message` 增 `BotID` —— 按机器人区分特权的前提，由各 receiver 投递时填充 |
| `internal/core/bridge.go` | `handleTunnelCommand` 判定加 `msg.BotID == adminBotID`；非 admin 机器人回「⛔ 无权操作」。<br>✅ 切片 2b 在此基础上把 `senders` 拆成「渠道名 + bot id」两张表并加锁 |
| `internal/channel/lark/*` | ✅ 切片 2b：`Adapter` 带 `botID`、`WebhookMux` 单路由按 bot 分发 |
| `cmd/pieqi/larkchannel.go` | ✅ 切片 2b：一实例 = 一台机器人，实例集由 `BotStore` 决定 |
| `internal/api/router.go` | 注册 bots 路由；**读接口放开到 ExternalAuth**（见 §4.1 的 ⚠️）；**`/api/tunnel/start` 并入 `tunnelMutate`**（需 token，见 §4.2.1） |
| `internal/config/config.go` | 增 `bots_dir`（默认 `~/.pieqi/bots`）；保留 `larkRegCredPath` 以兼容旧数据 |

---

## 6. 风险与机制

1. **权限语义（D3）** —— 归属已明确（特权跟账号走）。实现拆三件，进度：
   - ✅ `Message.BotID` + `bridge.go` 双道校验（切片 2a）
   - ✅ **receiver 侧填充 `BotID`**（切片 2b：per-bot 适配器 + `Bridge` 按 bot id 回执）——
     这一件落地后 Q2 的机器人校验**已实际生效**（具名实例标注来源，渠道级实例仍按未标注处理）
   - ✅ `/api/tunnel/start` 收口（关掉匿名 bootstrap，见 §4.2.1 / D3-c）—— 断言已反转
   三件全部落地 → **Q2 完全成立**：IM 命令按机器人区分特权，外网 bootstrap 不再是匿名可领。
   `.cap-tag` 从"看到的语义"变成"真的拦得住"。
2. **原型与实现需要一份对账机制**：原型是**目标态**，实现是**当前态**。
   建议每落一片，在 SPEC 里把该节标注 `已落地 (commit)` 或 `待落地`，避免两者长期分叉后无法判断谁为准。
3. **回归脚本要继续守原型**：`_verify.mjs` 的 200 项断言是**设计意图的守卫**，
   落地后若某条断言不再成立，应显式改断言（说明设计变了），而不是让它失效。

---

## 7. 剩余待办（切片 0–6 + ③ 全部落地后的缺口）

按"不改会造成什么后果"排序，不是按发现顺序。

### 7.1 已完成（2026-09-11 收尾轮）

| # | 项 | 怎么落地的 |
|---|---|---|
| **0** | 仪表盘瘦身与本周概览 | `DashboardPage.vue` 重写为**只有两区块**：待审批（复用 `ApprovalCard`，无审批时不渲染该 section）+ 本周概览（`InsightSection.vue`）。删掉了 `StatCard.vue`（旧 chip 统计卡，随之一并删） |
| 1 | 原生 `confirm()` 清零 | `TurnCard.vue`（整轮 + 单文件回退）、`SessionPage.vue`（删除任务）全部改为内联确认条。**现在项目里已经没有原生 `confirm()` 了** |
| 3 | `ProjectsPage.vue` | 删除，路由 `/projects` 一并移除 |
| 4 | 桌面 `/tasks` 深链 | 路由 `beforeEnter`：非移动端（>768px）重定向 `/dashboard`，断点与 `useResponsive` 同一条 media query |
| 5 | 移动端删除任务 | 任务行新增常驻删除入口（`TaskBrowserGroup.vue`）+ 内联确认条。**常驻是关键**：移动端没有 hover，"悬浮才出现"的按钮等于不存在 |
| 6 | `.smoke-proj-*` | 已删除。绕不开的根因是**句柄被旧 pieqi 进程占用** —— 停掉进程后 `rm` 一次成功，与删除方式无关（此前试过 `rm` / `rmdir` / `fs.rmSync` / `.NET` 全部 EBUSY） |

**其中一个刻意不做的决定**：SPEC §6.2 给「本周概览」列了四个指标，只做三个 ——
**「一次通过率」drop 了**。理由是"是否一次通过"在产品语义上没定性
（从未等过人？没有追加干预？只有一个 Turn？三种定义各说各话），硬挑一个会得到
「看着有数、无人知道它在说什么」的指标。承认这块还没定义好，比给一个假指标有用。

**顺手补上的后端**：`model.DiffStat` + `core.SnapshotDiffStat`，在任务**进入终态那一刻**
固化累计代码改动。这是**随时间丢失的数据** —— worktree 一旦清理就再也拿不回来。
挂载点是已有的 `captureTurnEnd`（并没有预想的"3 处 FinishedAt 需要收敛"，那个钩子早已
是一处统一的终态入口），只需要新增调用。**只有 completed 记**（失败/取消不算产出）。

### 7.2 已完成（2026-09-11 决策轮）

用户拍板：**A 走"后端打标、三层改全"，B 走"删干净"**。

| # | 项 | 怎么落地的 |
|---|---|---|
| **A** | 风险分级接通到卡片 | `model.RiskLevel` + `Decision.Risk`（**定义在 model 而非 core** —— Decision 要带它，而 model 不能反向依赖 core）；`core.RiskOfKind()` 由 `riskLevelKinds` **派生成反查表**，与自动放行**共用同一张表**；`PermissionWire` 一路把 `ToolKind` 传到 `setWaitingApproval` 打标；前端 `riskOf()` 缺省兜底 L2、卡片三档视觉 + L3 内联二次确认、仪表盘按风险降序且同风险按等待时间升序 |
| **B** | 删除审批中心 | 删侧栏审批项、`ApprovalsPage.vue`、`BottomNav` 里失效的 `/approvals` 与 `/projects` 匹配项；`/approvals` 保留为**重定向到 `/dashboard`**（旧链接不能断，入口消失 ≠ 把在路上的人扔下） |

**三个判断值得留下**：

1. **L0/L1 的卡片刻意不用警告色。** 这两档是**可以自动放行的** —— 它们出现在卡片上只是因为用户
   关掉了自动放行，属于"你想看一眼"，不是"这里有危险"。警告色铺满所有审批就等于没有警告色：
   真正要警觉的 L3 会淹没在一片黄里。**强度差是信号，不是装饰 —— 铺满即失效。**
2. **风险分级只给工具审批（路径 A）打标。** 路径 B（Claude 文本提问）没有工具语义，
   硬套一个等级只会给出假信息；它的 `Risk` 为空，前端按 L2 兜底。
   **未定级不等于低风险** —— "不知道"必须往保守那侧倒（与 `other` 归 L2 同一判据）。
3. **区块标题跟着最严重的那条走**（有 L3 就转红 + 加"含破坏性操作"）。
   标题是扫视时的第一落点，固定用黄色会让"有破坏性操作在等"这个事实被淹没。

> 顺带确认：`ApprovalsPage.vue` 删除前看过了 —— 它只是用同一个 `ApprovalCard`
> 重复了仪表盘的待审批组，**没有任何仪表盘给不了的东西**，所以"删干净"不是损失功能。

### 7.3 已完成（2026-09-11 变更反馈轮）

用户发现"变更反馈跟 UI 不一致"，核对后确认：**SPEC §5.2 / §5.2.1 那套形态从来没落地过** ——
`IMPLEMENTATION-PLAN.md` 全文搜索「变更反馈」**0 处命中**，它没被排进任何切片。
这与仪表盘（§7.1 第 0 条）是**同一类差距**：SPEC 已定案、无对应切片。
但它更隐蔽 —— 它看上去"有个能用的面板"，所以一直没人觉得缺。

| # | SPEC | 原来 | 现在 |
|---|---|---|---|
| 1 | 右栏常驻，宽度由 `grid-template-columns` 驱动 | `v-if` 挂载 + 内联 `width: 480px` 的 dock | `1fr 420px` ⇄ `1fr 46px`，展开/收起只切 class |
| 2 | **默认收起** | 默认不存在，要点头部「反馈」才出现 | 默认收起为贴边条 |
| 3 | 46px 贴边条（左向 `‹` + 竖排文字 + 变更文件数） | 无 | `FeedbackPanel` 的 `railOnly` 分支 |
| 4 | 面板头「收起」 | 只有「×」 | dock 档「收起」/ drawer 档「关闭」/ 移动端**没有**（无目标可收 = 死控件） |
| 5 | Tabs 概览 · 变更 · 检查 · 预览 | 无 Tab，六块内容纵向堆叠 | 四个 Tab；**双视角（本轮/累计）降级进「变更」Tab 内部** |
| 6 | 移动端整屏 + 顶部 `时间线 / 变更反馈` 分段 | 底部 Sheet 覆盖 | 整屏 + 顶部分段 |
| 7 | `<768` / `768–1279` / `≥1280` 三档 | 只有 768 两档 | `useResponsive` 新增 `isWide`（≥1280） |
| 8 | Turn 尾部变更摘要 →「查看 Diff」联动右栏 | 无 | **未做 —— 前提不存在，见 §7.4** |

实现要点（每条都是踩出来的）：

- **收起态放 store，不放 `SessionPage` 的本地 ref。** SPEC 要求"切页、切视口都保持"；
  本地 ref 会在路由切走再回来时重置成默认展开，用户每次进详情都得重新点一次。
- **`useResponsive.bind()` 必须立即取一次初值。** 只挂 `change` 监听的话，
  "挂载时查询已经匹配"的情况下浏览器不会再补发一次 `change`，值会永远停在 `false`。
- **收起态也要拉 `/feedback`。** 贴边条上的变更文件数就是"要不要展开"的判据 ——
  这是 SPEC 对"能否默认收起"提的硬条件，不是可以省掉的一次请求。
- **文件数取后端 `cumulative.files`**（走 `git diff --numstat`），不是自己数 turns 的去重路径：
  后者只覆盖**有快照的 Turn**，老任务会数出 0，于是"明明有改动却显示没事"。
- **`PreviewSection` 重新挂载时若处于 `starting` 要续上轮询。** 它现在是一个 Tab，
  切走再切回会重新挂载；只刷新不续轮询，状态会永远停在"启动中"，看起来像卡死。
- **量出来的，不是猜的**：`isWide` 的阈值取 1280 而不是 768 ——
  常驻右栏要同时容下「240px 侧栏 + 时间线 + 420px 反馈」，1280 以下给不出这三样。

**顺带修掉一个真会白屏的既有隐患**：`/outcome` 与 `/evidence` 在没有 checks / issues /
rewinds 时，Go 的 nil 切片序列化成 `null`，而 DTO 声明的是数组 ——
`outcome.checks.length` 这种在 TS 里完全合法的写法在运行期抛
`Cannot read properties of null`，**反馈面板一打开就崩**，且只在"这份 outcome 恰好没有检查项"
的任务上复现，看起来像随机故障。已在适配层归一（`getChecks` 早就在用同一套路，这次补齐
`getOutcome` / `getEvidence`），并补了 `feedback.spec.ts` 三条回归。

### 7.4 未完成

| # | 项 | 为什么值得做 | 落点 |
|---|---|---|---|
| **C** | **Timeline 没有按 Turn 分组** —— `features/timeline/` 里**一处 `turn` 都没有**（grep 零命中），事件是一条扁平流水。于是 SPEC §5.2 的「Turn 尾部的变更摘要卡」以及「点它联动右栏、**收起态先自动展开再切 Tab**」都没有安放处 | SPEC §5.2 前半节整块未实现。这不只是美观：`─── Turn 1 ───` 是把"我说的"和"Agent 改的"对应起来的唯一标记，没有它，时间线越长越读不出因果关系 | `SessionTimeline.vue` 按 `EventUser` 切分 + Turn 分隔件 + 摘要卡（需消费 feedback bundle，与面板共用一份数据） |

> C 条是**做变更反馈时发现的**：§7.3 第 8 项原以为只是"加个链接"，
> 动手才发现前提不存在 —— 链接要挂在 Turn 尾部，而 Turn 边界本身还没画出来。
> 所以本轮**没有硬做**：挂一个悬空入口比不挂更糟（点了不知道会去哪）。
> 它现在是一个**独立的切片**，不该塞进这次的范围里。

后续若要继续推进，还有几项属**产品范围**而非设计还原：
机器人管理员转移/回收的建模、`sw.js` 的 `ASSET_CACHE` 随构建失效（当前
`VERSION='v2'` 自 8/13 未变，缓存只增不减）、「一次通过率」的口径定义。
