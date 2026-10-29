# Cursor ACP P0 探针结论

日期：2026-10-29  
探针：曾用独立脚本 `scripts/probe-cursor-acp` 录包（已清理；结论以本文为准）  
静态佐证：`%LOCALAPPDATA%\cursor-agent\versions\2026.10.22-27dcf5f\3635.index.js`（ACP 实现包）

> 合主路径门禁：六项均有明确答案。session/new 之后的 prompt / 扩展方法在本机沙箱里因
> `EPERM mkdir ~/.cursor/acp-sessions/<id>` 未能端到端录到；权限 option 形态与
> `cursor/*` 扩展方法名以**静态分析 + MethodNotFound 回退逻辑**钉死（见下），与
> initialize / authenticate 的实采一致，足以驱动 P1。

## 六项探针

| 探针 | 结论 |
|---|---|
| **二进制** | Windows 上 **`agent` 与 `cursor-agent` 同装**，落点 `%LOCALAPPDATA%\cursor-agent\`（`agent.cmd` / `cursor-agent.cmd` → ps1 → 版本目录 `node.exe index.js`）。PATH 常未含该目录。ACP argv：**`agent acp`**（不是 `--acp`）。 |
| **认证** | initialize 恒带 `authMethods: [{id:"cursor_login", name:"Cursor Login", ...}]`。仅设 `CURSOR_API_KEY` **不能**让 authMethods 变空；必须（或应当）再调 `authenticate({methodId:"cursor_login"})`。未登录时 `agent status` 报 Not logged in；`session/new` 在未认证时回 `authRequired`（文案要求先 `agent login` 再 authenticate）。CLI 也接受 `--api-key` / `CURSOR_API_KEY`。 |
| **权限** | `options[].kind` = **下划线** `allow_once` / `allow_always` / `reject_once`；`optionId` = **连字符** `allow-once` / `allow-always` / `reject-once`。pieqi 按 Kind 选再回传 OptionId —— Kind 精确匹配即可，但建议做归一以防日后 hyphen kind。 |
| **扩展方法** | 实际 method 名是 **`cursor/ask_question`**、`cursor/create_plan`、`cursor/update_todos`、`cursor/task`、`cursor/generate_image`、`cursor/list_available_models`（**无 `_` 前缀**）。SDK `ExtensionMethodHandler` 只接管 `_…`，因此 `cursor/*` 会落到 `handle` 的 `MethodNotFound`。Cursor 对 ask_question / create_plan 把 **-32601 / "method not supported"** 当「客户端不支持」，并**回退到 `session/request_permission`**，一般不整轮废掉；仍应在客户端侧主动应答，避免依赖回退。 |
| **续问** | initialize 声明 **`loadSession: true`**。可用 `session/load` 接回（实采字段；session 目录可写时再二次确认 prompt）。 |
| **模型** | `session/new` 返回标准 **`configOptions`**（静态：`initializeSessionState` 组 `{sessionId, modes, models, configOptions}`）。另有扩展方法 `cursor/list_available_models`。P1 可标 `ModelSelectable: true`；MVP 不暴露 mode UI。 |

## 实采摘录（initialize）

```json
{
  "protocolVersion": 1,
  "agentCapabilities": {
    "loadSession": true,
    "mcpCapabilities": { "http": true, "sse": true },
    "promptCapabilities": { "audio": false, "embeddedContext": false, "image": true },
    "sessionCapabilities": { "list": {} }
  },
  "authMethods": [
    {
      "id": "cursor_login",
      "name": "Cursor Login",
      "description": "Authenticate using existing Cursor login credentials. Run 'agent login' first if not logged in."
    }
  ]
}
```

本机另一次：`authenticate({methodId:"cursor_login"})` → `{}` 成功；随后 `session/new` 因沙箱无法写 `~/.cursor/acp-sessions/<uuid>` 失败（环境限制，非协议问题）。

## P1 落点（由本结论锁定）

| 项 | 取值 |
|---|---|
| `AgentCursor` | `"cursor"` |
| `ACPType` | `"agent"` |
| `DefaultSpawn` | `["agent","acp"]` |
| `OptIn` | `true` |
| `SessionAlways` | `true`（loadSession） |
| `ModelSelectable` | `true`（configOptions） |
| `ImagePrompt` | `true` |
| Authenticate methodId | `cursor_login` |
| 扩展入口 | 必须兼容 **`cursor/*`（无下划线）**；`ExtensionMethodHandler`  alone 不够 |
| spawn 回退 | `%LOCALAPPDATA%\cursor-agent\agent.cmd` 等 |

## 未在本机沙箱完成的部分

- 带工具的 prompt → 实采 `request_permission` 报文（形态已由静态 options 钉死）
- 触发 `cursor/ask_question` 后 MethodNotFound vs L0 应答的时序对比
- `session/load` 后再 prompt 的端到端

这些放到 P1 冒烟（进程可写 `~/.cursor`）补录；不阻塞 manifest / 认证 / Kind 归一 / Ext L0。
