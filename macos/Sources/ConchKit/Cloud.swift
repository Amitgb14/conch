import Foundation

/// Conch Cloud in the app: the account and its machines, all of it asked
/// of the conch binary (`conch account -json`, `conch cloud machine ls
/// -json`, `conch login -json`), so the sign-in and its tokens are conch's
/// alone and the app has no second copy of the device flow.
///
/// A conch that has never signed in has no cloud.json, and then nothing is
/// asked of conch or the network: `CloudModel.signedIn` reads the file's
/// presence, which is what conch itself goes by.
public enum CloudModel {
    /// cloud.json in conch's folder: there exactly while signed in.
    public static func accountFile(conchDir: String) -> String {
        (conchDir as NSString).appendingPathComponent("cloud.json")
    }

    public static func signedIn(conchDir: String, fileManager: FileManager = .default) -> Bool {
        fileManager.fileExists(atPath: accountFile(conchDir: conchDir))
    }

    /// The production server, when Settings names none.
    public static let defaultServer = "https://api.cloud.conch.dev"

    /// Hours, to a tenth: "2 h", "12.5 h".
    public static func hours(_ seconds: Int) -> String {
        let h = Double(seconds) / 3600
        if h == h.rounded() { return "\(Int(h)) h" }
        return String(format: "%.1f h", h)
    }

    /// "2 h of 100 h", "12.5 of 100 GB".
    public static func allowanceLines(_ a: CloudAllowance) -> (hours: String, storage: String) {
        let st = a.storageGBUsed == a.storageGBUsed.rounded() ? "\(Int(a.storageGBUsed))" : String(format: "%.1f", a.storageGBUsed)
        return ("\(hours(a.runningSecondsUsed)) of \(hours(a.runningSeconds))", "\(st) of \(a.storageGB) GB")
    }

    /// The share of the month's hours used, 0…1, for a bar.
    public static func usedFraction(_ a: CloudAllowance) -> Double {
        guard a.runningSeconds > 0 else { return 0 }
        return min(1, max(0, Double(a.runningSecondsUsed) / Double(a.runningSeconds)))
    }
}

/// `conch account -json`.
public struct CloudAccount: Decodable, Equatable, Sendable {
    public var signedIn: Bool
    public var api: String
    public var deviceID: String?
    public var me: CloudMe?
    public var devices: [CloudDevice]?
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case signedIn = "signed_in", api, deviceID = "device_id", me, devices, error
    }

    public init(signedIn: Bool, api: String, deviceID: String? = nil, me: CloudMe? = nil, devices: [CloudDevice]? = nil, error: String? = nil) {
        self.signedIn = signedIn
        self.api = api
        self.deviceID = deviceID
        self.me = me
        self.devices = devices
        self.error = error
    }

    /// This device, as the account lists it.
    public var thisDevice: CloudDevice? { devices?.first(where: { $0.current }) }
}

public struct CloudMe: Decodable, Equatable, Sendable {
    public var id: String
    public var handle: String
    public var email: String?
    public var plan: String
    public var allowance: CloudAllowance
}

public struct CloudAllowance: Decodable, Equatable, Sendable {
    public var runningSeconds: Int
    public var runningSecondsUsed: Int
    public var storageGB: Int
    public var storageGBUsed: Double
    public var maxMachines: Int

    enum CodingKeys: String, CodingKey {
        case runningSeconds = "running_seconds", runningSecondsUsed = "running_seconds_used"
        case storageGB = "storage_gb", storageGBUsed = "storage_gb_used", maxMachines = "max_machines"
    }

    public init(runningSeconds: Int, runningSecondsUsed: Int, storageGB: Int, storageGBUsed: Double, maxMachines: Int) {
        self.runningSeconds = runningSeconds
        self.runningSecondsUsed = runningSecondsUsed
        self.storageGB = storageGB
        self.storageGBUsed = storageGBUsed
        self.maxMachines = maxMachines
    }
}

public struct CloudDevice: Decodable, Equatable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var client: String
    public var current: Bool
}

