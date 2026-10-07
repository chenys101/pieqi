@echo off
title Pieqi
setlocal

REM Repo root (%~dp0 always ends with a backslash).
set "REPO=%~dp0"
REM Run the copy OUTSIDE the repo: an exe inside the workspace can only write
REM the workspace, so "%REPO%pieqi.exe" cannot write ~/.pieqi (task create 500).
set "RUNDIR=%USERPROFILE%\.pieqi\bin"

echo Starting Pieqi...
echo.

if not exist "%RUNDIR%" mkdir "%RUNDIR%"
copy /Y "%REPO%pieqi.exe" "%RUNDIR%\pieqi.exe" >nul

cd /d "%RUNDIR%"
set "PIEQI_CONFIG=%REPO%config.yaml"
pieqi.exe
pause
