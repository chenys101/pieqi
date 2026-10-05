# 只读 shell 命令从 L2(execute) 降级为 L0(read)

## 背景

pieqi 的免审名单按 **ACP ToolKind** 匹配，而 `riskLevelKinds` 把 `execute` 归进 **L2**（`internal/core/settings_store.go`），L2 永远不自动放行。这个分级对 `rm -rf` 是对的，但对只读命令是过度的：qodercli 把**所有** shell 调用一律报成 `execute`，因此 `sed -n '1,35p' file.go`、`ls -la`、`grep -n foo` 与真正的写操作在 pieqi 眼里**完全无法区分**，一律弹卡。

实测影响：会话 `6164e2a2`（排查 dsh ACP 问题）453 次工具调用产生 **106 次人工审批**，其中 99 次是通过的 shell 探测，绝大多数是 `cd <dir>; sed/grep/echo ...` 的同构重复。用户体感是"一直在审批"。

## 决策

在 agent 层（`internal/agent`，ACP 与 print 两条路径共用的入口）加一层**保守的只读命令启发式**：当 ToolKind 为 `execute`、且命令可被证明无副作用时，**降级为 `read`（L0）**，从而命中 L0 免审、不再中断。

启发式必须满足"证明无副作用"而非"看起来像只读"：

- **切段后逐段判定**：先按 `;`、`&&`、`||`、`|`、换行切段，**每一段都必须是只读命令**才降级。任意一段是写操作，整条不降级。
- **白名单命令名**：`ls cat head tail sed grep rg find wc echo pwd date which where type sort uniq cut tr nl file stat du df diff comm join column jq awk` 等只读工具。
- **不含写标志**：如 `sed -i`、`sort -o`、`find -delete`/`-exec` 等一律不降级。
- **放行无害重定向**：`2>/dev/null`、`2>&1`、`>/dev/null` 是丢弃输出，放行；其它 `>` 一律拒绝。反引号与 `$(` 命令替换绝不放行（它会执行另一条任意命令）。
- **`cd` 例外**：纯 `cd <dir>`（不带动词与重定向）视为只读。它不改变文件系统，且是复合命令的常见前缀。

降级只影响**审批强度分类**，不改变命令的实际执行权限：放行仍由 `PermissionWire.tryAutoApprove` 按 `read` 走 L0 免审。**未命中启发式的一切命令仍旧是 L2，行为与现在完全一致。**

## 为什么必须切段，而不是"见复合语法就放弃"

最初的实现是后者（任何 `;`/`|`/`>` 出现即放弃降级），**实测命中率为 0**：该会话 100 次审批里 81 条含 `;`、78 条含 `|`、53 条含 `2>/dev/null` —— qoder 的探查命令几乎全是复合的（`ls -la X 2>/dev/null; echo "---"; find Y | head -60`）。只认单条命令等于这个功能没有任何用户能受益。

切段后同一份数据命中 **25/100（25%）**，且逐条复核剩余的 75 条，拒绝理由都成立：含 `node`/`curl`/`python`/`netstat`/`mkdir`/`sleep`/`for` 循环等白名单外命令，或含 `cp`、`-o` 等写操作。

## 为什么不做成"给 execute 开配置开关"

那等于把 L2 变成可配置的，直接摧毁 `Settings` 里"L2/L3 没有对应字段、靠字段不存在承载不可配置性"的设计。降级必须发生在**分类阶段**（把某条具体命令证明为只读），而不是**放行阶段**（给一整档风险发通行证）。

## 代价与边界

- 启发式是**基于文本**的，理论上可被构造的命令绕过（如 `sed --expression` 的怪异用法、alias、shell 函数）。因此它是"减少噪音"而非"安全门禁"：真正的兜底仍是 worktree + Baseline/Checkpoint 可回退（ADR-0002），以及 L3 的删除类操作仍需人工确认。
- `find` 等命令的写形态（`-delete`、`-exec`）必须显式排除，否则会把 L3 语义的操作降成 L0 —— 这是本启发式**最主要的误判风险**，回归测试须覆盖。
- 白名单之外的命令（`npm`、`go`、`docker`、`curl`）一律不降级：它们可能有网络/构建副作用，保持 L2。

## 与既有 ADR 的关系

不推翻任何既有决策。ADR-0007 的非目标清单不涉及审批粒度。本 ADR 与 `riskLevelKinds` 的"`other` 归 L2、未知往保守侧倒"原则一致：降级只对**能证明**是只读的具体命令生效，证明不了就维持 L2。
