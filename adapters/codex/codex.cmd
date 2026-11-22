@echo off
rem ctx native Windows shim for Codex CLI
"%~dp0ext.exe" ctx run codex %*
exit /b %ERRORLEVEL%
