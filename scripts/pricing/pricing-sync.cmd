@echo off
node "%~dp0pricing-sync.cjs" %*
exit /b %errorlevel%
