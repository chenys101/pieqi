# Pieqi Frontend V2 迁移完成报告

> 依据《Pieqi Frontend V2 架构升级技术方案.md》完成，日期：2026-09-01

## 一、验收结果

| 验收项 | 命令 | 结果 |
|---|---|---|
| 类型检查 | `npm run typecheck`（vue-tsc --noEmit） | ✅ 0 错误 |
| 生产构建 | `npm run build`（vue-tsc -b && vite build） | ✅ dist 正常生成 |
| 单元测试 | `npm run test`（vitest run） | ✅ 6 文件 44 用例全部通过 |
| Go 单二进制 | `go build ./cmd/pieqi` | ✅ ~36MB，dist 已嵌入 |

构建产物：`web/dist/`（index.html + 带 hash 的 assets + sw.js + manifest.webmanifest + icons）。

## 二、架构落地情况（对照方案）

### 技术栈
Vue 3.5 + Vite 5 + TypeScript 5.6 + Tailwind CSS 3.4 + Pinia 3 + Vue Router 4 + Vitest 2。

### 目录结构（方案 §6）
- `src/app/`：App.vue / router.ts / providers.ts（一次性接线）
- `src/layouts/`：AppLayout（响应式切换）/ DesktopLayout（Sidebar）/ MobileLayout（TopBar + BottomNav）
- `src/pages/`：Dashboard / Tasks / Session / Agents / Approvals / Projects / Settings
- `src/features/`：task / session / timeline / approval / agent / dashboard / settings（含 components + index.ts 出口）
- `src/components/ui/`：Button / Modal / Badge / Card / Spinner / EmptyState / ToastHost
- `src/stores/`：app / task / session / approval / agent / notification
- `src/services/api/`：client（HTTP 统一层 + DTO Adapter）/ tasks / skills / auth / tunnel / larkreg
- `src/services/websocket/`：client（重连）/ reconnect（指数退避）/ normalizer / dispatcher
- `src/composables/`：useTask / useSession / useWebSocket / useResponsive / usePwa
- `src/types/`：task / session / event / approval / agent / api（wire DTO）

### 关键设计决策的落实
- **Backend First / 协议零改动**（§3.1）：未新增/修改任何后端 API；无 allow_always，审批仅 approve/deny。
- **Event Normalizer 隔离层**（§41/§42）：后端 wire format → 前端稳定模型全部收敛在 `normalizer.ts` + `adaptTask`；含旧数据「↻ 续问: 」前缀归一。
- **事件去重**（§14）：`EventDeduper` 按 `taskId:seq` / delta 合成 id 去重，覆盖重连重复推送。
- **流式增量合并**（§39）：`mergeDeltaIntoEvents` 与后端 appendTextDelta 互为镜像；首次正文清除思考占位。
- **API 不出现在 Component**（§33）：组件只碰 store / composable。
- **乐观 UI**（§36）：审批提交、续问提交均本地即时更新，WS 校准。
- **兼容旧深链接**（§26）：`/session/<id>` 重定向到 `/sessions/:id`。
- **PWA**（§38）：manifest（SVG any+maskable icon）+ sw.js（HTML network-first、hash 资产 cache-first、API/WS 不缓存）+ usePwa 安装提示。

### 单元测试覆盖（§48）
- `normalizer.spec.ts`：消息归一/校验、事件类型映射、续问前缀归一（8 用例）
- `event.spec.ts`：去重集合、增量合并、首次正文判定（8 用例）
- `format.spec.ts`：标题截断、路径归一 groupKey、工具参数格式化（13 用例）
- `reconnect.spec.ts`：指数退避 1s→30s 封顶、reset（3 用例）
- `task.spec.ts`：getters（running/waiting/needsAttention/counts/分组）、upsert、乐观更新（6 用例）
- `session.spec.ts`：事件流同步、流式追加、思考占位、本地用户消息（6 用例）

## 三、旧前端清理（方案 §65）

已删除 V1 Vanilla JS：`web/src/main.js`、`auth.js`、`autocomplete.js`、`drawer.js`、`larkreg.js`、`settings.js`、`tunnel.js`、`styles.css`、`web/vite.config.js`（替换为 `.ts`）。未保留 legacy/old/v1 目录。

V1 → V2 功能迁移对照：

| V1 | V2 |
|---|---|
| main.js 任务列表/路由 | TasksPage + router |
| drawer.js 抽屉侧栏 | DesktopLayout Sidebar / MobileLayout BottomNav |
| autocomplete.js 斜杠补全 | features/session/PromptInput.vue |
| auth.js 鉴权上下文 | services/api/client.ts（tunnelToken/feishuOpenId/401 处理） |
| tunnel.js 隧道控制 | features/settings/TunnelPanel.vue |
| larkreg.js 飞书接入 | features/settings/LarkConfigModal.vue |
| settings.js 设置 | pages/SettingsPage.vue |
| styles.css | styles/tokens.css + components.css + Tailwind 工具类 |

## 四、遗留与后续（方案 Phase 8/9，稳定后实施）

- Agent Observability：Tool Trace / Event Filter / Event Search / Cost / Token
- 高级功能：Replay / Session Fork / Watchdog / Agent Graph / Project Brain
- E2E：Playwright 已列入 devDependencies（`npm run test:e2e`），用例待后端联调环境就绪后补充
