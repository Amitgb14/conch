import Foundation

/// Where conch's server listens: $CONCH_SOCKET, else conch.sock in
/// $CONCH_HOME or ~/.config/conch (internal/config.SocketPath).
public func conchSocketPath(env: [String: String] = ProcessInfo.processInfo.environment,
                            home: String = NSHomeDirectory()) -> String {
    if let s = env["CONCH_SOCKET"], !s.isEmpty { return s }
    let dir = env["CONCH_HOME"].flatMap { $0.isEmpty ? nil : $0 } ?? (home as NSString).appendingPathComponent(".config/conch")
    return (dir as NSString).appendingPathComponent("conch.sock")
}

public struct ConchError: Error, CustomStringConvertible, Equatable {
    public var code: String
    public var message: String
    public var description: String { message }
    public init(code: String, message: String) {
        self.code = code
        self.message = message
    }
}

/// A client of conch's server: newline-delimited JSON over its unix
/// socket, as internal/client speaks it. Calls are answered by id; pushes
/// (events) go to onEvent on the queue given, in the order they came.
public final class Connection: @unchecked Sendable {
    /// Where messages are read from and written to: one socket, or a
    /// bridge process's stdout and stdin.
    private let fd: Int32
    private let wfd: Int32
    private let process: Process?
    private let lock = NSLock()
    private var nextID = 1
    private var pending: [String: (Result<Data, Error>) -> Void] = [:]
    private var closed = false
    private let queue: DispatchQueue

    /// Events: the event's name and its data.
    public var onEvent: ((String, Data) -> Void)?
    /// Called once when the connection goes, with why.
    public var onClose: ((Error?) -> Void)?

    public private(set) var hello: HelloResult?

