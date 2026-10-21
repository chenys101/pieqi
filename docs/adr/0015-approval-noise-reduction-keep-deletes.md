# 审批降噪：测试/构建/只读探查免审，删除一律人工

## 背景（2026-10-21 的实测数据）

统计本机 10-20 03:19 至 10-21 15:5x 的全部 dsh 会话落盘数据（`~/.dsh/sessions/*/session.v4.jsonl.zstd`，按 `approval/asked` + `approval/decided` 配对，再回联 `tool/call` 取命令原文）：

- **101 次人工审批**：88 次 allowed-once、13 次 cancelled；等待累计 **13514s（约 3.75 小时）**，平均 154s/次，最长 1589s；13 次 cancelled 每次都等满 1800s 超时。
- 按性质分类：

| 类 | 条数 | 代表命令 | 结论 |
|---|---|---|---|
| 测试 / 构建 / 类型检查 | **67（66%）** | `npm run test`、`npm run typecheck`、`npm run build`、`npx vitest run …`、`npx playwright test --project=…` | 不该审 |
| git 写 | 7 | `git add -- …`、`git commit -F …`、`git push origin main` | add/commit 不该审；push 该 |
| git 读 | 2 | `git status -sb; git rev-parse …; git log --oneline origin/main..HEAD` | 不该审 |
| 工作区外写 | 7 | 投递到 `~/.pieqi/bin`、改 `~/.dsh/AGENTS.md` | 该 |
| 工作区内删除 | 1 | `Remove-Item internal\core\zz_debug_test.go` | **该（用户明确要求保留）** |
| 写 + 跑混在一条 | 11 | `Remove-Item x; npx vitest …`、`$c.Replace(…); WriteAllText(…); npx vitest …` | 该（但都是可以拆开的） |
| 其他 | 6 | `node -e "…readFileSync…"`、`git -C … push` | node -e 不该；push 该 |

根因不是判据写错，而是两层结构性缺口：

1. **白名单只认 POSIX 读取命令**。`git` / `npm` / `npx` / `node` / PowerShell cmdlet（`Get-Content`、`Select-String`、`Test-Path`…）一个都不在表里 ⇒ 本机（Windows + PowerShell）的日常命令几乎全部落 L2。
2. **单次提权把"要不要跑测试"放大成"要不要放开全盘"**。vitest 要 spawn esbuild，受限沙箱必然 `EPERM`；要跑测试就只能带 `sandbox_permissions: danger-full-access`，而该字段触发一次全新人工审批、**没有"本会话已授权"的连续性**。于是 12 次测试命令被连续取消、每条卡满 30 分钟 —— 一整个下午无法验收。

## 决策

### 1. 测试 / 构建类命令按**脚本名 + 实际会跑什么**分档（`internal/agent/shell_runner.go`）

- `npm/pnpm/yarn run <script>` 只在脚本名命中**固定**分档表时免审：`test`/`typecheck`/`tsc`/`lint`/`check`/`vet` → `read`(L0)；`build` 等 → `edit`(L1，写可重生成的产物)。
- `npx <bin>` 只认本地 devDependencies 里确知的测试/构建器（`vitest`/`tsc`/`vue-tsc`/`playwright`/`eslint`/`vite`）。
- 一律拒绝：装/改依赖（`install`/`add`/`-g`/`--package`）、常驻（`--watch`/`--ui`/`--dev`）、对外发布（`publish`/`audit`）、改基线（`--update-snapshots`/`-u`）、`playwright install/codegen/merge-reports`。
- **不按前缀放行**：`npm run <任意脚本>` 等于把 `package.json` 变成免审白名单，明确否决。

### 2. PowerShell 原生命令进只读白名单（`readonly_kind.go`）

补 `Get-Content/Get-ChildItem/Select-String/Select-Object/Format-*`/`Out-String`/`Out-Null`/`Test-Path`/`Get-FileHash` 等只读 cmdlet，以及 `netstat`/`tasklist`/`ipconfig`/`whoami` 等诊断命令。命令名统一小写（cmdlet 大小写不敏感），并剥 `.exe/.cmd/.bat/.com` 扩展名。

- 输出整形（`Out-String`/`Out-Null`）作为管道尾放进 wrapper 表：它们不是动作。
- **`git fetch` 从 L2 降为 L0**：只更新 `.git` 里的远端跟踪引用，不改工作区/本地分支/共享远端。

### 3. 「解释器 + 内联代码」只在**原文完整**时判读

`node -e "…"` 的语义全在引号里那段代码，而切段前会把引号内容剥空（否则中文散文会干扰分段）。所以：