/// A cloud machine, as `conch cloud machine ls -json` says it.
public struct CloudMachine: Decodable, Equatable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var size: String
    public var region: String
    public var state: String
    public var stateDetail: String?
    public var provider: CloudProvider?
    public var webURL: String?

    enum CodingKeys: String, CodingKey {
        case id, name, size, region, state, provider
        case stateDetail = "state_detail", webURL = "web_url"
    }

    public init(id: String, name: String, size: String, region: String, state: String,
                stateDetail: String? = nil, provider: CloudProvider? = nil, webURL: String? = nil) {
        self.id = id
        self.name = name
        self.size = size
        self.region = region
        self.state = state
        self.stateDetail = stateDetail
        self.provider = provider
        self.webURL = webURL
    }

    /// What it can be asked to do now: wake one that sleeps, sleep one
    /// that runs. Anything on its way somewhere is left to get there.
    public var canWake: Bool { ["asleep", "archived"].contains(state) }
    public var canSleep: Bool { state == "running" }

    /// Its state in words, with why for an error.
    public var stateText: String {
        if state == "error", let d = stateDetail, !d.isEmpty { return "error: \(d)" }
        return state
    }

    /// How its state is drawn: on, moving, resting or wrong.
    public var tone: CloudTone {
        switch state {
        case "running": return .on
        case "waking", "creating", "sleeping", "destroying": return .moving
        case "error": return .error
        default: return .resting
        }
    }

    /// "standard · us-west · Tensorlake".
    public var subtitle: String {
        [size, region, provider?.name].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
    }
}

public struct CloudProvider: Decodable, Equatable, Sendable {
    public var id: String
    public var name: String
    public var kind: String
}

public enum CloudTone: Equatable, Sendable { case on, moving, resting, error }

public struct CloudMachines: Decodable, Sendable {
    public var machines: [CloudMachine]
}

/// One line of `conch login -json`.
public struct CloudLoginEvent: Decodable, Equatable, Sendable {
    public var event: String
    public var userCode: String?
    public var verificationURI: String?
    public var verificationURIComplete: String?
    public var expiresIn: Int?
    public var api: String?
    public var handle: String?
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case event, api, handle, error
        case userCode = "user_code", verificationURI = "verification_uri"
        case verificationURIComplete = "verification_uri_complete", expiresIn = "expires_in"
    }

    /// The page to approve the code at: with the code in it when the server
    /// gives one.
    public var link: String? { verificationURIComplete ?? verificationURI }

    public static func parse(_ line: String) -> CloudLoginEvent? {
        guard let d = line.data(using: .utf8) else { return nil }
        return try? JSONDecoder().decode(CloudLoginEvent.self, from: d)
    }
}

/// Where a sign-in is.
public enum CloudLoginState: Equatable, Sendable {
    case idle
    case starting
    case code(String, link: String?)
    case failed(String)
    case done

    /// The state after an event of `conch login -json`.
    public func after(_ e: CloudLoginEvent) -> CloudLoginState {
        switch e.event {
        case "code": return .code(e.userCode ?? "", link: e.link)
        case "signed_in": return .done
        case "error": return .failed(e.error ?? "the sign-in failed")
        default: return self
        }
    }

    /// The state when conch exits with status, having said nothing final.
    public func exited(_ status: Int32) -> CloudLoginState {
        switch self {
        case .done, .failed: return self
        default: return status == 0 ? .done : .failed("conch login stopped (status \(status))")
        }
    }
}

/// `conch login -json` run for the app, which can be stopped.
public final class CloudLoginRun: @unchecked Sendable {
    private let process = Process()

    public init() {}

    /// Starts it, handing each event to onEvent and the exit status to
    /// onExit, both off the main thread.
    public func start(binary: String, onEvent: @escaping @Sendable (CloudLoginEvent) -> Void,
                      onExit: @escaping @Sendable (Int32) -> Void) throws {
        process.executableURL = URL(fileURLWithPath: binary)
        process.arguments = ["login", "-json", "-no-browser", "-client", "mac_app"]
        process.environment = conchEnvironment()
        let out = Pipe()
        process.standardOutput = out
        process.standardError = FileHandle.nullDevice
        process.standardInput = FileHandle.nullDevice
        let buffer = LineBuffer()
        out.fileHandleForReading.readabilityHandler = { h in
            let d = h.availableData
            if d.isEmpty { return }
            for line in buffer.add(d) { if let e = CloudLoginEvent.parse(line) { onEvent(e) } }
        }
        process.terminationHandler = { proc in
            out.fileHandleForReading.readabilityHandler = nil
            let rest = out.fileHandleForReading.readDataToEndOfFile()
            for line in buffer.add(rest) + buffer.flush() { if let e = CloudLoginEvent.parse(line) { onEvent(e) } }
            onExit(proc.terminationStatus)
        }
        try process.run()
    }

    public func cancel() {
        if process.isRunning { process.terminate() }
    }
}
