# Thirai guide: Windows, Android, tablets and streaming

Thirai's phone side is a web page, so it runs on any iPhone, iPad, Android phone or Android tablet, against either the Windows server or the Mac server. Mac setup is in [MAC.md](MAC.md).

| Device | Mac server (original, Swift) | Windows server (new, Go) |
| --- | --- | --- |
| iPhone 4S and newer, iPad 2 and newer (Safari) | Yes | Yes |
| Android 5 and newer, phone or tablet (Chrome) | Yes | Yes |
| Stream page (OBS, Discord, music, links) and Sounds page | No | Yes |
| Camera and mic tab | Modern iPhones only, needs the HTTPS setup | Not available |

Tablets get their own layout: three rows of five or six app keys (14 or 17 apps plus Take note) and bigger Stream and Sounds grids. A tablet is anything whose shorter screen side is at least 600 points, which covers every iPad and most 7-inch and larger Android tablets.

## Windows quick start

1. Copy `windows/dist/thirai-windows-x64.exe` to the PC. Use the `arm64` build on a Snapdragon laptop.
2. Double-click it. A console window prints a link like `http://192.168.1.20:8045/#k=…`.
3. When Windows Firewall asks, tick Private networks only and click Allow.
4. On the phone, open the link (Safari on iPhone, Chrome on Android).
5. Add it to the home screen. On iPhone use Share, then Add to Home Screen. On Android use the Chrome menu, then Add to Home screen or Install app.
6. Hold any key to pick your apps (7 on a phone, 14 or 17 on a tablet).

The window has to stay open while you use the deck. To start it with Windows, press Win+R, type `shell:startup`, and put a shortcut to the .exe in that folder.

Windows Defender SmartScreen may warn about an unsigned app the first time. Click More info, then Run anyway, or build it yourself (below) if you'd rather not trust a binary.

### What each widget uses on Windows

| Deck feature | Mac | Windows |
| --- | --- | --- |
| App keys | `/Applications` | Start menu apps (desktop and Store), opened through `shell:AppsFolder` |
| App icons | NSWorkspace | Shell icon API, cached in `%USERPROFILE%\.thirai\icons` |
| Now playing | AppleScript to Spotify / Music | Windows media controls, so Spotify, Apple Music, browsers, VLC and anything else that shows in the volume flyout |
| Play / next / previous | AppleScript | Media keys |
| Internet speed | `networkQuality` | Cloudflare's public speed test endpoints (about 15 seconds) |
| Traffic graph | `en*` interface counters | Busiest connected Ethernet or Wi-Fi adapter |
| Batteries | IOKit, ioreg, system_profiler | Laptop battery, plus Bluetooth devices that report a level to Windows |
| Take note | `claude://` link, then Return | Same link, then Enter, only if Claude is the window in front |

Options: `--port 8045`, `--usb` (wired mode, below), `--bind <ip>` (listen on one address only) and `--no-autosend` (Take note fills in the chat but leaves it unsent).

## Stream page (Windows)

The Stream and Sounds tabs appear at the top of the deck when it's connected to the Windows server.

The Stream page has three parts. The top line shows LIVE or OFFLINE with the stream time, a REC timer, frame rate, CPU and dropped frames. Under it is one button per OBS scene, with the current scene in blue. Below that is a grid of action buttons. The defaults are:

| Button | What it does |
| --- | --- |
| Go live | Starts or ends the stream. Press and hold, so a stray tap can't do it |
| Record, Pause rec | Start or stop recording, pause or resume it |
| Save clip | First tap arms OBS's replay buffer, later taps save the last moments as a clip |
| Mic, Desktop | Mute or unmute OBS's mic and desktop audio; turns red when muted |
| Discord: Mute, Deafen | Sends `Ctrl+Shift+Alt+M` and `Ctrl+Shift+Alt+D` (set these as Discord keybinds, below) |
| Play/Pause, Next track | Media keys, so Spotify or whatever is playing |
| Volume −, Volume +, Mute PC | System volume |
| Virtual cam | OBS virtual camera on or off |
| Transition | Turns on OBS studio mode, then each tap sends preview to program |
| Thanks | Types a chat message into whatever window has focus |
| YT Studio, Twitch | Opens YouTube Studio or the Twitch dashboard on the PC |

### Connect OBS (once)

1. In OBS 28 or newer, open Tools, then WebSocket Server Settings.
2. Tick Enable WebSocket server. Leave authentication on and click OK.

That's all. Thirai reads the port and password from OBS's own settings on the same PC, so there's nothing to copy. If OBS runs on another computer, put its address in `"host"` and the password from Show Connect Info in `"password"` in `config.json`.

