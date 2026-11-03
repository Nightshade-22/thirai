@echo off
rem Starts OldPhoneDeck in USB mode: Android phone over a cable, nothing on Wi-Fi.
cd /d "%~dp0"
if exist oldphonedeck-windows-arm64.exe if /i "%PROCESSOR_ARCHITECTURE%"=="ARM64" (
  oldphonedeck-windows-arm64.exe --usb
  goto :eof
)
oldphonedeck-windows-x64.exe --usb