- 分类器（`ClassifyShellCommand`）看到的内联代码永远是空 —— 它对这类**一律给 execute**，不许说 read。
- 唯一能判它只读的地方是权限入口（`DowngradeReadonlyPermission`）：原文还完整，且只有"单条内联代码命令"时才按"原文无任何写/执行动词"放行。
- 这条是实测教训：一次编辑缩进错误让"内联代码的读审闸"掉了两秒，立刻出现 `node -e "…writeFileSync…"` 被判 read —— 靠回归用例抓回来的。

### 4. 原文扫描：危险痕迹一出现就整条不降级（`permissionReqHasUnsafeText`）

分段会剥引号内容，于是"藏在引号里的删除/直写/进程控制"对分段判定不可见。对**命令原文**再扫一遍，命中即放弃降级：

- 删除类：`Remove-Item`、`rm -rf`、`del /`、`rd /s`、`Clear-Content` …；
- 直写类：`Out-File`、`Set-Content`、`WriteAllText`、`New-Item`、`Copy/Move/Rename-Item`、`Tee-Object`、写向重定向；
- 进程/系统控制：`Stop-Process`、`taskkill`、`Stop-Service`、`icacls`、`takeown`、`Set-ExecutionPolicy`；
- 提权/包装：`runas`、`Start-Job`、`Invoke-Expression`、`Invoke-WebRequest`、`Start-BitsTransfer`；
- 内联代码的写/执行 API：`writeFileSync`/`execSync`/`child_process`/`subprocess.`/`os.system`/`shutil.` …

刻意**不**收裸词 `rm`/`rmdir`：本仓库文档与测试大量**提到**它们（`"rm -rf build/"` 是测试用例字符串），字符扫描会把源码文本误判为危险，整条降级链会被它废掉。

### 5. 非 shell 工具按**参数**分档（`noncommand_tool.go`）

dsh 的 `lsp`（代码智能）、`job_output/job_list`、`todo_write` 在 `dshToolKinds` 里刻意留在 `other`(L2)。新增一层：只看 rawInput —— 键必须全在"查询类字段"白名单里、且不出现写动词（`rename`/`edit`/`apply`…），才给 `read`。识别 `gopls__definition` 这类命名空间前缀（按 `__` 切分逐段匹配）。写动词表**故意不含 `write`** —— 它会误伤 `todo_write`（实测被回归用例抓到）。

### 6. **删除一律人工审**（用户 2026-10-21 明确要求）

"E 类删除需要审核"。落实为三条：分类器与原文扫描都把删除类动词判为必须人工；`delete` 仍是 L3；`Settings.AutoApproveTools()` 只由 L0/L1 推导（L3 没有配置字段）。回归测试 `TestDowngradeReadonlyPermission_DeleteStaysManual` 用 6 种删除形态钉住这一点。

### 7. 命令原文进降级链（字段扩展）

入参里承载可执行文本的字段从只有 `command` 扩展为 `command|cmd|code|script|input|shell`；**入参里压根没有可执行文本**（如 lsp 的 `{method,params}`）时**不降级** —— 否则"没有命令"会被当成"没有副作用"。

## 仍维持人工审的（明确不变）

- 工作区**外**的写（`~/.pieqi/bin` 投递、`~/.dsh/AGENTS.md`）
- `git push` / `pull` / `clone` / `submodule` / 任何影响共享远端的动作
- 改基线（`--update-snapshots`）、`playwright install`
- 所有删除（含递归删除）
- 装依赖 / 常驻进程 / 对外发布
- 无法分档的复合命令（`execute`）

## 代价与边界

- 全部判据仍是**基于文本的启发式**，可被刻意构造的命令绕过。目标从"安全门禁"下调为"减少噪音"，兜底仍是 worktree + Baseline/Checkpoint（ADR-0002）与 L3 的人工确认。
- `npm run build` 归 L1 而非 L0：它会写 `web/dist` 等产物，虽然可重生成，但确实写了盘。
- 白名单外命令（`go`/`curl`/`docker`）一律维持 L2：没有"本地可逆"的通用判据，硬造只会扩大放行面。

## 与既有 ADR 的关系

不推翻 ADR-0008 的判据取向（"证明不了就往保守侧倒"），是在同一框架内**补全"能证明"的那一侧**：原白名单只覆盖 POSIX 读取命令，本 ADR 把本机实际使用的 PowerShell / package runner 也纳入可证明范围。ADR-0008 的 `sed -i`、`find -delete`、重定向等排除规则全部保留。
