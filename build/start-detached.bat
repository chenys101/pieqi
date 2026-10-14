@echo off
REM ===================================================================
REM  start-detached.bat - run restart.bat in a NEW console window.
REM
REM  Why: restart.bat ends with "%EXE%" (foreground), so pieqi becomes a
REM  CHILD of the batch file. Launched from an AI coding session, the whole
REM  chain (pieqi.exe <- cmd.exe <- ... session tree) gets reclaimed when the
REM  session's turn ends - observed many times, with no "shutdown" line in
REM  the log and port 3000 silently going away.
REM
REM  "start" opens a NEW window, so the new process ends up parented by
REM  Explorer (explorer.exe -> cmd.exe -> pieqi.exe) and is NOT part of the
REM  session process tree. It survives the session ending.
REM
REM  All the real work (kill :3000 -> install build\pieqi.new.exe -> copy the
REM  artifact OUT of the Low-labelled repo -> launch from
REM  %USERPROFILE%\.pieqi\bin) lives in restart.bat. This file only wraps it,
REM  so there is exactly ONE copy of that logic to keep correct.
REM
REM  Usage:  double-click it, or (from a session)
REM          explorer.exe "D:\code\go\pieqi\start-detached.bat"
REM          explorer.exe returns rc=1 normally - do NOT judge success by rc,
REM          check whether port 3000 is LISTENING.
REM
REM  !! KEEP THIS FILE ASCII-ONLY !!  cmd.exe on a Chinese Windows reads .bat
REM  as GBK, not UTF-8; non-ASCII bytes inside a REM turn into mojibake and
REM  get EXECUTED as commands.
REM ===================================================================
setlocal
REM This script lives in <repo>\build\; restart.bat is its sibling, so %~dp0
REM (which is "...\build\") already points at the right directory -- no repo-root
REM maths needed here. (restart.bat, by contrast, DOES need the parent, because
REM it uses %REPO% to find go.mod / config.yaml / build\.)
set "HERE=%~dp0"
REM The first quoted argument of "start" is the window TITLE (do not drop it).
start "Pieqi (restart)" "%HERE%restart.bat"
exit /b 0
