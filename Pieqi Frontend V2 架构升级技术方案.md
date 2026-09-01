# Pieqi Frontend V2 架构升级技术方案

> **状态：Proposal / Ready for Implementation**
>
> **目标：** 将 Pieqi 当前 PWA 前端直接重构为 Vue 3 + Vite + TypeScript + Tailwind CSS + Pinia 架构，并以 Agent Control / Agent Observability 为下一阶段产品演进基础。
>
> **原则：** 前端激进重构，后端协议保持稳定；不做双前端、不做兼容层、不做渐进式 Vue 迁移。

---

# 1. 背景

Pieqi 当前已经具备较完整的 Agent Runtime 桥接能力：

```text
飞书 / 企业微信 / 微信 / PWA
              │
              ▼
           Pieqi Go
              │
        ┌─────┴─────┐
        │ AgentManager
        └─────┬─────┘
              │
      ┌───────┼────────┐
      ▼       ▼        ▼
   Claude    Qoder    ACP Agent
      │
      ▼
 Task / Session / EventBus
              │
              ▼
          WebSocket
              │
              ▼
             PWA
```

当前仓库已经采用：

- Go Backend
- Vite PWA
- `//go:embed` 嵌入前端
- WebSocket 实时事件
- Task
- Agent Session
- Approval
- Intervene
- Skill
- Tunnel Control
- Session Deep Link

这些能力已经不再适合使用“简单管理后台”的前端组织方式。

下一阶段预计增加：

- Agent Timeline
- Tool Trace
- Agent Status
- Replay
- Session Fork
- Agent Watchdog
- Cost / Token
- Agent Judge
- Agent Graph
- Project / Knowledge
- 更强的移动端 Agent Control

因此必须在此阶段建立新的前端架构。

---

# 2. 升级目标

## 2.1 技术目标

将：

```text
Current
Vite + Vanilla/Lightweight frontend
```

升级为：

```text
Vue 3
+
Vite
+
TypeScript
+
Tailwind CSS
+
Pinia
+
Vue Router
+
PWA
```

推荐基础版本策略：

| 技术 | 选择 | 要求 |
|---|---|---|
| Vue | Vue 3 | 必须 |
| Vite | Vite | 必须 |
| TypeScript | TS | 必须 |
| Tailwind CSS | Tailwind | 必须 |
| Pinia | Pinia | 必须 |
| Vue Router | Router | 必须 |
| PWA | 保留 | 必须 |
| WebSocket | 原生封装 | 必须 |
| Nuxt | 不使用 | 禁止引入 |
| React | 不使用 | 禁止引入 |
| UI Framework | 暂不绑定 | 谨慎 |
| SSR | 不需要 | 不使用 |

Pinia 官方定位就是 Vue 的类型安全、模块化状态管理方案，并提供 DevTools、HMR 和 TypeScript 支持，适合本项目的跨页面实时 Session 状态。

---

# 3. 核心架构原则

## 3.1 Backend First

本次重构：

> **不修改后端业务协议作为前提。**

保持：

```text
HTTP API
WebSocket
Task
TaskEvent
Session
Approval
Skill
Tunnel
Auth
```

不因为前端重构而修改。

如果前端发现后端 API 不合理：

1. 先通过 Adapter 适配；
2. 只有确认无法表达未来模型时，才单独提出 Backend API Evolution。

---

# 4. 为什么直接重写而不是迁移

本次不采用：

```text
旧 Vue
  ↓
逐个 component 改 Vue 3
```

也不采用：

```text
Old UI
   +
New UI
```

而采用：

```text
旧 frontend
     │
     └── 删除 / 替换
             │
             ▼
        Frontend V2
```

原因：

1. 当前前端规模尚未大到值得维护兼容层；
2. 当前 `web/package.json` 仅包含 Vite，前端依赖非常轻；
3. 未来 UI 模型会发生明显变化；
4. Session / Event / Agent 是新的核心领域模型；
5. 继续兼容旧 UI 会形成大量历史包袱。

---

# 5. 最终前端架构

```text
                         ┌──────────────────────┐
                         │       Vue 3          │
                         │       App            │
                         └──────────┬───────────┘
                                    │
                 ┌──────────────────┼──────────────────┐
                 │                  │                  │
                 ▼                  ▼                  ▼
              Router             Layout             Pinia
                                                     │
                              ┌──────────────────────┼─────────────────────┐
                              │                      │                     │
                              ▼                      ▼                     ▼
                         Task Store            Session Store          Agent Store
                              │                      │                     │
                              └──────────────┬───────┴─────────────────────┘
                                             │
                                             ▼
                                      Service Layer
                                             │
                           ┌─────────────────┼─────────────────┐
                           │                 │                 │
                           ▼                 ▼                 ▼
                         HTTP              WS Client         Local/PWA
                           │                 │
                           └────────┬────────┘
                                    │
                                    ▼
                                Go Backend
```

