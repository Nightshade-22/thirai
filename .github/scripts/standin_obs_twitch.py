"""Stand-in OBS WebSocket (v5) and Twitch Helix servers for testing Thirai without OBS or a Twitch account.

    python3 standin_obs_twitch.py [--obs-port 4455] [--twitch-port 9911] [--password secret]

OBS keeps state (live, scenes, mutes, replay buffer) so button taps can be checked. Twitch answers
the Helix calls Thirai makes and keeps chat-mode state. GET /__state on the Twitch port returns both.
Only listens on 127.0.0.1.
"""
import argparse, asyncio, base64, hashlib, json, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse
import websockets

ap = argparse.ArgumentParser()
ap.add_argument("--obs-port", type=int, default=4455)
ap.add_argument("--twitch-port", type=int, default=9911)
ap.add_argument("--password", default="")
args = ap.parse_args()

SCENES = ["Starting soon", "Gameplay", "Just chatting", "BRB", "Ending"]
st = {"live": False, "since": 0.0, "rec": False, "recPaused": False, "replay": False, "vcam": False,
      "studio": False, "scene": "Gameplay", "Mic/Aux": False, "Desktop Audio": False, "replaySaved": 0}
tw = {"emote_mode": False, "slow_mode": False, "follower_mode": False, "subscriber_mode": False,
      "clips": 0, "markers": 0, "chat": [], "ads": 0}


def respond(rt, d):
    now = time.time()
    simple = {"GetReplayBufferStatus": "replay", "GetVirtualCamStatus": "vcam"}
    if rt == "GetStreamStatus":
        return {"outputActive": st["live"], "outputDuration": int((now - st["since"]) * 1000) if st["live"] else 0,
                "outputSkippedFrames": 3, "outputTotalFrames": 1000}
    if rt in simple:
        return {"outputActive": st[simple[rt]]}
    if rt == "GetRecordStatus":
        return {"outputActive": st["rec"], "outputPaused": st["recPaused"], "outputDuration": 0}
    if rt == "GetStudioModeEnabled":
        return {"studioModeEnabled": st["studio"]}
    if rt == "GetStats":
        return {"cpuUsage": 7.5, "activeFps": 60.0}
    if rt == "GetSceneList":
        return {"currentProgramSceneName": st["scene"],
                "scenes": [{"sceneName": s, "sceneIndex": i} for i, s in enumerate(reversed(SCENES))]}
    if rt == "GetCurrentProgramScene":
        return {"currentProgramSceneName": st["scene"]}
    if rt == "SetCurrentProgramScene":
        st["scene"] = d["sceneName"]
        return {}
    if rt == "GetSpecialInputs":
        return {"desktop1": "Desktop Audio", "mic1": "Mic/Aux"}
    if rt == "GetInputMute":
        return {"inputMuted": st[d["inputName"]]}
    if rt == "ToggleInputMute":
        st[d["inputName"]] = not st[d["inputName"]]
        return {"inputMuted": st[d["inputName"]]}
    if rt == "ToggleStream":
        st["live"] = not st["live"]; st["since"] = now
        return {"outputActive": st["live"]}
    if rt == "ToggleRecord":
        st["rec"] = not st["rec"]
        return {}
    if rt == "ToggleRecordPause":
        st["recPaused"] = not st["recPaused"]
        return {}
    if rt == "StartReplayBuffer":
        st["replay"] = True
        return {}
    if rt == "SaveReplayBuffer":
        st["replaySaved"] += 1
        return {}
    if rt == "ToggleVirtualCam":
        st["vcam"] = not st["vcam"]
        return {}
    if rt == "SetStudioModeEnabled":
        st["studio"] = d["studioModeEnabled"]
        return {}
    if rt == "TriggerStudioModeTransition":
        return {}
    return None


