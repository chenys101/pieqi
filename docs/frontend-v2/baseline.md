# Frontend V2 Baseline（Phase 0）

> 重构前固定当前行为。后端协议一律不变，本文档是 V2 重写的契约基准。

## 1. 现有页面与路由

| 路径 | 行为 |
|---|---|
| `/` | 新建态：主区为「新建任务」表单 |
| `/session/<taskId>` | Deep Link：直接选中对应任务详情（刷新/分享/浏览器前进后退均保持） |
| 子路径部署 | 应用可能挂在隧道子路径下，旧代码用 `/<base>/session/<id>` 前缀推断 |

V2 必须保留 `/session/:id` deep link（方案 §26），新增 `/sessions/:id` 并将 `/session/:id` 重定向兼容。

## 2. HTTP API 契约（不修改）

所有请求携带 `X-Feishu-Openid`（如有）与 `Authorization: Bearer <tunnel_token>`（如有）。

| 方法 | 路径 | 请求体 | 响应 |
|---|---|---|---|
| GET | `/api/tasks` | - | `{ projects: [{ project_id, project_path, counts: Record<status,int>, tasks: Task[] }] }` |
| GET | `/api/tasks/:id` | - | `Task` / 404 |
| POST | `/api/tasks` | `{ project_path, prompt }` | 201 `Task`（预置 seq=1 的 user 事件） |
| POST | `/api/tasks/:id/intervene` | `{ kind: "decision"\|"append_prompt", decision_id?, choice?: "approve"\|"deny", text? }` | `{ ok }` / `{ ok, resumed }` |
| POST | `/api/tasks/:id/cancel` | `{}` | `{ ok }` |
| DELETE | `/api/tasks/:id` | - | `{ ok }`（运行中先取消并关闭 ACP 会话） |
| GET | `/api/skills` | - | `{ skills: [{ name, description, dir }] }` |
| GET | `/api/commands` | - | `{ commands: [{ name, description, dir }] }` |
| GET | `/api/auth/status` | - | `{ bound, debug, openid?, nickname?, bound_at? }`（外网只回 bound/debug） |
| POST | `/api/auth/bind` | `{ openid, user_id?, nickname? }` | 仅内网 |
| DELETE | `/api/auth/bind` | - | 仅内网 |
| GET | `/api/tunnel/status` | - | `{ active, tunnel_url?, expires_at? … }` |
| POST | `/api/tunnel/start` | `{ ttl: "15m"\|"1h"\|"4h" }` | `{ tunnel_url, lark_deep_link, token, expires_at }` |
| POST | `/api/tunnel/stop` | `{}` | `{ status: "stopped" }` |
| POST | `/api/tunnel/reset` | `{}` | `{ token }` |
| POST | `/api/tunnel/renew` | `{ ttl }` | 同 `/start` |
| GET | `/api/tunnel/qrcode?text=` | - | PNG 二维码 |
| POST | `/api/larkreg/start` | `{}` | `{ qr_url }`（仅内网，403 表示外网） |
| GET | `/api/larkreg/poll` | - | 202 等待中 / 200 `{ app_id, hint? }` |
| GET | `/api/larkreg/status` | - | `{ registered, app_id }` |
| GET/POST | `/api/larkreg/config` | `{ app_id, app_secret, verify_token, encrypt_key, event_mode: "longconn"\|"webhook" }` | GET 回显 `secret_set`，POST 热应用 |

### Task 模型（后端 JSON 字段）

```
id, source("http"|"im"|"cli"), project_id, project_path, worktree_path,
claude_session_id, acp_session_id?, status, prompt, title?, output?,
events?: [{ seq, type, text?, tool_name?, tool_use_id?, input?, result?, is_error?, at }],
current_decision?: { id, kind("approval"|"choice"), tool_name?, summary, options, created_at },
error?, origin_channel?, origin_chat_id?, origin_identity?,
created_at, updated_at, started_at?, finished_at?
```

- 状态机：`pending → running → waiting_input ⇄ running → completed | failed | cancelled`
- 事件类型：`text | user | thinking | tool_use | tool_result | status`
- 决策：`approval`（approve/deny，进程存活走 hook）；`choice`（已废弃，兜底提示直接文本回复）

## 3. WebSocket 协议（`GET /api/ws?token=<tunnel_token>`）

连接后服务端先推快照，再持续转发 EventBus；服务端 30s 心跳 ping。

| 消息 | 结构 | 前端行为 |
|---|---|---|
| snapshot | `{ type: "snapshot", tasks: Task[] }` | 全量替换任务列表，恢复 URL 选中 |
| task_created / task_updated | `{ type, task_id, task: Task }` | upsert 任务 |
| task_deleted | `{ type, task_id }` | 移除任务（当前选中则清空） |
| task_delta | `{ type, task_id, delta: { text, is_thought } }` | 真流式增量：同类型末事件追加 text，否则新建事件；非思考增量同时累积到 `task.output` |

## 4. 认证

- `X-Feishu-Openid`：飞书环境提供（sessionStorage 缓存，debug 可用 `?openid=`）。
- tunnel token：sessionStorage，首次从 URL `?token=` 捕获；WS 连接也带 `?token=`。
- 401：一次性顶部横幅提示「隧道 token 无效或已过期」。

## 5. PWA

- `manifest.webmanifest`：name/short_name/display:standalone/theme `#1a1a2e`/start_url `/`（当前 icons 为空且未在 public/ 下，实际未被 embed——V2 修复到 public/）。
- `public/sw.js`：导航 network-first、`/assets/*` cache-first、API/WS 不缓存、版本化清理。

## 6. 现有功能清单（V2 必须覆盖）

- 任务侧栏：项目分组（路径归一：斜杠统一、Windows 大小写）、组内按 updated_at 倒序、含 running/waiting_input 组自动展开、状态计数徽标。
- 任务项：状态点、标题（LLM 生成 title 优先，prompt 智能截断兜底）、timeAgo、更多菜单（删除，confirm）。
- 新建任务：项目下拉（最近使用派生）+ 自定义路径切换、prompt 输入、Ctrl/Cmd+Enter、在途防重、成功后跳转详情 + 「思考中」占位。
- 会话详情：header（状态徽章、#id、项目、timeAgo、标题）、事件流（user 右对齐气泡 / text 气泡 / thinking 折叠 / tool_use 卡片 / tool_result 折叠+错误标红）、旧任务 output 兜底。
- 决策横幅：approval → 批准/拒绝；choice → 提示直接文本回复。
- 干预输入框：运行中为「中止」■；终态为「发送」↑（append_prompt 续问走 Resume）；Ctrl+Enter；在途防重；乐观渲染用户气泡 + 思考中徽章；斜杠补全（commands+skills 分组、↑↓ 选择、回车插入）。
- 滚动策略：切任务/发送后强制到底；在底部时跟随流式输出；翻历史不被重置。
- 设置区：渠道（飞书扫码/手动配置，仅内网）、外网隧道（状态全员可读、控制仅飞书移动端、QR 展示）。
- 移动端（≤720px）：侧栏抽屉、点任务自动收起、ESC 关闭。
- auth status boot 轮询：debug 横幅 / 未绑定提示。

## 7. 构建

`web/ → npm run build → web/dist → go:embed (web/embed.go) → gin NoRoute SPA fallback（/api、/internal、/webhook 除外）→ 单二进制`。