---

# 6. 目录结构

最终目录：

```text
web/
├── public/
│   ├── icons/
│   ├── favicon.svg
│   └── ...
│
├── src/
│   ├── app/
│   │   ├── App.vue
│   │   ├── router.ts
│   │   └── providers.ts
│   │
│   ├── layouts/
│   │   ├── AppLayout.vue
│   │   ├── DesktopLayout.vue
│   │   └── MobileLayout.vue
│   │
│   ├── pages/
│   │   ├── DashboardPage.vue
│   │   ├── TasksPage.vue
│   │   ├── SessionPage.vue
│   │   ├── AgentsPage.vue
│   │   ├── ApprovalsPage.vue
│   │   ├── ProjectsPage.vue
│   │   └── SettingsPage.vue
│   │
│   ├── features/
│   │   ├── task/
│   │   │   ├── components/
│   │   │   ├── composables/
│   │   │   └── index.ts
│   │   │
│   │   ├── session/
│   │   │   ├── components/
│   │   │   ├── composables/
│   │   │   └── index.ts
│   │   │
│   │   ├── agent/
│   │   │   ├── components/
│   │   │   └── index.ts
│   │   │
│   │   ├── approval/
│   │   │   ├── components/
│   │   │   └── index.ts
│   │   │
│   │   ├── timeline/
│   │   │   ├── components/
│   │   │   └── index.ts
│   │   │
│   │   └── dashboard/
│   │       ├── components/
│   │       └── index.ts
│   │
│   ├── components/
│   │   ├── ui/
│   │   ├── agent/
│   │   ├── task/
│   │   ├── session/
│   │   └── timeline/
│   │
│   ├── stores/
│   │   ├── app.ts
│   │   ├── task.ts
│   │   ├── session.ts
│   │   ├── agent.ts
│   │   ├── approval.ts
│   │   └── notification.ts
│   │
│   ├── services/
│   │   ├── api/
│   │   │   ├── client.ts
│   │   │   ├── tasks.ts
│   │   │   ├── sessions.ts
│   │   │   ├── skills.ts
│   │   │   ├── auth.ts
│   │   │   └── tunnel.ts
│   │   │
│   │   └── websocket/
│   │       ├── client.ts
│   │       ├── dispatcher.ts
│   │       └── reconnect.ts
│   │
│   ├── composables/
│   │   ├── useTask.ts
│   │   ├── useSession.ts
│   │   ├── useWebSocket.ts
│   │   ├── useResponsive.ts
│   │   └── usePwa.ts
│   │
│   ├── types/
│   │   ├── task.ts
│   │   ├── session.ts
│   │   ├── agent.ts
│   │   ├── event.ts
│   │   ├── approval.ts
│   │   └── api.ts
│   │
│   ├── utils/
│   │   ├── date.ts
│   │   ├── format.ts
│   │   └── event.ts
│   │
│   ├── styles/
│   │   ├── index.css
│   │   ├── tokens.css
│   │   └── components.css
│   │
│   └── main.ts
│
├── index.html
├── package.json
├── package-lock.json
├── tsconfig.json
├── tsconfig.app.json
├── vite.config.ts
├── tailwind.config.ts
├── postcss.config.js
├── embed.go
└── manifest.webmanifest
```

---

# 7. Feature 与 Components 的边界

必须遵循：

```text
pages
  ↓
features
  ↓
components
  ↓
ui
```

而不是：

```text
pages
  ↓
components
  ↓
所有业务逻辑
```

---

## 7.1 pages

只负责：

- 页面布局
- 路由参数
- 页面级数据加载
- Feature 组合

禁止：

- WebSocket 监听
- 复杂业务逻辑
- 直接调用 fetch
- 操作后端 API

---

## 7.2 features

负责业务能力。

例如：

```text
features/session/
```

负责：

- Session Timeline
- Session Header
- Session Actions
- Session Status
- Session Intervention

---

## 7.3 components/ui

只做通用 UI：

```text
Button
Badge
Card
Dialog
Drawer
Tabs
Dropdown
Input
Textarea
Progress
Tooltip
```

禁止在 `components/ui` 中出现：

```text
task
session
agent
approval
```

等领域概念。

---

# 8. 核心领域模型

前端必须围绕以下 5 个核心对象设计：

```text
Agent
Task
Session
Event
Approval
```

关系：

```text
Agent
  │
  └── Session
        │
        └── Task
              │
              ├── Event
              ├── Approval
              └── Artifacts
```

