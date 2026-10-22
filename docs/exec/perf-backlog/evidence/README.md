# 证据目录

**规则：每段两个文件，`before.md` 必须在改代码之前写完。**

```
evidence/
  A/ before.md  after.md     # P0-1
  B/ before.md  after.md     # P0-2 + P1-3
  C/ before.md  after.md     # P1-1 + P1-2（真机帧耗时）
  E/ observation.md          # P3-1（只有观察结论）
```

## 每条证据必须包含

1. **测量方法**：具体命令 / 端口 / `PIEQI_HOME` / 任务 id / 请求头。
   —— 换个会话复现不出来就等于没有证据。
2. **原始数字**：不要只写"变快了"。写 `5.42 MB → 3.15 MB（-41.9%）` 这种可复核的算式。
3. **与基线的并列表**：backlog 里的基线数字 + 本次实测 + 差值，三列放在一起。
4. **失败/异常记录**：沙箱拒绝、进程起不来、测得不准 —— 照实写。
   历史教训（`AGENTS.md`）：把提权升级悄悄跳过、改走替代路径，会让问题藏起来。

## 隔离实例起法（仓库硬性约定）

```powershell
$env:PIEQI_SERVER_PORT = "3100"          # 绝不用 :3000
$env:PIEQI_HOME        = "$env:TEMP\pieqi-perf-<段>"
```

⚠️ 实测记录：`state.md` 里已注明 **`:3000` 当前无监听进程**，不要假设线上实例在跑。
另注意 `docs/performance/perf-backlog-2026-10-22.md` 附录 C 的教训：探针实例若与生产共用
`PIEQI_HOME`，会对 `~/.pieqi/logs` 产生写入副作用 —— **要完全干净就用独立 `PIEQI_HOME`**，
代价是空任务库、测不到真实负载。两者按测量目标二选一，并在证据里写明选了哪个。
