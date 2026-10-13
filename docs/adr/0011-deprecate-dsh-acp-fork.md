# 弃用 dsh-acp 本地 fork，改用官方版 + 恢复 profile pin

## 背景

`@deepseek-ai/dsh-acp` 的上游实现有两个限制，pieqi 曾用一份**本地 fork**（`forks/dsh-acp/`）绕开：

1. **配置缺失即不可用**：`initialSelection(config)` 是静态 pin，`provider` / `model` 任一缺失就返回
   `undefined`，于是 `modelControl.selected` 悬空 —— `configOptions` 恒空，
   `set_config_option` 抛 `this session has no model selection`，**会话彻底没有模型**。
2. **模型不能外部指定**：改模型只能走 `session/set_config_option`，而它只认
   **建会话那一刻** `state().choices` 里的值（否则抛 `unknown model option`）。
   于是「建会话时指定」「每轮换模型」都无处落脚。

fork 用 ACP 的 `_meta` 扩展位补上这两点（A1–A3 隐藏 `configOptions` + 经
`_meta["pieqi/configOptions"]` 旁路回传清单；B1–B3 支持 `_meta.model` 指定会话级与按轮模型）。

**但它带来一个不可接受的代价**：为了触发 B1 兜底，profile 里的 pin 必须显式清空
（`provider/model: !!js undefined`）。而 `initialize` 读的是**静态 pin 而不是 `deploymentSelection`**，
所以 pin 一空，`promptCapabilities.image` **必为 false** —— 带图 prompt 被
`admitAcpPrompt` 直接拒：

```
-32602 Invalid params: inline image prompts were not advertised by this connection
```

这是去 pin 的固有代价（与 B1 无关）。要两全只能改 `initialize` 的回落链。

## 决策

**优先使用官方版本，弃用本地 fork，并恢复 profile 的 `acp` pin。**

2026-10-13 在此之前已把运行时切回官方（`f124100`）；本次把**源码也一并删除**，
消除「仓库里躺着一份不再使用的 fork」这一误导。

### 关键澄清：删 fork **不影响**按轮切换模型

这是本次决策前专门核实的一点（曾一度误判为「fork 独占的能力」）。事实是
**按轮切换模型在官方版上是标准能力**，有专门实现与回归测试：

- `internal/agent/acp.go` 的 `promptOnce`：判据 `catalogSourceOf() == catalogStandard` 时，
  先 `set_config_option` 再发 prompt（顺序不能反 —— `set` 对**会话**生效，反了这轮还跑在旧模型上）。
- 回归测试 `TestACPAgent_TurnModelUsesStandardChannel` 断言了 `set` 必须恰好一次、
  且调用顺序为 `[set, prompt]`，并断言原生 agent 的 prompt **不该**带 `_meta.model`。

现场实测（直连 dsh，不经 pieqi）：

| 观测点 | 官方 dsh-acp 0.2.0-rc.2 |
|---|---|
| `configOptions` | **2 项**（`model` / `reasoning_effort`），9 个模型 |
| `_meta["pieqi/configOptions"]` | `undefined`（fork 的私有通道随 fork 消失） |
| `promptCapabilities.image` | **`true`** |
| `session/prompt` | `end_turn`，正常跑通 |

⇒ 走 **`catalogStandard`** 通道，即 `acp.go` 里为「原生 ACP agent」写的那条路。**pieqi 侧无需改动。**

### 一并决定：**保留**双通道机制（`catalogStandard` / `catalogMeta`）

删 fork 后 `catalogMeta` 分支暂时没有 agent 会走到，但**不能删**：

- 它是「标准字段恒空时怎么活」的协议容错。`bd0cd28` 当年为迁就 fork 把读取**整体**换成
  `_meta`，结果**误伤了 qoder** —— qoder 是原生 ACP 实现、`_meta` 整个缺失，于是清单恒空、
  前端显示「该 Agent 不提供可选模型」，而它其实既下发清单又接受 `set_config_option`。
- 双通道 + `catalogSource`（读哪条决定写哪条）正是那次事故的修复成果。
- 未来任何不合规 / 被改造的 ACP agent 都会落到 `catalogMeta`，删掉等于假定所有 agent 都合规。

同理，`modelCatalogMetaKey = "pieqi/configOptions"` 常量保留（它是对 fork 的键名契约；
fork 已删，但保留常量才能让 `catalogMeta` 分支继续可用）。对应回归测试
`TestPickCatalog_PrefersStandardThenMeta`、`only _meta → catalogMeta` 全部保留。

## 代价（明确背上）

1. **pin 要手工跟随上游清单漂移**。已踩过两次坑（命名空间写错；上游下架 glm 导致建会话即失败），
   记录在 `~/.dsh/profiles/acp/cordis.patch.yml` 开头。
2. **放弃「无 pin + 按轮换模型」这条组合**。若将来确实需要（例如要跑一个没有可用 pin 的
   部署环境），只能重新 fork —— 依据是：
   - 上游版本已固定记录：`0.2.0-rc.2`
   - 6 处改动的位置 / 做法 / 理由 / 踩坑，全部写在被删的 `forks/dsh-acp/FORK.md`（277 行）
   - 删前指纹（2026-10-13）：`FORK.md` sha256 `432CC41D18213C3E`；
     `lib/index.js` `CB6B4F290855BE32`；`lib/index.orig.js` `C08ED51EBE53B7B4`
   - **找回方式**：`git show 9c5ab35:forks/dsh-acp/FORK.md`（引入 fork 的提交），
     或 `git show f124100^:...`。删除提交的前一个版本即完整 fork。
3. **`_meta` 模型清单通道暂时无用**。pieqi 侧 `fallbackModel`（未选模型时取清单首项）
   这条安全网仍在，且现在走标准通道，比原来更可靠。

## 与之关联的既有决策

- ADR-0010（图片只留元数据）：本次删除**恢复了** dsh 的图片能力（`image: true`），
  与 0010 里「dsh 无 pin 时 image 必为 false」的记录方向一致 —— 恢复 pin 就恢复了图片。
- `docs/adr/0009`（自重启接续发起者）：无关，仅编号相邻。