    /// Connects and says hello. It does not start a server.
    public init(path: String, client: String = "conch-mac", queue: DispatchQueue = .main) throws {
        self.queue = queue
        process = nil
        fd = socket(AF_UNIX, SOCK_STREAM, 0)
        wfd = fd
        if fd < 0 { throw ConchError(code: "socket", message: "no socket: \(String(cString: strerror(errno)))") }
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            close(fd)
            throw ConchError(code: "socket", message: "socket path too long: \(path)")
        }
        withUnsafeMutableBytes(of: &addr.sun_path) { buf in
            for (i, b) in bytes.enumerated() { buf[i] = b }
            buf[bytes.count] = 0
        }
        let ok = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        if ok != 0 {
            let why = String(cString: strerror(errno))
            close(fd)
            throw ConchError(code: "connect", message: "conch's server is not answering at \(path) (\(why))")
        }
        noSigPipe(fd)
        let reader = Thread { [weak self] in self?.readLoop() }
        reader.name = "conch-socket"
        reader.start()
    }

    /// Speaks the protocol through a program's stdin and stdout — `conch
    /// machine bridge ID`, for a machine other than this one. What it says
    /// on stderr is kept, to say why when it ends before answering.
    public init(command: String, args: [String], environment: [String: String], queue: DispatchQueue = .main) throws {
        self.queue = queue
        let p = Process()
        p.executableURL = URL(fileURLWithPath: command)
        p.arguments = args
        p.environment = environment
        let input = Pipe(), output = Pipe(), errors = Pipe()
        p.standardInput = input
        p.standardOutput = output
        p.standardError = errors
        try p.run()
        process = p
        stderr = errors
        fd = output.fileHandleForReading.fileDescriptor
        wfd = input.fileHandleForWriting.fileDescriptor
        pipes = [input, output, errors]
        noSigPipe(wfd)
        let reader = Thread { [weak self] in self?.readLoop() }
        reader.name = "conch-bridge"
        reader.start()
    }

    /// Whether a write to a gone peer is an error here rather than SIGPIPE
    /// (noSigPipe): the test runner ignores the signal itself, so only the
    /// descriptor can say.
    public var writesWithoutSigPipe: Bool { fcntl(wfd, F_GETNOSIGPIPE) == 1 }

    private var pipes: [Pipe] = []
    private var stderr: Pipe?

    /// What the bridge said on stderr, once it has ended.
    public var bridgeError: String {
        guard let e = stderr, process?.isRunning == false else { return "" }
        let data = e.fileHandleForReading.availableData
        return String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }

    deinit { shutdown() }

    /// Says hello, as every client must first.
    public func handshake(version: String = "mac") async throws {
        struct Hello: Encodable {
            var client: String
            var version: String
            var `protocol`: Int
            var capabilities: [String]
        }
        hello = try await call("hello", Hello(client: "conch-mac", version: version, protocol: 1, capabilities: []), as: HelloResult.self)
    }

    public func call<R: Decodable>(_ method: String, _ params: some Encodable, as: R.Type) async throws -> R {
        let data = try await callRaw(method, try JSONEncoder().encode(params))
        return try conchDecoder().decode(R.self, from: data.isEmpty ? Data("{}".utf8) : data)
    }

    public func call(_ method: String, _ params: some Encodable) async throws {
        _ = try await callRaw(method, try JSONEncoder().encode(params))
    }

    public func call<R: Decodable>(_ method: String, as: R.Type) async throws -> R {
        let data = try await callRaw(method, nil)
        return try conchDecoder().decode(R.self, from: data.isEmpty ? Data("{}".utf8) : data)
    }

    private func callRaw(_ method: String, _ params: Data?) async throws -> Data {
        try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Data, Error>) in
            lock.lock()
            if closed {
                lock.unlock()
                cont.resume(throwing: ConchError(code: "closed", message: "the connection to conch's server closed"))
                return
            }
            let id = String(nextID)
            nextID += 1
            pending[id] = { cont.resume(with: $0) }
            lock.unlock()
            var msg: [String: Any] = ["id": id, "method": method]
            if let p = params, let obj = try? JSONSerialization.jsonObject(with: p, options: [.fragmentsAllowed]) {
                msg["params"] = obj
            }
            if !write(msg) {
                finish(id, .failure(ConchError(code: "closed", message: "the connection to conch's server closed")))
            }
        }
    }

    /// A message the server does not answer.
    public func notify(_ method: String, _ params: some Encodable) {
        var msg: [String: Any] = ["method": method]
        if let p = try? JSONEncoder().encode(params), let obj = try? JSONSerialization.jsonObject(with: p) {
            msg["params"] = obj
        }
        _ = write(msg)
    }

    private func finish(_ id: String, _ r: Result<Data, Error>) {
        lock.lock()
        let f = pending.removeValue(forKey: id)
        lock.unlock()
        f?(r)
    }

    private func write(_ msg: [String: Any]) -> Bool {
        guard var line = try? JSONSerialization.data(withJSONObject: msg) else { return false }
        line.append(0x0a)
        lock.lock()
        defer { lock.unlock() }
        if closed { return false }
        return line.withUnsafeBytes { buf in
            var off = 0
            while off < buf.count {
                let n = Darwin.write(wfd, buf.baseAddress! + off, buf.count - off)
                if n < 0 && errno == EINTR { continue }
                if n <= 0 { return false }
                off += n
            }
            return true
        }
    }

    private func readLoop() {
        var buf = [UInt8](repeating: 0, count: 65536)
        var acc = Data()
        while true {
            let n = read(fd, &buf, buf.count)
            if n <= 0 { break }
            acc.append(contentsOf: buf[0..<n])
            while let nl = acc.firstIndex(of: 0x0a) {
                let line = acc[acc.startIndex..<nl]
                acc.removeSubrange(acc.startIndex...nl)
                handle(Data(line))
            }
        }
        shutdown()
    }

    /// One line from the server: an answer to a call, or an event.
    func handle(_ line: Data) {
        guard let obj = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any] else { return }
        if let event = obj["event"] as? String {
            let data = obj["data"].flatMap { try? JSONSerialization.data(withJSONObject: $0, options: [.fragmentsAllowed]) } ?? Data()
            queue.async { [weak self] in self?.onEvent?(event, data) }
            return
        }
        guard let id = obj["id"] as? String else { return }
        if let err = obj["error"] as? [String: Any] {
            finish(id, .failure(ConchError(code: err["code"] as? String ?? "error", message: err["message"] as? String ?? "error")))
            return
        }
        let data = obj["result"].flatMap { try? JSONSerialization.data(withJSONObject: $0, options: [.fragmentsAllowed]) } ?? Data()
        finish(id, .success(data))
    }

    public func shutdown() {
        lock.lock()
        if closed {
            lock.unlock()
            return
        }
        closed = true
        let waiting = pending
        pending = [:]
        lock.unlock()
        if let p = process {
            if p.isRunning { p.terminate() }
            try? pipes[0].fileHandleForWriting.close()
        } else {
            Darwin.shutdown(fd, SHUT_RDWR)
            close(fd)
        }
        for (_, f) in waiting { f(.failure(ConchError(code: "closed", message: "the connection to conch's server closed"))) }
        queue.async { [weak self] in self?.onClose?(nil) }
    }
}

/// A write to a peer that has gone — a server reloading or stopped, a
/// bridge's ssh that died — is an error to return, not SIGPIPE, which ends
/// the whole app. Darwin's per-descriptor switch, for a socket or a pipe.
func noSigPipe(_ fd: Int32) {
    _ = fcntl(fd, F_SETNOSIGPIPE, 1)
}
