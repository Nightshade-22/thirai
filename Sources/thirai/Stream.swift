import AppKit
import ApplicationServices
import AVFoundation

/// The Stream and Sounds pages: config.json buttons, OBS, Twitch, hotkeys, volume and the soundboard.
/// The phone can only press buttons listed in config.json, by number; it never sends its own
/// keystrokes, text or links to this Mac. Same config format and HTTP API as the Windows server.

struct Button: Codable {
    var type: String
    var label: String
    var sub: String?
    var action: String?
    var keys: String?
    var text: String?
    var url: String?
    var file: String?
    var value: String?
    var icon: String?
    var color: String?

    init(_ type: String, _ label: String, sub: String? = nil, action: String? = nil, keys: String? = nil,
         text: String? = nil, url: String? = nil, value: String? = nil, icon: String? = nil, color: String? = nil) {
        self.type = type; self.label = label; self.sub = sub; self.action = action; self.keys = keys
        self.text = text; self.url = url; self.value = value; self.icon = icon; self.color = color
    }
}

struct StreamConfig: Codable {
    struct OBSConf: Codable { var host: String?; var port: Int?; var password: String? }
    struct TwitchConf: Codable { var client_id: String? }
    var obs: OBSConf?
    var twitch: TwitchConf?
    var buttons: [Button]?

    static let defaults = StreamConfig(
        obs: OBSConf(host: "127.0.0.1", port: 4455, password: ""),
        twitch: TwitchConf(client_id: ""),
        buttons: [
            // Page 1: what you reach for during a stream.
            Button("obs", "Go live", action: "stream"),
            Button("obs", "Record", action: "record"),
            Button("obs", "Save clip", action: "replay"),
            Button("obs", "Mic", action: "mic"),
            Button("obs", "Desktop", action: "desktop"),
            Button("hotkey", "Discord", sub: "Mute", keys: "ctrl+shift+alt+m", icon: "mic-off", color: "indigo"),
            Button("hotkey", "Discord", sub: "Deafen", keys: "ctrl+shift+alt+d", icon: "headphones", color: "indigo"),
            Button("twitch", "Twitch clip", action: "clip"),
            Button("twitch", "Marker", action: "marker"),
            Button("twitch", "Thanks", sub: "To chat", action: "chat", text: "Thanks for hanging out! ❤"),
            Button("media", "Play/Pause", action: "playpause"),
            Button("media", "Next track", action: "next"),
            Button("volume", "Volume −", action: "down"),
            Button("volume", "Volume +", action: "up"),
            Button("twitch", "Emote-only", action: "emoteonly"),
            Button("twitch", "Run ad", action: "ad", value: "60"),
            Button("obs", "Transition", action: "studio"),
            // Page 2.
            Button("obs", "Pause rec", action: "pause"),
            Button("obs", "Virtual cam", action: "vcam"),
            Button("twitch", "Slow mode", action: "slow", value: "30"),
            Button("twitch", "Followers-only", action: "followers"),
            Button("twitch", "Sub-only", action: "subonly"),
            Button("url", "Dashboard", url: "https://dashboard.twitch.tv", icon: "link", color: "purple"),
            Button("url", "YT Studio", url: "https://studio.youtube.com", icon: "link", color: "red"),
            Button("volume", "Mute Mac", action: "mute"),
        ])
}

final class StreamDeck {
    let configDir: URL
    private(set) var config = StreamConfig.defaults
    private(set) var obs: OBS!
    private(set) var twitch: Twitch!
    private let music: Music

    init(configDir: URL, music: Music) {
        self.configDir = configDir
        self.music = music
        loadConfig()
        obs = OBS { [unowned self] in
            (self.config.obs?.host ?? "127.0.0.1", self.config.obs?.port ?? 4455, self.config.obs?.password ?? "")
        }
        twitch = Twitch(configDir: configDir) { [unowned self] in self.config.twitch?.client_id ?? "" }
    }

    func start() { obs.start(); twitch.start() }

    var buttons: [Button] { config.buttons ?? [] }

    private func loadConfig() {
        let file = configDir.appendingPathComponent("config.json")
        guard let data = try? Data(contentsOf: file) else {
            // First run: write the defaults so there's a file to edit.
            let enc = JSONEncoder(); enc.outputFormatting = [.prettyPrinted, .withoutEscapingSlashes]
            try? FileManager.default.createDirectory(at: configDir, withIntermediateDirectories: true)
            if let d = try? enc.encode(StreamConfig.defaults) {
                try? d.write(to: file)
                try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
            }
            return
        }
        do {
            var c = try JSONDecoder().decode(StreamConfig.self, from: data)
            if c.buttons == nil { c.buttons = StreamConfig.defaults.buttons }
            config = c
        } catch {
            print("config.json has an error, using defaults: \(error.localizedDescription)")
        }
    }

