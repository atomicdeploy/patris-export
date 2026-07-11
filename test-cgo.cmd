@echo off
setlocal EnableExtensions

set "ROOT=%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%ROOT%scripts\windows\Invoke-CGO.ps1" go test ./...
exit /b %ERRORLEVEL%
