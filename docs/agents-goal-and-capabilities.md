# 三个 Agent 的 goal 能力：证据链与结论（2026-10-18 实测 + 官方文档核验）

> 本文回答三个问题：
> 1. claude / qoder / dsh 各自有没有 goal？
> 2. **经 pieqi 走的那条通道（ACP / SDK）能不能用？**
> 3. 三者语义是不是一回事？
>
> 结论都带**独立证据**（官方文档 / 二进制字串 / 真实协议实测），不靠"名字像"推断。
> 起因是一次真实的犯错记录，见 §6。

---

## 1. 结论速览

| | **claude**（SDK Bridge） | **qoder**（qodercli --acp） | **dsh**（dsh --profile acp） |
|---|---|---|---|
| 存在 goal | ✅ | ✅（三者中最完整） | ✅ |
| **经 pieqi 可用** | ◐ **部分**（命令生效，状态丢失） | ✗ **不可用** | ✓ **完整** |
| 语义 | **完成条件**（condition） | **目标**（objective）+ 轮次上限 | **目标**（objective）+ 轮次上限 |
| 阻塞点 | 桥缺 `active_goal` 转发 | ACP 只开放 4 个斜杠命令，goal 不在其中；goal 是 SDK control request | 无 |

> 用 `✓ / ◐ / ✗` 三档，**不要写成"支持/不支持"二值** —— claude 是真实的部分可用状态。

---

## 2. qoder：有 goal，但 ACP 路径不可达（三方证据）

### 证据 A：官方 ACP 文档列出的命令**只有 4 个**

