import AppKit

/// The home folder: $HOME when it's set (like other command-line tools, and so tests can use a scratch
/// folder), otherwise the account's home. ~/.thirai and the notes file live under it.
let userHome: URL = {
    if let h = ProcessInfo.processInfo.environment["HOME"], !h.isEmpty { return URL(fileURLWithPath: h, isDirectory: true) }
    return URL(fileURLWithPath: NSHomeDirectory(), isDirectory: true)
}()

/// The app launcher: lists installed apps, renders their icons, and opens them.
enum Apps {
    private static let lock = NSLock()
    private static var iconCache: [String: Data] = [:]

    static func list() -> [[String: String]] {
        let fm = FileManager.default
        let dirs = ["/Applications", "/Applications/Utilities", "/System/Applications",
                    "/System/Applications/Utilities", NSHomeDirectory() + "/Applications"]
        var seen = Set<String>()
        var out: [[String: String]] = []
        for dir in dirs {
            for item in (try? fm.contentsOfDirectory(atPath: dir)) ?? [] where item.hasSuffix(".app") {
                let name = String(item.dropLast(4))
                guard seen.insert(name).inserted else { continue }
                out.append(["name": name, "path": dir + "/" + item])
            }
        }
        return out.sorted { $0["name"]!.localizedCaseInsensitiveCompare($1["name"]!) == .orderedAscending }
    }

    /// Only paths that appear in our own listing may be launched or rendered.
    static func isKnown(_ path: String) -> Bool { list().contains { $0["path"] == path } }

    static func icon(_ path: String, size: Int = 114) -> Data? {
        lock.lock(); if let d = iconCache[path] { lock.unlock(); return d }; lock.unlock()
        // Resolve symlinks (e.g. /Applications/Safari.app) so the icon has no alias-arrow badge.
        let img = NSWorkspace.shared.icon(forFile: URL(fileURLWithPath: path).resolvingSymlinksInPath().path)
        guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
                                         bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                         colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0) else { return nil }
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
        img.draw(in: NSRect(x: 0, y: 0, width: size, height: size))
        NSGraphicsContext.restoreGraphicsState()
        guard let png = rep.representation(using: .png, properties: [:]) else { return nil }
        lock.lock(); iconCache[path] = png; lock.unlock()
        return png
    }

    static func launch(_ path: String) {
        let cfg = NSWorkspace.OpenConfiguration()
        cfg.activates = true
        NSWorkspace.shared.openApplication(at: URL(fileURLWithPath: path), configuration: cfg) { _, err in
            if let err { print("Launch failed for \(path): \(err.localizedDescription)") }
        }
    }
}

/// Which apps the phone launcher shows, in order. Stored on the Mac so Safari and the
/// home-screen app (which have separate storage on iOS) see the same list.
enum Launcher {
    static let file = userHome.appendingPathComponent(".thirai/launcher.json")

    /// nil = never configured (phone shows everything and suggests editing).
    static func selected() -> [String]? {
        guard let data = try? Data(contentsOf: file),
              let paths = try? JSONSerialization.jsonObject(with: data) as? [String] else { return nil }
        return paths
    }

    @discardableResult
    static func save(_ paths: [String]) throws -> Int {
        let known = Set(Apps.list().compactMap { $0["path"] })
        let clean = paths.filter { known.contains($0) }
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONSerialization.data(withJSONObject: clean, options: .prettyPrinted).write(to: file)
        return clean.count
    }
}

/// Take note: appends short notes to "Thirai notes.txt" in Documents (or ~/.thirai/notes.txt if
/// Documents can't be written) and opens the file in TextEdit when the key is held.
enum Notes {
    static let header = "Thirai notes. Times in [brackets] are how far into the stream or recording you were.\n\n"
    private static let lock = NSLock()

    private static var candidates: [URL] {
        let home = userHome
        return [home.appendingPathComponent("Documents/Thirai notes.txt"),
                home.appendingPathComponent(".thirai/notes.txt")]
    }

    /// The first notes file that exists or can be created.
    static func file() -> URL? {
        for url in candidates {
            if FileManager.default.fileExists(atPath: url.path) { return url }
            try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            if FileManager.default.createFile(atPath: url.path, contents: Data(header.utf8),
                                              attributes: [.posixPermissions: 0o600]) { return url }
        }
        return nil
    }

    /// `streamTime` is "[LIVE 1:02:13]" or "[REC 0:04:10]" while OBS is live or recording.
    static func save(_ text: String, streamTime: String? = nil) -> Bool {
        let oneLine = text.split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
        let stamp = DateFormatter()
        stamp.dateFormat = "yyyy-MM-dd HH:mm"
        let line = stamp.string(from: Date()) + (streamTime.map { "  " + $0 } ?? "") + "  " + String(oneLine.prefix(4000)) + "\n"
        lock.lock(); defer { lock.unlock() }
        guard let url = file(), let handle = try? FileHandle(forWritingTo: url) else { return false }
        defer { try? handle.close() }
        do {
            try handle.seekToEnd()
            try handle.write(contentsOf: Data(line.utf8))
            return true
        } catch { return false }
    }

    static func open() -> Bool {
        guard let url = file() else { return false }
        if let editor = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.TextEdit") {
            NSWorkspace.shared.open([url], withApplicationAt: editor, configuration: NSWorkspace.OpenConfiguration())
        } else {
            NSWorkspace.shared.open(url)
        }
        return true
    }
}
