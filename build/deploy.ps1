# 投递 + 自重启（Windows 版 deploy-dev.sh）。
#
#   build/pieqi.new.exe  --copy-->  <dataRoot>\bin\pieqi.new.exe
#   POST /api/admin/restart  ->  服务自己按"有无产物"决定 swap-and-restart / restart-only
#
# 为什么投递位是 <dataRoot>\bin\pieqi.new.exe 而不是直接覆盖 pieqi.exe：
#   运行中的 exe 被进程锁定，Windows 不允许覆盖。服务端收到 restart 后自己把运行中的
#   exe 改名（Windows 允许）再落新文件，所以这里只负责"把产物放到它认得的交付位"。
#   判据与 restart.bat 逐字对齐：有 pieqi.new.exe 就换版，没有就只重启。
#
# 为什么必须 Copy-Item（不是 Move-Item）：仓库目录带 Low 完整性标签，build/ 里的文件
#   都继承它；新进程完整性 = min(父令牌, 镜像文件标签)，跑 Low 镜像的进程写不了
#   ~/.pieqi（创建任务 500、日志零增长）。Copy 会让新文件继承目标目录的 Medium。
#   同理不要用 xcopy/robocopy（可能连 SACL 一起搬）。
#
# 发起者会话 id：agent 在 pieqi 里跑时拿得到 PIEQI_TASK_ID，人类手动跑时为空 —— 正常。
#   它让交接单知道"是谁发起的重启"，从而把这个会话接回来（ADR-0009）。
[CmdletBinding()]
param(
    [string]$Repo = '',
    [string]$RunDir = "$env:USERPROFILE\.pieqi\bin",
    [int]$Port = 3000,
    [int]$ReadyTimeoutSec = 60
)

$ErrorActionPreference = 'Stop'
# 同 release.ps1：$PSScriptRoot 在 param 默认值里是空的，必须进脚本体再解析。
if (-not $Repo) { $Repo = Split-Path -Parent $PSScriptRoot }
$artifact = Join-Path $Repo 'build\pieqi.new.exe'
$base = "http://127.0.0.1:$Port"

if (-not (Test-Path $artifact) -or (Get-Item $artifact).Length -eq 0) {
    Write-Error "没有可投放的产物（或为零字节）: $artifact`n先跑 build\release.bat /no-deploy"
    exit 1
}

# ---- 步骤 2：投放 ---------------------------------------------------------
if (-not (Test-Path $RunDir)) { New-Item -ItemType Directory -Force $RunDir | Out-Null }
$staged = Join-Path $RunDir 'pieqi.new.exe'
Copy-Item -Force $artifact $staged
Write-Host ("==> [2] 已投放 -> {0} ({1} bytes)" -f $staged, (Get-Item $staged).Length)

# ---- 步骤 3：触发自重启 ------------------------------------------------
Write-Host "==> [3] POST $base/api/admin/restart"
$headers = @{}
if ($env:PIEQI_TASK_ID) { $headers['X-Pieqi-Task-Id'] = $env:PIEQI_TASK_ID }

# 重启编排在后台跑：这个请求返回 202 之后进程随时可能退场，所以**不能**重试/长等。
try {
    $resp = Invoke-RestMethod -Method Post -Uri "$base/api/admin/restart" -Headers $headers `
        -NoProxy -TimeoutSec 60
} catch {
    Write-Error "POST 失败（服务没在 $base 上监听？）: $($_.Exception.Message)"
    exit 1
}
$resp | ConvertTo-Json -Compress | Write-Host

if ($resp.swapped -eq $true) {
    Write-Host '==> 已受理：swap-and-restart（换版并重启）'
} elseif ($resp.swapped -eq $false) {
    Write-Host '==> 已受理：restart-only（未换版）—— 检查运行位是不是 $RunDir'
} else {
    Write-Host '==> 已受理（响应未带 swapped，需人工核对）'
}

# ---- 等待新实例并就绪校验 ------------------------------------------------
# 判据不能用"进程活着"：旧实例还在等新实例报到。用端口 + 接口真的答话。
#
# ⚠️ 必须**先看到服务至少挂一次**再去等它回来（2026-10-23 实测踩到）：
#   旧实例受理 restart 后不是立刻退场 —— 它要等新实例 bind 成功才放手（2026-10-13 的
#   死锁事故就出在这段时序里）。若直接从"现在"开始轮询，第一次 GET 会打到**正在退场
#   的旧实例**并立刻成功，于是脚本报"就绪"、实际换版还没完成，紧接着进程消失，
#   调用方（agent 会话）在轮末被一起回收。
#   所以：先等它真的答不上话（或端口真的不在了），再等它答得上。
Write-Host "==> 等待旧实例退场 ..."
$goneDeadline = (Get-Date).AddSeconds(30)
$sawDown = $false
while ((Get-Date) -lt $goneDeadline) {
    $listening = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue).Count -gt 0
    if (-not $listening) { $sawDown = $true; break }
    try {
        Invoke-RestMethod -Method Get -Uri "$base/api/admin/restart" -NoProxy -TimeoutSec 1 | Out-Null
    } catch {
        $sawDown = $true
        break
    }
    Start-Sleep -Milliseconds 300
}
if ($sawDown) {
    Write-Host '    旧实例已退场'
} else {
    Write-Host '    未观察到退场（可能重启尚未开始，或服务没接住请求）'
}

Write-Host "==> 等待新实例在 :$Port 就绪 ..."
$deadline = (Get-Date).AddSeconds($ReadyTimeoutSec)
while ((Get-Date) -lt $deadline) {
    Start-Sleep -Seconds 1
    try {
        $st = Invoke-RestMethod -Method Get -Uri "$base/api/admin/restart" -NoProxy -TimeoutSec 3
        Write-Host '==> 新实例就绪'
        $st | ConvertTo-Json -Compress | Write-Host
        # staged 必须回到 false：服务换完二进制会比对摘要并清理已消费的产物。
        # 仍是 true ⇒ 换版没生效，别让调用方以为发完了。
        if ($st.staged -eq $true) {
            Write-Host '    ⚠️ staged 仍为 true —— 产物未被消费，换版可能没生效'
            exit 1
        }
        exit 0
    } catch {
        # 还没起来，继续等
    }
}

Write-Error "WARN: ${ReadyTimeoutSec}s 内未就绪。检查 $RunDir\..\logs\ 与 $RunDir\pieqi.exe.failed"
exit 1
