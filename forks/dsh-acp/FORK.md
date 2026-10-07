# dsh-acp fork —— 配置可缺失 + 模型可外部指定

`@deepseek-ai/dsh-acp` 的本地改动版。解决三件事：

1. **配置缺失兜底**：profile 里 `- id: acp` 的 `provider` / `model` 缺失时，回落到部署默认模型
   （`agent-default-model`），不再让 `selected` 悬空、整个会话不可用。
2. **建会话时指定模型**：`session/new` / `session/resume` 可以带模型。
3. **发提示词时指定模型**：`session/prompt` 每一轮都可以换模型。

> `README.md` / `README.zh.md` / `README.i18n.yaml` 是上游原样文件，未改动。本文件才是这份 fork 的说明。

「隐藏 ACP config」是**顺带**的：模型清单改由 `_meta` 旁路回传（见下），`configOptions` 就没必要再
暴露给通用 ACP 客户端了。它是手段，不是目的。

## 这是什么

| 项 | 值 |
| --- | --- |
| 上游 | `github.com/deepseek-ai/deepseek-harness` → `packages/acp/acp`（公开仓库） |
| 版本 | `0.2.0-rc.2` |
| 改动 | 6 处（A1–A3 / B1–B3），均以 `[pieqi-fork]` 注释标注 |
| 原版留档 | `lib/index.orig.js`（**勿删**，还原与对照都用它） |

本机 dsh 是这样加载它的：`~/.dsh-runtime/node_modules/@deepseek-ai/dsh/lib/bin.js --profile acp`
→ profile bundle `@deepseek-ai/dsh-acp-app` → `@deepseek-ai/dsh-acp`。
dsh 没有任何「加载本地 fork」的口子，所以本 fork 靠 `install.mjs` 把补丁后的 `lib/index.js`
覆盖回 `~/.dsh-runtime/node_modules/@deepseek-ai/dsh-acp/lib/index.js`。

## 上游为什么不支持指定模型

上游把配置能力收在 `AcpModelControl` 里，但**对外只开一条路**：

- 路由来自 `initialSelection(config)` —— 静态 pin。`provider` / `model` 任一缺失即返回 `undefined`，
  于是 `modelControl.selected` 悬空：`configOptions` 恒空，`set_config_option` 抛
  `this session has no model selection`，会话彻底没有模型。
- 想改只能走 `session/set_config_option`，而它只认**已经在 `state().choices` 里**的值
  （否则抛 `unknown model option: <value>`），`choices` 又只来自建会话那一刻。
- 于是「建会话时指定」和「每轮换模型」都无处落脚。

`NewSessionRequest` / `PromptRequest` 都带扩展字段 `_meta`，且 zod 生成里没有 `strict()`，
`_meta` 不会被剥掉 —— **这是唯一合法的外部传参通道**，fork 就走它。

## 改了什么

### A. 隐藏 ACP config（对通用客户端）

上游把配置能力通过 **三处出口** 暴露给客户端：

1. `session/new` 响应的 `configOptions`
2. `session/resume` 响应的 `configOptions`
3. `config_option_update` 通知（由 `llm/adapters-updated` 事件触发 `topologyChanged()`）

| 改动 | 做法 |
| --- | --- |
| A1 | `AcpSession.configOptions()` 仍解析并校验路由，但对外 `then(() => [])` |
| A2 | `AcpSession.topologyChanged()` 直接 `return`，不再推 `config_option_update` |
| A3 | 新增 `AcpSession.visibleConfigOptions()`；`session/new` / `session/resume` 把真实清单挂到响应 `_meta["pieqi/configOptions"]` |

A1 之所以**保留** `this.modelControl.options(signal)` 的调用而不是直接 `return []`：它顺带完成
路由解析与校验（`resolveSelection` → `resolveCallConfig`），并在成功时置 `hasResolvedState`。
去掉它会让「配置里的 model 已失效」从**建会话即报错**变成 prompt 阶段才失败，白丢一次早报机会。

