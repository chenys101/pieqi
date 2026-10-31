# L2 拆档：本机回环执行可选用免审，外溢仍永远人工

## 背景（2026-11-01）

ADR-0008 / 0015 与后续 P0–P3（配方会话记忆、Go 分档、分段展示、可信配方）之后，
`go test` / `npm run test` 等已能进 L0/L1 免审，但 **`curl 127.0.0.1:3000/ready`**
一类「只打本机探活」仍落 L2，每次弹卡。

用户需要的不是「整档 execute 可配」（那会拆掉 L2 硬边界），而是：
在**能证明只打本机回环**时，可选地少打断一次。

## 决策

1. **不新增 RiskLevel，不给整档 L2 开开关。** `RiskLevel.AutoApprovable` 对 L2/L3 仍为 false。
2. 新增 Settings 字段 `auto_approve_local_execute`（默认 **false**）：打开后，
   仅当整条命令的**每一段**都能证明是「本机回环 HTTP(S) 探活」或已能分档为
   read/edit（L0/L1）时，才走免审。
3. 证明面刻意极窄（`internal/agent/local_execute.go`）：
   - 只认 `curl` / `wget` / `Invoke-WebRequest` / `Invoke-RestMethod`；
   - URL 主机必须是 `127.0.0.1` / `localhost` / `::1` / `[::1]`；
   - 出现非回环 URL、管道写文件、删除类原文 → 整条不放行。
4. 与 `trusted_recipes` 并存：可信配方是用户显式枚举；本开关是「回环探活」这一类的批量捷径。硬边界（删除 / push / install / go run）两条路都不收。

## 否决的方案

- **给 L2 加 `auto_approve_l2`**：等于把装依赖、任意脚本一并放行，直接违反「字段不存在承载不可配置性」。
- **把 curl 一律降为 read**：外网 curl 有副作用与数据外泄面，不能与回环探活同档。
- **新增 L2a 风险枚举并改全栈展示**：收益只是文案，却要动 Decision / 前端 RISK_LABELS / IM 文案；本开关语义用设置项说清即可。

## 后果

- 卡片上仍可能显示 L2（ToolKind 仍是 execute），但打开开关后回环探活不再弹卡。
- 默认关闭：未显式选择的用户行为与改前一致。
- 改动只影响**之后新建的会话**（与免审名单 / 可信配方同一纪律）。
