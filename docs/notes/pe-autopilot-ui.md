# PE 统一轨 Autopilot（UI 定案稿 · 待确认）

| 字段 | 值 |
|---|---|
| 状态 | **入口/升格 UI 已落地**（PE 统一轨 runner / 需求页换轨仍待施工） |
| 日期 | 2026-11-23 |
| 线框 | [ui-mock-pe-autopilot.html](ui-mock-pe-autopilot.html) |
| 相关 | ADR-0023、ADR-0021 Promote、[plan-engineering.md](plan-engineering.md)、[workflow-execution-efficiency.md](workflow-execution-efficiency.md) |
| 用户拍板 | **统一轨**：合并 Plan Engineering 手填 + Agent 拆分 |
| 会话拍板 | 阶段**门禁**拆开；规划默认**一条会话**；按阶段换 Agent **非主路径** |
| 入口拍板 | **入口分立**；一句话走新建任务；桥 = 会话「升格」；新建需求须说明\|AC |

## 0. 一句话

短意图走**新建任务 → 会话打磨**；人点「升格」进 PE。已有材料走**新建需求**直进 PE。PE：阶段自动推进 → 门禁空则 Publish+物化 → 节点按依赖 Start；**决策收件箱空可离开**。

## 1. 人介入白名单

| 打断 | 原因 |
|---|---|
| OpenQuestion | 阻断 Publish |
| Assumption 未 `accepted_risk` | 阻断 Publish |
| Publish 门禁其它失败 | 需人改或 force |
| 覆盖已有 running/completed 票 | 二次确认 |
| 真·高风险工具审批（L2+/L3） | 既有审批卡 |

**不打断**：阶段切章、保存草案、选 Agent/模型（设置默认）、L0 免审探测、依赖齐后自动 Start。  
**升格**须人点（禁止自动 Promote）。

## 2. 页面结构

```
仪表盘双入口（不合并）
  ├─ 新建任务 → 任务会话（头部次要「升格」；默认可一直短路径）→ 轻确认 → PE
  └─ 新建需求（说明|AC 门禁）→ PE
需求详情 PE Autopilot：
  Header / 进度条 / 主区三态（推进中 | 决策卡 | 节点列表）
  次要：暂停 · 打开规划会话 · 查看 Revision
```

### 否决

- 新建需求页主 CTA 去建 Task / 统一「你想做什么」吞掉双入口
- 另做打磨页；自动升格；升格后再空聊 Intent
- 打磨 Task 升格后继续当实现票跑

### 采用

| 项 | V1 |
|---|---|
| 新建任务 | 一句话 OK → 会话；打磨 = 既有 SessionPage |
| 新建需求 | 标题+cwd；说明\|AC **至少一侧非空**；创建并启动 PE；旁注去新建任务 |
| 升格 CTA | 短路径且未挂需求时头部次要（桌面 ghost / 移动标题旁文字）；**不** sticky；「够用」= 人点 |
| 轻确认 | 可编辑标题/说明/AC → 一键确认（非五步说明书） |
| 升格动作 | 建需求 + Promote 挂靠 + **停写**打磨 Task + Intent 种子过门 + 进 PE |
| PE 续跑 | 从 Business/Solution 续；单规划会话；实现票 Publish 后另开 |
| 打磨 Task | `role=discovery` 证据源；不改 Timeline |

## 2.1 规划会话模型

| 项 | V1 |
|---|---|
| Intent→Engineering | 一条规划会话；阶段 = 章节 + 过门 |
| 升格热启动 | Intent 章可仅种子摘要，不重开聊 |
| 用户默认面 | 进度 + 摘要；「打开规划会话」钻取 |
| 非目标 | 每阶段新起 Agent；主 UI 分阶段绑模型 |

## 3. 状态机（产品层）

```text
新建任务 → task session（可一直短路径收工）
  → 人点升格 → 轻确认
  → requirement created + promote + polish stopped
  → pe_running（Intent 已种子）⇄ pe_waiting_decision
  → pe_publishing → exec_running ⇄ exec_waiting_approval
  → done | failed | paused

新建需求（材料门禁）→ pe_running → …
```

## 4. 相对现状的改动点（施工时）

- `NewRequirementPage`：强化说明\|AC 门禁；去掉「短文建议后主路径变建 Task」的混淆；旁注去新建任务
- `NewTaskPage` / 会话：短路径未挂需求时头部次要「升格」（非 composer 上方 sticky）
- 升格：先 `POST /requirements`（带种子）再既有 `POST .../promote`；停写打磨 Task；PlanDraft 热启动
- 需求页：换成 PE 统一轨（替换双轨面板）

## 5. 确认后施工顺序

1. 线框 OK（本文件 + html）
2. 会话升格 CTA + 轻确认 + Promote/种子/停写
3. 新建需求材料门禁文案与校验
4. `pe` stage runner（单会话 + 门禁；升格路径跳过空 Intent）
5. 需求页统一轨组件
6. Publish 自动物化 + 根 Start；决策卡 API；真机 e2e

## 6. 非目标（本期）

- 旧数据迁移；削弱 L3；多智能体编排炫技
- 按 PE 阶段绑不同 Agent 主路径 UI
- 自动从任务升格；合并仪表盘双入口
