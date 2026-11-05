# Thirai on a Mac

The Mac server is written in Swift and has no dependencies. It runs the Deck page (app keys, now playing, internet, batteries, Take note), the Stream and Sounds pages, and, for modern iPhones, a Phone tab that streams the camera and mic to the Mac.

## Requirements

- A Mac with macOS 14 or later and the Xcode Command Line Tools (`xcode-select --install`)
- An iPhone or iPad with Safari: anything from iOS 8/9 (iPhone 4S, 5, iPad 2…) to the latest
- Both on the same Wi-Fi network

## Quick start

```bash
git clone https://github.com/Nightshade-22/thirai.git
cd thirai
swift build -c release
.build/release/thirai
```

It prints a link like `http://192.168.x.x:8045/#k=<token>`. On the phone:

1. Open that link in **Safari**.
2. Tap **Share → Add to Home Screen** and launch it from there to run it full screen.
3. **Hold any key** to choose your apps.

An Android phone works too: open the link in Chrome, then use the Chrome menu, Add to Home screen. For a USB cable instead of Wi-Fi, run `./usb-android.sh` alongside the server (see the [guide](GUIDE.md#usb-mode-the-default-android)).

Tips for a dedicated desk display: set **Auto-Lock → Never**, and use **Guided Access** (Settings → Accessibility) to lock the phone to the deck.

### macOS permissions (asked on first use)

| Permission | Why |
|---|---|
| **Automation → Spotify / Music** | Reading what's playing and controlling playback |
| **Accessibility** | Hotkey and text buttons on the Stream page (Discord mute and deafen, for example). Asked the first time you tap one |

They're granted to the app you run `thirai` from (e.g. Terminal).

### Modern iPhone: voice, camera and mic (optional)

Safari only allows the microphone, camera and speech recognition on **HTTPS** pages, so Thirai creates a private certificate authority on your Mac (stored in `~/.thirai/tls`).

1. On the iPhone, open `http://<mac-ip>:8045/thirai.mobileconfig` and tap **Allow**.
2. **Settings → Profile Downloaded → Install.**
3. **Settings → General → About → Certificate Trust Settings** → turn on **Thirai CA**. *(Easy to miss: without it Safari keeps saying "This Connection Is Not Private".)*
4. Open `https://<mac-ip>:8047/#k=<token>` and add it to the Home Screen.

**Camera → video calls:** in [OBS](https://obsproject.com), add a *Media Source* with `http://localhost:8045/camera.mjpg?k=<token>`, then click **Start Virtual Camera**.
**Mic → video calls:** install [BlackHole 2ch](https://existential.audio/blackhole/) and restart Thirai; pick "BlackHole 2ch" as the microphone in your call app.
*(For plain FaceTime or Zoom calls, macOS's built-in Continuity Camera is simpler.)*

## Stream and Sounds pages

They work as on Windows: OBS scenes and controls, Twitch, Discord, media and volume keys, and a soundboard. The [guide](GUIDE.md#stream-page) covers connecting OBS and Twitch and changing the buttons. On a Mac:

- Settings are in `~/.thirai/config.json`, written with the default buttons the first time Thirai starts. Edit it and restart Thirai.
- OBS: turn on Tools > WebSocket Server Settings > Enable. Thirai reads the port and password from OBS's own settings on the same Mac.
- Twitch: add your client ID to `config.json` (see the guide), restart, then tap any Twitch button. The activation page opens on the Mac.
- Discord mute and deafen press Control+Shift+Option+M and Control+Shift+Option+D. Set those as Discord keybinds, then allow Terminal (or whichever app runs `thirai`) in System Settings > Privacy & Security > Accessibility when macOS asks.
- Sounds: put `.mp3` or `.wav` files in `~/.thirai/sounds`. They play through the Mac's current output, so add it to OBS with a macOS Audio Capture source.
- Take note adds `[LIVE 1:02:13]` (or `[REC …]`) to the line while OBS is live or recording.

## How it works

```
iPhone (Safari)                     Mac: thirai (Swift, no dependencies)
┌──────────────────────┐  Wi-Fi    ┌───────────────────────────────────────────────────┐
│ deck page            │ ────────▶ │ :8045 HTTP   page, apps, stats, music, Take note  │
│ (ES5 JavaScript so   │           │ :8047 HTTPS  same, for modern iPhones             │
│  iOS 8/9 can run it) │ ────────▶ │ :8048 WSS    camera frames + mic audio            │
└──────────────────────┘           └───────────────────────────────────────────────────┘
```

| Feature | Mac side |
|---|---|
| App keys | `NSWorkspace` lists `/Applications`, renders icons, opens apps |
| Take note | Adds the note to `~/Documents/Thirai notes.txt` (or `~/.thirai/notes.txt` if Documents is blocked) with the time and stream time; holding the key opens it in TextEdit |
| Stream page | OBS WebSocket v5 with `URLSessionWebSocketTask`, Twitch Helix with `URLSession`, keystrokes with `CGEvent`, volume with `osascript` |
| Sounds page | `AVAudioPlayer` |
| Now playing | AppleScript to Spotify / Music, with a timeout so a pending permission prompt can't freeze it |
| Internet | `/usr/bin/networkQuality -c` + `en*` interface byte counters |
| Batteries | IOKit power sources, `ioreg -r -k BatteryPercent`, `system_profiler SPBluetoothDataType` |
| HTTPS | Local CA and server certificate generated with the built-in `openssl`, served as an iOS profile |

The whole phone UI is one file, [`Sources/thirai/web/index.html`](../Sources/thirai/web/index.html). It's deliberately written in ES5 (no arrow functions, `let`, `fetch`…) so iOS 8/9 Safari can run it.

## Options

```
thirai --port 8045           # HTTP port (HTTPS = port+2, WebSocket = port+3)
thirai --mic-device NAME     # audio output that receives the phone's mic (default: BlackHole)
```

Settings live in `~/.thirai/`: `token` (the access code), `launcher.json` (your keys) and `tls/` (certificates).

## Security

- It listens on your **local network only**, and every request needs the random token from `~/.thirai/token`.
- Anyone with the link on your Wi-Fi can open apps on your Mac, so **treat the URL like a password** and **never port-forward it to the internet**.
- Delete `~/.thirai/token` to issue a new one.

## Troubleshooting

| Problem | Fix |
|---|---|
| Phone shows a blank page | Is the Mac on the same Wi-Fi? Check the printed IP. Old iOS can't run modern JavaScript, so if you edited `index.html`, keep it ES5. |
| "This Connection Is Not Private" | Finish step 3 of the HTTPS setup (Certificate Trust Settings). |
| Now playing stays empty | Approve macOS's "allow to control Spotify / Music" prompt, then wait a few seconds. |
| Take note says it couldn't save | macOS may ask whether Terminal can use your Documents folder. Allow it, or the note goes to `~/.thirai/notes.txt` instead. |
| Mic says "No audio device matching BlackHole" | Install BlackHole 2ch, or ignore it if you don't use the mic. |
