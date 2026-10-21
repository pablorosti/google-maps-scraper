@echo off
rem Windows wrapper for leads.sh: run "leads estado cordoba" from PowerShell or CMD.
setlocal
set "GITBASH=%ProgramFiles%\Git\bin\bash.exe"
if not exist "%GITBASH%" (
  echo No se encontro Git Bash en "%GITBASH%". Instalar Git for Windows: https://git-scm.com/download/win
  exit /b 1
)
"%GITBASH%" "%~dp0leads.sh" %*
