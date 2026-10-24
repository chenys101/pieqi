# ADR-0018：产物（Artifact）默认可访问，agent HTML 走同源隔离

- 状态：**已采纳**（2026-10-24）
- 影响面：`internal/api/artifacts.go`（新端点）、`internal/core/preview.go`（`EnsureStatic`）、
  `internal/api/file_preview.go`（CSP sandbox）、`web/src/features/feedback/PreviewSection.vue`（产物列表）

## 背景

ADR-0017 让静态产物可以**零进程**被 serve，但入口仍是 "preview" 这个服务语义：

1. 用户得先点「启动」——可静态产物没有准备期，这一步纯粹是摩擦；
2. 用户得**知道路径**才能打开某个文件（面板只给一个目录入口）；
3. agent 产出散落在别处时，连入口都没有。

而 UI 迭代的真实动作是"**看一眼 agent 刚写出来的东西**"：方案对比页、HTML 报告、截图、PDF。
它们是**文件**，不是服务。用 preview 承载它们，等于逼用户用"跑项目"的方式看一张图。

## 结论

**引入产物（Artifact）层：列得出来、点得开、默认可访问——但**不给宿主同源权限**。**

1. **清单**：`GET /api/tasks/:id/artifacts` 返回本任务的可查看产物。
   事实源是 `TaskEvent → DeriveFileChanges`（同一口径已用于 Rewind 与文件预览），
   只列白名单类型（html / markdown / image / pdf），按 html → markdown → image → pdf 排序。
   **不另造"产物登记"**：磁盘现状为准（stat 不过就跳过），未触碰的敏感文件不进清单。
2. **默认可访问**：`PreviewManager.EnsureStatic(task)` 是唯一入口 —— 认出静态产物目录就地登记
   （无进程、零成本），`Status` 直接报 `running`。三处（status / 代理 / 产物清单）共用它，
   **复制三份必然漂移**（症状：同一件 HTML 这次用 `/preview` 打开、下次退化成 `/file`，
   而 `/file` 不解析相对资源 ⇒ 只有多文件页面坏，极难查）。用户显式 Stop 过则不自动拉起。
3. **打开通道由服务端算好**：目录内 HTML → `/preview/<id>/<rel>`（相对 css/js 能一起解析）；
   其余 → `/api/tasks/:id/file?path=`（原始字节）。
4. **同源隔离**：`/file` 返回 `text/html` 或 `image/svg+xml` 时，强制
   `Content-Security-Policy: sandbox allow-scripts allow-forms allow-popups allow-modals`
   **且不写 `allow-same-origin`**，配 `Referrer-Policy: no-referrer`。
   效果：页面与脚本照常运行（方案页能点），但文档落在 opaque origin —— 读不到 pieqi 的
   localStorage（token 在那）、发不出带凭据的 `/api` 请求、无法以用户身份驱动 agent。
5. 静态预览的「启动 / 重启 / 停止」按钮对 `framework=static` 隐藏：
   无进程可重启，"停止"只会让页面暂时打不开（纯坑）。

## 为什么"默认给访问权限"必须配 sandbox

`/api/tasks/:id/file` 是**同源**的：agent 写的 HTML 一旦以 `text/html` 直接渲染，
它就站在 pieqi 的 origin 上 —— 能读 token、能调 `/api`（创建任务、审批工具、管隧道）。
"产物默认可访问"如果没这一步，等价于**把 XSS 变成默认能力**：agent 被提示注入写一个
带 `<script>` 的 HTML，用户点开就中招。sandbox 是这条产品决定的**前提**，不是加固选项。

## 被否掉的方案

| 方案 | 否掉原因 |
|---|---|
| 把 worktree 根 / 任意散落 html 自动当预览根 | 静态预览是**整目录 serve**，认领根目录等于把源码全暴露在 `/preview/<id>/` 下。散落的只报告（`suspected`） |
| 同源直接渲染 agent HTML（不加 CSP） | 等于默认开放 XSS → token 外泄 / 以用户身份执行 |
| 用独立端口/子域做"安全 origin" | 要再过一遍鉴权与隧道（quick tunnel 只给一个 host），且移动端跨端口 cookie/凭据全要重设计 —— 成本远高于一行 CSP |
| 让前端 iframe + `sandbox` 属性隔离 | 只能保护**内嵌**场景；用户在新标签直接打开 `/file` 时没有 iframe，保护就消失了。CSP 是文档级、两条路径都生效 |
| 只做 `/file` 清单、不做 preview 通道 | 多文件方案页（`index.html` + `style.css` + 若干变体页）在 `/file` 下相对路径全裂 |

## 已知限制

- 目录外的 HTML 只能以 `/file` 打开：沙箱下相对资源可能取不到（单文件/内联样式的方案页没问题）。
  多文件产品请放进静态产物目录。
- 产物清单上限 200 条；同路径多次写入只列一条（取事件派生的最后一次）。
- markdown 产物打开是原始字节（`text/markdown`），前端另有渲染通道，未在此统一。
