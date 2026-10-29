# web — Pieqi PWA 前端

Vite + Vue3 + TypeScript + Tailwind + Pinia。构建产物 `web/dist` 由 [`embed.go`](embed.go) 通过 `//go:embed` 嵌入 Go 二进制，
因此**改了前端必须先 `npm run build` 再重编 Go**，否则二进制里还是旧前端。

## 命令

```bash
npm install
npm run dev              # :5174，/api、/internal 代理到 :3000
npm run build            # vue-tsc -b && vite build → web/dist
npm run typecheck        # vue-tsc --noEmit（含 e2e 的 tsconfig.e2e.json）
npm run test             # vitest run（jsdom，逻辑层）
npm run test:e2e         # Playwright：layout + visual + interaction
npm run test:e2e:impact  # 按 git diff 只跑相关用例
npm run test:e2e:online  # 部署后线上复量
```

> ⚠️ `npm run test` 绿灯 **≠** UI 验收。jsdom 量不出尺寸、遮挡、滚动与首屏渲染代价，
> 涉及这些的改动必须走 Playwright 并**与基线比数字**。规程见 [`docs/testing/ui-e2e.md`](../docs/testing/ui-e2e.md)。

## 目录结构

```
web/
├── src/
│   ├── app/           # App.vue、router（页面懒加载）、providers（全局插件/主题装配）
│   ├── pages/         # 薄路由页：Dashboard / Tasks / NewTask / Session / Agents / Settings
│   ├── layouts/       # AppLayout / DesktopLayout / MobileLayout / AppSidebar
│   ├── features/      # 按域切分，域内自带 components / 纯函数 / spec
│   │   ├── agent/       # Agent 能力卡与矩阵
│   │   ├── approval/    # 审批卡与审批摘要
│   │   ├── dashboard/   # 仪表盘洞察（待审批、本周概览）
│   │   ├── feedback/    # 反馈面板：TurnCard / DiffView / ChecksPanel / EvidenceCard / OutcomeCard / RewindCard / PreviewSection / FilePreview
│   │   ├── session/     # 会话页：时间线容器、输入框、模型选择器、图片选择、Turn 分组
│   │   ├── settings/    # 设置页：隧道面板、机器人列表与创建
│   │   ├── task/        # 任务浏览器分组与列表窗口
│   │   └── timeline/    # 事件渲染：文本/思考/工具卡/工具结果/语义化
│   ├── stores/        # Pinia：task / session / approval / agent / app / notification / feedback*
│   ├── services/      # api/（按域拆的 REST 客户端）、websocket/（重连、序号、归一化、分发）
│   ├── composables/   # useWebSocket / useSession / useTheme / useResponsive / usePwa / useContentWidth
│   ├── components/    # ui/（Button/Input/Modal/Drawer…）、layout/、task/（StatusBadge）、artifacts/（跨域复用的产物清单）
│   ├── types/         # 后端协议的 TS 类型（task / event / session / approval / agent）
│   ├── styles/        # tokens.css（设计变量）/ components.css / index.css
│   └── utils/         # 纯函数：format / date / event / filePreview / fileGroups …
├── e2e/               # Playwright（见下）
├── public/            # PWA 资源：manifest / sw.js / 图标
└── index.html
```

**结构约定**：`pages/` 只做路由与布局拼装，业务逻辑放 `features/<域>`；`features` 之间不互相 import 组件，
共享的东西下沉到 `components/`（`ui/`、`layout/`、`task/`、`artifacts/`）、`composables/`、`stores/`、`utils/`。
跨域复用的**纯函数**一律放 `utils/`，不要留在某个 feature 里让别的 feature 反向依赖
（例：`artifactOpensInApp` 曾住在 `features/feedback/filePreview.ts`，被 timeline 与 stores 引用 ——
已下沉到 `utils/filePreview.ts`）。

## e2e（Playwright 四层）

| project | 目录 | 管什么 |
|---|---|---|
| `health` | `e2e/health/` | 环境分诊：夹具后端、dev server、浏览器能否 spawn |
| `layout` | `e2e/layout/` | 数值断言：DOM 量、遮挡、溢出、LCP/CLS，**与 `e2e/baseline/layout-baseline.json` 比** |
| `visual` | `e2e/visual/` | 视觉基线：6 场景 × 3 视口 × 双主题，产物落系统临时目录 |
| `interaction` | `e2e/interaction/` | 行为：点下去请求有没有发、参数对不对 |
| `online` | `e2e/online/` | 部署后对线上实例复量（`test:e2e:online`） |

基线（`e2e/baseline/*.json`、`e2e/visual/__screenshots__/**`）**入库**——它们就是"上次验收通过的标准答案"。
更新方式：`npm run test:e2e:update-baseline` / `npm run test:e2e:update-snapshots`。

> 坑：改 `tailwind.config.ts` 或夹具后**必须重启 dev server**（`reuseExistingServer` 会复用旧进程，
> Tailwind 的内容扫描不会重扫，表现为"改的样式没生效"且不报错）。