async def obs(ws):
    salt, challenge = "c2FsdA==", "Y2hhbGxlbmdl"
    hello = {"obsWebSocketVersion": "5.5.0", "rpcVersion": 1}
    if args.password:
        hello["authentication"] = {"salt": salt, "challenge": challenge}
    await ws.send(json.dumps({"op": 0, "d": hello}))
    async for raw in ws:
        m = json.loads(raw)
        if m["op"] == 1:
            if args.password:
                secret = base64.b64encode(hashlib.sha256((args.password + salt).encode()).digest()).decode()
                want = base64.b64encode(hashlib.sha256((secret + challenge).encode()).digest()).decode()
                if m["d"].get("authentication") != want:
                    await ws.close(4009, "Authentication failed")
                    return
            await ws.send(json.dumps({"op": 2, "d": {"negotiatedRpcVersion": 1}}))
        elif m["op"] == 6:
            d = m["d"]
            data = respond(d["requestType"], d.get("requestData") or {})
            status = {"result": data is not None, "code": 100 if data is not None else 204}
            if data is None:
                status["comment"] = "unknown request"
            await ws.send(json.dumps({"op": 7, "d": {"requestType": d["requestType"], "requestId": d["requestId"],
                                                     "requestStatus": status, "responseData": data or {}}}))


class Twitch(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, obj, code=200):
        b = json.dumps(obj).encode()
        self.send_response(code); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        try:
            return json.loads(self.rfile.read(n) or b"{}")
        except ValueError:
            return {}

    def _authed(self):
        if self.headers.get("Authorization") != "Bearer test-access" or self.headers.get("Client-Id") != "test-client":
            self._send({"message": "Invalid OAuth token"}, 401)
            return False
        return True

    def do_GET(self):
        p = urlparse(self.path).path
        if p == "/__state":
            return self._send({"obs": st, "twitch": tw})
        if p.endswith("/oauth2/validate"):
            return self._send({"client_id": "test-client", "login": "test_streamer", "user_id": "42", "expires_in": 9999})
        if not self._authed():
            return
        if p.endswith("/streams"):
            return self._send({"data": [{"viewer_count": 128, "title": "Ranked", "game_name": "Valorant"}] if st["live"] else []})
        if p.endswith("/channels/followers"):
            return self._send({"total": 4321, "data": []})
        if p.endswith("/channels"):
            return self._send({"data": [{"title": "Ranked", "game_name": "Valorant"}]})
        if p.endswith("/chat/settings"):
            return self._send({"data": [{k: tw[k] for k in ("emote_mode", "slow_mode", "follower_mode", "subscriber_mode")}]})
        return self._send({"message": "not found"}, 404)

    def do_POST(self):
        p = urlparse(self.path).path
        if not self._authed():
            return
        b = self._body()
        if p.endswith("/clips"):
            tw["clips"] += 1
            return self._send({"data": [{"id": "Clip1", "edit_url": "https://clips.twitch.tv/Clip1/edit"}]}, 202)
        if p.endswith("/streams/markers"):
            tw["markers"] += 1
            return self._send({"data": [{"id": "1", "position_seconds": 60}]})
        if p.endswith("/chat/messages"):
            tw["chat"].append(b.get("message", ""))
            return self._send({"data": [{"message_id": "m1", "is_sent": True}]})
        if p.endswith("/channels/commercial"):
            tw["ads"] += 1
            return self._send({"data": [{"length": b.get("length", 60), "retry_after": 480, "message": ""}]})
        return self._send({"message": "not found"}, 404)

    def do_PATCH(self):
        p = urlparse(self.path).path
        if not self._authed():
            return
        b = self._body()
        if p.endswith("/chat/settings"):
            for k in ("emote_mode", "slow_mode", "follower_mode", "subscriber_mode"):
                if k in b:
                    tw[k] = bool(b[k])
            return self._send({"data": [{k: tw[k] for k in ("emote_mode", "slow_mode", "follower_mode", "subscriber_mode")}]})
        return self._send({"message": "not found"}, 404)


async def main():
    threading.Thread(target=ThreadingHTTPServer(("127.0.0.1", args.twitch_port), Twitch).serve_forever, daemon=True).start()
    async with websockets.serve(obs, "127.0.0.1", args.obs_port, subprotocols=["obswebsocket.json"]):
        print("stand-in OBS on", args.obs_port, "and Twitch on", args.twitch_port, flush=True)
        await asyncio.Future()

asyncio.run(main())
