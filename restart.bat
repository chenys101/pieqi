@echo off
title Pieqi (restart)
cd /d "G:\workspace\pieqi"

echo Restarting Pieqi...
echo.

REM 杀掉占用 3000 端口的旧进程（go run 临时 exe 或 pieqi.exe）
for /f "tokens=5" %%a in ('netstat -ano -p TCP ^| findstr ":3000 " ^| findstr "LISTENING"') do (
  echo killing old process PID=%%a
  taskkill /F /PID %%a >nul 2>&1
)

echo.
echo Starting go run ./cmd/pieqi ...
echo.
go run ./cmd/pieqi
pause
