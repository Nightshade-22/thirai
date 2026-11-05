import CryptoKit
import Foundation

/// OBS control through OBS's built-in WebSocket server (OBS 28+: Tools > WebSocket Server Settings),
/// speaking obs-websocket v5. Mirrors windows/obs.go so both servers report the same state.

typealias JSON = [String: Any]

extension Dictionary where Key == String, Value == Any {
    func str(_ k: String) -> String { self[k] as? String ?? "" }
    func bool(_ k: String) -> Bool { (self[k] as? NSNumber)?.boolValue ?? false }
    func int(_ k: String) -> Int { (self[k] as? NSNumber)?.intValue ?? 0 }
    func i64(_ k: String) -> Int64 { (self[k] as? NSNumber)?.int64Value ?? 0 }
    func dbl(_ k: String) -> Double { (self[k] as? NSNumber)?.doubleValue ?? 0 }
    func obj(_ k: String) -> JSON { self[k] as? JSON ?? [:] }
    func arr(_ k: String) -> [JSON] { self[k] as? [JSON] ?? [] }
}

struct ThiraiError: LocalizedError {
    let message: String
    init(_ m: String) { message = m }
    var errorDescription: String? { message }
}

// MARK: Connection

final class OBSClient {
    private let task: URLSessionWebSocketTask
    private let lock = NSLock()
    private var pending: [String: (Result<JSON, Error>) -> Void] = [:]
    private var nextID = 0
    private(set) var dead = false

    private init(task: URLSessionWebSocketTask) { self.task = task }

    static func connect(host: String, port: Int, password: String) throws -> OBSClient {
        guard let url = URL(string: "ws://\(host.contains(":") ? "[\(host)]" : host):\(port)") else {
            throw ThiraiError("Bad OBS address in config.json")
        }
        let task = URLSession.shared.webSocketTask(with: url, protocols: ["obswebsocket.json"])
        task.resume()
        let c = OBSClient(task: task)
        do {
            let hello = try c.receiveOne(timeout: 5)
            guard hello.int("op") == 0 else { throw ThiraiError("not an OBS WebSocket server") }
            var ident: JSON = ["rpcVersion": 1, "eventSubscriptions": 0]
            let auth = hello.obj("d").obj("authentication")
            if !auth.isEmpty {
                if password.isEmpty {
                    throw ThiraiError("OBS asks for a password: add it to config.json, or turn on authentication in OBS's WebSocket settings so Thirai can read it")
                }
                ident["authentication"] = obsAuth(password, salt: auth.str("salt"), challenge: auth.str("challenge"))
            }
            try c.sendSync(["op": 1, "d": ident])
            guard let ok = try? c.receiveOne(timeout: 5), ok.int("op") == 2 else {
                throw ThiraiError("OBS refused the connection: check the password in config.json")
            }
        } catch {
            task.cancel(with: .goingAway, reason: nil)
            throw error
        }
        c.readLoop()
        return c
    }

    private func receiveOne(timeout: Double) throws -> JSON {
        let sem = DispatchSemaphore(value: 0)
        var out: Result<JSON, Error> = .failure(ThiraiError("OBS did not answer"))
        task.receive { r in
            switch r {
            case .success(let m):
                if let j = OBSClient.decode(m) { out = .success(j) } else { out = .failure(ThiraiError("not an OBS WebSocket server")) }
            case .failure(let e): out = .failure(e)
            }
            sem.signal()
        }
        if sem.wait(timeout: .now() + timeout) == .timedOut { throw ThiraiError("OBS did not answer") }
        return try out.get()
    }

    private static func decode(_ m: URLSessionWebSocketTask.Message) -> JSON? {
        let data: Data
        switch m {
        case .string(let s): data = Data(s.utf8)
        case .data(let d): data = d
        @unknown default: return nil
        }
        return (try? JSONSerialization.jsonObject(with: data)) as? JSON
    }

    private func sendSync(_ obj: JSON) throws {
        let data = try JSONSerialization.data(withJSONObject: obj)
        let sem = DispatchSemaphore(value: 0)
        var err: Error?
        task.send(.string(String(decoding: data, as: UTF8.self))) { e in err = e; sem.signal() }
        if sem.wait(timeout: .now() + 5) == .timedOut { throw ThiraiError("OBS did not answer") }
        if let err { throw err }
    }

    private func readLoop() {
        task.receive { [weak self] r in
            guard let self else { return }
            switch r {
            case .failure:
                self.close()
            case .success(let m):
                if let msg = OBSClient.decode(m), msg.int("op") == 7 {
                    let d = msg.obj("d")
                    self.lock.lock()
                    let cb = self.pending.removeValue(forKey: d.str("requestId"))
                    self.lock.unlock()
                    cb?(.success(d))
                }
                self.readLoop()
            }
        }
    }

    func close() {
        lock.lock()
        dead = true
        let waiting = pending
        pending = [:]
        lock.unlock()
        task.cancel(with: .goingAway, reason: nil)
        for cb in waiting.values { cb(.failure(ThiraiError("OBS disconnected"))) }
    }