实际运行关系：

```text
Task
 │
 └── Session
       │
       ├── Agent
       │
       └── Events[]
```

---

# 9. TypeScript 类型

## 9.1 Task

```ts
export type TaskStatus =
  | 'pending'
  | 'running'
  | 'waiting_input'
  | 'completed'
  | 'failed'
  | 'cancelled'

export interface Task {
  id: string
  title: string
  project?: string
  status: TaskStatus
  agent?: string
  sessionId?: string
  createdAt: string
  updatedAt: string
}
```

---

## 9.2 Session

```ts
export interface AgentSession {
  id: string
  taskId: string
  agent: string
  status: TaskStatus
  startedAt?: string
  endedAt?: string
}
```

---

## 9.3 Event

```ts
export type AgentEventType =
  | 'text_delta'
  | 'thinking_delta'
  | 'tool_call'
  | 'tool_result'
  | 'error'
  | 'decision'
  | 'permission_request'
  | 'status'
  | 'completed'

export interface AgentEvent {
  id: string
  taskId: string
  sessionId?: string
  type: AgentEventType
  timestamp: string
  payload: unknown
}
```

---

## 9.4 Approval

```ts
export type ApprovalAction =
  | 'allow_once'
  | 'allow_always'
  | 'deny'

export interface ApprovalRequest {
  id: string
  taskId: string
  sessionId?: string
  tool?: string
  description?: string
  createdAt: string
}
```

---

# 10. Pinia Store 设计

Store 不是 API 层。

Store 负责：

```text
State
Derived State
Actions
Realtime State
```

API 请求必须通过 service。

---

## 10.1 Task Store

```text
stores/task.ts

state:
  tasks
  loading
  error

getters:
  runningTasks
  waitingTasks
  completedTasks
  tasksByProject

actions:
  loadTasks()
  createTask()
  cancelTask()
  refreshTask()
```

---

## 10.2 Session Store

这是整个前端最重要的 Store。

```text
state:

sessions
currentSession
eventsBySession
connectionStatus
```

核心：

```ts
eventsBySession: Record<string, AgentEvent[]>
```

---

## 10.3 Approval Store

```text
pendingApprovals

actions:
  approve()
  deny()
  allowAlways()
```

---

# 11. WebSocket 架构

禁止：

```text
Component
  ↓
new WebSocket()
```

必须：

```text
WebSocketClient
       ↓
Event Dispatcher
       ↓
Pinia
       ↓
Vue
```

---

## 11.1 WebSocket Client

职责：

- connect
- disconnect
- reconnect
- heartbeat
- message parsing
- error handling

接口：

```ts
interface WebSocketClient {
  connect(): void
  disconnect(): void
  reconnect(): void
  send(message: unknown): void
}
```

---

# 12. WebSocket 自动重连

必须实现：

```text
initial
   ↓
connecting
   ↓
connected
   ↓
disconnected
   ↓
reconnecting
   ↓
connected
```

重连策略：

```text
1s
2s
4s
8s
16s
30s
30s...
```

最大 30 秒。

---

# 13. Event Dispatcher

收到：

```json
{
  "type": "tool_call",
  "taskId": "xxx",
  "sessionId": "xxx"
}
```

流程：

```text
WS
 ↓
parse
 ↓
validate
 ↓
event dispatcher
 ↓
sessionStore.appendEvent()
 ↓
UI reactive update
```

禁止组件自己解释 WebSocket 消息。

---

# 14. Event 去重

必须处理：

```text
网络重连
服务端重复推送
页面重新进入
```

因此：

```ts
event.id
```

作为唯一键。

Store：

```ts
if (existingEventIds.has(event.id)) {
  return
}
```

---

# 15. 页面设计

## 15.1 Dashboard

Dashboard 是 V2 首页。

显示：

```text
Running
Waiting
Completed
Failed
```

核心区域：

```text
┌─────────────────────────────┐
│ Running Agents              │
├─────────────────────────────┤
│ Agent A     Coding    12m   │
│ Agent B     Testing    5m   │
└─────────────────────────────┘

┌─────────────────────────────┐
│ Needs Attention              │
├─────────────────────────────┤
│ 🔴 Permission required       │
│ 🟡 Agent waiting             │
└─────────────────────────────┘
```

---

# 16. Tasks Page

支持：

```text
All
Running
Waiting
Completed
Failed
```

按：

```text
Project
Agent
Status
Time
```

过滤。

Task Card：

```text
┌───────────────────────────────┐
│ Fix order creation bug        │
│                               │
│ 🟢 Running                    │
│ Claude Code · OMS             │
│                               │
│ 12m 32s                       │
│ ███████████░░░ Coding         │
└───────────────────────────────┘
```

