@echo off
title Pieqi (restart)
setlocal

REM Repo root (%~dp0 always ends with a backslash).
set "REPO=%~dp0"
REM The runtime copy MUST live OUTSIDE the repo dir.
REM An exe located inside the workspace is restricted to writing only the
REM workspace, so running "%REPO%pieqi.exe" makes pieqi fail to write
REM ~/.pieqi -> "open ...tasks\*.json.tmp.<pid>: Access is denied" -> task
REM create returns 500. Copy it out and run that copy instead.
set "RUNDIR=%USERPROFILE%\.pieqi\bin"

echo Restarting Pieqi...
echo.

REM 1) Kill the old process holding port 3000 (go run temp exe or pieqi.exe)
REM    MUST happen BEFORE the copy: Windows cannot overwrite a running exe.
for /f "tokens=5" %%a in ('netstat -ano -p TCP ^| findstr ":3000 " ^| findstr "LISTENING"') do (
  echo killing old process PID=%%a
  taskkill /F /PID %%a >nul 2>&1
)

REM 2) Give the OS a moment to release the exe file lock (~2s).
ping -n 3 127.0.0.1 >nul

REM 3) Refresh the runtime copy from the freshly built repo binary.
if not exist "%RUNDIR%" mkdir "%RUNDIR%"
copy /Y "%REPO%pieqi.exe" "%RUNDIR%\pieqi.exe" >nul && echo copied new build || echo WARN: copy failed, using existing build

echo.
echo Starting pieqi.exe from %RUNDIR% ...
echo.
cd /d "%RUNDIR%"
set "PIEQI_CONFIG=%REPO%config.yaml"
pieqi.exe
pause
