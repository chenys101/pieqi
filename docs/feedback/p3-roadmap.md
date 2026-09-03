# Pieqi Feedback P3 路线图（探索）

> Status: Roadmap / Exploration（不承诺排期，作为长期差异化方向记录）
> Updated: 2026-09-03
> 关联：`CONTEXT.md`、`docs/adr/0001~0007`、`p0-design.md`、`p1-design.md`、`p2-design.md`

---

## 1. 定位

P3 是**探索性能力**，不进入当前主 RoadMap。价值在于指明差异化方向，并确保 P0–P2 的数据结构不为未来 P3 挖坑。规划对应 §27、§28。

## 2. DOM Inspection（规划 §27）

**现状（P2 后）**：Screenshot 只有像素。用户在截图上看到问题，但无法把「位置」变成 Agent 能用的语义。

**目标**：从截图像素反查页面 DOM 节点/选择器。

```text
Screenshot
   ↓
点击坐标 (x, y)
   ↓
visual-capture 服务在打开的页面执行
  document.elementFromPoint(x, y) → 选择器（id / data-attr / nth 路径）
   ↓
DOM Selector
```

**可行性**：Playwright 已在 P2 底座，`elementFromPoint` 一行 JS。依赖 P2 的持续 attach（页面要保持打开才能查询 DOM）。

## 3. Region Annotation（规划 §27）

**目标**：用户在截图/页面上框选区域 → 映射为 DOM 区域 → 作为给 Agent 的视觉输入。

```text
Screenshot → 框选 Region (x1,y1,x2,y2)
   ↓
映射 DOM 节点集 / 选择器
   ↓
Visual Evidence（区域 + 选择器 + 用户描述「这个按钮有问题」）
   ↓
Agent
```

**关键难点**：像素框 ↔ DOM 的映射在响应式/滚动布局下会漂移；需要截图时记录视口与滚动位置。P3 主要工作在这里。

## 4. Visual Evidence → Agent（规划 §27）

**目标**：把带区域的视觉证据喂给 Agent，形成闭环。

```text
「页面顶部按钮仍然错位」+ 框选区域
   ↓
Pieqi 组装：截图引用 + DOM 选择器 + 用户描述
   ↓
Continue Agent
```

依赖 P1 的 Evidence → Continue 扩展 + Provider 的**视觉理解能力**（Agent 侧是否看图由 provider 决定，Pieqi 负责把区域语义化后传引用）。这是 Feedback 从「文字证据」升级到「视觉证据」的最后一公里。

## 5. Task Replay（规划 §28）

**目标**：重新查看「Agent 是如何一步一步把项目改成现在这样的」。

```text
Task → Turn → Change → Check → Evidence（P0-P2 已全部持久化/可派生）
```

**数据基础（P0–P2 已具备）**：

| 需要 | 来源 |
|---|---|
| Turn 序列 | TaskEvent（EventUser 边界，ADR-0003） |
| 每 Turn 变更 | FileChange 派生（ADR-0001） |
| 每 Turn 文件状态 | Checkpoint（P0） |
| Checks / Outcome | P1 |
| 视觉证据 | P2 |

**结论**：Task Replay 不需要新数据采集，P0–P2 的模型已覆盖；它主要是**播放视图**（按 Turn 步进回放 diff/check/截图）。可为未来 **Project Brain** 提供数据基础。

## 6. 触发条件（何时值得做）

以下任一成立时，从 P3 池中挑一项进入设计：

- 用户高频出现「我说不清楚按钮哪里不对」的反馈（→ Region Annotation 优先）
- 移动端验收场景要求「看图说话」闭环（→ Visual Evidence → Agent）
- 需要向用户解释「Agent 到底怎么改的」（→ Task Replay）

**当前建议**：P3 不做。优先把 P0 → P1 → P2 的闭环跑稳，同时确保 P0–P2 的数据结构（Checkpoint 粒度、Evidence 挂载点、Screenshot 存储）不阻碍未来 P3。