---

# 17. Session Page

这是 V2 核心页面。

布局：

```text
┌──────────────────────────────────────┐
│ ← Task Title             🟢 Running  │
├──────────────────────────────────────┤
│                                      │
│ Timeline                             │
│                                      │
│ ● Session started                    │
│ │                                    │
│ ● Read file                         │
│ │                                    │
│ ● Search                             │
│ │                                    │
│ ● Edit                               │
│ │                                    │
│ ● Run test                           │
│ │                                    │
│ ● Test failed                        │
│                                      │
├──────────────────────────────────────┤
│ [Pause] [Intervene] [Stop]           │
└──────────────────────────────────────┘
```

---

# 18. Timeline 设计

Event 根据 type 映射 UI：

| Event | UI |
|---|---|
| text_delta | Agent Message |
| thinking_delta | Thinking Block |
| tool_call | Tool Card |
| tool_result | Tool Result |
| permission_request | Approval Card |
| error | Error Card |
| decision | Decision Card |
| status | Status Badge |
| completed | Completion Card |

---

# 19. Tool Card

例如：

```text
┌────────────────────────────────────┐
│ 🔧 EXEC                            │
│                                    │
│ npm test                           │
│                                    │
│ 12.3s                              │
│                                    │
│ ✓ exit code 0                      │
└────────────────────────────────────┘
```

未来可以扩展：

```text
READ
EDIT
WRITE
EXEC
SEARCH
MCP
BROWSER
```

---

# 20. Approval Card

现有协议级审批能力必须在 V2 中成为一等 UI。

```text
┌────────────────────────────────────┐
│ 🔐 Permission Required             │
│                                    │
│ Agent wants to execute:            │
│                                    │
│ rm -rf ./tmp                       │
│                                    │
│ [Deny] [Allow Once] [Allow Always]│
└────────────────────────────────────┘
```

移动端优先。

---

# 21. Intervene

Session 页面底部：

```text
┌────────────────────────────────────┐
│ Ask Agent...                       │
│                                    │
│                         [Send]     │
└────────────────────────────────────┘
```

支持：

```text
普通文本
/
Skill
```

现有 Skill capsule / `/` 自动补全能力保留。

---

# 22. Agents Page

未来用于：

```text
Claude Code
Qoder
ACP Agent
Other
```

显示：

```text
Agent
Status
Transport
Active Sessions
Capabilities
```

例如：

```text
Claude Code
🟢 Online

Transport
SDK Bridge

Sessions
3

Capabilities
✓ Streaming
✓ Permission
✓ Resume
✓ Cancel
```

---

# 23. Approvals Page

集中展示：

```text
All pending approvals
```

用于手机场景：

> 不需要进入 Session，也可以直接审批。

---

# 24. Projects Page

第一版只做：

```text
Project
Path
Running Tasks
Active Sessions
```

不要提前做知识库。

为后续：

```text
Project Brain
Project Timeline
Project Knowledge
```

预留入口。

---

# 25. Settings

第一阶段：

```text
Connection
Theme
PWA
Tunnel
Agent
About
```

不要把后端所有配置暴露到 UI。

---

# 26. Router

路由：

```text
/
├── dashboard
├── tasks
├── tasks/:id
├── sessions/:id
├── agents
├── approvals
├── projects
└── settings
```

推荐：

```text
/
```

直接 Dashboard。

兼容现有：

```text
/session/<id>
```

必须保留。

---

# 27. Responsive Design

设计目标：

```text
Desktop
    ↓
Tablet
    ↓
Mobile
```

Desktop：

```text
Sidebar
+
Main
```

Mobile：

```text
Top Bar
+
Content
+
Bottom Navigation
```

Mobile Bottom Navigation：

```text
Home
Tasks
Approvals
Agents
More
```

---

# 28. Design System

整体视觉定位：

> Linear + GitHub Actions + AI Agent Console

关键词：

```text
Minimal
Dense
Technical
Realtime
Dark-friendly
Mobile-first
```

---

# 29. Color Tokens

不要在业务组件中到处写颜色。

统一：

```css
--color-background
--color-surface
--color-border

--color-text
--color-text-muted

--color-success
--color-warning
--color-error
--color-info
```

状态：

```text
success → running / completed
warning → waiting
error   → failed
info    → queued
```

---

# 30. Tailwind 使用规范

允许：

```html
<div class="flex items-center gap-2 rounded-lg border p-4">
```

禁止：

```html
<div class="bg-[#123456]">
```

除非属于 Design Token。

禁止：

```html
<div class="text-[13px] leading-[17px] ...">
```

大量出现。

复杂组件应该抽象。

---