[Qoder CLI · ACP](https://docs.qoder.com/cli/acp) 原文：

> **Slash Commands** — Currently supported commands are listed below:
> - `/init`、`/memory`、`/about`、`/help`

同一页的 Features 表列出 Built-in Tools / Subagent / MCP / Permission Configuration /
Context Compression / Multimodal —— **唯独没有 goal**。

### 证据 B：官方 Slash Commands 把 `/goal` 归在**交互式会话**命令

[Slash Commands](https://docs.qoder.com/cli/slash-reference)：

> In an **interactive session**, commands starting with `/` are slash commands.
> Typing `/` brings up the command completion list.

`/goal` 与 `/plan`、`/loop`、`/quest`、`/kanban` 并列在「Work Modes」组 ——
全是 TUI 面板类命令，而 ACP 只实现了其中 4 个。

### 证据 C：goal 是 **SDK control request**，不是 agent 消息

[Agent SDK · How it works](http://docs.qoder.com/cli/sdk/how-it-works) 把通信分成两类：
**Agent messages**（user/assistant/tool/result）与 **control messages**（初始化、中断、
会话操作、权限决策、hooks）。

二进制里 `get_goal` / `set_goal` / `clear_goal` / `resume_goal` / `set_goal_max_turns`
全部挂在 `HeadlessSession` 的 **control request 分支**上（`d==="get_goal"`、`d==="clear_goal"`…），
即 SDK 模式通道，**不在 ACP 路径上**。

### 证据 D：真实 ACP 实测（两组对照）

用 `qodercli 1.1.64 --acp -m Qwen3.8-Flash` 直接说 JSON-RPC：

| 组 | 输入 | tool_calls | goal 工具 | 权限请求 |
|---|---|---|---|---|
| A | 字面 `/goal 把 web/e2e 的 P0 待办实现掉` | 37 | **0** | 4 |
| B | 自然语言「持续把这个目标做完直到完成：…」 | 25 | **0** | 3 |

- `session/new` 返回 `sessionId, modes, models, configOptions`，**`availableCommands: null`**；
- A 组模型直接把 `/goal xxx` 当成"去处理 xxx"，回复原文：
  *"I'll find the P0 backlog items in the e2e docs first."*
- 两组的事件流里 `create_goal` / `update_goal` / `get_goal` **出现 0 次**。

### qoder 的 goal 到底有什么（官方 [Goal Command Reference](https://docs.qoder.com/cli/goal-reference)）

子命令：`/goal <description> [--turns N]`、`status`、`clear`、`pause`、`resume`、`take`（跨会话认领）。

持久字段：`objective`、`status(active/paused/complete)`、`maxTurns`、`turnsUsed`、
`timeUsedSeconds`、`ownerSessionId`、`planWasActive`。

行为要点：
- `turnsUsed` 达上限自动暂停，`/goal resume` 再给 100 轮；
- 进程异常终止后 active goal 下次启动降级为 paused；
- **Permission Mode Independence**：goal 从不改变权限模式（见 §4）。

> ⚠️ **不由此得出"以后永远不可用"**：这是**当前版本（1.1.64）ACP 的开放范围**。
> Qoder 若把 `/goal` 加进 ACP 命令列表，或暴露 goal 的 control request，结论即变。
> 能改的只有对端，pieqi 侧无解。

### 开源问题

qodercli **闭源**：`qodercli.exe` 是 207MB 单文件（Bun 打包，路径含 `B:/~BUN/root/chunk-*.js`），
`~/.qoder` 下无源码与 LICENSE。npm 上的 `@qodercn-ai/qoderclicn` 是**安装分发包**，不是可读源码。
**因此"读源码确认"这条路不存在**，只能用「官方文档 + 二进制字串 + 协议实测」三方交叉验证。

---

## 3. 另外两家

### dsh：完整可用（实测）

同一套 ACP 探针，发字面 `/goal xxx`：

```
[tool_call] title:"create_goal" kind:"other"
   → {"goal":{"id":"goal-…","revision":1,"phase":"active",
              "roundsStarted":0,"maxGoalRounds":256},"activation":"armed"}
[tool_call] title:"update_goal" action:"edit"      ← 模型自己纠正一次
!! PERMISSION REQUEST: options=[allow-once, reject-once]
```

**机制**：dsh 的 goal 是**模型可见的工具**（`create_goal`/`get_goal`/`update_goal`），
`/goal` 命令由 `dsh-command-goal` 提供但落在命令平面；即便命令到不了，模型也会从
"工具说明"里读出目标意图并调工具。所以 **ACP 下能生效**。

权限细节（`dsh-tool-goal` 的 authority 检查）：
- `create`/`edit`/`pause`/`resume` 要求当前轮次内存在来源为 `user` 的消息；
- **goal 自动续行（Round）产生的消息来源是 `goal`，不是 `user`** ——
  所以自动 Round 里不能再创建/编辑 goal；
- `blocked` 要连续 N 轮同一阻塞条件才被接受。

### claude：命令生效，但状态被桥丢弃

SDK 类型定义里确有该事件（`@anthropic-ai/claude-agent-sdk` 0.3.269）：

```ts
export declare type SDKActiveGoalMessage = {
  type: 'active_goal'
  value: { condition: string; iterations: number; set_at: number;
           tokens_at_start: number; last_reason?: string } | null
}
```

实测（同一次 SDK 会话的对照）：

| 输入 | assistant 输出 |
|---|---|
| `/goal 把 P0 待办实现掉` | **`Goal set: 把 P0 待办实现掉`** |
| `把 P0 待办实现掉`（对照） | （无此回执） |
| `/goal`（裸） | **`No goal set. Usage: \`/goal <condition>\``** —— 只发了 1 个 system 消息 |

`Goal set: …` 是**本地命令解析**产生的回执，不是模型回答（裸命令时模型根本没被调用）。

**但 pieqi 看不见状态**：`services/claude-sdk-bridge/src/session.js` 的消息 switch 只有
`system` / `stream_event` / `assistant` / `user`，**没有 `case "active_goal"`** ——
goal 的迭代次数、上次未满足原因全部被静默丢弃。

---

## 4. 三个容易混为一谈的概念（都在这里分清楚）

### ① `/goal` 命令 与 `create_goal` 工具：**同一个概念的两个入口**

都在回答"这次算什么算完"。区别只在入口：

| | 命令 | 工具 |
|---|---|---|
| 谁解析 | CLI 本地 | 模型推理后调用 |
| 通道 | 命令平面 / TUI | agent 消息（tool_call） |

所以**不要把两者当成不同概念**；但也不要把"语义"也统一：见 ③。

### ② yolo ≠ goal：它是**权限模式**的别名

`qodercli` 的 `--permission-mode` 五档（二进制原文）：

```
Accepts: default, accept_edits, auto, bypass_permissions, dont_ask
Aliases: yolo = bypass_permissions, acceptEdits = accept_edits, dontAsk = dont_ask
```

`--yolo` 是隐藏独立标志（`--help` 不列）。TUI 里 `Ctrl+Y` 切换。

| 模式 | 语义 |
|---|---|
| `default` | 逐次询问 |
| `accept_edits` | 自动接受文件编辑，其余照问 |
| `auto` | **分类器**判定（分类器不可用时只能提示，不能批准） |
| `bypass_permissions`（`yolo`） | 跳过所有权限检查 |
| `dont_ask` | 不询问，直接拒绝 |

⚠️ 有安全例外：`bypass_immune.keep_ask` —— 一类操作在 yolo 下**仍会问**。

### ③ goal 语义：**不能统一**

- **claude**：`condition`（什么条件算完成）→ Stop hook 判定
- **qoder / dsh**：`objective`（目标）+ 轮次上限 + 自动续行

dsh 与 qoder 可以共用一套抽象（设置/读取/变更/推进四动作），**claude 必须单独处理**。

### ④ goal 与免审批是**两个独立开关**（官方明文）

> **Permission Mode Independence**: Goal never changes the Permission Mode; tool approvals
> during execution follow the current mode. For unattended execution, switch to `auto`
> or `yolo` beforehand. —— [Qoder · Goal Command Reference](https://docs.qoder.com/cli/goal-reference)

**这直接回答"goal 能不能顺带免审批"：不能。** 想免审批必须单独切权限模式，
而开了 `yolo` 之后 pieqi 的审批卡会**彻底失效**（对端不再发 `session/request_permission`），
这正是 `internal/core/agent_perm.go` 那条注释警告的"门禁降级成上次我点过"。

---

## 5. 对 pieqi 的落点

1. **能力矩阵按 `✓ / ◐ / ✗` 三档展示**（§1），并附本文链接作为依据。
2. **补桥的 `active_goal` 转发**（`session.js` 加一个 `case`）—— 这是最便宜的一处修复，
   把 claude 从 `◐` 往回补。
3. **不要在 pieqi 侧伪造 goal**：三条通道的上限由对端决定，pieqi 只能展示与转发。
4. **不要把 yolo 暴露成"免审批按钮"**：一旦开启，pieqi 的审批链路整体失效，
   与 `agent_perm.go` 的设计前提冲突；要开也只能作为"显式放弃 pieqi 审批"的高危设置。

---

## 6. 犯错记录（写下来是为了不再犯）

同一问题我连续错了三次，根因相同：**顺着提问里的词往下推，没先做独立验证**。

| # | 错误结论 | 正确结论 | 当时缺的证据 |
|---|---|---|---|
| 1 | "claude 与 dsh 的 goal 概念不同" | 同一概念的两个入口；语义差异是产品级 | 没区分"入口"与"语义" |
| 2 | （把 yolo 当成 goal 类功能） | yolo 是**权限模式**别名 | 没查 `--permission-mode` 取值表 |
| 3 | "qoder 没有 goal" | **有**，且最完整；只是 ACP 不开放 | 没查官方文档，只看了实测 |

**可复用的判据**：对一个 agent 的能力下结论，至少要有两条**互相独立**的证据
（文档 / 二进制 / 协议实测），且必须明确"测的是哪条通道"。
本轮 qoder 的最终结论就是四条独立证据（A/B/C/D）互相印证的结果。

---

## 7. 复现方法

```bash
# 1) 扫二进制字串（不依赖源码）
#    qodercli 里搜 --permission-mode 取值与别名、goal 工具名
# 2) ACP 实测：直说 JSON-RPC，不经 pieqi
#    initialize → session/new → session/prompt，观察 session/update 里的 tool_call
#    ⚠️ qodercli 的会话落盘在 E:\appData\AICache\.qoder，受限沙箱下会 EPERM
# 3) 官方文档
#    https://docs.qoder.com/cli/acp
#    https://docs.qoder.com/cli/goal-reference
#    https://docs.qoder.com/cli/slash-reference
#    http://docs.qoder.com/cli/sdk/how-it-works
```

dsh 侧：`C:\Users\13631\.dsh-runtime\node_modules\@deepseek-ai\` 下各包的
`README.zh.md` 与 `lib/index.js` 可读，是判断"协议暴露了什么"的第一手资料。