    /// Sends one request and returns its responseData. Blocks up to 4 seconds.
    @discardableResult
    func request(_ type: String, _ data: JSON? = nil) throws -> JSON {
        lock.lock()
        if dead { lock.unlock(); throw ThiraiError("OBS disconnected") }
        nextID += 1
        let id = String(nextID)
        let sem = DispatchSemaphore(value: 0)
        var out: Result<JSON, Error> = .failure(ThiraiError("\(type) timed out"))
        pending[id] = { r in out = r; sem.signal() }
        lock.unlock()
        var d: JSON = ["requestType": type, "requestId": id]
        if let data { d["requestData"] = data }
        do { try sendSync(["op": 6, "d": d]) } catch {
            lock.lock(); pending[id] = nil; lock.unlock()
            throw error
        }
        if sem.wait(timeout: .now() + 4) == .timedOut {
            lock.lock(); pending[id] = nil; lock.unlock()
            throw ThiraiError("\(type) timed out")
        }
        let resp = try out.get()
        let status = resp.obj("requestStatus")
        if !status.bool("result") {
            throw ThiraiError("\(type): \(status.str("comment"))")
        }
        return resp.obj("responseData")
    }
}

func obsAuth(_ password: String, salt: String, challenge: String) -> String {
    let secret = Data(SHA256.hash(data: Data((password + salt).utf8))).base64EncodedString()
    return Data(SHA256.hash(data: Data((secret + challenge).utf8))).base64EncodedString()
}

// MARK: State the phone shows, refreshed every second

struct OBSAudio { var name: String; var muted: Bool }

struct OBSState {
    var connected = false
    var error = ""
    var live = false, liveMs: Int64 = 0
    var rec = false, recPaused = false, recMs: Int64 = 0
    var replay = false, vcam = false, studio = false
    var scene = ""
    var scenes: [String] = []
    var mic: OBSAudio?
    var desktop: OBSAudio?
    var fps = 0.0, cpu = 0.0, dropped = 0.0

    var json: JSON {
        func audio(_ a: OBSAudio?) -> Any { a.map { ["name": $0.name, "muted": $0.muted] as JSON } ?? NSNull() }
        var j: JSON = ["connected": connected, "live": live, "liveMs": liveMs, "rec": rec, "recPaused": recPaused,
                       "recMs": recMs, "replay": replay, "vcam": vcam, "studio": studio, "scene": scene,
                       "scenes": scenes, "mic": audio(mic), "desktop": audio(desktop),
                       "fps": fps, "cpu": cpu, "dropped": dropped]
        if !error.isEmpty { j["error"] = error }
        return j
    }
}

final class OBS {
    private let lock = NSLock()
    private var client: OBSClient?
    private var now = OBSState()
    private let config: () -> (host: String, port: Int, password: String)

    init(config: @escaping () -> (host: String, port: Int, password: String)) { self.config = config }

    func snapshot() -> OBSState { lock.lock(); defer { lock.unlock() }; return now }
    private func set(_ s: OBSState) { lock.lock(); now = s; lock.unlock() }
    private var current: OBSClient? { lock.lock(); defer { lock.unlock() }; return client }

    func start() {
        Thread.detachNewThread { [self] in
            var slow = 0
            while true {
                var cl = current
                if cl == nil || cl!.dead {
                    let c = withLocalOBS(config())
                    do {
                        let nc = try OBSClient.connect(host: c.host, port: c.port, password: c.password)
                        lock.lock(); client = nc; lock.unlock()
                        cl = nc; slow = 0
                        print("OBS: connected")
                    } catch {
                        set(OBSState(error: obsHint(error)))
                        Thread.sleep(forTimeInterval: 5)
                        continue
                    }
                }
                guard let c = cl else { continue }
                if (try? poll(c, full: slow % 3 == 0)) == nil, c.dead {
                    lock.lock(); client = nil; lock.unlock()
                    print("OBS: disconnected")
                    set(OBSState(error: "OBS closed"))
                    continue
                }
                slow += 1
                Thread.sleep(forTimeInterval: 1)
            }
        }
    }

    /// OBS's own WebSocket settings on this Mac, so nobody has to copy the password.
    private struct Local { var found = false, enabled = false, auth = false, port = 0, password = "" }

    private func readLocal() -> Local {
        let url = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/obs-studio/plugin_config/obs-websocket/config.json")
        guard let d = try? Data(contentsOf: url), let j = (try? JSONSerialization.jsonObject(with: d)) as? JSON else { return Local() }
        return Local(found: true, enabled: j.bool("server_enabled"), auth: j.bool("auth_required"),
                     port: j.int("server_port"), password: j.str("server_password"))
    }

    /// Fills in the password and port from OBS's settings when config.json leaves them at their defaults.
    private func withLocalOBS(_ c: (host: String, port: Int, password: String)) -> (host: String, port: Int, password: String) {
        guard c.host == "127.0.0.1" || c.host == "localhost" else { return c }
        let l = readLocal()
        guard l.found else { return c }
        var out = c
        if out.password.isEmpty && l.auth { out.password = l.password }
        if out.port == 4455 && l.port > 0 { out.port = l.port }
        return out
    }