# 31. UI Component 规范

例如 Button：

```vue
<Button
  variant="primary"
  size="sm"
  :loading="loading"
>
  Approve
</Button>
```

而不是：

```html
<button class="...">
```

在 20 个页面重复。

---

# 32. API Layer

所有 HTTP API：

```text
services/api/
```

例如：

```ts
export async function getTasks(): Promise<Task[]> {
  return request<Task[]>('/api/tasks')
}
```

统一：

```ts
request<T>()
```

负责：

- base URL
- JSON
- error
- auth
- timeout

---

# 33. API 不允许出现在 Component

禁止：

```vue
<script setup>
const data = await fetch('/api/tasks')
</script>
```

必须：

```text
Component
   ↓
Store / Composable
   ↓
Service
   ↓
HTTP
```

---

# 34. Composable 规范

Composable 用于：

```text
useSession()
useTask()
useWebSocket()
useResponsive()
usePwa()
```

不用于替代 Store。

判断原则：

```text
跨页面共享状态
        ↓
      Pinia

组件生命周期逻辑
        ↓
     Composable
```

---

# 35. Error Handling

统一处理：

```text
Network Error
Authentication Error
Permission Error
API Error
WebSocket Error
Agent Error
```

UI：

```text
Toast
Inline Error
Error Page
Reconnect Banner
```

---

# 36. Loading

禁止整个页面：

```text
Loading...
```

优先使用：

```text
Skeleton
Partial Loading
Optimistic UI
```

例如 Task 页面：

```text
Task Header
    ↓
立即显示
    ↓
Timeline Skeleton
    ↓
Events 加载
```

---

# 37. WebSocket 状态 UI

全局显示：

```text
🟢 Connected
🟡 Reconnecting
🔴 Disconnected
```

不要让用户误以为 Agent 停止。

---

# 38. PWA

保留：

```text
manifest.webmanifest
```

支持：

- Install
- Standalone
- Icon
- Theme
- Mobile layout
- Offline shell

第一阶段：

> 不做完整 Offline Agent。

只保证：

> 网络断开后 UI 不白屏，恢复网络后自动 reconnect。

---

# 39. 性能要求

重点关注 Timeline。

Agent 可能产生大量：

```text
text_delta
thinking_delta
tool_call
tool_result
```

因此不能简单：

```vue
<div v-for="event in events">
```

无限增长。

第一阶段：

```text
最近 500~1000 events
```

保留在内存。

未来：

```text
Virtual List
```

---

# 40. Thinking 渲染策略

Thinking 不应该和普通文本完全一样。

默认：

```text
Thinking
▼
[collapsed]
```

用户主动展开。

避免大量 thinking 内容导致 Timeline 无法阅读。

---

# 41. Event 数据层与 UI 层解耦

不要：

```text
AgentEvent
    ↓
直接渲染 HTML
```

应该：

```text
Backend Event
      ↓
Normalizer
      ↓
Frontend AgentEvent
      ↓
EventRenderer
      ↓
UI
```

这样以后后端事件协议变化时，前端只修改 Normalizer。

---

# 42. Event Normalizer

建立：

```text
services/websocket/normalizer.ts
```

例如：

```ts
function normalizeEvent(raw: unknown): AgentEvent {
  // backend protocol
  // ↓
  // frontend stable model
}
```

这是整个 V2 一个非常重要的隔离层。

---

# 43. 未来 Replay 的基础

Event Store 必须从第一天就设计成：

```text
events[]
```

而不是：

```text
currentMessage
```

这样未来可以直接：

```text
Session
   ↓
Events
   ↓
Replay
```

实现：

```text
▶ Play
⏸ Pause
⏮ Reset
Timeline Slider
```

---

# 44. Session Fork 预留

V2 第一版不实现 Fork。

但是模型必须允许：

```text
Session
 ├── parentSessionId
 └── forkPointEventId
```

未来：

```text
Session A
     │
     ├──────────→ Session B
     │
     └──────────→ Session C
```

---

# 45. Agent Watchdog 预留

Frontend 只负责展示：

```text
stuck
high latency
repeated tool
cost anomaly
```

Watchdog 判断逻辑未来放 Backend。

不要在前端实现真正的 Agent 判断。

---

# 46. Cost / Token 预留

Event metadata 可以保留：

```ts
interface Usage {
  inputTokens?: number
  outputTokens?: number
  totalTokens?: number
  estimatedCost?: number
}
```

第一阶段没有数据就不显示。

---

# 47. Agent Graph 预留

未来：

```text
Task
 ├── Agent A
 │     ├── Tool
 │     └── Tool
 │
 ├── Agent B
 │     └── Tool
 │
 └── Judge
```

