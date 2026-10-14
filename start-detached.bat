@echo off
REM ===================================================================
REM  FORWARDER - the real script lives in build\start-detached.bat
REM
REM  Kept at the repo root so existing muscle memory and docs keep working
REM  after the 2026-10-14 move of all maintenance scripts into build\
REM  (git-tracked for scripts, artifacts ignored -- see .gitignore).
REM
REM  !! KEEP THIS FILE ASCII-ONLY !!  cmd.exe on a Chinese Windows reads .bat
REM  as GBK, not UTF-8; non-ASCII bytes inside a REM become mojibake and get
REM  EXECUTED as commands.
REM ===================================================================
setlocal
for %%I in ("%~dp0") do set "REPO=%%~fI\"
start "Pieqi (restart)" "%REPO%build\start-detached.bat"
exit /b 0