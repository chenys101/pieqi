# 一键发布（Windows 版 build-dev.sh + deploy-dev.sh），可在 agent 会话内直接调用：
#
#     powershell -NoProfile -ExecutionPolicy Bypass -File build\release.ps1
#     powershell ... -File build\release.ps1 -NoDeploy     # 只编不发布
#
# 顺序：前端 build → 后端编译 → 投递 → 自重启。
#   * 前端必须先编：web/dist 是 //go:embed 进二进制的，反了就"改了没生效"且不报错。
#   * GOTMPDIR 必须指向**工作区内**的 build\.gotmp（见 docs/adr/0016 与 build\release.bat 头部）。
#   * 落点是 build/pieqi.new.exe：直接写 pieqi.exe 会让"回滚备份"保存刚编出来的那份。
#
# 为什么要有 -NoDeploy：调用方可能还没提交（"先提交、再编译、再重启"是硬纪律），
# 想先确认能编出来再决定要不要发版时用它。
[CmdletBinding()]
param(
    [string]$Repo = '',
    [switch]$NoDeploy
)

$ErrorActionPreference = 'Stop'
# $PSScriptRoot **不能**用在 param 默认值里：参数默认值在脚本作用域建立之前求值，
# 那时它还是空的（实测报 "Cannot bind argument to parameter 'Path' because it is an empty string"）。
$ScriptDir = $PSScriptRoot
if (-not $Repo) { $Repo = Split-Path -Parent $ScriptDir }
Set-Location $Repo

# ---- 1/3 前端 -------------------------------------------------------------
Write-Host '==> [1/3] frontend build'
Push-Location (Join-Path $Repo 'web')
try {
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "npm run build 失败（exit $LASTEXITCODE）" }
} finally {
    Pop-Location
}

# ---- 2/3 后端 -------------------------------------------------------------
Write-Host '==> [2/3] go build -> build\pieqi.new.exe'
$gotmp = Join-Path $Repo 'build\.gotmp'
if (-not (Test-Path $gotmp)) { New-Item -ItemType Directory -Force $gotmp | Out-Null }
$env:GOTMPDIR = $gotmp
$artifact = Join-Path $Repo 'build\pieqi.new.exe'
Remove-Item $artifact -Force -ErrorAction SilentlyContinue   # 免得把上次的产物当这次的成功
go build -o build/pieqi.new.exe ./cmd/pieqi
if ($LASTEXITCODE -ne 0) { throw "go build 失败（exit $LASTEXITCODE）" }

# 判据是**产物真的在且非零字节**，不是退出码：零字节产物会被下游当成"没有产物"。
if (-not (Test-Path $artifact) -or (Get-Item $artifact).Length -eq 0) {
    throw "build/pieqi.new.exe 不存在或为零字节 —— 构建未成功"
}
Write-Host ("    artifact: {0} bytes, {1}" -f (Get-Item $artifact).Length, (Get-Item $artifact).LastWriteTime)

# ---- 3/3 投递 + 自重启 ---------------------------------------------------
if ($NoDeploy) {
    Write-Host 'OK: -NoDeploy，仅构建；产物在 build\pieqi.new.exe'
    exit 0
}
Write-Host '==> [3/3] stage + self-restart'
& (Join-Path $PSScriptRoot 'deploy.ps1') -Repo $Repo
if ($LASTEXITCODE -ne 0) { throw "deploy 失败（exit $LASTEXITCODE）" }
Write-Host 'OK: release completed'
