# Windows Low 完整性与发版落点（深潜）

| 字段 | 值 |
|---|---|
| 状态 | 现行事实（操作摘要在 `AGENTS.md`「发版」） |
| 定案 | 2026-10-14（落点）；同日修订「与谁启动无关」 |
| 相关 | ADR-0009（自重启接续）、ADR-0016（GOTMPDIR） |

本文保存 AGENTS 精简后仍需可查的**判据与踩坑**；日常发版只看 `AGENTS.md` + `build\release.ps1`。

## 两个目录、两种职责

| 路径 | 角色 | 唯一写者 |
|---|---|---|
| `<repo>/build/pieqi.new.exe` | 编译/交付产物（build/ 里唯一二进制） | 编译命令 / agent |
| `%USERPROFILE%\.pieqi\bin\pieqi.exe` | 运行产物 | `build\restart.bat`（**copy**，不是 move） |
| `%USERPROFILE%\.pieqi\bin\pieqi.exe.old` | 回滚备份 | `core.SelfRestart` / `restart.bat` |
| `%USERPROFILE%\.pieqi\bin\pieqi.exe.failed` | 起不来的版本留证 | `core.SelfRestart` |

- `.new` = 编译期待交付 ⇒ 必须在仓库内 `build/`，且只有一个。
- `.old` / `.failed` = 运行期证据 ⇒ 必须与正在执行的 exe 同目录（`~/.pieqi\bin\`）。
- **不要往 `build/` 放 `.old`**（双写者、双真相）。
- 构建**绝不**直接写 `pieqi.exe`（否则回滚备份变成刚编的那份）。

## 为什么运行位必须在仓库外

仓库目录可带 `Mandatory Label\Low Mandatory Level`。Low 标签被仓内新建文件继承。

Windows 新进程完整性 = `min(父令牌, 镜像文件强制标签)` ⇒ 跑仓内 exe **必然是** Low 进程 ⇒ 写不了 `~/.pieqi`（创建任务 500；日志 sink 建不起时 `NewLogger` 静默降级，**端口在听但日志零增长**）。

- **与谁启动无关**（Explorer / svchost / 计划任务皆然）。曾误判「只要不在会话内启动就安全」，双击 `restart.bat` 仍复现。
- **改标签修不了**（需 WRITE_OWNER）；**copy 能修**（新文件继承目标目录 Medium）。
- `move` / 部分 `xcopy`/`robocopy` 会带走 Low 标签或 SACL —— 安装必须用 **copy/cp**。

## Staging 约定

- 交付目录 = `<dataRoot>/bin`（默认 `%USERPROFILE%\.pieqi\bin\pieqi.new.exe`），见 `resolveStagingDir`。
- **已废弃**：`<config.yaml 所在目录>/build`（config 常在 `~/.pieqi\bin`，会算出不存在的路径 ⇒ 静默 restart-only）。
- **已删除**：`PIEQI_STAGING_DIR`（漏设即静默退化，不要加回）。
- 算不出 / 目录不可用 ⇒ 返回空串并报错，不代建、不猜路径。

## 有没有产物决定路径

`build/pieqi.new.exe` 存在且非零 ⇒ `swap-and-restart`；否则 `restart-only`（不 copy、不写 `.old`、不删产物）。`POST /api/admin/restart` 与 `restart.bat` 对齐；有产物时优先 swap（勿因 restart-only 接线而丢掉新版）。

## GOTMPDIR

必须指到工作区内（`build\.gotmp`）。工作区外任意目录（含 `%TEMP%`）⇒ `creating work dir: Access is denied`。见 ADR-0016。