    // MARK: What the Stream and Sounds pages poll

    func state() -> JSON {
        // The phone only needs what to draw; keystrokes, text and links stay on the Mac.
        let b: [JSON] = buttons.map {
            ["type": $0.type, "label": $0.label, "sub": $0.sub ?? "", "action": $0.action ?? "",
             "icon": $0.icon ?? "", "color": $0.color ?? "", "value": $0.value ?? ""]
        }
        return ["obs": obs.snapshot().json, "twitch": twitch.snapshot().json, "buttons": b,
                "sounds": sounds(), "status": status()]
    }

    /// The short OBS / Twitch / Discord summary shown in the top bar on every page.
    func status() -> JSON {
        let o = obs.snapshot(), t = twitch.snapshot()
        let obsStatus: JSON = ["connected": o.connected, "live": o.live, "liveMs": o.liveMs, "rec": o.rec, "recPaused": o.recPaused]
        let twitchStatus: JSON = ["configured": t.configured, "connected": t.connected, "live": t.live, "viewers": t.viewers]
        let discordStatus: JSON = ["running": discordRunning()]
        return ["obs": obsStatus, "twitch": twitchStatus, "discord": discordStatus]
    }

    /// "[LIVE 1:02:13]" or "[REC 0:04:10]" for Take note while OBS is live or recording.
    func streamTime() -> String? {
        let o = obs.snapshot()
        func clock(_ ms: Int64) -> String { let s = Int(ms / 1000); return String(format: "%ld:%02ld:%02ld", s / 3600, s / 60 % 60, s % 60) }
        if o.live { return "[LIVE \(clock(o.liveMs))]" }
        if o.rec { return "[REC \(clock(o.recMs))]" }
        return nil
    }

    // MARK: Taps from the phone

    /// Runs one /action request. Blocking (OBS and Twitch calls), so call it off the HTTP queue.
    func handle(_ p: [String: String]) -> (Int, String) {
        var what = "", msg = ""
        do {
            switch p["kind"] ?? "" {
            case "button":
                guard let i = Int(p["i"] ?? ""), buttons.indices.contains(i) else { throw ThiraiError("unknown button") }
                let b = buttons[i]
                what = b.sub.map { "\(b.label) \($0)" } ?? b.label
                msg = try run(b)
            case "scene":
                let name = p["name"] ?? ""
                what = "Scene"
                guard obs.isKnownScene(name) else {
                    throw ThiraiError(obs.snapshot().scenes.isEmpty ? "OBS is not connected" : "not one of your OBS scenes")
                }
                what = "Scene " + name
                try obs.setScene(name)
                msg = "OBS switched to " + name
            case "sound":
                let name = p["name"] ?? ""
                what = "Sound"
                guard sounds().contains(name) else { throw ThiraiError("unknown sound") }
                let title = (name as NSString).deletingPathExtension
                what = "Sound " + title
                playSound(name)
                msg = "▶ " + title
            case "stopsound":
                what = "Stop sound"
                stopSound()
                msg = "Stopped"
            default:
                throw ThiraiError("unknown action")
            }
            logTap(what, msg)
            return (200, msg)
        } catch {
            logTap(what, "failed: " + error.localizedDescription)
            return (500, error.localizedDescription)
        }
    }

    private func logTap(_ what: String, _ result: String) {
        guard !what.isEmpty else { return }
        let f = DateFormatter(); f.dateFormat = "HH:mm:ss"
        print("\(f.string(from: Date()))  \(what.padding(toLength: max(20, what.count), withPad: " ", startingAt: 0)) -> \(result)")
    }