A2 必须与 A1 一起改：只清空 `configOptions` 而不掐通知，会话仍会在适配器更新时被这条通知
**重新灌回完整模型清单**，等于白隐藏。

A3 是「隐藏」与「pieqi 需要清单」的调和：`_meta` 是各方共用的扩展位，通用 ACP 客户端不读它。

### B. 模型可外部指定（不改协议字段）

| 改动 | 位置 | 做法 |
| --- | --- | --- |
| B1 | `deploymentSelection(ctx, config)` | pin 缺 `provider`/`model` 时 `ctx.get("agentDefaultModel").currentSelection()` 兜底；取不到就退回原行为（悬空） |
| B2 | `session/new` / `session/resume` | 读请求 `_meta` 的模型，作为本次会话的 `fallbackSelection` |
| B3 | `session/prompt` | 读请求 `_meta` 的模型，先 `setConfig(MODEL_CONFIG_ID, …)` 落一次，再跑该轮 |

两个实现细节值得记下来：

- **B1 必须软取服务**：`dsh-acp` 的 `package.json` 没有依赖 `@deepseek-ai/dsh-agent-default-model`，
  所以用 `ctx.get("agentDefaultModel")` 而不是写进 `inject` —— 后者会让 Cordis 等一个未声明的
  服务而卡死。取不到（undefined）或调用抛错都按「没有兜底」处理。
- **B3 只设 `MODEL_CONFIG_ID`，不设 `reasoning_effort`**：`AcpModelControl.set()` 的 reasoning
  分支用旧 `current.model` 解析，换 provider 时会抛。按轮换模型时跟着改 reasoning 反而会炸。
  另外 `AcpSession.prompt()` 入口会先 `snapshot()` 当前 selection 并 `pinTurn()` 到该轮，
  所以**发 prompt 前落一次 setConfig 恰好对这一轮生效**，不会污染后续轮。

### B1 的「配置缺失」到底指什么（2026-10-08 实测）

**坑：把 profile 里的 `- id: acp` 整行删掉，不算「配置缺失」。**

`@deepseek-ai/dsh-acp` 自己的 Config schema 只有 `provider: Schema.string()` / `model: Schema.string()`，
**没有默认值**；但 `@deepseek-ai/dsh-acp-app` 这个 bundle 自带：

```yaml
- id: acp
  name: '@deepseek-ai/dsh-acp'
  config:
    provider: deepseek-official
    model: deepseek-v4-flash
```

所以删掉自己那行 patch 之后，生效的是 bundle 的 `deepseek-official` —— 那个 provider 要
`DEEPSEEK_API_KEY`，本机没有，于是**建会话能过、第一轮 prompt 才炸**：

```
llm-deepseek: no API key for provider route "deepseek-official";
store DEEPSEEK_API_KEY through the credentials service …
```

要真正表达「不指定模型、用部署默认」，必须在 patch 里**显式写空**：

```yaml
- id: acp
  config:
    provider: !!js undefined
    model: !!js undefined
```

这时 B1 才生效：`currentSelection()` 取自 `- id: agent-default-model` 那条
（本机是 `magpie` / `workbuddy/deepseek-v4.1-flash`），会话可正常跑完一轮。
用 `dsh --profile acp --dump-config` 看 `- id: acp` 段即可确认 provider/model 是否真的不在了。

### `_meta` 里模型的两种等价写法

```jsonc
// 1) ACP 原生不透明选择值（pieqi 用的就是这个 —— 直接透传下拉框拿到的 value）
{ "_meta": { "model": "[\"magpie\",\"group/auto-deepseek-v4-1-flash\"]" } }

// 2) 拆开写（便于人工调试）
{ "_meta": { "provider": "magpie", "model": "group/auto-deepseek-v4-1-flash" } }
```

`session/new` 还接受 `_meta.reasoningEffort`。`session/prompt` 只认模型。

