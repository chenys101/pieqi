# dsh 图片能力：上游源码复核记录

> 目的：把「pieqi 侧图片能力判定链」的结论从**构建产物反推**升级为**上游源码直证**，
> 并判定 `pieqi-image-capability-changeset.md`（harness 侧会话产出）中 Task A 的取舍是否成立。
>
> 取证对象：
> - 上游源码 `D:\code\agent\deepseek-harness`，`packages/*/package.json` 版本 `0.2.1-alpha.1`；
> - 本机运行产物 `~/.dsh-runtime`，版本 `0.2.0-rc.2`（当前为 **fork** 版，官方原版留档 `forks/dsh-acp/lib/index.orig.js`）。

## 1. 判定链（源码级，非 bundle 反推）

| 结论 | 源码位置 | 佐证 |
|---|---|---|
| 图片能力是**四道前置**的合取 | `packages/acp/acp/src/content.ts:94-97` | `attachments` 服务在、`llm` 服务在、`provider` 与 `model` 均非 undefined、`attachments.imageLimits.mediaTypes` 含四个栅格类型之一 |
| 还要模型自己声明收图 | `content.ts:99-100` | `resolveModelInfo(provider, model).inputModalities?.includes('image') === true` |
| 只算一次、连接内冻结 | `src/index.ts:106 / 179 / 185 / 364` | `imagePromptEnabled` **全文件唯一赋值处**在 `initialize`；`session/prompt` 只是把它传下去 |
| 广告闸门先于逐轮校验 | `content.ts:137` vs `content.ts:154` | 先 `if (!imageEnabled) throw AcpContentError('inline image prompts were not advertised by this connection','invalid')`，再 `await assertImageRoute(ctx, route, signal)` |
| 逐轮校验用当轮路由 | `content.ts:63-79` | `assertImageRoute` 读 `ModelSelection` 的 provider/model → `inputModalities` 必须含 `image` |
| 无 pin ⇒ 会话没有模型 | `src/index.ts:452-456` + `core/agent/src/model-selection.ts:82-95` | `initialSelection` 在缺任一字段时返回 `undefined`；而 `{{model}}` / `{{provider}}` 装配变量的**唯一**生产者是 `installModelSelection` 的 `system-prompt/assemble` 钩子，且 `selection.current === undefined` 时**直接透传** ⇒ 没人填变量 |

上游测试**就是这条契约的正式定义**，可直接引用：

- `packages/acp/acp/tests/content.spec.ts:66-86`
  —— provider 缺、model 缺、mediaTypes 为空、模型未知、模型为纯文本：**全 false**；只有 image 模型为 true。
- `packages/acp/acp/tests/bridge.spec.ts:39-70`
  —— 默认夹具广告 `promptCapabilities.image = false`；`imageCapable: true` 才 true；**没有 attachment store 时同样 false**。

## 2. 上游的设计意图（决定性）

`.agents/notes/implemented/simplification/2026-07-23-acp-automation-only-protocol.md`

- 第 21 行：
  > Image capability is **truthful rather than structural**: `initialize` advertises it only when a durable
  > attachment store exists and the configured exact provider/model resolves with explicit image input.
  > … **Unknown capability is false at initialization; prompt admission rechecks the live route.**

- 第 51 行（**被明确否决**的替代方案）：
  > **Advertise image support whenever the ACP SDK has an image block.** Rejected because protocol vocabulary
  > does not prove this deployment can persist bytes or that the configured exact route accepts visual input.

**读法**：上游是**故意**保守取假。所谓"admission rechecks the live route"是**窄化**校验（只能 true→false），
不是补偿。两道闸门是 **AND**：广告为假时，同一个连接内**不可能**再恢复图片能力
（`setSessionConfigOption` 改不动 `imagePromptEnabled`——它不在 `AcpModelControl` 里）。

## 3. 由此判定 `pieqi-image-capability-changeset.md` 的 Task A

**它的 §0 事实表全对**（行号 81/111/1142 与官方原版 `index.js.orig.js` 逐一定位一致，确系读过真产物）。
错误只在**结论的力度**：

- pieqi 的 `agentSupportsImagePrompt(caps)` 读 `initResp.AgentCapabilities.PromptCapabilities.Image`
  （`internal/agent/acp.go:573`），而该字段就是 dsh 的 `imagePromptEnabled`（`index.ts:185`）。
  ⇒ **pieqi 的那道闸门与 dsh 自己的闸门是同一个值的两次使用。**