    private func run(_ b: Button) throws -> String {
        switch b.type {
        case "obs":
            return try obs.action(b.action ?? "")
        case "twitch":
            return try twitch.action(b)
        case "hotkey":
            try Keys.press(b.keys ?? "")
            return "Pressed " + Keys.pretty(b.keys ?? "")
        case "media":
            guard ["playpause", "next", "previous"].contains(b.action ?? "") else { throw ThiraiError("media action must be playpause, next or previous") }
            music.control(b.action!)
            return "Media key: " + b.label
        case "volume":
            try Volume.change(b.action ?? "")
            return b.label
        case "text":
            try Keys.type(b.text ?? "")
            return "Typed: " + b.label
        case "url":
            guard let s = b.url, s.hasPrefix("https://") || s.hasPrefix("http://"), let u = URL(string: s) else {
                throw ThiraiError("only http and https links can be opened")
            }
            DispatchQueue.main.async { NSWorkspace.shared.open(u) }
            return "Opening " + b.label
        case "sound":
            guard let f = b.file, sounds().contains(f) else { throw ThiraiError("sound file not found in the sounds folder") }
            playSound(f)
            return "▶ " + b.label
        default:
            throw ThiraiError("unknown button type " + b.type)
        }
    }

    // MARK: Soundboard: ~/.thirai/sounds, played through the Mac's output so OBS can pick it up

    var soundsDir: URL { configDir.appendingPathComponent("sounds") }

    func sounds() -> [String] {
        try? FileManager.default.createDirectory(at: soundsDir, withIntermediateDirectories: true)
        let names = (try? FileManager.default.contentsOfDirectory(atPath: soundsDir.path)) ?? []
        return names.filter { n in
            let l = n.lowercased()
            return !n.hasPrefix(".") && (l.hasSuffix(".mp3") || l.hasSuffix(".wav"))
        }.sorted { $0.localizedStandardCompare($1) == .orderedAscending }
    }

    private var player: AVAudioPlayer?

    private func playSound(_ name: String) {
        let url = soundsDir.appendingPathComponent(name)
        DispatchQueue.main.async { [self] in
            player?.stop()   // a new sound replaces the one playing
            player = try? AVAudioPlayer(contentsOf: url)
            player?.play()
        }
    }

    private func stopSound() { DispatchQueue.main.async { [self] in player?.stop(); player = nil } }

    // MARK: Discord, cached for a few seconds: the phone asks every second or two

    private let dcLock = NSLock()
    private var dcAt = Date.distantPast, dcLast = false

    func discordRunning() -> Bool {
        dcLock.lock(); defer { dcLock.unlock() }
        if Date().timeIntervalSince(dcAt) > 5 {
            dcLast = ["com.hnc.Discord", "com.hnc.DiscordPTB", "com.hnc.DiscordCanary"].contains {
                !NSRunningApplication.runningApplications(withBundleIdentifier: $0).isEmpty
            }
            dcAt = Date()
        }
        return dcLast
    }
}

// MARK: Volume (AppleScript through osascript: no permission needed)

enum Volume {
    static func change(_ action: String) throws {
        let script: String
        switch action {
        case "up": script = "set volume output volume ((output volume of (get volume settings)) + 6)"
        case "down": script = "set volume output volume ((output volume of (get volume settings)) - 6)"
        case "mute": script = "set volume output muted (not (output muted of (get volume settings)))"
        default: throw ThiraiError("volume action must be up, down or mute")
        }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", script]
        try p.run()
    }
}

// MARK: Keystrokes (hotkey and text buttons). macOS asks once for Accessibility permission.

enum Keys {
    private static let mods: [String: (code: CGKeyCode, flag: CGEventFlags)] = [
        "ctrl": (0x3B, .maskControl), "control": (0x3B, .maskControl),
        "shift": (0x38, .maskShift),
        "alt": (0x3A, .maskAlternate), "option": (0x3A, .maskAlternate), "opt": (0x3A, .maskAlternate),
        "cmd": (0x37, .maskCommand), "command": (0x37, .maskCommand), "win": (0x37, .maskCommand),
    ]
    private static let named: [String: CGKeyCode] = [
        "a": 0x00, "s": 0x01, "d": 0x02, "f": 0x03, "h": 0x04, "g": 0x05, "z": 0x06, "x": 0x07, "c": 0x08, "v": 0x09,
        "b": 0x0B, "q": 0x0C, "w": 0x0D, "e": 0x0E, "r": 0x0F, "y": 0x10, "t": 0x11, "1": 0x12, "2": 0x13, "3": 0x14,
        "4": 0x15, "6": 0x16, "5": 0x17, "=": 0x18, "9": 0x19, "7": 0x1A, "-": 0x1B, "8": 0x1C, "0": 0x1D, "]": 0x1E,
        "o": 0x1F, "u": 0x20, "[": 0x21, "i": 0x22, "p": 0x23, "l": 0x25, "j": 0x26, "'": 0x27, "k": 0x28,
        ";": 0x29, "\\": 0x2A, ",": 0x2B, "/": 0x2C, "n": 0x2D, "m": 0x2E, ".": 0x2F, "`": 0x32,
        "enter": 0x24, "return": 0x24, "tab": 0x30, "space": 0x31, "backspace": 0x33, "esc": 0x35, "escape": 0x35,
        "delete": 0x75, "del": 0x75, "home": 0x73, "end": 0x77, "pageup": 0x74, "pagedown": 0x79,
        "left": 0x7B, "right": 0x7C, "down": 0x7D, "up": 0x7E,
        "f1": 0x7A, "f2": 0x78, "f3": 0x63, "f4": 0x76, "f5": 0x60, "f6": 0x61, "f7": 0x62, "f8": 0x64, "f9": 0x65,
        "f10": 0x6D, "f11": 0x67, "f12": 0x6F, "f13": 0x69, "f14": 0x6B, "f15": 0x71, "f16": 0x6A, "f17": 0x40,
        "f18": 0x4F, "f19": 0x50, "f20": 0x5A,
        "numpad0": 0x52, "numpad1": 0x53, "numpad2": 0x54, "numpad3": 0x55, "numpad4": 0x56, "numpad5": 0x57,
        "numpad6": 0x58, "numpad7": 0x59, "numpad8": 0x5B, "numpad9": 0x5C,
    ]

