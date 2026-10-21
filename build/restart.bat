@echo off
title Pieqi (restart)
setlocal EnableExtensions

REM ---------------------------------------------------------------------------
REM WHAT THIS SCRIPT DOES NOW (2026-10-21, user request)
REM
REM   If a freshly compiled artifact exists   -> swap it in, then start.
REM       repo layout   : build\pieqi.new.exe
REM       release layout: pieqi.exe next to this script
REM   If there is NO new artifact             -> just restart the service.
REM       The runtime exe is left untouched. Nothing is copied, nothing is deleted.
REM
REM WHY "restart only" had to be added:
REM   The old version dropped into "ERROR: nothing to install" whenever
REM   build\pieqi.new.exe was absent (e.g. the service is hung and there is
REM   nothing new to deploy). That made "just restart it" impossible from any
REM   entry point -- the API refused with 409 as well. The rule is now:
REM   the presence of the artifact decides the flow, never the caller.
REM
REM TOPOLOGY (unchanged, root-caused 2026-10-14):
REM
REM   %REPO%build\                STAGING  - the agent compiles pieqi.new.exe here
REM   %USERPROFILE%\.pieqi\bin\   RUNTIME  - the copy that is actually launched
REM
REM   The repo directory carries a mandatory Low integrity label:
REM       icacls "D:\code\go\pieqi"
REM         -> Mandatory Label\Low Mandatory Level:(OI)(CI)(NW)
REM   Every file created inside inherits it. Windows sets a new process'
REM   integrity to the LOWER of the parent token and the IMAGE FILE's label, so
REM   an in-repo exe is always a LOW process -- it can READ ~/.pieqi but every
REM   write up to Medium is denied (POST /api/tasks -> 500, and no log file is
REM   written at all). COPYING drops the Low label (the copy picks up the
REM   destination directory's Medium label); "move" would carry it over.
REM   Plain "copy" only -- never xcopy/robocopy (they can carry the source SACL).
REM
REM NOTE: this file must stay pure ASCII. cmd.exe parses it using the OEM code
REM   page (936 here), so UTF-8 Chinese comments without a BOM get torn into
REM   fragments and executed as commands.
REM ---------------------------------------------------------------------------

REM Repo root. This script lives in <repo>\build\, so the repo is its PARENT --
REM %~dp0 is "...\build\" and always ends with a backslash, hence "%~dp0..".
REM Getting this wrong is silent and expensive: STAGEDIR would become
REM "<repo>\build\build" and every check below would look at the wrong place.
for %%I in ("%~dp0..") do set "REPO=%%~fI\"

REM STAGEDIR = where the compiled artifact is delivered.
REM   Repo layout   : <repo>\build\           (this script lives there too)
REM   Release layout: <unpacked release>\     (no build\ subdir; pieqi.exe is flat)
REM Detect by presence of the repo's go.mod, so one script serves both layouts.
set "RUNDIR=%USERPROFILE%\.pieqi\bin"
set "EXE=%RUNDIR%\pieqi.exe"

if exist "%REPO%go.mod" (
  set "STAGEDIR=%REPO%build"
  set "ARTIFACT=%REPO%build\pieqi.new.exe"
) else (
  set "STAGEDIR=%REPO:~0,-1%"
  REM Release layout: the shipped runtime exe sitting next to this script is the
  REM artifact to install (a release has already been "compiled").
  set "ARTIFACT=%STAGEDIR%\pieqi.exe"
)

if not exist "%RUNDIR%" mkdir "%RUNDIR%"

REM Decide the flow ONCE, up front, so both the printout and the actions agree.
set "MODE=restart-only"
if exist "%ARTIFACT%" set "MODE=swap-and-restart"
REM A zero-byte artifact is the debris of an interrupted build; installing it
REM would replace a working service with an empty file. Treat it as "missing".
if exist "%ARTIFACT%" (
  for %%F in ("%ARTIFACT%") do if %%~zF EQU 0 set "MODE=restart-only"
)

echo Restarting Pieqi...
echo   mode    : %MODE%
if /I "%MODE%"=="swap-and-restart" echo   artifact: %ARTIFACT%
echo   runtime : %RUNDIR%
echo.

REM ---------------------------------------------------------------------------
REM STEP 1 -- stop the instance that holds the port.
REM
REM MUST happen before any file work: Windows cannot overwrite a running exe.
REM In "restart only" mode it is still required -- that is the whole point.
REM We kill by the PID that LISTENs on :3000 (not by image name) so that we never
REM touch an unrelated pieqi.exe (e.g. a release sitting in another folder).
REM ---------------------------------------------------------------------------
set "KILLED=0"
for /f "tokens=5" %%a in ('netstat -ano -p TCP ^| findstr ":3000 " ^| findstr "LISTENING"') do (
  echo stopping old instance PID=%%a
  taskkill /F /PID %%a >nul 2>&1
  if not errorlevel 1 set "KILLED=1"
)

REM GOTO, not an IF/ELSE block: a literal "(starting fresh)" inside an echo
REM inside parentheses still closes the block early -- the "^(" escape only
REM works outside blocks, and a stray ")" there makes cmd die with
REM "was unexpected at this time". Labels are immune to that class of bug.
if not "%KILLED%"=="1" goto not_running
REM Give the OS a moment to release the exe file lock (~2s).
ping -n 3 127.0.0.1 >nul
goto after_kill
:not_running
echo no running instance found on :3000 -- starting fresh
:after_kill

REM ---------------------------------------------------------------------------
REM STEP 2 -- install the artifact, but ONLY in swap mode.
REM
REM "restart only" deliberately skips this whole block: no copy, no .old
REM snapshot, no deletion. The exe that was already running is the exe that
REM starts again -- that is what "just restart" means to the caller.
REM
REM Written with GOTO labels instead of IF/ELSE blocks on purpose: this file
REM has already been bitten by a literal ")" inside an echo inside a block
REM (the "^(" escape does not help there, cmd reports "was unexpected at this
REM time"). Labels have no such failure mode and read top-to-bottom.
REM ---------------------------------------------------------------------------
if /I not "%MODE%"=="swap-and-restart" goto restart_only

REM Safety copy before overwriting: a HUMAN-initiated pre-install snapshot.
REM It intentionally reuses the service's own ".old" name so that only ONE
REM rollback file exists and it always means "the version that ran before the
REM most recent swap". The runtime state (.old/.failed) lives next to the
REM running binary -- never inside build\ (see the 2026-10-14 correction).
if exist "%EXE%" copy /Y "%EXE%" "%EXE%.old" >nul
copy /Y "%ARTIFACT%" "%EXE%" >nul
if errorlevel 1 goto copy_failed
echo installed %ARTIFACT% --^> %EXE%
REM Consume the delivery: build\ goes back to "clean, nothing pending".
REM (Only in repo layout -- in release layout ARTIFACT *is* the source file.)
if exist "%REPO%go.mod" del /Q "%ARTIFACT%" >nul 2>&1
goto after_install

:copy_failed
echo ERROR: could not copy the artifact to "%EXE%"
pause
exit /b 1

:restart_only
if exist "%EXE%" goto keep_binary
echo ERROR: no artifact to install AND no runtime copy at "%EXE%".
echo        Compile one first:  go build -o build/pieqi.new.exe ./cmd/pieqi
pause
exit /b 1

:keep_binary
echo keeping the current runtime binary -- no new artifact to install

:after_install

REM ---------------------------------------------------------------------------
REM STEP 3 -- guard: refuse to launch a Low-integrity image. That IS the
REM failure this script exists to prevent (see the header). icacls prints the
REM label in ASCII even on a Chinese Windows.
REM ---------------------------------------------------------------------------
icacls "%EXE%" | findstr /C:"Low Mandatory Level" >nul 2>&1
if not errorlevel 1 (
  echo ERROR: "%EXE%" carries a Low integrity label.
  echo        The process would be sandboxed and could not write the pieqi data root.
  echo        Aborting instead of starting a service that cannot save tasks.
  pause
  exit /b 1
)

REM ---------------------------------------------------------------------------
REM STEP 4 -- start it.
REM
REM PIEQI_CONFIG is the ONLY thing the service needs to locate the repo: the
REM delivery (staging) directory is DERIVED from it by convention --
REM   dir(config.yaml) + \build
REM so there is deliberately NO staging variable to set here. Setting one would
REM be a stale knob the code ignores (the old PIEQI_STAGING_DIR did exactly that).
REM Keep this in sync with cmd/pieqi/main.go resolveStagingDir.
REM
REM Start it DETACHED (explorer.exe): a process started directly by this
REM console dies with the window. Going through Explorer puts the new instance
REM under a normal (Medium) parent, which is what we want anyway.
REM ---------------------------------------------------------------------------
echo.
echo Starting pieqi.exe from %RUNDIR% ...
echo.
cd /d "%RUNDIR%"
set "PIEQI_CONFIG=%REPO%config.yaml"
start "" "%EXE%"

REM ---------------------------------------------------------------------------
REM Wait for the port, then summarise.
REM
REM Loop discipline in plain cmd:
REM   * "%WAITED%" can NOT be used as the loop condition -- `%VAR%` in a block or
REM     goto-loop is expanded once, so "if %WAITED% lss 30" is frozen at its
REM     first value (measured: infinitely loops or exits on pass 2).
REM   * `%TIME%` numbers carry leading spaces (" 9:13:05.78"), which break
REM     `set /a`. The FOR/F trick below re-splits and re-pads them, so each
REM     iteration reads the *current* wall clock. 30 s budget: bind retry is 10s.
REM ---------------------------------------------------------------------------
set "T0="
for /f "tokens=1-4 delims=:., " %%a in ("%TIME%") do set /a T0=%%a*3600+%%b*60+%%c
:waitfor
ping -n 2 127.0.0.1 >nul
netstat -ano -p TCP | findstr ":3000 " | findstr "LISTENING" >nul
if not errorlevel 1 goto ready
set "T1="
for /f "tokens=1-4 delims=:., " %%a in ("%TIME%") do set /a T1=%%a*3600+%%b*60+%%c
set /a ELAPSED=T1-T0
REM Midnight rollover makes ELAPSED negative; treat that as "keep waiting once".
if %ELAPSED% lss 0 set /a ELAPSED=0
if %ELAPSED% lss 30 goto waitfor

echo WARNING: :3000 is still not listening after %ELAPSED%s.
echo          Check %USERPROFILE%\.pieqi\logs\ and the pieqi.log next to the runtime copy.
pause
exit /b 1

:ready
echo.
echo Pieqi is up on :3000 -- mode=%MODE%
if /I not "%MODE%"=="swap-and-restart" goto ready_no_swap
echo Rollback copy kept at "%EXE%.old".
goto ready_end

:ready_no_swap
echo Binary unchanged -- this was a plain restart.

:ready_end
echo.
echo This window closes in 5 seconds.
ping -n 6 127.0.0.1 >nul
exit /b 0
