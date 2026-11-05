import AppKit

/// Twitch: sign-in with Twitch's device code flow (the user approves on twitch.tv, Thirai never sees
/// the password), then Helix API calls for status, clips, markers, ads, chat and chat modes.
/// Mirrors windows/twitch.go; the token file in ~/.thirai/twitch.json has the same format.

private let twitchScopes = "clips:edit channel:manage:broadcast channel:edit:commercial user:write:chat " +
    "moderator:manage:chat_settings moderator:read:followers"

struct TwitchState {
    var configured = false, connected = false
    var login = "", code = "", codeURL = "", error = ""
    var live = false, viewers = 0, followers = 0
    var title = "", game = ""
    var emoteOnly = false, slow = false, followOnly = false, subOnly = false

    var json: JSON {
        var j: JSON = ["configured": configured, "connected": connected, "live": live, "viewers": viewers,
                       "followers": followers, "emoteOnly": emoteOnly, "slow": slow, "followOnly": followOnly,
                       "subOnly": subOnly]
        for (k, v) in [("login", login), ("code", code), ("codeUrl", codeURL), ("error", error), ("title", title), ("game", game)]
            where !v.isEmpty { j[k] = v }
        return j
    }
}

private struct TwitchToken: Codable {
    var client_id: String
    var access_token: String
    var refresh_token: String
    var expires_at: Int64
    var user_id: String
    var login: String
}

/// A blocking HTTP call (Thirai calls Twitch from its own background threads).
func httpSync(_ req: URLRequest) throws -> (Int, Data) {
    let sem = DispatchSemaphore(value: 0)
    var out: Result<(Int, Data), Error> = .failure(ThiraiError("no answer"))
    var r = req
    r.timeoutInterval = 10
    URLSession.shared.dataTask(with: r) { data, resp, err in
        if let err { out = .failure(err) } else {
            out = .success(((resp as? HTTPURLResponse)?.statusCode ?? 0, data ?? Data()))
        }
        sem.signal()
    }.resume()
    _ = sem.wait(timeout: .now() + 12)
    return try out.get()
}

func formEncode(_ pairs: [(String, String)]) -> Data {
    var allowed = CharacterSet.alphanumerics
    allowed.insert(charactersIn: "-._~")
    return Data(pairs.map { "\($0.0)=\($0.1.addingPercentEncoding(withAllowedCharacters: allowed) ?? "")" }
        .joined(separator: "&").utf8)
}

final class Twitch {
    private let lock = NSLock()
    private var tok: TwitchToken?
    private var now = TwitchState()
    private var pendingSignIn = false
    private let poke = DispatchSemaphore(value: 0)
    private let clientID: () -> String
    private let tokenFile: URL

    init(configDir: URL, clientID: @escaping () -> String) {
        self.clientID = clientID
        tokenFile = configDir.appendingPathComponent("twitch.json")
    }

    private var idBase: String { ProcessInfo.processInfo.environment["THIRAI_TWITCH_ID_URL"] ?? "https://id.twitch.tv" }
    private var apiBase: String { ProcessInfo.processInfo.environment["THIRAI_TWITCH_API_URL"] ?? "https://api.twitch.tv/helix" }
    private var pollEvery: Double { Double(ProcessInfo.processInfo.environment["THIRAI_TWITCH_POLL"] ?? "") ?? 15 }

    func snapshot() -> TwitchState { lock.lock(); defer { lock.unlock() }; return now }
    private func update(_ f: (inout TwitchState) -> Void) { lock.lock(); f(&now); lock.unlock() }

    func start() {
        Thread.detachNewThread { [self] in
            while true {
                poll()
                _ = poke.wait(timeout: .now() + pollEvery)
            }
        }
    }

    private func loadToken(_ id: String) -> TwitchToken? {
        guard let d = try? Data(contentsOf: tokenFile), let t = try? JSONDecoder().decode(TwitchToken.self, from: d),
              !t.access_token.isEmpty, t.client_id == id else { return nil }   // another Twitch app: sign in again
        return t
    }

