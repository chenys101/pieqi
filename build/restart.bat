@echo off
title Pieqi (restart)
setlocal

REM Repo root. This script lives in <repo>\build\, so the repo is its PARENT --
REM %~dp0 is "...\build\" and always ends with a backslash, hence "%~dp0..".
REM Getting this wrong is silent and expensive: STAGEDIR would become
REM "<repo>\build\build" and the guard below would refuse to start anything.
for %%I in ("%~dp0..") do set "REPO=%%~fI\"

REM ---------------------------------------------------------------------------
REM TWO DIRECTORIES, AND THEY MUST STAY DIFFERENT  (root-caused 2026-10-14)
REM
REM   %REPO%build\                STAGING  - the agent compiles pieqi.new.exe here
REM   %USERPROFILE%\.pieqi\bin\   RUNTIME  - the copy that is actually launched
REM
REM WHY THE RUNTIME MAY NOT LIVE IN THE REPO -- and it is NOT about who launches
REM it (the 2026-10-18 note that used to sit here got this wrong):
REM
REM   The repo directory itself carries a mandatory integrity label:
REM       icacls "D:\code\go\pieqi"
REM         -> Mandatory Label\Low Mandatory Level:(OI)(CI)(NW)
REM   (D:\code\go and D:\ are normal/Medium -- only the project dir is a Low
REM   island, put there by the sandbox that provisioned it.)
REM
REM   Windows sets a new process' integrity level to the LOWER of the parent
REM   token and the IMAGE FILE's mandatory label. The Low label is inherited by
REM   every file created inside the repo, so build\pieqi.exe is tagged Low, and
REM   launching it yields a LOW-integrity process no matter who launched it
REM   (Explorer double-click, svchost, scheduled task -- all identical).
REM
REM   A Low process can still READ ~/.pieqi (read-down is allowed) but every
REM   write up to Medium is denied:
REM       open C:\Users\<user>\.pieqi\tasks\<id>.json.tmp.<pid>: Access is denied
REM   -> POST /api/tasks answers 500, and the service writes NO log file at all
REM      (the file sink degrades to console-only, silently).
REM
REM   Relabelling an in-repo artifact does not help: a non-elevated Medium
REM   process cannot raise the label (icacls /setintegritylevel -> Access is
REM   denied; it needs WRITE_OWNER). COPYING is what works -- "copy" creates a
REM   brand new file, so the Low label is dropped and the copy picks up the
REM   destination directory's Medium label. Verified with icacls:
REM       build\pieqi.exe                 -> Low
REM       ~/.pieqi\bin\pieqi.exe (a copy) -> Medium
REM
REM DISCIPLINE: never launch anything that lives in %REPO%build.
REM   Do NOT use xcopy/robocopy to install the runtime copy -- they can carry
REM   the source SACL (and therefore the Low label) over. Plain "copy" only.
REM
REM NOTE: this file must stay pure ASCII. cmd.exe parses it using the OEM code
REM   page (936 here), so UTF-8 Chinese comments without a BOM get torn into
REM   fragments and executed as commands.
REM ---------------------------------------------------------------------------
REM STAGEDIR = where the compiled artifact (pieqi.new.exe) is delivered.
REM   Repo layout   : <repo>\build\           (this script lives there too)
REM   Release layout: <unpacked release>\     (no build\ subdir; pieqi.exe is flat)
REM Detect by presence of the repo's go.mod, so one script serves both layouts
REM without the user editing anything.
set "RUNDIR=%USERPROFILE%\.pieqi\bin"
set "EXE=%RUNDIR%\pieqi.exe"

if exist "%REPO%go.mod" (
  set "STAGEDIR=%REPO%build"
) else (
  set "STAGEDIR=%REPO:~0,-1%"
)

echo Restarting Pieqi...
echo   staging : %STAGEDIR%
echo   runtime : %RUNDIR%
echo.

if not exist "%STAGEDIR%" mkdir "%STAGEDIR%"
if not exist "%RUNDIR%" mkdir "%RUNDIR%"

REM 0) Make sure there is something to install. In the repo layout the artifact is
REM    build\pieqi.exe (produced by a build, or shipped by a release). In the
REM    release layout it is pieqi.exe next to this script. Normalise to
REM    "%STAGEDIR%\pieqi.exe" so the rest of the script stays layout-agnostic.
if not exist "%STAGEDIR%\pieqi.exe" (
  if exist "%REPO%pieqi.exe" (
    echo adopting %REPO%pieqi.exe as the artifact
    copy /Y "%REPO%pieqi.exe" "%STAGEDIR%\pieqi.exe" >nul
  ) else if exist "%REPO%pieqi-windows-amd64.exe" (
    echo adopting %REPO%pieqi-windows-amd64.exe as the artifact
    copy /Y "%REPO%pieqi-windows-amd64.exe" "%STAGEDIR%\pieqi.exe" >nul
  )
)

