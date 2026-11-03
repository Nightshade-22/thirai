@echo off
rem Starts Thirai in USB mode: Android phone over a cable, nothing on Wi-Fi.
cd /d "%~dp0"
if exist thirai-windows-arm64.exe if /i "%PROCESSOR_ARCHITECTURE%"=="ARM64" (
  thirai-windows-arm64.exe --usb
  goto :eof
)
thirai-windows-x64.exe --usb
