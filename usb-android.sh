#!/bin/sh
# Mac + Android over a USB cable. Run this alongside .build/release/thirai.
# adb's reverse tunnel makes http://localhost:8045 on the phone reach this Mac over the cable.
# Needs: brew install android-platform-tools, and USB debugging on the phone.
# Note: the Mac server itself still listens on Wi-Fi too; block it in the macOS firewall
# (System Settings > Network > Firewall) if you want cable only.
PORT="${1:-8045}"
TOKEN="$(cat "$HOME/.thirai/token" 2>/dev/null)"
[ -n "$TOKEN" ] || { echo "Start thirai once first so it creates ~/.thirai/token"; exit 1; }
command -v adb >/dev/null || { echo "adb not found: brew install android-platform-tools"; exit 1; }

echo "Waiting for an Android phone on USB (Ctrl-C to stop)..."
DONE=""
while true; do
  for S in $(adb devices | awk 'NR>1 && $2=="device" {print $1}'); do
    case " $DONE " in *" $S "*) continue ;; esac
    adb -s "$S" reverse "tcp:$PORT" "tcp:$PORT" || continue
    adb -s "$S" shell "svc power stayon usb"
    adb -s "$S" shell "am start -a android.intent.action.VIEW -d 'http://localhost:$PORT/#k=$TOKEN'" >/dev/null
    echo "$(adb -s "$S" shell getprop ro.product.model) connected; the deck is open on the phone."
    DONE="$DONE $S"
  done
  # Forget phones that were unplugged so they're set up again next time.
  NOW="$(adb devices | awk 'NR>1 && $2=="device" {print $1}')"
  KEEP=""; for S in $DONE; do echo "$NOW" | grep -qx "$S" && KEEP="$KEEP $S"; done; DONE="$KEEP"
  sleep 3
done