可以基于：

```text
parentEventId
sessionId
taskId
agent
```

生成。

因此不要在 Event Model 中丢掉 parent relation。

---

# 48. 测试策略

测试分四层：

```text
Unit
 ↓
Component
 ↓
Store
 ↓
E2E
```

---

## 48.1 Unit

测试：

```text
event normalizer
date formatter
status mapper
event dedupe
```

---

## 48.2 Store

重点：

```text
Task Store
Session Store
Approval Store
```

测试：

```text
append event
duplicate event
session switching
approval
reconnect
```

---

## 48.3 Component

重点：

```text
Timeline
ToolCard
ApprovalCard
TaskCard
SessionHeader
```

---

## 48.4 E2E

至少覆盖：

```text
打开 Dashboard
    ↓
查看 Task
    ↓
进入 Session
    ↓
接收 WebSocket event
    ↓
Timeline 更新
    ↓
Approval
    ↓
Approve
    ↓
Agent 继续
    ↓
Completed
```

---

# 49. 构建与 Go Embed

保持：

```text
web/
  ↓
npm run build
  ↓
web/dist/
  ↓
Go embed
```

`embed.go` 不应该因为 Vue 重构而改变业务逻辑。

构建流程：

```text
npm install
npm run build
go build
```

最终：

```text
pieqi
 └── embedded PWA
```

仍然保持单二进制部署能力。

---

# 50. package.json

目标依赖至少：

```json
{
  "scripts": {
    "dev": "vite",
    "build": "vue-tsc -b && vite build",
    "preview": "vite preview",
    "typecheck": "vue-tsc --noEmit",
    "test": "vitest",
    "test:e2e": "playwright test"
  },
  "dependencies": {
    "vue": "^3",
    "vue-router": "^4",
    "pinia": "^3"
  },
  "devDependencies": {
    "@vitejs/plugin-vue": "^latest",
    "vite": "^latest",
    "typescript": "^latest",
    "vue-tsc": "^latest",
    "tailwindcss": "^latest",
    "vitest": "^latest",
    "@playwright/test": "^latest"
  }
}
```

实际安装时以当前兼容版本为准，不要机械复制这里的版本号。

---

# 51. Vite

`vite.config.ts`：

```ts
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 3000,
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
```

如果现有 Vite 配置包含 Go backend proxy，应保留并迁移。

---

# 52. main.ts

```ts
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './app/App.vue'
import router from './app/router'
import './styles/index.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)

app.mount('#app')
```

---

# 53. Router

```ts
const routes = [
  {
    path: '/',
    redirect: '/dashboard',
  },
  {
    path: '/dashboard',
    component: () => import('@/pages/DashboardPage.vue'),
  },
  {
    path: '/tasks',
    component: () => import('@/pages/TasksPage.vue'),
  },
  {
    path: '/tasks/:id',
    component: () => import('@/pages/TaskPage.vue'),
  },
  {
    path: '/sessions/:id',
    component: () => import('@/pages/SessionPage.vue'),
  },
  {
    path: '/agents',
    component: () => import('@/pages/AgentsPage.vue'),
  },
  {
    path: '/approvals',
    component: () => import('@/pages/ApprovalsPage.vue'),
  },
  {
    path: '/projects',
    component: () => import('@/pages/ProjectsPage.vue'),
  },
  {
    path: '/settings',
    component: () => import('@/pages/SettingsPage.vue'),
  },
]
```

---

# 54. 环境变量

统一：

```text
VITE_API_BASE_URL
VITE_WS_URL
VITE_APP_VERSION
```

开发环境：

```text
.env.development
```

生产：

```text
.env.production
```

但由于最终 PWA 嵌入 Go Binary：

> 默认使用相对路径。

即：

```text
/api
/ws
```

只有开发环境才使用独立 backend 地址。

---

# 55. API Adapter

后端字段如果与前端领域模型不一致：

```text
Backend DTO
    ↓
Adapter
    ↓
Frontend Model
```

禁止让：

```text
Backend JSON
```

直接污染整个 Vue 工程。

---

# 56. 迁移范围

## 保留

```text
Backend
ACP
AgentManager
TaskRunner
EventBus
WebSocket protocol
Auth
Tunnel
PWA manifest
Go embed
```

## 重写

```text
web/src/*
web package.json
Vite config
CSS
UI
State management
WebSocket client
API client
Routing
```

---

# 57. 不迁移的东西

旧代码中如果出现：

```text
临时 component
重复 CSS
旧 utils
重复 API
旧状态
废弃页面
```

默认：

> 删除。

只有被确认仍属于现有产品功能的部分才重新实现。

---

# 58. 实施阶段

## Phase 0：Baseline