    private func saveToken(_ t: TwitchToken) {
        let enc = JSONEncoder(); enc.outputFormatting = .prettyPrinted
        guard let d = try? enc.encode(t) else { return }
        try? d.write(to: tokenFile)
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tokenFile.path)
    }

    func poll() {
        let id = clientID()
        lock.lock()
        if id.isEmpty { tok = nil; now = TwitchState(); lock.unlock(); return }
        if tok == nil || tok!.client_id != id { tok = loadToken(id) }
        let t = tok
        let pending = pendingSignIn
        lock.unlock()
        guard let t else {
            update { s in
                s = TwitchState(configured: true, code: pending ? s.code : "", codeURL: pending ? s.codeURL : "", error: s.error)
            }
            return
        }
        var st = TwitchState(configured: true, connected: true, login: t.login)
        do {
            let streams = try api("GET", "/streams?user_id=\(t.user_id)").arr("data")
            if let s = streams.first {
                st.live = true; st.viewers = s.int("viewer_count"); st.title = s.str("title"); st.game = s.str("game_name")
            } else if let ch = try? api("GET", "/channels?broadcaster_id=\(t.user_id)").arr("data").first {
                st.title = ch.str("title"); st.game = ch.str("game_name")
            }
        } catch let e as ThiraiError where e.message == Twitch.signedOut {
            return
        } catch {
            st.error = error.localizedDescription
        }
        if let f = try? api("GET", "/channels/followers?first=1&broadcaster_id=\(t.user_id)") { st.followers = f.int("total") }
        if let c = try? api("GET", "/chat/settings?broadcaster_id=\(t.user_id)&moderator_id=\(t.user_id)").arr("data").first {
            st.emoteOnly = c.bool("emote_mode"); st.slow = c.bool("slow_mode")
            st.followOnly = c.bool("follower_mode"); st.subOnly = c.bool("subscriber_mode")
        }
        update { $0 = st }
    }

    // MARK: Sign-in (device code grant, public client)

    private func form(_ endpoint: String, _ pairs: [(String, String)]) throws -> JSON {
        var req = URLRequest(url: URL(string: idBase + endpoint)!)
        req.httpMethod = "POST"
        req.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        req.httpBody = formEncode(pairs)
        let (code, data) = try httpSync(req)
        let j = ((try? JSONSerialization.jsonObject(with: data)) as? JSON) ?? [:]
        if code >= 300 || code == 0 {
            let m = j.str("message")
            throw ThiraiError(m.isEmpty ? "HTTP \(code)" : m)
        }
        return j
    }

    /// Starts sign-in (once) and returns what to show the user.
    func connect() throws -> String {
        let id = clientID()
        guard !id.isEmpty else { throw ThiraiError("Add your Twitch client ID to config.json first") }
        lock.lock()
        if pendingSignIn { let c = now.code; lock.unlock(); return "Enter \(c) at twitch.tv/activate" }
        pendingSignIn = true
        lock.unlock()
        let dev: JSON
        do {
            dev = try form("/oauth2/device", [("client_id", id), ("scopes", twitchScopes)])
        } catch {
            lock.lock(); pendingSignIn = false; lock.unlock()
            if error.localizedDescription.lowercased().contains("client") {
                throw ThiraiError("Twitch rejected the client ID in config.json (\(error.localizedDescription))")
            }
            throw error
        }
        let userCode = dev.str("user_code"), uri = dev.str("verification_uri")
        update { $0.code = userCode; $0.codeURL = uri; $0.error = "" }
        print("Twitch: approve Thirai at \(uri) (code \(userCode))")
        if let u = URL(string: uri), u.scheme == "https" || u.scheme == "http" {
            DispatchQueue.main.async { NSWorkspace.shared.open(u) }   // the page on the Mac opens with the code filled in
        }
        let deviceCode = dev.str("device_code"), iv = dev.int("interval"), expires = dev.int("expires_in")
        Thread.detachNewThread { [self] in waitForApproval(id, deviceCode, iv < 1 ? 5 : iv, expires) }
        return "Approve on the Mac, or enter \(userCode) at twitch.tv/activate"
    }

    private func waitForApproval(_ clientID: String, _ deviceCode: String, _ interval: Int, _ expiresIn: Int) {
        defer {
            lock.lock(); pendingSignIn = false; lock.unlock()
            poke.signal()
        }
        let deadline = Date().addingTimeInterval(Double(expiresIn))
        while Date() < deadline {
            Thread.sleep(forTimeInterval: Double(interval))
            let j: JSON
            do {
                j = try form("/oauth2/token", [("client_id", clientID), ("scopes", twitchScopes), ("device_code", deviceCode),
                                               ("grant_type", "urn:ietf:params:oauth:grant-type:device_code")])
            } catch {
                let m = error.localizedDescription
                if m.contains("authorization_pending") || m.contains("slow_down") { continue }
                update { $0.code = ""; $0.codeURL = ""; $0.error = "Twitch sign-in failed: " + m }
                return
            }
            var t = TwitchToken(client_id: clientID, access_token: j.str("access_token"), refresh_token: j.str("refresh_token"),
                                expires_at: Int64(Date().timeIntervalSince1970) + Int64(j.int("expires_in")), user_id: "", login: "")
            do { try validate(&t) } catch {
                update { $0.code = ""; $0.codeURL = ""; $0.error = "Twitch sign-in failed: " + error.localizedDescription }
                return
            }
            saveToken(t)
            lock.lock(); tok = t; lock.unlock()
            update { $0.code = ""; $0.codeURL = ""; $0.error = "" }
            print("Twitch: signed in as \(t.login)")
            return
        }
        update { $0.code = ""; $0.codeURL = ""; $0.error = "The Twitch code expired: tap a Twitch button to get a new one" }
    }

    private func validate(_ t: inout TwitchToken) throws {
        var req = URLRequest(url: URL(string: idBase + "/oauth2/validate")!)
        req.setValue("OAuth " + t.access_token, forHTTPHeaderField: "Authorization")
        let (code, data) = try httpSync(req)
        guard code == 200, let j = (try? JSONSerialization.jsonObject(with: data)) as? JSON else {
            throw ThiraiError("token not accepted")
        }
        t.login = j.str("login"); t.user_id = j.str("user_id")
    }

    static let signedOut = "Twitch sign-in expired: tap a Twitch button to sign in again"

    private func refresh(_ t: inout TwitchToken) throws {
        let j = try form("/oauth2/token", [("client_id", t.client_id), ("grant_type", "refresh_token"), ("refresh_token", t.refresh_token)])
        t.access_token = j.str("access_token")
        t.expires_at = Int64(Date().timeIntervalSince1970) + Int64(j.int("expires_in"))
        if !j.str("refresh_token").isEmpty { t.refresh_token = j.str("refresh_token") }
        saveToken(t)
        lock.lock(); tok = t; lock.unlock()
    }

    private func signOut() {
        lock.lock(); tok = nil; lock.unlock()
        try? FileManager.default.removeItem(at: tokenFile)
        update { $0 = TwitchState(configured: true, error: Twitch.signedOut) }
    }

    /// Calls Helix as the signed-in user, refreshing the token once if needed.
    @discardableResult
    private func api(_ method: String, _ path: String, _ body: JSON? = nil) throws -> JSON {
        lock.lock(); var t = tok; lock.unlock()
        guard t != nil else { throw ThiraiError("Twitch is not connected") }
        if t!.expires_at > 0 && Int64(Date().timeIntervalSince1970) > t!.expires_at - 60 {
            do { try refresh(&t!) } catch { signOut(); throw ThiraiError(Twitch.signedOut) }
        }
        for attempt in 0..<2 {
            var req = URLRequest(url: URL(string: apiBase + path)!)
            req.httpMethod = method
            req.setValue("Bearer " + t!.access_token, forHTTPHeaderField: "Authorization")
            req.setValue(t!.client_id, forHTTPHeaderField: "Client-Id")
            if let body {
                req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                req.httpBody = try JSONSerialization.data(withJSONObject: body)
            }
            let (code, data) = try httpSync(req)
            if code == 401 && attempt == 0 {
                do { try refresh(&t!) } catch { signOut(); throw ThiraiError(Twitch.signedOut) }
                continue
            }
            let j = ((try? JSONSerialization.jsonObject(with: data)) as? JSON) ?? [:]
            if code >= 300 || code == 0 {
                let m = j.str("message")
                throw ThiraiError(m.isEmpty ? "HTTP \(code)" : m)
            }
            return j
        }
        throw ThiraiError(Twitch.signedOut)
    }

    // MARK: Button actions

    func action(_ b: Button) throws -> String {
        let st = snapshot()
        lock.lock(); let t = tok; lock.unlock()
        guard st.connected, let uid = t?.user_id else { return try connect() }
        defer { poke.signal() }
        switch b.action ?? "" {
        case "clip":
            guard st.live else { throw ThiraiError("Clips need you to be live") }
            try api("POST", "/clips?broadcaster_id=\(uid)")
            return "Clip created"
        case "marker":
            guard st.live else { throw ThiraiError("Markers need you to be live") }
            let desc = (b.text ?? "").isEmpty ? "Thirai marker" : b.text!
            try api("POST", "/streams/markers", ["user_id": uid, "description": desc])
            return "Marker added"
        case "ad":
            guard st.live else { throw ThiraiError("Ads need you to be live") }
            var secs = Int(b.value ?? "") ?? 60
            if secs <= 0 { secs = 60 }
            let r = try api("POST", "/channels/commercial", ["broadcaster_id": uid, "length": secs])
            if let m = r.arr("data").first?.str("message"), !m.isEmpty { return m }
            return "Running a \(secs) s ad"
        case "chat":
            let text = (b.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { throw ThiraiError("This chat button has no text in config.json") }
            let r = try api("POST", "/chat/messages", ["broadcaster_id": uid, "sender_id": uid, "message": b.text!])
            if let d = r.arr("data").first, !d.bool("is_sent") {
                let m = d.obj("drop_reason").str("message")
                throw ThiraiError(m.isEmpty ? "Twitch dropped the message" : "Twitch dropped the message: " + m)
            }
            return "Sent to chat"
        case "emoteonly", "slow", "followers", "subonly":
            var body: JSON = [:]
            var field = "", name = "", on = false
            switch b.action! {
            case "emoteonly": field = "emote_mode"; on = !st.emoteOnly; name = "Emote-only"
            case "slow":
                field = "slow_mode"; on = !st.slow; name = "Slow mode"
                if on {
                    var secs = Int(b.value ?? "") ?? 30
                    if secs < 3 { secs = 30 }
                    body["slow_mode_wait_time"] = secs
                }
            case "followers": field = "follower_mode"; on = !st.followOnly; name = "Followers-only"
            default: field = "subscriber_mode"; on = !st.subOnly; name = "Sub-only"
            }
            body[field] = on
            try api("PATCH", "/chat/settings?broadcaster_id=\(uid)&moderator_id=\(uid)", body)
            poll()
            return name + (on ? " on" : " off")
        default:
            throw ThiraiError("unknown Twitch action \(b.action ?? "")")
        }
    }
}
