@echo off
rem ctx native Windows shim for Claude Code
"%~dp0ext.exe" ctx run claude %*
exit /b %ERRORLEVEL%