    static func parse(_ keys: String) throws -> (mods: [(code: CGKeyCode, flag: CGEventFlags)], key: CGKeyCode) {
        var m: [(code: CGKeyCode, flag: CGEventFlags)] = []
        var key: CGKeyCode?
        for part in keys.lowercased().replacingOccurrences(of: " ", with: "").split(separator: "+").map(String.init) {
            if let mod = mods[part] { m.append(mod); continue }
            guard key == nil else { throw ThiraiError("hotkey has more than one non-modifier key: \(keys)") }
            guard let k = named[part] else { throw ThiraiError("unknown key \(part)") }
            key = k
        }
        guard let key else { throw ThiraiError("hotkey needs a key: \(keys)") }
        return (m, key)
    }

    /// Accessibility permission is what lets an app send keystrokes to other apps.
    private static func allowed() throws {
        let opts = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
        guard AXIsProcessTrustedWithOptions(opts) else {
            throw ThiraiError("Allow Terminal in System Settings > Privacy & Security > Accessibility, then tap again")
        }
    }

    /// Presses a combination such as ctrl+shift+alt+m the way a keyboard would.
    static func press(_ keys: String) throws {
        let (m, key) = try parse(keys)
        try allowed()
        let src = CGEventSource(stateID: .hidSystemState)
        var flags = CGEventFlags()
        func post(_ code: CGKeyCode, _ down: Bool) {
            let e = CGEvent(keyboardEventSource: src, virtualKey: code, keyDown: down)
            e?.flags = flags
            e?.post(tap: .cghidEventTap)
        }
        // Human-like timing: some shortcut listeners miss a combination sent all at once.
        for mod in m { flags.insert(mod.flag); post(mod.code, true); usleep(20_000) }
        post(key, true); usleep(60_000); post(key, false)
        for mod in m.reversed() { usleep(20_000); flags.remove(mod.flag); post(mod.code, false) }
    }

    /// Types text into whatever has focus (chat boxes), as Unicode keystrokes.
    static func type(_ text: String) throws {
        guard !text.isEmpty else { return }
        try allowed()
        let src = CGEventSource(stateID: .hidSystemState)
        let units = Array(text.utf16)
        var i = 0
        while i < units.count {
            let chunk = Array(units[i..<min(i + 16, units.count)])
            for down in [true, false] {
                let e = CGEvent(keyboardEventSource: src, virtualKey: 0, keyDown: down)
                chunk.withUnsafeBufferPointer { e?.keyboardSetUnicodeString(stringLength: chunk.count, unicodeString: $0.baseAddress) }
                e?.post(tap: .cghidEventTap)
            }
            usleep(10_000)
            i += 16
        }
    }

    /// "ctrl+shift+alt+m" becomes "Ctrl+Shift+Alt+M".
    static func pretty(_ keys: String) -> String {
        keys.split(separator: "+").map { k in
            let s = k.trimmingCharacters(in: .whitespaces)
            if s.count <= 3 && !["ctrl", "alt", "cmd", "win"].contains(s) { return s.uppercased() }
            return s.prefix(1).uppercased() + s.dropFirst()
        }.joined(separator: "+")
    }
}