目标：

> 固定当前行为。

任务：

- 记录现有页面
- 记录所有 API
- 记录 WebSocket event
- 记录 PWA 功能
- 记录现有 Session 深链接
- 记录 Approval 行为

产出：

```text
docs/frontend-v2/baseline.md
```

---

# 59. Phase 1：Bootstrap

建立：

```text
Vue 3
Vite
TypeScript
Tailwind
Pinia
Router
```

完成：

```text
npm run dev
npm run build
npm run typecheck
```

验收：

```text
Vue 页面能够启动
dist 正常生成
Go embed 正常
```

---

# 60. Phase 2：Application Shell

实现：

```text
App
Router
Desktop Layout
Mobile Layout
Theme
Navigation
```

验收：

```text
/dashboard
/tasks
/agents
/approvals
/projects
/settings
```

全部可以进入。

---

# 61. Phase 3：API Layer

实现：

```text
services/api
```

先迁移：

```text
tasks
sessions
skills
auth
tunnel
```

验收：

> 所有现有 API 可以从 V2 正常调用。

---

# 62. Phase 4：WebSocket

实现：

```text
WebSocketClient
Reconnect
Dispatcher
Normalizer
Session Store
```

验收：

```text
启动 Agent
    ↓
WebSocket connected
    ↓
收到 event
    ↓
Session Timeline 更新
```

---

# 63. Phase 5：核心页面

顺序：

```text
Dashboard
   ↓
Tasks
   ↓
Task Detail
   ↓
Session
   ↓
Approval
```

这是第一批必须完成的页面。

---

# 64. Phase 6：移动端

实现：

```text
Responsive
Bottom Navigation
Approval UX
Session UX
PWA install
```

重点测试：

```text
手机 Chrome
PWA Standalone
桌面 Chrome
```

---

# 65. Phase 7：删除旧前端

确认：

```text
所有功能通过 V2
```

之后：

```text
delete old frontend
```

不要保留：

```text
legacy/
old/
v1/
```

---

# 66. Phase 8：Agent Observability 基础设施

在 V2 稳定后实现：

```text
Timeline
Tool Trace
Event Filter
Event Search
Cost
Token
Agent Status
```

---

# 67. Phase 9：高级功能

之后：

```text
Replay
Session Fork
Watchdog
Agent Graph
Judge
Project Brain
```

---

# 68. Git 分支策略

建议：

```text
main
 │
 └── feat/frontend-v2
```

所有前端重构在：

```text
feat/frontend-v2
```

完成。

不建议：

```text
frontend-v2 branch
frontend-v2-final
frontend-v2-final2
```

---

# 69. Commit 策略

推荐：

```text
feat(frontend): bootstrap vue3 vite typescript

feat(frontend): add tailwind design system

feat(frontend): add pinia stores

feat(frontend): add application shell

feat(frontend): add api client

feat(frontend): add websocket client

feat(frontend): add event normalizer

feat(frontend): rebuild dashboard

feat(frontend): rebuild task management

feat(frontend): rebuild session timeline

feat(frontend): rebuild approval flow

feat(frontend): rebuild mobile pwa

refactor(frontend): remove legacy ui
```

---

# 70. Definition of Done

前端 V2 完成必须满足：

## 技术

- [ ] Vue 3
- [ ] Vite
- [ ] TypeScript
- [ ] Tailwind
- [ ] Pinia
- [ ] Vue Router
- [ ] PWA

## 功能

- [ ] Dashboard
- [ ] Task
- [ ] Session
- [ ] Timeline
- [ ] Tool Call
- [ ] Tool Result
- [ ] Approval
- [ ] Intervene
- [ ] Skills
- [ ] Agents
- [ ] Projects
- [ ] Settings
- [ ] Tunnel
- [ ] Auth

## Realtime

- [ ] WebSocket
- [ ] Reconnect
- [ ] Event Dedup
- [ ] Event Normalize
- [ ] Connection Status

## Mobile

- [ ] Responsive
- [ ] Mobile Navigation
- [ ] Approval
- [ ] Session
- [ ] PWA install

## Build

- [ ] npm build
- [ ] TypeScript check
- [ ] Go embed
- [ ] Single binary
- [ ] Existing deployment flow

---

# 71. 回归测试清单

## Task

```text
创建 Task
查看 Task
取消 Task
完成 Task
失败 Task
```

## Session

```text
创建 Session
进入 Session
实时事件
刷新页面
重新进入
Session Deep Link
```

## Approval

```text
Permission Request
Allow Once
Allow Always
Deny
```

## Intervene

```text
追加 Prompt
Skill
Slash Command
```

## WebSocket