清单回传键固定为 `_meta["pieqi/configOptions"]`；pieqi 侧读同名键
（`internal/agent/model_catalog.go` 的 `modelCatalogMetaKey`）。**改键名要两边一起改**，
否则清单恒空，表现是前端没有模型下拉。

## 实际效果

- 通用客户端在 `session/new` / `session/resume` 后拿到 `configOptions: []`，不渲染选择器，也收不到
  `config_option_update`。
- 模型仍然生效，且默认固定：由 profile（`~/.dsh/profiles/acp/cordis.patch.yml`）里 `- id: acp`
  的 `provider` / `model` 决定；**该 pin 缺失时回落到 `agent-default-model`**（B1，注意"缺失"的
  正确写法见 B.1 小节 —— 删掉整行不算）。
- pieqi 的 `GET /api/agents/dsh/models` 走 `_meta` 拿清单，正常可用。
- 不传 `_meta` 的调用方行为与上游一致（除 B1 的兜底）。

## 用法

```bash
node install.mjs            # 安装（自动备份 + 语法/sha256 校验）
node install.mjs --check    # 只看 runtime 与 fork 是否一致（不一致退出码 2）
node install.mjs --restore  # 还原成上游原版
```

目标目录默认 `<home>/.dsh-runtime/node_modules/@deepseek-ai/dsh-acp`，可用环境变量
`DSH_ACP_TARGET` 覆盖。

装完**新起的** dsh 会话才生效；已经在跑的 dsh 进程不受影响（它们是独立进程，代码已加载进内存）。

## 验证：probe-config.mjs

直连 dsh 起一次性会话（不经 pieqi），把 `configOptions` / `_meta` / `session/prompt` 的返回原样打出来。

```bash
node probe-config.mjs
PROBE_MODEL='["magpie","workbuddy/deepseek-v4.1-flash"]' node probe-config.mjs
PROBE_PROMPT="只回复两个字：好的" PROBE_SWITCH_MODEL='["magpie","workbuddy-ai/hy4-preview-f"]' node probe-config.mjs
```

| 环境变量 | 作用 |
| --- | --- |
| `PROBE_MODEL` | 建会话时经 `_meta.model` 指定的模型（验 B2） |
| `PROBE_SWITCH_MODEL` | 本轮经 `_meta.model` 指定的模型（验 B3，需配合 `PROBE_PROMPT`） |
| `PROBE_PROMPT` | 顺带跑一个真实 turn |
| `PROBE_NODE` | 指定 node 可执行文件 |
| `PROBE_TIMEOUT_MS` | 等待上限（默认 150s；dsh 冷启动实测可达 90s，别当卡死） |

退出码：

| 码 | 含义 |
| --- | --- |
| 0 | 隐藏生效 + 清单经 `_meta` 回传（且指定模型被采纳） |
| 2 | `configOptions` 非空 —— A1 未生效 |
| 3 | `_meta` 里没有清单 —— A3 未生效 |
| 4 | 带 `PROBE_MODEL` 但 `currentValue` 与指定值不一致 —— B2 未生效 |
| 1 | 出错 / 超时（含 dsh 提前退出、prompt 失败） |

## 何时需要重跑 install

**dsh 升级，或在 `~/.dsh-runtime` 里跑过 `npm install` / `npm ci` 之后。**
那时 npm 会把 `lib/index.js` 换回官方版，所有 6 处改动静默失效。判断方法：

```bash
node install.mjs --check     # 退出码 2 或提示「不一致」= 需要重跑 install
```

上游换了版本时不要直接覆盖：先看新版 `lib/index.js` 里 `configOptions(` / `topologyChanged(` /
`initialSelection(` 几处是否还是同样形状，再决定是否更新 `lib/index.orig.js` 与补丁。

## 回滚

```bash
node install.mjs --restore
```

`backups/` 里还留着每次安装前的 runtime 内容（按 sha + 时间戳命名），需要时可手动取用。
