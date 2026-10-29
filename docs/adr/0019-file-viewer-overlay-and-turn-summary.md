# ADR-0019：文件查看层与每轮总结 —— 单一入口、默认展开、不参与布局

- 状态：**已采纳**（2026-10-29）
- 影响面：
  - `web/src/features/feedback/components/ArtifactViewer.vue`（重写为 fixed 覆盖层）
  - `web/src/features/feedback/components/CodeView.vue` + `web/src/utils/highlight.ts`（新增）
  - `web/src/features/feedback/components/{FeedbackPanel,TurnCard,FilePreview}.vue`
  - `web/src/features/session/components/{SessionTimeline,TurnSummary}.vue`
  - `web/src/pages/SessionPage.vue` + `web/src/features/session/useSessionPage.ts`
  - `web/src/stores/artifacts.ts`（删 `forTurn`）
  - 删除 `web/src/components/artifacts/ArtifactList.vue`（时间线产物列表已无调用方）

## 背景

三件事叠在一起，表现为「点代码，页面乱了」：

1. **查看器参与了父级 flex 布局**。`ArtifactViewer` 曾是 `SessionPage` 里
   `flex h-full flex-col` 的兄弟节点，而 `Drawer` 的 PC 分支是 `h-full` 的 `aside`。
   打开时它把上面的会话 grid 压成 0 高（实测：查看器 480×906、反馈面板 46×**0**、
   时间线 **32px** 高），两套内容同时绘制、互相覆盖。看起来像"换了一页/新开了 tab"，
   实际是布局塌了。
2. **详情页与反馈面板各列一遍文件**。时间线每轮挂 `ArtifactList`，反馈面板的
   TurnCard / 累计列表又列一次 —— 同一份数据两个入口，条数/路径迟早对不上，
   而且每轮产物多时详情页要渲染的 DOM 直接翻倍。
3. **反馈面板的文件分组默认收起**。用户点进「变更」还要逐目录展开，正是"多看一次"。

## 结论

1. **查看层是 fixed 覆盖层（Teleport 到 body），不参与任何布局**。
   桌面宽 `min(920px, 68vw)`，移动端整屏；Esc / ← 返回。
   code/text 走行号 + 轻量着色 + 文件内搜索 + 换行 + 复制（`CodeView`）；
   markdown/pdf 复用 `FilePreview`；**html/图片不进查看层**，仍新标签打开（真页面 + CSP sandbox）。
   `Ctrl+F` 在查看层内搜索，不触发浏览器查找。
2. **时间线不再列文件**。每轮输出末尾只留一条 `TurnSummary`：
   `本轮完成 · N 个文件 +A -D · [查看本轮变更]`。按钮把 turn 交给页面，
   由页面决定三档落点（宽屏展开右栏 / 平板开抽屉 / 移动端切反馈整屏），
   反馈面板负责切到「变更 / 本轮变化」、展开该 Turn 与其目录、滚动并描边。
   数字来自 `TurnInfo.summary`（与 Turn 头、反馈面板同源）。
3. **反馈面板默认全展开 + 顶部固定工具条**：
   - 累计：所有目录分组默认展开；`全部收起/展开` 一键切换；
   - 本轮：最新一轮默认展开，其余收起；`全部收起` **连 Turn 一起收**；
   - 目录分组头 sticky（每个目录一个包裹层，避免多个组头叠在顶部）；
   - 目录 > 6 时出现「目录 N」下拉索引（不是横向 chip：30 个目录也能滚）；
   - 筛选：目录名/文件名都匹配；命中时强制展开，只渲染命中的目录/文件。
4. **渲染预算**：产物后端上限 200 条；时间线每轮只有一条总结（不再挂产物行）；
   反馈面板不做虚拟列表（会破坏浏览器 `Ctrl+F` 与目录跳转），
   用"默认展开 + 一键收起 + sticky 组头 + 筛选"控制阅读成本；
   查看器的大文件只先渲染前 3000 行（可「显示全部」），搜索仍在全量文本上。

## 被否掉的方案

- **把查看器改成 `position: fixed` 但保留 Drawer 的宽度/位置逻辑**：Drawer 的 PC 形态
  天生是"参与 flex 的一列"，保留它就得在每个使用点约束父级 —— 事故会复发。
- **反馈列表虚拟化**：200 条以内的 DOM 不值得，且虚拟列表会让浏览器查找、目录跳转
  （目标行未挂载）失效。
- **按目录数量自动收起**：把"默认全展开"变成"看数量才生效"的承诺，用户无法预期；
  目录多时用索引 + 筛选解决导航，而不是偷偷改默认。
- **在 420px 面板里内联铺代码**：可读性差、且与时间线的产物列表重复；
  代码查看统一到覆盖层。

## 后果

- `artifactsStore.forTurn` 与 `components/artifacts/ArtifactList.vue` 删除；
  正文里的产物链接、反馈面板的「查看」仍共用 `artifactsStore.byPath`。
- `FeedbackPanel` 的默认展开/收起状态是组件内状态（切任务复位），
  只有 `activeTurn` / 折叠态仍在 `feedbackPanel` store（跨路由保持）。
- e2e 布局/视觉基线随详情页（每轮总结条替代旧产物列表）更新；新增
  `TurnCard` / `TurnSummary` / `highlight` 单测。
