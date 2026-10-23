@echo off
REM ===================================================================
REM  release.bat - ONE-SHOT local release, runnable from an agent session.
REM
REM      frontend build -> go build -> stage -> self-restart
REM
REM  Why this file exists (2026-10-23):
REM    The Go build inside an agent session fails with
REM        go: creating work dir: mkdir ...\Temp\go-buildNNN: Access is denied
REM    whenever GOTMPDIR points OUTSIDE the workspace - including %TEMP%
REM    itself and any fresh writable dir under it. Pointing GOTMPDIR at
REM    <repo>\build\.gotmp (inside the workspace) makes the build succeed.
REM    Measured, by presence of the artifact (not by exit code):
REM        GOTMPDIR=build\.gotmp -> built
REM        GOTMPDIR=%TEMP%\...   -> not built
REM        GOTMPDIR unset        -> not built
REM    Also: -o must write INSIDE the workspace; writing to %TEMP% fails with
REM    "copying ...: open %TEMP%\x.exe: Access is denied".
REM    See docs/adr/0016.
REM
REM  The restart is delegated to deploy.ps1 -> POST /api/admin/restart, i.e.
REM  the SELF-restart path: the running instance swaps in the staged binary and
REM  hands its in-flight sessions over. No human double-click is needed.
REM
REM  ORDER IS LOAD-BEARING: commit first, then compile, then restart. The agent
REM  host is pieqi itself, so a restart interrupts this session; uncommitted
REM  work would be lost.
REM
REM  !! KEEP THIS FILE ASCII-ONLY !!  cmd.exe on a Chinese Windows reads .bat
REM  as GBK; non-ASCII bytes get torn apart and executed as commands.
REM
REM  Usage:  build\release.bat            (build + deploy + self-restart)
REM          build\release.bat /no-deploy (build only)
REM ===================================================================
setlocal EnableExtensions

set "HERE=%~dp0"
for %%I in ("%HERE%..") do set "REPO=%%~fI\"
cd /d "%REPO%" || goto fail

REM ---- 1/3 frontend -------------------------------------------------
REM Must come first: web/dist is //go:embed'ed into the binary.
echo ==^> [1/3] frontend build
pushd web
call npm run build
if errorlevel 1 (popd & goto fail)
popd

REM ---- 2/3 backend ---------------------------------------------------
REM GOTMPDIR MUST be inside the workspace (see header).
echo ==^> [2/3] go build -^> build\pieqi.new.exe
if not exist "%REPO%build\.gotmp" mkdir "%REPO%build\.gotmp"
set "GOTMPDIR=%REPO%build\.gotmp"
go build -o build/pieqi.new.exe ./cmd/pieqi
if errorlevel 1 goto fail

REM A zero-byte artifact is the debris of an interrupted build; every
REM downstream consumer treats it as "nothing to install", so fail loudly here.
if not exist "%REPO%build\pieqi.new.exe" goto fail
for %%F in ("%REPO%build\pieqi.new.exe") do if %%~zF EQU 0 goto fail
for %%F in ("%REPO%build\pieqi.new.exe") do echo     artifact: %%~zF bytes

REM ---- 3/3 deploy + self-restart ------------------------------------
if /I "%~1"=="/no-deploy" goto done
echo ==^> [3/3] stage + self-restart
powershell -NoProfile -ExecutionPolicy Bypass -File "%HERE%deploy.ps1"
if errorlevel 1 goto fail

:done
echo OK: release completed
exit /b 0

:fail
echo ERROR: release failed at the step above - nothing was deployed.
exit /b 1
