@echo off
REM ===================================================================
REM  FORWARDER - the real script lives in build\restart.bat
REM
REM  Kept at the repo root so existing muscle memory, docs and the scheduled
REM  task launcher keep working after the 2026-10-14 move of all maintenance
REM  scripts into build\ (git-tracked for scripts, artifacts ignored -- see
REM  .gitignore).
REM
REM  Why a forwarder instead of moving the file back: build\ is where delivery
REM  artifacts (pieqi.new.exe) land, so the script that consumes them belongs
REM  next to them. This root copy exists only so callers do not break.
REM
REM  !! KEEP THIS FILE ASCII-ONLY !!  cmd.exe on a Chinese Windows reads .bat
REM  as GBK, not UTF-8; non-ASCII bytes inside a REM become mojibake and get
REM  EXECUTED as commands.
REM ===================================================================
setlocal
for %%I in ("%~dp0") do set "REPO=%%~fI\"
start "Pieqi (restart)" "%REPO%build\restart.bat"
exit /b 0