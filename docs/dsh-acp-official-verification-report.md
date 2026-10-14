# 官方 dsh-acp 兼容性验证报告

**日期**：2026-10-09
**目的**：验证切回**官方最新版** dsh 后，pieqi 当前交互是否完整满足
**结论**：✅ **完整满足，且 pieqi 无需改代码**

---

## 一、测试环境（完全隔离，未触碰线上）

| 项 | 值 |
|---|---|
| 官方 dsh 版本 | **`0.2.1-alpha.1`**（npm `alpha` tag；`latest` tag 指向陈旧的 `0.0.1-rc.1`，是坑） |
| 官方 dsh-acp 版本 | `0.2.1-alpha.1`（产物 sha256 `c08ed51e…`，**与 0.2.0-rc.2 完全相同**） |
| 安装位置 | `build/dsh-official-test/runtime/`（`--ignore-scripts`，绕沙箱 spawn EPERM） |
| DSH_HOME | `build/dsh-official-test/dsh-home/`（环境变量覆盖，不碰 `~/.dsh`） |
| pieqi 实例 | 端口 **3200**，`PIEQI_HOME` 独立，飞书渠道关闭 |
| 线上实例 | PID **8708** / 端口 **3000** —— 全程未受影响 |
| `~/.dsh-runtime` | sha256 `8cd3572e…`（fork 版）**从未改动** |

**关键事实**：官方 `0.2.1-alpha.1` 的 `dsh-acp` 产物与 `0.2.0-rc.2` **字节级相同**（均 55804 字节、sha256 `c08ed51e…`）。说明 `dsh-acp` 在这两个版本间**没有任何代码改动**，fork 的 6 处补丁官方至今未做。因此 `forks/dsh-acp/lib/index.orig.js` 就是最新官方件，用它 `--restore` **不会降级**。

---

## 二、验证结论总表

| # | pieqi 交互 | 官方版结果 | 证据 |
|---|---|---|---|
| 1 | **模型清单下发** | ✅ 完整 | `configOptions` 含 `id=model` 的 select，6 个模型 / 2 分组 |
| 2 | **默认路由（pin）** | ✅ 正确 | `currentValue = ["magpie","group/auto-deepseek-v4-1-flash"]` = profile pin |
| 3 | **建任务时选模型** | ✅ 生效 | 任务 completed，模型**自报** `workbuddy-ai/hy4-preview-f` = 指定值 |
| 4 | **会话内切模型** | ✅ 生效 | HTTP 200 `changed:true`，时间线留 `model_switch` 事件 |
| 5 | **续问时本轮换模型** | ✅ 生效 | 会话默认已切 A，本轮指定 B，模型自报 **B** |
| 6 | **清单校验（非法值）** | ✅ 拦截 | 提交清单外值 → HTTP 400 `unknown model for agent dsh` |
| 7 | **续问跨进程 resume** | ✅ 保留 | resume 后 `currentValue` 仍是原值，上下文保留 |
| 8 | **fork 私有通道** | ⚪ 不存在 | 官方**无** `_meta["pieqi/configOptions"]`（符合预期） |

**8/8 通过。**

---

## 三、关键技术发现

### 3.1 官方版自动落到 pieqi 的「标准通道」

官方版下发**标准 `configOptions`**，于是 pieqi 的 `pickCatalog`（`model_catalog.go:162`）按「标准字段优先」判为 **`catalogStandard`**，后续自动走：

- 读：标准 `configOptions`
- 写：`session/set_config_option`（`acp.go:716`）
- 按轮：`promptOnce` 先 `applyModel` 再 prompt（`acp.go:998`）

**这条路径 pieqi 早已实现并通过单测**（`TestACPAgent_NativeAgentReadsStandardConfigOptions`、`TestACPAgent_TurnModelUsesStandardChannel`）。所以**切回官方不需要改任何 pieqi 代码**。

### 3.2 决定性验证：`set_config_option` 影响第一轮

官方 `session/new` **不接受**模型参数（`index.orig.js:1168-1206`，`fallbackSelection: initialSelection(config)` 只取 profile pin）。所以「建任务时选模型」必须在建会话**之后**补一次 `set_config_option`。

**担心点**：官方注释说 `setConfig` "Apply ... to later ACP turns"（`L758`），第一轮算不算 "later"？

**实测答案**：算。模型自报确认第一轮就跑在设定的模型上（`workbuddy-ai/hy4-preview-f`）。

### 3.3 官方 `choices` 是动态构建的（FORK.md 说法有误）

`FORK.md:41-42` 称「`set_config_option` 只认**已经在 `state().choices` 里**的值，而 `choices` 又只来自建会话那一刻」——**这句不准确**。