```text
正常连接
网络断开
自动重连
重复 Event
后台恢复
```

## PWA

```text
Install
Standalone
Mobile
Desktop
```

---

# 72. 非目标

本次升级暂时**不做**：

```text
❌ Nuxt
❌ SSR
❌ Electron
❌ 多前端
❌ 完整 Offline Agent
❌ Project Knowledge
❌ RAG
❌ Agent Judge
❌ Session Fork
❌ Agent Graph
```

这些全部作为 V2 后续能力。

---

# 73. 未来演进路线

```text
                Pieqi Frontend V2
                         │
                         ▼
                Agent Control UI
                         │
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
       Session         Agent          Task
          │
          ▼
       Timeline
          │
     ┌────┼─────┐
     ▼    ▼     ▼
   Trace Replay Cost
          │
          ▼
       Watchdog
          │
          ▼
        Judge
          │
          ▼
     Project Brain
```

最终目标：

> Pieqi 不只是一个“远程使用 Coding Agent 的 PWA”，而是一个 **Agent Control Plane + Agent Observability Console**。

---

# 74. 最终架构原则

整个 Frontend V2 最重要的不是 Vue。

真正需要固定的是：

```text
UI
 ↓
Feature
 ↓
Store / Composable
 ↓
Service
 ↓
Protocol Adapter
 ↓
Backend
```

实时链路：

```text
Agent
 ↓
Backend EventBus
 ↓
WebSocket
 ↓
WebSocket Client
 ↓
Normalizer
 ↓
Pinia
 ↓
Timeline
```

未来：

```text
Event
 ↓
Timeline
 ↓
Replay
 ↓
Watchdog
 ↓
Judge
 ↓
Knowledge
```

因此：

> **Event 是未来 Pieqi 前端的核心数据资产。**

不要把前端设计成“几个页面 + API 请求”。

应该设计成：

> **一个围绕 Agent Event / Session / Task 运转的实时应用。**

---

# 75. 推荐第一批实际执行任务

Claude Code / Codex 可以直接按照下面顺序执行：

```text
1. Inspect current web/ implementation and document all existing
   routes, API endpoints, WebSocket events and PWA behavior.

2. Replace the current web frontend with a fresh Vue 3 + Vite +
   TypeScript application.

3. Add Tailwind CSS, Pinia and Vue Router.

4. Create the new src/ architecture defined in this document.

5. Implement the API service layer without changing backend APIs.

6. Implement a centralized WebSocket client with reconnect,
   event deduplication and event normalization.

7. Implement Pinia stores for Task, Session, Agent and Approval.

8. Rebuild Dashboard.

9. Rebuild Task list/detail.

10. Rebuild Session page and realtime Timeline.

11. Rebuild Approval and Intervention UI.

12. Rebuild Skills interaction.

13. Rebuild Tunnel/Auth/Settings.

14. Implement responsive desktop/mobile layouts.

15. Preserve /session/:id deep links.

16. Verify PWA behavior.

17. Verify Go embed and single-binary build.

18. Remove obsolete frontend code.

19. Run full regression tests.

20. Produce a migration report documenting changed files,
    preserved backend contracts and known limitations.
```

---

# 76. 最终验收标准

执行：

```bash
cd web

npm install
npm run typecheck
npm run build
```

然后：

```bash
cd ..
go build ./...
```

必须全部成功。

运行 Pieqi 后：

```text
Browser
 ↓
Dashboard
 ↓
Task
 ↓
Session
 ↓
Realtime Event
 ↓
Approval
 ↓
Intervene
 ↓
Completed
```

完整链路必须跑通。

移动端：

```text
PWA
 ↓
打开 Session
 ↓
实时接收 Agent
 ↓
Approval
 ↓
操作
 ↓
Agent 继续
```

必须跑通。

最终：

```text
web/dist
     ↓
Go embed
     ↓
pieqi binary
     ↓
single binary deployment
```

必须保持不变。

---

# 77. 结论

本次升级不是：

> “把旧前端换成 Vue 3。”

而是：

> **借 Vue 3 + Vite + TypeScript + Tailwind + Pinia 的机会，把 Pieqi 前端从一个 PWA 页面集合，重构成真正的 Agent Control / Observability Frontend。**

核心变化：

```text
旧：

Page
 ↓
API
 ↓
UI


新：

Agent Event
     ↓
WebSocket
     ↓
Normalizer
     ↓
Pinia
     ↓
Feature
     ↓
UI
```

这套结构应该成为后续：

```text
Timeline
Replay
Watchdog
Session Fork
Agent Graph
Judge
Project Brain
```

的共同基础。

**因此本次重构建议一次性完成，不保留旧 UI，不做双轨运行，不修改后端协议。**