REM 1) Kill the old process holding port 3000.
REM    MUST happen BEFORE touching the exe: Windows cannot overwrite a running
REM    exe.
for /f "tokens=5" %%a in ('netstat -ano -p TCP ^| findstr ":3000 " ^| findstr "LISTENING"') do (
  echo killing old process PID=%%a
  taskkill /F /PID %%a >nul 2>&1
)

REM 2) Give the OS a moment to release the exe file lock (~2s).
ping -n 3 127.0.0.1 >nul

REM 3) The in-repo staging area holds EXACTLY ONE file: pieqi.new.exe (the
REM    delivery). There is deliberately NO copy step and NO .old backup here.
REM
REM    Why (2026-10-14 correction): ".new" and ".old" are RUNTIME staging --
REM    they only make sense next to the binary that is actually executing, and
REM    the service's own self-restart already puts them there, exactly once:
REM        internal/core/selfrestart.go:168   backup := s.execPath + ".old"
REM        internal/core/selfrestart.go:484   failed := s.execPath + ".failed"
REM    i.e. %USERPROFILE%\.pieqi\bin\pieqi.exe.old / .failed.
REM
REM    An earlier version of this script ALSO produced <repo>\build\pieqi.exe
REM    and build\pieqi.exe.old. That duplicated the rollback backup in two
REM    places with two writers -- so "which one is the real rollback?" had no
REM    single answer, and build/ accumulated binaries nobody could attribute.
REM    Keep build/ = delivery only (one file); runtime state lives with the
REM    running binary.
REM
REM    Do NOT "restore" the removed copy step: pieqi.exe.old under
REM    %USERPROFILE%\.pieqi\bin is the ONLY rollback artifact, and it is
REM    written by the service, not by this script. (This script does still make
REM    a pre-install safety copy at step 4 -- see the note there.)
if not exist "%STAGEDIR%\pieqi.new.exe" (
  echo no pieqi.new.exe found in %STAGEDIR%
  echo Build it first:   go build -o build/pieqi.new.exe ./cmd/pieqi
  echo (For the release layout: put pieqi.exe next to this script.)
  REM Fall back to a shipped binary in the release layout, so a release package
  REM still runs. We copy it to a TEMPORARY name only to feed the install path
  REM below, then remove it -- build/ must end up with just pieqi.new.exe.
  if exist "%REPO%pieqi.exe" (
    echo adopting %REPO%pieqi.exe as the artifact
    copy /Y "%REPO%pieqi.exe" "%STAGEDIR%\pieqi.new.exe" >nul
  ) else if exist "%REPO%pieqi-windows-amd64.exe" (
    echo adopting %REPO%pieqi-windows-amd64.exe as the artifact
    copy /Y "%REPO%pieqi-windows-amd64.exe" "%STAGEDIR%\pieqi.new.exe" >nul
  )
)

if not exist "%STAGEDIR%\pieqi.new.exe" (
  echo ERROR: nothing to install: "%STAGEDIR%\pieqi.new.exe" not found.
  pause
  exit /b 1
)

REM 4) THE KEY STEP: copy the artifact OUT of the Low-labelled repo.
REM    Plain "copy" (not xcopy/robocopy) so no SACL travels with it.
REM
REM    Safety copy before overwriting the runtime binary: this is a
REM    HUMAN-initiated pre-install snapshot, and it intentionally uses the SAME
REM    name the service uses (.old) so that only one rollback file exists and it
REM    always means "the version that ran before the most recent swap".
if exist "%EXE%" copy /Y "%EXE%" "%EXE%.old" >nul
copy /Y "%STAGEDIR%\pieqi.new.exe" "%EXE%" >nul
if errorlevel 1 (
  echo ERROR: could not copy the artifact to "%EXE%"
  pause
  exit /b 1
)

REM Consume the delivery: build/ must be left holding nothing (see step 3).
del /Q "%STAGEDIR%\pieqi.new.exe" >nul 2>&1

REM 5) Guard: refuse to launch a Low-integrity image -- that IS the failure
REM    this script exists to prevent. (icacls prints the label in ASCII even on
REM    a Chinese Windows: "Mandatory Label\Low Mandatory Level".)
icacls "%EXE%" | findstr /C:"Low Mandatory Level" >nul 2>&1
if not errorlevel 1 (
  echo ERROR: "%EXE%" carries a Low integrity label.
  echo        The process would be sandboxed and could not write the pieqi data root.
  echo        Aborting instead of starting a service that cannot save tasks.
  pause
  exit /b 1
)

echo.
echo Starting pieqi.exe from %RUNDIR% ...
echo.
cd /d "%RUNDIR%"
REM PIEQI_CONFIG is the ONLY thing the service needs to locate the repo: the
REM delivery (staging) directory is DERIVED from it by convention --
REM   dir(config.yaml) + \build
REM so there is deliberately NO staging variable to set here. Setting one would
REM be a stale knob the code ignores (that bug already happened once: the old
REM PIEQI_STAGING_DIR was set by this script while the code never read it).
REM Keep this in sync with cmd/pieqi/main.go resolveStagingDir.
set "PIEQI_CONFIG=%REPO%config.yaml"
"%EXE%"
pause
