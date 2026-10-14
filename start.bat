@echo off
REM Kept for backwards compatibility: identical to start-detached.bat.
REM The real logic lives in restart.bat (single source of truth).
setlocal
set "REPO=%~dp0"
start "Pieqi (restart)" "%REPO%restart.bat"
exit /b 0