The top line of the Stream page says what's wrong when OBS isn't reachable: OBS not running, the WebSocket server switched off, or a wrong password.

### Connect Twitch (once)

Twitch needs you to register a small app of your own. It takes two minutes and is free.

1. Turn on two-factor authentication on your Twitch account if it isn't on yet (Twitch requires it for developer apps).
2. Go to dev.twitch.tv/console/apps and click Register Your Application.
3. Fill in: any unique name (for example `Thirai-yourname`), OAuth Redirect URL `http://localhost`, Category `Broadcasting Suite`, Client Type `Public`. Click Create.
4. Open the app with Manage and copy its Client ID.
5. Paste it into `config.json` as `"twitch": { "client_id": "..." }` and restart Thirai.
6. Tap any Twitch button on the phone. Twitch's activation page opens on the PC with a code filled in; click Authorize. The phone also shows the code, in case you'd rather enter it at twitch.tv/activate on another device.

Thirai never sees your Twitch password. It keeps the sign-in in `twitch.json` in the `.thirai` folder (readable only by your Windows account) and renews it on its own. Delete that file to sign out.

### Discord mute and deafen (once)

Discord has no open way for other apps to mute you, so these buttons press keyboard shortcuts.

1. In Discord, open User Settings, then Keybinds, then Add a Keybind.
2. Pick Toggle Mute, click the shortcut box and press `Ctrl+Shift+Alt+M`.
3. Add another for Toggle Deafen with `Ctrl+Shift+Alt+D`.

Discord shows no mute state to other apps either, so these two buttons don't change colour.

### Change the buttons

Edit the `buttons` list in `config.json` and restart. Each button has a `type`, a `label`, an optional second line `sub`, an optional `icon` and `color`, plus the field its type needs:

| type | Needs | Example |
| --- | --- | --- |
| `obs` | `action`: `stream`, `record`, `pause`, `replay`, `vcam`, `mic`, `desktop` or `studio` | `{"type": "obs", "action": "replay", "label": "Clip"}` |
| `hotkey` | `keys`, such as `ctrl+shift+f13` | `{"type": "hotkey", "keys": "ctrl+alt+s", "label": "Screenshot"}` |
| `media` | `action`: `playpause`, `next` or `previous` | |
| `volume` | `action`: `up`, `down` or `mute` | |
| `text` | `text` to type | `{"type": "text", "text": "!socials", "label": "Socials"}` |
| `url` | an `http` or `https` link | `{"type": "url", "url": "https://twitch.tv/yourname", "label": "Channel"}` |
| `sound` | `file`, a file in the sounds folder | `{"type": "sound", "file": "airhorn.mp3", "label": "Airhorn"}` |
| `twitch` | `action`: `clip`, `marker`, `ad` (seconds in `value`), `chat` (message in `text`), `emoteonly`, `slow` (seconds in `value`), `followers` or `subonly` | `{"type": "twitch", "action": "chat", "text": "!discord", "label": "Discord link"}` |

Twitch buttons are purple unless you give them a colour. Clip, marker and ad dim while you're offline; the chat-mode buttons turn green while the mode is on.

Colours: `indigo`, `teal`, `red`, `purple`, `green`, `orange`, `blue`, `pink`, `yellow`. Icons: `broadcast`, `rec`, `pause`, `clip`, `camera`, `transition`, `mic`, `mic-off`, `speaker`, `speaker-off`, `headphones`, `keyboard`, `playpause`, `next`, `prev`, `vol-up`, `vol-down`, `vol-mute`, `chat`, `link`, `note`, `stop`, `scissors`, `flag`, `megaphone`, `smile`, `clock`, `heart`, `star`.

When there are more buttons than fit, the last key becomes More and flips through pages. A phone fits 18 keys per page and a tablet 24. The phone can only press buttons from this list, by number. It never sends its own keystrokes, text or links to the PC.

## Sounds page (Windows)

Put `.mp3` or `.wav` files in `%USERPROFILE%\.thirai\sounds` and they appear as buttons, named after the file. A new sound replaces the one playing, and Stop silences it. Sounds play through the PC's default speakers, so OBS picks them up with desktop audio.

### Build it yourself

You need Go 1.22 or newer. From the `windows` folder:

```
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/thirai-windows-x64.exe .
```

On a Windows machine, plain `go build` works. On macOS or Linux, `go run .` starts a demo server with sample data, which is handy for working on the page. The page is embedded at build time from `windows/web/`, which is a copy of `Sources/thirai/web/`; copy it again after editing.

## Wired mode (USB cable, Android)

Wi-Fi mode sends the access link in plain HTTP, so anyone sniffing the network can copy it. USB mode avoids that: the phone talks to the computer through the cable and the server doesn't listen on Wi-Fi at all.

