@echo off
rem Starts Thirai in Wi-Fi mode: for iPhones, iPads, or phones without a cable.
rem The link travels over Wi-Fi without encryption, so use this on your home network only.
cd /d "%~dp0"
if exist thirai-windows-arm64.exe if /i "%PROCESSOR_ARCHITECTURE%"=="ARM64" (
  thirai-windows-arm64.exe --wifi
  goto :eof
)
thirai-windows-x64.exe --wifi
