@echo off
rem ctx native Windows shim for nerdctl/containerd
"%~dp0ext.exe" ctx run nerdctl %*
exit /b %ERRORLEVEL%
