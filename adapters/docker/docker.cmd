@echo off
rem ctx native Windows shim for Docker
"%~dp0ext.exe" ctx run docker %*
exit /b %ERRORLEVEL%
