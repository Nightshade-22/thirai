"""Checks a running Thirai server's Stream and Sounds API against standin_obs_twitch.py.

    python3 stream_api_test.py http://127.0.0.1:8045 <token> http://127.0.0.1:9911

Taps buttons the way the phone does (POST /action) and checks what reached the stand-in OBS and
Twitch. Skips buttons with side effects on the machine itself (hotkeys, volume, links, media keys).
"""
import json, sys, time, urllib.error, urllib.parse, urllib.request

BASE, TOKEN, STANDIN = sys.argv[1], sys.argv[2], sys.argv[3]
failures = []


def call(method, path, form=None, token=TOKEN):
    q = ("&" if "?" in path else "?") + "k=" + urllib.parse.quote(token)
    data = urllib.parse.urlencode(form).encode() if form is not None else None
    req = urllib.request.Request(BASE + path + q, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/x-www-form-urlencoded")
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def stream():
    s, body = call("GET", "/stream")
    assert s == 200, (s, body)
    return json.loads(body)


def standin():
    with urllib.request.urlopen(STANDIN + "/__state", timeout=5) as r:
        return json.loads(r.read())


def check(name, ok, detail=""):
    print(("PASS " if ok else "FAIL ") + name + (("  " + str(detail)) if detail and not ok else ""), flush=True)
    if not ok:
        failures.append(name)


def wait_for(name, pred, timeout=25):
    end = time.time() + timeout
    last = None
    while time.time() < end:
        try:
            last = pred()
            if last:
                return True
        except Exception as e:  # server still starting
            last = e
        time.sleep(0.5)
    check(name, False, last)
    return False


def button(st, action=None, label=None):
    for i, b in enumerate(st["buttons"]):
        if (action is None or b["action"] == action) and (label is None or b["label"] == label):
            return i
    raise KeyError(action or label)


def tap(i):
    return call("POST", "/action", {"kind": "button", "i": str(i)})


# --- access ---
s, _ = call("GET", "/info", token="wrong-token")
check("bad token is refused", s == 403, s)
s, body = call("GET", "/info")
check("/info says the Stream page is available", s == 200 and json.loads(body).get("stream") is True, body)

# --- connected state ---
if not wait_for("OBS and Twitch connect", lambda: stream()["obs"]["connected"] and stream()["twitch"]["connected"]):
    print(json.dumps(stream(), indent=1))
    sys.exit(1)
st = stream()
check("scenes listed in OBS order", st["obs"]["scenes"] == ["Starting soon", "Gameplay", "Just chatting", "BRB", "Ending"], st["obs"]["scenes"])
check("mic source found", (st["obs"].get("mic") or {}).get("name") == "Mic/Aux", st["obs"].get("mic"))
check("Twitch login shown", st["twitch"].get("login") == "test_streamer", st["twitch"])
check("followers shown", st["twitch"].get("followers") == 4321, st["twitch"])
check("status block present", st["status"]["obs"]["connected"] is True, st["status"])
check("buttons carry no keystrokes, text or links", all(set(b) <= {"type", "label", "sub", "action", "icon", "color", "value"} for b in st["buttons"]))

# --- scenes ---
s, body = call("POST", "/action", {"kind": "scene", "name": "Just chatting"})
check("switch scene", s == 200 and standin()["obs"]["scene"] == "Just chatting", (s, body))
s, body = call("POST", "/action", {"kind": "scene", "name": "Not a scene"})
check("unknown scene refused", s == 500 and standin()["obs"]["scene"] == "Just chatting", (s, body))

# --- OBS buttons ---
s, body = tap(button(st, "mic"))
check("Mic button mutes the mic", s == 200 and standin()["obs"]["Mic/Aux"] is True, (s, body))
i = button(st, "replay")
tap(i)
check("first Save clip starts the replay buffer", standin()["obs"]["replay"] is True)
s, body = tap(i)
check("second Save clip saves a clip", s == 200 and standin()["obs"]["replaySaved"] == 1, (s, body))
s, body = tap(button(st, "stream"))
check("Go live starts the stream", s == 200 and standin()["obs"]["live"] is True, (s, body))
wait_for("OBS live shows on the phone", lambda: stream()["obs"]["live"])
wait_for("Twitch live shows on the phone", lambda: stream()["twitch"]["live"] and stream()["twitch"]["viewers"] == 128)

# --- Twitch buttons ---
s, body = tap(button(st, "clip"))
check("Twitch clip", s == 200 and standin()["twitch"]["clips"] == 1, (s, body))
s, body = tap(button(st, "marker"))
check("Twitch marker", s == 200 and standin()["twitch"]["markers"] == 1, (s, body))
s, body = tap(button(st, "chat"))
check("chat message sent", s == 200 and len(standin()["twitch"]["chat"]) == 1, (s, body))
s, body = tap(button(st, "ad"))
check("ad started", s == 200 and standin()["twitch"]["ads"] == 1, (s, body))
i = button(st, "emoteonly")
s, body = tap(i)
check("emote-only on", s == 200 and standin()["twitch"]["emote_mode"] is True, (s, body))
wait_for("emote-only shows on", lambda: stream()["twitch"]["emoteOnly"])
s, body = tap(i)
check("emote-only off", s == 200 and standin()["twitch"]["emote_mode"] is False, (s, body))

# --- sounds and bad input ---
sounds = stream()["sounds"]
check("sounds folder listed", "Test tone.wav" in sounds, sounds)
s, body = call("POST", "/action", {"kind": "sound", "name": "Test tone.wav"})
check("play a sound", s == 200, (s, body))
s, body = call("POST", "/action", {"kind": "sound", "name": "../token"})
check("sound outside the folder refused", s == 500, (s, body))
s, body = call("POST", "/action", {"kind": "stopsound"})
check("stop sound", s == 200, (s, body))
s, body = call("POST", "/action", {"kind": "button", "i": "999"})
check("unknown button refused", s == 500, (s, body))

# --- the top bar on the Deck page ---
s, body = call("GET", "/stats")
check("/stats carries the OBS/Twitch/Discord status", s == 200 and json.loads(body).get("status", {}).get("obs", {}).get("live") is True, body[:300])

s, body = call("POST", "/note", {"text": "clip the clutch round"})
check("Take note saves while live", s == 200, (s, body))

s, body = tap(button(st, "stream"))
check("End stream", s == 200 and standin()["obs"]["live"] is False, (s, body))

print("\n%d failed" % len(failures) if failures else "\nAll checks passed")
sys.exit(1 if failures else 0)
