# ADR-0016：`go build` 的 work dir 必须落在工作区内（前一条"会话禁止建目录"的结论已推翻）

- 状态：**已更正**（2026-10-23；初版结论错误，当日推翻）
- 影响面：在 dsh 会话内编译本机 Go 二进制的所有操作

## 结论（一句话）

```powershell
$env:GOTMPDIR = "$PWD\build\.gotmp"     # 必须在工作区内
go build -o build/pieqi.new.exe ./cmd/pieqi   # -o 也必须在工作区内
```

**必须同时满足两条**：`GOTMPDIR` 指向工作区内、输出也写在工作区内。满足则构建成功，
不满足则报 `creating work dir: mkdir C:\Users\...\Temp\go-buildNNN: Access is denied.`。

## 实测对照（判据是"产物真的生成"，不是退出码）

| `GOTMPDIR` | 产物 |
|---|---|
| `<repo>\build\.gotmp` | ✅ 生成（38 402 048 字节） |
| `%TEMP%\任意目录`（目录已存在、DACL 全开也是） | ❌ 不生成 |
| 未设置（落到系统 Temp） | ❌ 不生成 |

另有一条独立的坑：`-o` 写到 `%TEMP%` 会以
`copying ...: open C:\...\Temp\x.exe: Access is denied` 失败 —— 输出同样必须在工作区内。

## ★ 初版结论错在哪里（留档，避免重犯）

初版写的是「会话令牌禁止 `CreateDirectoryW`，任何 `go build` 都必然失败，提权也不解」。
**那是错的**，错在实验设计：我试过的每一个 `GOTMPDIR` 目录（会话私有 Temp、上级 Temp、
专门新建的空目录、`build-dev.sh` 早期建议的位置）**全部位于工作区外**，于是观察到的
现象被归纳成了"会话禁止建目录"。真正被禁止的是**在工作区外建目录**。

这组实验里还有两处方法错误值得记住：

1. **读错退出码**：管道里跑 `go build ... | Select-Object` 时 `$LASTEXITCODE` 会串到
   别的命令上，我因此一度"看到"一次成功并据此下了相反结论。**判据必须是产物是否存在
   且非零字节**，不是退出码。
2. **"脱离会话令牌就失败"的解释只差一步**：用计划任务跑确实也失败，但那只是因为
   它同样把 work dir 放进了系统 Temp —— 两种情况的原因同一个，不需要引入"令牌"假设。

## 为什么 `go list` / `go run` 不失败

它们不需要 work dir（`go run` 缓存命中时产物直接进 `GOCACHE`）。所以"能 list 不能 build"
这个组合本身就是一个提示：问题在 work dir，不在工具链可用性。

## 落地

`build/` 下三个脚本已按这条结论写好，**会话内可一键发布、无需人类介入**：

| 脚本 | 作用 |
|---|---|
| `build\release.ps1` | 前端 build → `go build`（自动设 `GOTMPDIR`）→ 投递 → 自重启；`-NoDeploy` 只构建 |
| `build\release.bat` | 同上，纯 ASCII（cmd 在中文 Windows 按 GBK 读 .bat） |
| `build\deploy.ps1` | Windows 版 `deploy-dev.sh`：投放 `<dataRoot>\bin\pieqi.new.exe` → POST `/api/admin/restart` |

`build/build-dev.sh` 也已改为**无条件**使用 `build/.gotmp`（原来只在调用方未设
`GOTMPDIR` 时才设，而外部的 `GOTMPDIR` 恰恰是失败来源）。

## 发布链上的两条纪律（实测踩到，写进脚本注释）

1. **POST `/api/admin/restart` 不会返回**：旧进程正是随它退场的，调用它的 agent 会话
   会一起被切断。判断成功与否**一律用 GET `/api/admin/restart`**，`staged` 回到 `false`
   才说明产物被消费、换版生效。
2. **"等新实例就绪"不能从当前时刻开始轮询**：旧实例要等新实例 bind 成功才放手，所以
   第一次 GET 会打到正在退场的旧实例并立刻成功 —— 脚本会误报"就绪"，随后进程消失。
   必须先等它答不上话（或端口真空），再等它答得上。
