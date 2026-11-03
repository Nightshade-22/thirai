# OldPhoneDeck on iPhone, Android, Mac and Windows

This fork of [GLAUCOMA82/OldPhoneDeck](https://github.com/GLAUCOMA82/OldPhoneDeck) adds a Windows server and makes the phone page work properly on Android. The phone side was always a web page, so the same page now runs on any iPhone or Android phone, against either a Mac or a Windows PC.

| Phone | Mac server (original, Swift) | Windows server (new, Go) |
| --- | --- | --- |
| iPhone 4S and newer (Safari) | Yes | Yes |
| Android 5 and newer (Chrome) | Yes | Yes |
| Camera and mic tab | Modern iPhones only, needs the HTTPS setup | Not available |

## Windows quick start

1. Copy `windows/dist/oldphonedeck-windows-x64.exe` to the PC. Use the `arm64` build on a Snapdragon laptop.
2. Double-click it. A console window prints a link like `http://192.168.1.20:8045/#k=…`.
3. When Windows Firewall asks, tick Private networks only and click Allow.
4. On the phone, open the link (Safari on iPhone, Chrome on Android).
5. Add it to the home screen. On iPhone use Share, then Add to Home Screen. On Android use the Chrome menu, then Add to Home screen or Install app.
6. Hold any key to pick your 7 apps.

The window has to stay open while you use the deck. To start it with Windows, press Win+R, type `shell:startup`, and put a shortcut to the .exe in that folder.

Windows Defender SmartScreen may warn about an unsigned app the first time. Click More info, then Run anyway, or build it yourself (below) if you'd rather not trust a binary.

### What each widget uses on Windows

| Deck feature | Mac | Windows |
| --- | --- | --- |
| App keys | `/Applications` | Start menu apps (desktop and Store), opened through `shell:AppsFolder` |
| App icons | NSWorkspace | Shell icon API, cached in `%USERPROFILE%\.oldphonedeck\icons` |
| Now playing | AppleScript to Spotify / Music | Windows media controls, so Spotify, Apple Music, browsers, VLC and anything else that shows in the volume flyout |
| Play / next / previous | AppleScript | Media keys |
| Internet speed | `networkQuality` | Cloudflare's public speed test endpoints (about 15 seconds) |
| Traffic graph | `en*` interface counters | Busiest connected Ethernet or Wi-Fi adapter |
| Batteries | IOKit, ioreg, system_profiler | Laptop battery, plus Bluetooth devices that report a level to Windows |
| Take note | `claude://` link, then Return | Same link, then Enter, only if Claude is the window in front |

Options: `--port 8045` and `--no-autosend` (Take note fills in the chat but leaves it unsent).

### Build it yourself

You need Go 1.22 or newer. From the `windows` folder:

```
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/oldphonedeck-windows-x64.exe .
```

On a Windows machine, plain `go build` works. On macOS or Linux, `go run .` starts a demo server with sample data, which is handy for working on the page. The page is embedded at build time from `windows/web/`, which is a copy of `Sources/oldphonedeck/web/`; copy it again after editing.

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

## Mac changes

Small ones. The Mac server now serves the manifest and icons (`main.swift`, one new block before the token check) and its startup message mentions Android. Nothing else in the Swift code changed. I couldn't compile Swift while making this, so build it once and check before relying on it.

## Security review of the original

The design is sound for a home network. Every request except the page shell needs a random token, app launches are limited to apps the server itself listed, and the page writes untrusted text (track names, app names) with `textContent`, so a song title can't inject script.

Points worth knowing, and what the Windows server does about them:

1. Traffic is plain HTTP, so the token travels in the clear on your Wi-Fi. Anyone who can sniff that network can lift it. Fine at home, not on office or café Wi-Fi.
2. Take note sends text to the Claude desktop app and presses Return. Anyone with the token can therefore start Claude chats on your computer. If your Claude app has connectors or local tools enabled, treat the token as access to those too. The Windows server caps notes at 4,000 characters and has `--no-autosend`.
3. On Mac, the Accessibility permission goes to Terminal (or whatever runs the server), so every script you run from Terminal gets it as well. Windows needs no such permission.
4. The Mac server reads request bodies up to 512 MB before checking the token, so anyone on the network can make it buffer a lot of memory. The Windows server checks the token first and caps bodies at 64 KB.
5. The optional HTTPS setup installs a home-made root CA on the iPhone. Its private key sits unencrypted in `~/.oldphonedeck/tls/ca.key` on the Mac and the CA has no name constraints, so anyone who copies that key can intercept the phone's traffic to any site. Skip that setup unless you need the camera or mic, and delete the profile from the phone if you stop using it.
6. The Mac's token compare isn't constant-time. Minor on a LAN. The Windows server uses a constant-time compare and a longer token (16 characters instead of 12).

To issue a new token on either system, delete `.oldphonedeck/token` in your home folder and restart the server.

## Not tested on real hardware

The Windows server compiles and its HTTP side was tested against the phone page in emulated Android and iPhone browsers. The Windows-only parts (Start menu apps, icons, media controls, Bluetooth batteries, the Claude deep link) follow documented APIs but haven't run on a real Windows PC yet. If one fails, the deck shows that widget empty and the rest keeps working.
