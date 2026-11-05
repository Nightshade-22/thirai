#!/usr/bin/env bash
# Runs a Thirai server binary against the stand-in OBS and Twitch, then checks its Stream API.
#   .github/scripts/run_stream_test.sh path/to/thirai
# Needs python3 with the "websockets" package. Uses a throwaway home folder and ports 18045, 14455, 19911.
set -euo pipefail
BIN=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
HERE=$(cd "$(dirname "$0")" && pwd)
PY=${PYTHON:-python3}
WORK=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$WORK"' EXIT

mkdir -p "$WORK/home/.thirai/sounds"
printf 'testtoken2345678' > "$WORK/home/.thirai/token"
cat > "$WORK/home/.thirai/config.json" <<'EOF'
{ "obs": { "host": "127.0.0.1", "port": 14455, "password": "obs-secret" },
  "twitch": { "client_id": "test-client" } }
EOF
cat > "$WORK/home/.thirai/twitch.json" <<'EOF'
{ "client_id": "test-client", "access_token": "test-access", "refresh_token": "test-refresh",
  "expires_at": 4102444800, "user_id": "42", "login": "test_streamer" }
EOF
"$PY" - "$WORK/home/.thirai/sounds/Test tone.wav" <<'EOF'
import math, struct, sys, wave
w = wave.open(sys.argv[1], "wb"); w.setnchannels(1); w.setsampwidth(2); w.setframerate(22050)
w.writeframes(b"".join(struct.pack("<h", int(3000 * math.sin(i / 10))) for i in range(4410))); w.close()
EOF

"$PY" "$HERE/standin_obs_twitch.py" --obs-port 14455 --twitch-port 19911 --password obs-secret > "$WORK/standin.log" 2>&1 &
sleep 1
HOME="$WORK/home" THIRAI_TWITCH_API_URL=http://127.0.0.1:19911 THIRAI_TWITCH_ID_URL=http://127.0.0.1:19911 \
  THIRAI_TWITCH_POLL=1 "$BIN" --port 18045 > "$WORK/server.log" 2>&1 &

status=0
"$PY" "$HERE/stream_api_test.py" http://127.0.0.1:18045 testtoken2345678 http://127.0.0.1:19911 || status=$?
notes=$(find "$WORK/home" -name '*notes.txt' -print -quit)
if [ $status -eq 0 ] && ! grep -q '\[LIVE [0-9]*:[0-9][0-9]:[0-9][0-9]\]  clip the clutch round' "$notes" 2>/dev/null; then
  echo "FAIL Take note line carries the stream time"; cat "$notes" 2>/dev/null; status=1
elif [ $status -eq 0 ]; then
  echo "PASS Take note line carries the stream time"
fi
if [ $status -ne 0 ]; then
  echo "--- server output ---"; cat "$WORK/server.log"
  echo "--- stand-in output ---"; cat "$WORK/standin.log"
fi
exit $status
