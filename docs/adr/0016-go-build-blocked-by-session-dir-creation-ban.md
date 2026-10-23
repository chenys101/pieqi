# ADR-0016：本机会话内 `go build` 必然失败——根因是「会话令牌禁止建临时目录」，与 go 版本/项目/权限无关

- 状态：已确认（2026-10-23 实测）
- 影响面：所有在 dsh 会话内跑 `go build` / `go vet` / `go test` 的操作，以及据此得出的"是不是沙箱提权没生效"的判断

## 现象

```text
go: creating work dir: mkdir C:\Users\13631\AppData\Local\Temp\go-buildNNNNNNNNNN: Access is denied.
```

任何 `go build`、`go vet`、`go test` 都失败，**换包、换工程、换输出路径、换 TMP/TEMP/GOTMPDIR 都没用**。
但 `go list`、`go run`（缓存命中时）成功 —— 因为它们不创建 work dir。

## 根因（逐条实测，不是推断）

1. **不是 go 版本、不是项目**：一个全新的 `go.mod` 最小工程同样失败；
   `go env`、`go list ./internal/model` 正常。
2. **不是目录权限**：`C:\Users\13631\AppData\Local\Temp` 的 DACL 是
   `SYSTEM/Administrators/13631:(F)`，PowerShell 与 `cmd` 都能在其中建目录，
   包括 `go-build` 前缀的随机名目录。
3. **不是名字前缀**：用 Win32 `CreateDirectoryW` 在 Temp 下建任何名字都返回 `err=5`
   （ERROR_ACCESS_DENIED），而我编译出来的 exe **存放在 `D:\code\go\pieqi` 下时**
   正是这样；同一个 exe 从 `%TEMP%` 拷贝出来跑则一切正常。
4. **决定性实验**：同一个 `go build` 交给 **Windows 计划任务**执行（脱离 dsh 会话的
   令牌，工作目录同样是仓库），**依然失败**。
5. **对照**：从仓库外的 CWD 起新进程再 `cd` 进仓库执行，同样失败。

⇒ 结论：**被 dsh 会话施加的限制落在"进程能创建什么"上，而不是"文件路径的 ACL"上**。
它拦的是 `CreateDirectoryW` 本身（至少在用户 Temp 下），任何在该会话里跑的 `go build`
都会撞上，**无论是否提权**（`danger-full-access` 也不改变这一点；提权只放开"写哪些文件"）。

## 这推翻了 AGENTS.md 里的旧结论

AGENTS.md 曾写"`go build` 偶发失败，重试即过 / 把 `GOTMPDIR` 指向会话私有目录就好"。
实测那是**误判**：失败是 100% 确定性的，`GOTMPDIR` 改到任何位置都失败；
之前"重试就过"的记忆多半来自另一台机器或另一版本的环境。

同时：`go run` 能跑通，是因为它把产物写进了 `GOCACHE`（`%LOCALAPPDATA%\go-build`），
**不需要 work dir**。`GOCACHE` 仍可写，所以"依赖包能解析、能 list"但"编不出 exe"。

## 在会话内要拿到可运行的二进制

1. **正常路径仍是 `go build`** —— 交给**用户**在资源管理器/终端里跑，或让一个不在
   本会话树内的宿主（svchost / 计划任务之外的登录会话）执行。本会话内不要为此绕路。
2. 紧急情况下可以从**并行会话**的构建产物取（本次 `build/pieqi.new.exe` 就是另一个
   并行会话在同一份源码上编出来的），但要**核对源码一致性**：`main.go` 前缀是
   `pieqi/cmd/pieqi` 的源码嵌入，可据此确认是同一个包。
3. **不要**为此改构建命令形态（例如手工重放 `go build -n` 的 compile/link 计划）——
   实测那条路也走不通（`compile.exe` 报 `open .25`），而且会掩盖"命令本身能不能跑"这个
   真实问题。

## 与"启动纪律"的关系

仓库目录（`D:\code\go\pieqi`）本身带 `Mandatory Label\Low Mandatory Level`，
区内产物会继承 Low 标签，这是**另一条**已记录的纪律（见 AGENTS.md「启动纪律」）。
本次附带证实了它的后果：把它自己编出的测试 exe 从 `build/` 里直接跑，
连在用户 Temp 下建目录都会被拒 —— 所以"运行副本必须在仓库外"这条继续有效。