实测 `index.orig.js:460-484`：`choices` 在**每次 `state()` 调用时重新构建**，遍历 provider 的**全部**已声明模型，并把 `currentValue` 补入。因此可切换的值 = **profile 里 `llm-pi-ai.models` 声明的全部模型**（本机 6 个），不是"建会话时那一个"。

这与实测吻合：会话建立后成功切到 `deepseek-official/deepseek-v4-pro`（一个建会话时并未选中的值）。

### 3.4 发现的能力缺口（不影响 pieqi）

| 缺口 | 影响 | 说明 |
|---|---|---|
| **无配置兜底（fork B1）** | 低 | profile pin 缺失/失效时，官方是「会话彻底没模型」，fork 会回落 `agent-default-model`。本机 pin 已配对，不受影响；但 pin 漂移时错误更晚暴露 |
| **`_meta.model` 被忽略** | 无 | 官方不读 `_meta`，但 pieqi 走标准通道时本来就不发 `_meta`（`acp.go:1002` 的 else 分支只在非标准通道走） |

**注意**：`agentCapabilities` 中**无 `sessionCapabilities.load`**，只有 `close`/`list`/`resume`。pieqi `acp.go:644` 已有 `if a.agentCaps.LoadSession` 分支，会正确走 `ResumeSession`——实测通过。

---

## 四、仍存在的风险与建议

### 4.1 模型桶限流 ≠ 切换失败（重要）

实测 `workbuddy/deepseek-v4.1-flash` 返回 **429**：
```
WorkBuddy: 您的使用量已超出频率限制，将在 2026-10-09 13:50:11 UTC+8 重置
```
而 `set_config_option` **仍然成功**（`currentValue` 正确变化），失败发生在**下一轮 prompt**。

这正是 `silent-failure-checklist` 点名的形态：**「看起来切换成功、实际跑不动」**。建议在切回官方时一并处理——把「建会话 + 首轮」的失败尽早暴露给用户，而不是让人以为模型已换好。

### 4.2 切换后需要同步的文档/注释

切回官方后，以下位置的说法会**变成错误的**（它们描述的是 fork 的两通道世界）：

| 文件 | 位置 | 需要改什么 |
|---|---|---|
| `AGENTS.md` | 第 66 行 | 整段「ACP 的模型通道有两条」需要重写为「官方版走标准通道」 |
| `docs/multi-agent.md` | 252 / 261 / 272-300 行 | 6.2 节的 fork 叙述 |
| `internal/agent/model_catalog.go` | 文件头 12-29 行、`catalogSource` 注释、`explainProbeErr`（266-283） | 注释里大量「fork 的 dsh-acp」「`!!js undefined`」「B1 兜底」 |
| `internal/agent/acp.go` | 100-118、696-730、976-984 行 | 同上 |
| `forks/dsh-acp/FORK.md` | 41-42 行 | `choices` 那条说法需更正（见 3.3） |

**注意**：`catalogMeta` 这条通道的**代码可以保留**——它仍能正确处理"万一还有 fork 在位"的情况，且是 qoder 等原生 agent 的兜底。删掉反而会破坏 `TestACPAgent_ForkDshKeepsMetaChannel` 的覆盖。**改注释比删代码更合适。**

---

## 五、复现步骤

```powershell
# 1) 装官方最新版到隔离目录（不碰 ~/.dsh-runtime）
cd build/dsh-official-test/runtime
npm install --no-audit --no-fund --ignore-scripts '@deepseek-ai/dsh@0.2.1-alpha.1'

# 2) 隔离 DSH_HOME（复制 profile + 凭据）
#    见 build/dsh-official-test/dsh-home/

# 3) 协议层探针（Q1/Q2/Q3）
#    注意沙箱禁止管道 spawn，须用 [System.Diagnostics.Process] 由 PowerShell 驱动
#    证据：build/dsh-official-test/ 下的运行记录

# 4) pieqi 端到端
$env:PIEQI_CONFIG = 'build/dsh-official-test/pieqi-test/config.yaml'
$env:PIEQI_HOME   = 'build/dsh-official-test/pieqi-test/pieqi-home'
$env:DSH_HOME     = 'build/dsh-official-test/dsh-home'
build/dsh-official-test/pieqi-test/pieqi-test.exe

# 5) 验证接口
Invoke-WebRequest http://127.0.0.1:3200/api/agents/dsh/models   # 应返回 6 个模型
```

**沙箱注意**：本机 dsh 会话的 workspace-write 沙箱**禁止 Node 用管道 spawn 子进程**（`stdio: pipe` → EPERM；`ignore`/`inherit` 正常）。所以探针不能用 `child_process.spawn`，须由 PowerShell 用 `System.Diagnostics.Process` 驱动。
