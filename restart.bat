@echo off
title Pieqi (restart)
setlocal

REM Repo root (%~dp0 always ends with a backslash).
set "REPO=%~dp0"

REM ── 运行落点（2026-10-14 起）────────────────────────────────────────────────
REM 运行产物与编译产物**同在 build\ 下**，由编译规范保证：
REM     build\pieqi.exe      运行产物（本脚本启动它）
REM     build\pieqi.new.exe  编译产物（agent/人编译时只写这个）
REM 编译规范：go build -o build/pieqi.new.exe ./cmd/pieqi
REM
REM 为什么不再复制到 %USERPROFILE%\.pieqi\bin：
REM   旧布局把运行副本放工作区外，理由是「工作区内的 exe 会被沙箱限制、写不了
REM   ~/.pieqi」。但那条限制取决于**谁启动它**（受限令牌只在 dsh/agent 会话里），
REM   不取决于路径。本脚本与计划任务都由 svchost / Explorer 派生，不受限，
REM   所以 exe 放在工作区内的 build\ 是安全的。落点统一到一处后，就不再有
REM   「计划任务跑 A、手工重启跑 B」这种两份二进制不一致的状态。
REM
REM ⚠️ 纪律：build\pieqi.exe **只能由 svchost（计划任务）或 Explorer（双击）启动**。
REM    绝不要在 dsh / agent 会话里直接运行它 —— 那会继承受限令牌，症状是
REM    「创建任务返回 500：open ...tasks\*.json.tmp.<pid>: Access is denied」。
set "RUNDIR=%REPO%build"
set "EXE=%RUNDIR%\pieqi.exe"

echo Restarting Pieqi...
echo.

REM 确保 build\ 存在（首次在新布局下重启时可能还没有）。
if not exist "%RUNDIR%" mkdir "%RUNDIR%"

REM 1) Kill the old process holding port 3000 (go run temp exe or pieqi.exe).
REM    MUST happen BEFORE touching the exe: Windows cannot overwrite a running exe.
for /f "tokens=5" %%a in ('netstat -ano -p TCP ^| findstr ":3000 " ^| findstr "LISTENING"') do (
  echo killing old process PID=%%a
  taskkill /F /PID %%a >nul 2>&1
)

REM 2) Give the OS a moment to release the exe file lock (~2s).
ping -n 3 127.0.0.1 >nul

REM 3) 交付：把编译产物搬成运行产物。
REM    正常发版流程里这一步由**自重启**完成（它还会留 .old 备份）；本脚本是
REM    「自重启装载不了新逻辑」时的人工兜底，所以这里手工做等价的事：
REM    先备份现有运行产物，再搬新的。没有 .new 就沿用现有的（不报错）。
if exist "%RUNDIR%\pieqi.new.exe" (
  if exist "%EXE%" copy /Y "%EXE%" "%EXE%.old" >nul
  move /Y "%RUNDIR%\pieqi.new.exe" "%EXE%" >nul && echo installed new build from pieqi.new.exe || echo WARN: install failed, using existing build
) else (
  echo no pieqi.new.exe found; starting the existing build
)

if not exist "%EXE%" (
  echo ERROR: %EXE% not found. Build it first:
  echo     go build -o build/pieqi.new.exe ./cmd/pieqi
  pause
  exit /b 1
)

echo.
echo Starting pieqi.exe from %RUNDIR% ...
echo.
cd /d "%RUNDIR%"
set "PIEQI_CONFIG=%REPO%config.yaml"
"%EXE%"
pause
