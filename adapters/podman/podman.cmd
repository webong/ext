@echo off
rem ctx native Windows shim for Podman
"%~dp0ext.exe" ctx run podman %*
exit /b %ERRORLEVEL%