- 因而"删掉 pieqi 的预判、图片一律发出去"**不可能**把任何一张图送进去：
  拆掉的只是**前端入口的显隐依据**与**错误文案质量**（pieqi 的 400 文案具体；dsh 的
  `-32602 inline image prompts were not advertised by this connection` 不含可行动信息）。
- 它给出的理由 1（"粒度错配：换回多模态模型后仍然拒收"）描述的现象**存在**，但**归因错误**：
  真正的成因是"连接级广告不会随 `_meta.model` 刷新"，而不是"pieqi 多判了一次"。
  删掉 pieqi 的判断并不能让连接重新收图 —— 只能**重建连接**（新任务 = 新的 dsh 进程 = 新的 `initialize`）。
- 它的 §12（pin 保持不动）反而与源码一致，是对的。

**粒度错配的真身**：广告布尔取自 **pin 指向的模型**。若 pin 指向一个不声明 `input: [text, image]` 的模型，
即使当轮 `_meta.model` 换成多模态模型，连接也恒为 false。⇒ 修复方向是**选一个声明收图的 pin**，
不是拆掉 pieqi 的预判。

## 4. 附带核对

- changeset 第 24-25 行称"本机升级到 `0.2.1-alpha.2`（`dsh-acp` / `dsh-acp-app` 同为 `0.2.1-alpha.2`）"：
  与实测不符。`~/.dsh-runtime/node_modules/@deepseek-ai/dsh*/package.json` 均为 **`0.2.0-rc.2`**，
  源码仓库为 **`0.2.1-alpha.1`**，两者都不是 `0.2.1-alpha.2`。
- 本机 pin 指向的 `group/auto-deepseek-v4-1-flash` 在 `llm-pi-ai.models` 中声明 `input: [text, image]`
  ⇒ 该 pin 同时满足"能解析出路由"与"声明收图"两个条件，这正是它承担双重职责的原因。

## 4.5 后续（2026-10-13）：死结**已解开**，且解法与本文档的推论一致

> 本节在决策落地后补记，供对照本文档的 §3/§5 判断。

**已采取的动作**：还原**官方** `dsh-acp`（撤掉 fork 的 A1–A3/B1–B3），并把 profile 的 pin
**恢复**为 `provider: magpie` / `model: group/auto-deepseek-v4-1-flash`。

**实测结果**（`forks/dsh-acp/probe-config.mjs`）：

| 观测点 | 值 |
| --- | --- |
| `promptCapabilities.image` | **`true`** |
| `session/prompt` | `end_turn`，回复"好的" |
| `configOptions` | 2 项（标准通道） |
| `_meta["pieqi/configOptions"]` | `undefined`（fork 通道已随 fork 消失） |

**这印证了 §5 的第 1 条**：要图片 ⇒ pin 必须指向一个 `input` 含 `image` 的已声明模型。
`group/auto-deepseek-v4-1-flash` 在 `llm-pi-ai.models` 里正是 `input: [text, image]`，
所以恢复 pin 即恢复了图片能力——**不需要改 fork 的 `initialize` 回落链**（§5 第 3 条那条更重的路）。

**§3 对 changeset 的判定仍成立**：删掉 pieqi 的预判不能把图送进去，真正的旋钮是 pin。
**§5 的"死结"是在"必须无 pin"这个前提下的结论；该前提已于本次决策中撤销。**

**一个必须记住的联动**：pin 与 fork 是**配套**的，不能各选一边 ——
fork（B1）是"无 pin 还能用"的前提，官方版则**要求** pin 在场。当前组合是
**官方 + pin**；若切回 fork，必须同时把 pin 清成显式 `!!js undefined`。
详见 `forks/dsh-acp/FORK.md` 的「当前状态」一节。

## 5. 结论

连接级布尔是**死结**：

1. **要图片** ⇒ `- id: acp` 必须存在且 `provider`/`model` 都指向一个 `input` 含 `image` 的已声明模型；
2. **要无 pin** ⇒ 接受整个会话无图片（且必须 fork 补丁在位，否则首轮 `{{model}} has no value`）；
3. **不能两全**，除非改 fork 的 `initialize` 回落链（静态 pin → `agent-default-model` → 扫清单含 image），
   把"能力声明"与"选路"解耦 —— 这属于改上游行为，非配置层可解。