    private func obsHint(_ error: Error) -> String {
        let l = readLocal()
        if l.found && !l.enabled { return "Turn on OBS's WebSocket server: OBS > Tools > WebSocket Server Settings > Enable" }
        let ns = error as NSError
        if ns.domain == NSURLErrorDomain || ns.domain == NSPOSIXErrorDomain {
            return "OBS isn't running, or its WebSocket server is off (OBS: Tools > WebSocket Server Settings)"
        }
        return error.localizedDescription
    }

    private func audio(_ c: OBSClient, _ name: String) -> OBSAudio? {
        guard !name.isEmpty, let m = try? c.request("GetInputMute", ["inputName": name]) else { return nil }
        return OBSAudio(name: name, muted: m.bool("inputMuted"))
    }

    func poll(_ c: OBSClient, full: Bool) throws {
        var st = OBSState(connected: true)
        let stream = try c.request("GetStreamStatus")
        st.live = stream.bool("outputActive"); st.liveMs = stream.i64("outputDuration")
        let total = stream.dbl("outputTotalFrames")
        if total > 0 { st.dropped = stream.dbl("outputSkippedFrames") * 100 / total }
        if let r = try? c.request("GetRecordStatus") {
            st.rec = r.bool("outputActive"); st.recPaused = r.bool("outputPaused"); st.recMs = r.i64("outputDuration")
        }
        if let r = try? c.request("GetReplayBufferStatus") { st.replay = r.bool("outputActive") }
        if let r = try? c.request("GetVirtualCamStatus") { st.vcam = r.bool("outputActive") }
        if let r = try? c.request("GetStudioModeEnabled") { st.studio = r.bool("studioModeEnabled") }
        if let r = try? c.request("GetStats") { st.cpu = r.dbl("cpuUsage"); st.fps = r.dbl("activeFps") }

        let prev = snapshot()
        st.scenes = prev.scenes; st.scene = prev.scene; st.mic = prev.mic; st.desktop = prev.desktop
        if full || !prev.connected {
            if let sl = try? c.request("GetSceneList") {
                // OBS lists scenes bottom-up; show them in the order the OBS window does.
                st.scenes = Array(sl.arr("scenes").map { $0.str("sceneName") }.reversed())
            }
            if let sp = try? c.request("GetSpecialInputs") {
                st.mic = audio(c, sp.str("mic1"))
                st.desktop = audio(c, sp.str("desktop1"))
            }
        } else {
            if let m = st.mic { st.mic = audio(c, m.name) }
            if let d = st.desktop { st.desktop = audio(c, d.name) }
        }
        if let cur = try? c.request("GetCurrentProgramScene") { st.scene = cur.str("currentProgramSceneName") }
        set(st)
    }

    /// Runs one OBS button. Returns a short message for the phone.
    func action(_ action: String) throws -> String {
        guard let c = current, !c.dead else { throw ThiraiError("OBS is not connected") }
        try? poll(c, full: false)   // act on OBS's current state, not the last poll
        let st = snapshot()
        defer { try? poll(c, full: false) }
        switch action {
        case "stream":
            try c.request("ToggleStream"); return st.live ? "Ending stream" : "Going live"
        case "record":
            try c.request("ToggleRecord"); return st.rec ? "Recording stopped" : "Recording"
        case "pause":
            guard st.rec else { throw ThiraiError("Not recording") }
            try c.request("ToggleRecordPause"); return st.recPaused ? "Recording resumed" : "Recording paused"
        case "replay":
            if !st.replay {
                // Start the replay buffer the first time; the next tap saves a clip.
                try c.request("StartReplayBuffer"); return "Replay buffer on: tap again to save a clip"
            }
            try c.request("SaveReplayBuffer"); return "Clip saved"
        case "vcam":
            try c.request("ToggleVirtualCam"); return st.vcam ? "Virtual camera off" : "Virtual camera on"
        case "studio":
            if st.studio { try c.request("TriggerStudioModeTransition"); return "Transition" }
            try c.request("SetStudioModeEnabled", ["studioModeEnabled": true]); return "Studio mode on: tap again to transition"
        case "mic", "desktop":
            guard let a = action == "mic" ? st.mic : st.desktop else { throw ThiraiError("No \(action) audio source in OBS") }
            try c.request("ToggleInputMute", ["inputName": a.name])
            return a.muted ? "\(a.name) on" : "\(a.name) muted"
        default:
            throw ThiraiError("unknown OBS action")
        }
    }

    func setScene(_ name: String) throws {
        guard let c = current, !c.dead else { throw ThiraiError("OBS is not connected") }
        try c.request("SetCurrentProgramScene", ["sceneName": name])
        DispatchQueue.global().asyncAfter(deadline: .now() + 0.15) { [self] in try? poll(c, full: false) }
    }

    /// Only scene names OBS itself reported, so the phone can't send arbitrary text.
    func isKnownScene(_ name: String) -> Bool { snapshot().scenes.contains(name) }
}
