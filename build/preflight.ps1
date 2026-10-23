# 发布前分诊：**现在到底需不需要重启**（只读，绝不重启）。
#
# 判据来自技能 `pieqi-rebuild-restart` §2.0a 的四条独立证据。它们解决的是一个很实际的问题：
# 重启会打断别人的在途会话，且 `POST /api/admin/restart` 会让调用它的会话一起退场 ——
# 所以"这次是不是白杀一个健康实例"必须先量出来，而不是凭感觉。
#
#   ① 工作树：已跟踪文件无改动（`git status --short` 里没有非 ?? 行）
#   ② 运行副本 mtime 晚于所有源码改动
#   ③ 日志里的 `main.go:NNN` 与当前源码 `grep -n` 的行号对得上（**最硬的一条**，
#      能识破"运行副本被并行会话覆盖成别的版本"）
#   ④ 服务托管的前端 index-*.js == web/dist/assets 里现存的那个
#
# 用法：
#   powershell -NoProfile -ExecutionPolicy Bypass -File build\preflight.ps1
#   ... -File build\preflight.ps1 -Port 3000
#
# 退出码：0 = 已是最新，不必重启；10 = 需要发布；1 = 检查本身出错（服务没起等）。
[CmdletBinding()]
param(
    [string]$Repo = '',
    [string]$RunDir = "$env:USERPROFILE\.pieqi\bin",
    [int]$Port = 3000
)

$ErrorActionPreference = 'Stop'
if (-not $Repo) { $Repo = Split-Path -Parent $PSScriptRoot }
$base = "http://127.0.0.1:$Port"
$exe = Join-Path $RunDir 'pieqi.exe'

$reasons = New-Object System.Collections.Generic.List[string]
$evidence = [ordered]@{}

# --- 0) 服务在不在（不在就没有"白白重启"的问题，直接发版） ----------------
$alive = $false
try {
    Invoke-RestMethod -Method Get -Uri "$base/api/admin/restart" -TimeoutSec 3 | Out-Null
    $alive = $true
} catch {
    $reasons.Add('服务未在 :' + $Port + ' 应答 ⇒ 需要（重新）拉起')
}
$evidence['service_alive'] = $alive

if ($alive) {
    # --- ③ 日志行号对齐（最硬） --------------------------------------------
    # 取当前源码里 main.go 的启动日志行号，与日志里 `pieqi starting` 那行的 main.go:NNN 比。
    $srcLine = $null
    $srcMatch = Select-String -Path (Join-Path $Repo 'cmd\pieqi\main.go') -Pattern '"pieqi starting"' -SimpleMatch
    if ($srcMatch) { $srcLine = $srcMatch[0].LineNumber }

    $logDir = Join-Path (Split-Path -Parent $RunDir) 'logs'
    $log = Join-Path $logDir ('pieqi-{0}.log' -f (Get-Date -Format 'yyyy-MM-dd'))
    $runLine = $null
    if (Test-Path $log) {
        $last = Select-String -Path $log -Pattern 'pieqi starting' | Select-Object -Last 1
        if ($last -and $last.Line -match 'main\.go:(\d+)') { $runLine = [int]$Matches[1] }
    }
    $evidence['main_go_line_source'] = $srcLine
    $evidence['main_go_line_running'] = $runLine
    if ($srcLine -and $runLine -and $srcLine -ne $runLine) {
        $reasons.Add("跑的不是当前源码（main.go:${runLine} ≠ 源码 ${srcLine}）")
    } elseif ($null -eq $runLine) {
        $reasons.Add('日志里没有 pieqi starting 行 ⇒ 无法确认版本，按需要发布处理')
    }

    # --- ④ 前端资源 hash ---------------------------------------------------
    $distIdx = Get-ChildItem (Join-Path $Repo 'web\dist\assets') -Filter 'index-*.js' -ErrorAction SilentlyContinue |
        Select-Object -First 1
    $servedIdx = $null
    try {
        $html = (Invoke-WebRequest -Uri "$base/" -TimeoutSec 5 -UseBasicParsing).Content
        if ($html -match 'assets/(index-[A-Za-z0-9_-]+\.js)') { $servedIdx = $Matches[1] }
    } catch { }
    $evidence['index_dist'] = if ($distIdx) { $distIdx.Name } else { $null }
    $evidence['index_served'] = $servedIdx
    if ($distIdx -and $servedIdx -and $distIdx.Name -ne $servedIdx) {
        $reasons.Add("前端未更新（服务 ${servedIdx} ≠ dist $($distIdx.Name)）")
    }

    # --- ① 工作树 ---------------------------------------------------------
    Push-Location $Repo
    try {
        $dirty = @(git status --short 2>$null | Where-Object { $_ -notmatch '^\?\?' })
    } finally { Pop-Location }
    $evidence['dirty_tracked'] = $dirty.Count
    if ($dirty.Count -gt 0) { $reasons.Add("有未提交的已跟踪改动（$($dirty.Count) 个）") }

    # --- ② 运行副本时间戳 -------------------------------------------------
    $exeTime = if (Test-Path $exe) { (Get-Item $exe).LastWriteTime } else { $null }
    $evidence['exe_mtime'] = $exeTime
    if (-not $exeTime) {
        $reasons.Add("运行副本不存在: $exe")
    } else {
        Push-Location $Repo
        try { $lastCommit = git log -1 --format=%cI 2>$null } finally { Pop-Location }
        if ($lastCommit) {
            $commitTime = [datetime]::Parse($lastCommit)
            $evidence['last_commit'] = $commitTime
            if ($exeTime -lt $commitTime) {
                $reasons.Add("运行副本早于最后一次提交（$($exeTime.ToString('HH:mm:ss')) < $($commitTime.ToString('HH:mm:ss'))）")
            }
        }
    }

    # --- 在途任务：重启会打断它们（会被交接单接回，但仍值得知道） -----------
    try {
        $tasks = (Invoke-RestMethod -Method Get -Uri "$base/api/tasks" -TimeoutSec 10).projects |
            ForEach-Object { $_.tasks } | Where-Object { $_.status -in 'running', 'waiting_input' }
        $evidence['inflight_tasks'] = @($tasks).Count
    } catch { $evidence['inflight_tasks'] = $null }
}

Write-Host ''
Write-Host '=== 发布前分诊 ==='
foreach ($k in $evidence.Keys) { Write-Host ("  {0,-20} {1}" -f $k, $evidence[$k]) }
Write-Host ''

if ($reasons.Count -eq 0) {
    Write-Host '判定：已是最新，**不需要重启**（重启只会白杀一个健康实例）'
    exit 0
}

Write-Host '判定：需要发布，原因：'
$reasons | ForEach-Object { Write-Host "  - $_" }
Write-Host ''
Write-Host '下一步：powershell -NoProfile -ExecutionPolicy Bypass -File build\release.ps1'
exit 10