On Windows:

1. Install Android platform-tools once: `winget install Google.PlatformTools`. You can also download the platform-tools zip from Google and put the `platform-tools` folder next to the .exe.
2. On the phone, turn on Developer options (tap Build number seven times in About phone), then USB debugging.
3. Double-click `start-usb.bat` (or run the .exe with `--usb`).
4. Plug the phone in and tap Allow on the "Allow USB debugging?" prompt.

The deck then opens on the phone by itself and reconnects whenever you plug it back in. While USB mode runs, it also sets the phone to stay awake while plugged into USB. To undo that, run `adb shell svc power stayon false`.

On a Mac, run `./usb-android.sh` next to the normal server (needs `brew install android-platform-tools`). The Swift server still listens on Wi-Fi as well, so block it in the macOS firewall if you want cable only.

USB mode also makes Chrome treat the page as secure (it's on `localhost`), which turns on voice input for Take note. It needs Android 5.0 or newer.

### iPhone over a cable

There isn't a clean equivalent for iPhone. Apple's USB link lets a computer reach the phone, not the other way round, which is the direction the deck needs. The one workaround is Personal Hotspot over USB, which needs a SIM with hotspot enabled. That rules out most drawer iPhones. It can also route the computer's internet through the phone. If you do use it, start the server with `--bind` and the computer's address on that link (usually `172.20.10.2`), so it only listens there.

For an iPhone on Wi-Fi, the safer fix is HTTPS. The Mac server already has it. The Windows server doesn't yet.

## Android changes to the phone page

These apply to both servers, since both serve the same `index.html`.

- A web app manifest and icons, so Chrome's Add to Home screen installs the deck as a full-screen landscape app.
- Outside the installed app, the first tap switches Chrome to full screen.
- Long-press no longer opens Chrome's image menu, so "hold any key to edit" works.
- The camera tab is hidden on Android over plain HTTP, because its setup steps are iPhone-only.
- Missing app icons show the app's first letter instead of a broken image.
- Messages say "PC" or "Mac" depending on the server.

The page is still plain ES5, so the iPhone 4S on iOS 9 keeps working.

To keep an Android screen on, turn on Developer options, then Stay awake. The screen then stays on while the phone is charging. Browsers only allow a page to hold the screen on over HTTPS.

## Security notes

These come from a review of the original OldPhoneDeck code that Thirai grew from. The design is sound for a home network. Every request except the page shell needs a random token, app launches are limited to apps the server itself listed, and the page writes untrusted text (track names, app names) with `textContent`, so a song title can't inject script.

Points worth knowing, and what the Windows server does about them:

1. Traffic is plain HTTP, so the token travels in the clear on your Wi-Fi. Anyone who can sniff that network can lift it. Fine at home, not on office or café Wi-Fi. Wired mode (above) removes this for Android. With the Stream page, a lifted token can also end your stream or type in your chat, which is one more reason to use wired mode.
2. Take note sends text to the Claude desktop app and presses Return. Anyone with the token can therefore start Claude chats on your computer. If your Claude app has connectors or local tools enabled, treat the token as access to those too. The Windows server caps notes at 4,000 characters and has `--no-autosend`.
3. On Mac, the Accessibility permission goes to Terminal (or whatever runs the server), so every script you run from Terminal gets it as well. Windows needs no such permission.
4. The Mac server reads request bodies up to 512 MB before checking the token, so anyone on the network can make it buffer a lot of memory. The Windows server checks the token first and caps bodies at 64 KB.
5. The optional HTTPS setup installs a home-made root CA on the iPhone. Its private key sits unencrypted in `~/.thirai/tls/ca.key` on the Mac and the CA has no name constraints, so anyone who copies that key can intercept the phone's traffic to any site. Skip that setup unless you need the camera or mic, and delete the profile from the phone if you stop using it.
6. The Mac's token compare isn't constant-time. Minor on a LAN. The Windows server uses a constant-time compare and a longer token (16 characters instead of 12).

The OBS WebSocket password sits in plain text in `config.json`, readable only by your Windows account. Thirai only connects to OBS on this PC (`127.0.0.1`) unless you change `host`.

To issue a new token on either system, delete `.thirai/token` in your home folder and restart the server.

## Testing status

The Windows server compiles and its HTTP side was tested against the phone page in emulated Android and iPhone browsers. The Windows-only parts (Start menu apps, icons, media controls, Bluetooth batteries, the Claude deep link) follow documented APIs but haven't run on a real Windows PC yet. If one fails, the deck shows that widget empty and the rest keeps working. USB mode was tested with a stand-in for adb, and OBS control against a stand-in OBS WebSocket server with real password login, not with OBS itself.
