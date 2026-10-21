# claude-sdk-bridge — Claude Agent SDK 常驻桥

把官方 `@anthropic-ai/claude-agent-sdk` 的多轮会话、流式增量、权限回调、resume、图片块封装成
**HTTP + SSE**，供 Go 侧 [`internal/agent/claude/bridge/client.go`](../../internal/agent/claude/bridge/client.go) 调用。

pieqi 的 `agents.claude.transport` 默认就是 `sdk-bridge`：服务启动时探活 `base_url`（默认 `http://127.0.0.1:18790`），
`auto_start: true` 时探活失败会 spawn `node src/index.js` 把桥拉起；桥不可用则透明回退 print 传输。

```
pieqi (Go) ──HTTP──> bridge ──SDK──> claude 子进程
                  <─SSE──
```

## 文件

| 文件 | 职责 |
|---|---|
| `src/index.js` | HTTP 服务与路由；`/v1` 下除 `/v1/health` 外全部校验 `token` |
| `src/session.js` | 单会话生命周期：SDK query、流式事件转发、权限挂起/释放、resume；**`TOOL_KINDS` 工具名 → ACP ToolKind 映射表在这里** |
| `src/images.js` | `/prompt` 的 images 校验与 content block 拼装（拆出来是为了不 import `index.js` 就能单测） |

端点（`index.js` 顶部的契约）：

```
POST   /v1/sessions                       创建会话（可选 resumeSdkSessionId）
POST   /v1/sessions/:id/prompt            追加一轮（非阻塞 202；事件走 SSE）
GET    /v1/sessions/:id/events            SSE 事件流（连接时回放历史 ring）
POST   /v1/sessions/:id/permissions/:rid  审批 allow/deny
POST   /v1/sessions/:id/cancel            取消当前轮
DELETE /v1/sessions/:id                   结束会话（杀子进程）
GET    /v1/health                         探活
```

## 命令

```bash
npm install
npm start     # node src/index.js
npm test      # tool_kind / image_blocks / prompt_stream / image_http_probe / bridge_self_test
```

`bridge_self_test.mjs` 会起一个真实 Agent SDK 会话跑全链路，需要本机 `claude` CLI 已登录。

## ⚠️ 改 `TOOL_KINDS` 前必读

免审名单认的是 **ACP ToolKind**（`read / search / fetch / think / edit / move / execute / switch_mode / other / delete`），
不是工具名；分级表 `riskLevelKinds` 在 Go 侧 `internal/core/settings_store.go`。
**桥与 Go 侧任一处漏传 kind，表现都是静默退化成"全部人工审批"**。
Go 侧回归测试：`internal/agent/claude/session_test.go` 的 `TestSessionPermissionCarriesToolKind`；
本仓回归测试：`test/tool_kind_test.mjs`。
