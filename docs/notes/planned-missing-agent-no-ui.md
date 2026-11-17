# planned 缺 agent 无 UI 入口

## 现象

需求拆分确认落地后，任务 `planned` 且 agent 可空（设计允许）。用户打开会话页：没有 agent，也找不到地方再指定；需求任务图「缺 agent」徽章只展示、不是编辑入口。

## 现状（施工前）

- 后端 `POST /api/tasks/:id/start` body 可选 `{agent,model}`；仍空 → 400 `agent required before start`
- 拆分落地前 `SplitProposalEditor` 可改；落地后到启动前 UI 断了
- 前端未封装 `/tasks/:id/start`

## 目标

1. planned 且无 agent 时，会话页（或需求任务图）必须有明确入口：选 agent（可选 model）并启动
2. 启动走现有 `POST /api/tasks/:id/start`，不要另开 PATCH
3. 有 agent 的 planned 也应能一键启动

## 落点（已落地）

- **优先**：会话页 planned 态 — `PlannedStartPanel`（`Select` + `ModelPicker` +「启动」）；缺 agent 时禁用启动并提示
- **次选**：任务图「缺 agent」徽章可点开同款选择器（Modal + `docked=false`）
- 复用新建任务页/拆分确认前的选择器，不另开第三套
- API：`web/src/services/api/tasks.ts` → `startPlannedTask`；store：`taskStore.startPlannedTask`

## 非目标

- 不改「planned 允许空 agent」语义
- 不在运行中途换 agent
- 不改状态机 / `completeTask` 判据

## 验收

1. planned 空 agent 会话页可选 agent 并启动成功
2. planned 已有 agent 可一键 start
3. 仍空时点启动得到明确提示（对齐后端 400）
