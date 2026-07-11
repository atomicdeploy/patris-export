@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "ESC="
set "BOLD=%ESC%[1m"
set "DIM=%ESC%[2m"
set "RED=%ESC%[31m"
set "GREEN=%ESC%[32m"
set "YELLOW=%ESC%[33m"
set "CYAN=%ESC%[36m"
set "RESET=%ESC%[0m"

set "ROOT=%~dp0"
set "HAS_TARGET=0"
set "WANTS_HELP=0"

if exist "C:\Program Files\Go\bin\go.exe" set "PATH=C:\Program Files\Go\bin;%PATH%"
for /D %%D in ("C:\Program Files\Python*") do (
    if exist "%%~D\python.exe" set "PATH=%%~D;%PATH%"
)
if exist "C:\msys64\mingw64\bin\gcc.exe" set "PATH=C:\msys64\mingw64\bin;%PATH%"
for /D %%D in ("%LOCALAPPDATA%\Microsoft\WinGet\Packages\BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_*") do (
    if exist "%%~D\mingw64\bin\gcc.exe" set "PATH=%%~D\mingw64\bin;%PATH%"
)
if not defined PXLIB_ROOT (
    if exist "%USERPROFILE%\Desktop\AtomicDeploy\deps\pxlib-install-windows\include\paradox.h" (
        set "PXLIB_ROOT=%USERPROFILE%\Desktop\AtomicDeploy\deps\pxlib-install-windows"
    )
)

for %%A in (%*) do (
    if /I "%%~A"=="--target" set "HAS_TARGET=1"
    echo %%~A | findstr /B /I /C:"--target=" >nul && set "HAS_TARGET=1"
    if /I "%%~A"=="-h" set "WANTS_HELP=1"
    if /I "%%~A"=="--help" set "WANTS_HELP=1"
)

call :find_bash
if not defined BASH_EXE (
    call :err "Bash was not found. Install MSYS2 or Git for Windows, then retry."
    exit /b 1
)

call :info "🏗️  Patris Export Windows build launcher"
call :info "Bash: %BASH_EXE%"

if "%WANTS_HELP%"=="1" (
    "%BASH_EXE%" "%ROOT%build.sh" --help
    exit /b %ERRORLEVEL%
)

if "%HAS_TARGET%"=="1" (
    "%BASH_EXE%" "%ROOT%build.sh" %*
) else (
    "%BASH_EXE%" "%ROOT%build.sh" --target windows-native %*
)
set "EXIT_CODE=%ERRORLEVEL%"
if "%EXIT_CODE%"=="0" (
    call :ok "Build finished"
) else (
    call :err "Build failed with exit code %EXIT_CODE%"
)
exit /b %EXIT_CODE%

:find_bash
for %%B in (bash.exe) do (
    if not "%%~$PATH:B"=="" (
        set "BASH_EXE=%%~$PATH:B"
        goto :eof
    )
)
for %%B in (
    "%ProgramFiles%\Git\usr\bin\bash.exe"
    "%ProgramFiles(x86)%\Git\usr\bin\bash.exe"
    "C:\msys64\usr\bin\bash.exe"
    "C:\msys64\mingw64\bin\bash.exe"
) do (
    if exist "%%~B" (
        set "BASH_EXE=%%~B"
        goto :eof
    )
)
goto :eof

:info
echo %CYAN%%~1%RESET%
goto :eof

:ok
echo %GREEN%✅ %~1%RESET%
goto :eof

:err
echo %RED%❌ %~1%RESET% 1>&2
goto :eof
