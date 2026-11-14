import AppKit
import ConchKit
import SwiftUI

// Conch.app's own updates (#204). Once a day — and from Conch → Check for
// Updates… — the conch inside the app asks for a newer release
// (`conch app update check`); one found is said in the status bar and a
// notification, and Install has that conch download, verify and put it in
// place of this bundle (`conch app update install`) before the app
// relaunches. When to ask and what an answer does are ConchKit's
// (AppUpdate.swift); the asking and installing are Go's, tested there.

@MainActor
final class AppUpdater: ObservableObject {
    /// The app's one: every window's status bar shows it.
    static let shared = AppUpdater()

    /// A newer release the status bar offers.
    @Published var offer: AppUpdateCheck?
    /// The window (Store.windowID) its sheet is open in: the one that
    /// asked, so a sheet does not open in every window at once.
    @Published var sheetFor: String?
    /// The version being installed, while it is.
    @Published var installing: String?
    /// Installed unasked ([app] auto_update): it runs from the next launch.
    @Published var installed: String?
    /// The first launch of an update: offered a reload of the server onto
    /// this app's conch, so its panes and agents carry on on the new one.
    @Published var reloadOffer: String?

    /// The first window's store, which says what the daily check finds.
    weak var store: Store?
    private var timer: Timer?
    private var checking = false
    private let defaults = UserDefaults.standard

    private enum Key {
        static let lastCheck = "update.lastCheck"
        static let skipped = "update.skipped"
        static let notified = "update.notified"
        static let installed = "update.installed"
    }

    var version: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "" }
    var bundlePath: String { Bundle.main.bundlePath }
    var prefs: AppUpdatePrefs {
        AppUpdatePrefs(toml: store.flatMap { try? String(contentsOfFile: ($0.conchDir as NSString).appendingPathComponent("config.toml"), encoding: .utf8) })
    }
    private var schedule: UpdateSchedule { UpdateSchedule(last: defaults.object(forKey: Key.lastCheck) as? Date) }

    /// The bundle's conch: what is installed is this bundle, so the conch
    /// that installs it is its own, not one on PATH.
    private var conch: String? {
        let inside = (Bundle.main.resourcePath.map { ($0 as NSString).appendingPathComponent("conch") })
        if let inside, FileManager.default.isExecutableFile(atPath: inside) { return inside }
        return store?.binary
    }

    /// Whether an update left an app behind to go back to.
    var canRollBack: Bool { FileManager.default.fileExists(atPath: previousAppPath(bundlePath)) }

    func start() {
        if updatedSince(installed: defaults.string(forKey: Key.installed), running: version) {
            reloadOffer = version
        }
        defaults.removeObject(forKey: Key.installed)
        // A minute after launch, then hourly; the schedule says whether a
        // day has passed since the last asking.
        DispatchQueue.main.asyncAfter(deadline: .now() + 60) { [weak self] in self?.tick() }
        timer = Timer.scheduledTimer(withTimeInterval: 3600, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
    }

    func tick() {
        guard schedule.due(now: Date(), prefs: prefs, version: version) else { return }
        check(manual: false)
    }

    /// Asks the bundle's conch. The daily check stays quiet when it cannot
    /// reach GitHub and asks again in an hour; by hand it says why.
    /// from is the window Check for Updates… was chosen in: its sheet and
    /// its status bar say what was found.
    func check(manual: Bool, from: Store? = nil) {
        guard !checking else { return }
        let teller = from ?? store
        guard let bin = conch else {
            if manual { teller?.say("No conch to ask for updates", .err) }
            return
        }
        checking = true
        Task {
            defer { checking = false }
            do {
                let data = try await runConch(bin, ["app", "update", "check", "-json", "-version", version])
                let c = try JSONDecoder().decode(AppUpdateCheck.self, from: data)
                defaults.set(Date(), forKey: Key.lastCheck)
                act(appUpdateStep(c, prefs: prefs, skipped: defaults.string(forKey: Key.skipped), manual: manual), manual: manual, teller: teller)
            } catch {
                if manual { teller?.say("Couldn't check for updates: \(error)", .err) }
            }
        }
    }

    private func act(_ step: AppUpdateStep, manual: Bool, teller: Store?) {
        switch step {
        case .none:
            break
        case .offer(let c):
            offer = c
            if manual {
                sheetFor = teller?.windowID
            } else if defaults.string(forKey: Key.notified) != c.latest {
                // Once a version: the daily check finding it again is not news.
                defaults.set(c.latest, forKey: Key.notified)
                Notifier.shared.post(Alert(title: c.banner, body: "You have \(c.current). Install it from the status bar, or read what's new first."),
                                     id: "app-update", sound: false, desktop: true)
            }
        case .install(let v):
            install(v, relaunch: false)
        case .upToDate(let v):
            teller?.say("Conch \(v) is up to date")
        case .fromSource(let v):
            teller?.say("This Conch\(v.isEmpty ? "" : " (\(v))") is built from source: it updates from source, not from releases")
        }
    }

    /// Has the bundle's conch put the release in place of this app. By hand
    /// the app relaunches onto it; unasked it waits for the next launch.
    func install(_ v: String, relaunch: Bool) {
        guard installing == nil, let bin = conch else { return }
        installing = v
        sheetFor = nil
        Task {
            defer { installing = nil }
            do {
                _ = try await runConch(bin, ["app", "update", "install", "-app", bundlePath, v])
                defaults.set(v, forKey: Key.installed)
                offer = nil
                if relaunch {
                    self.relaunch()
                } else {
                    installed = v
                    store?.say("Conch \(v) is installed: it runs from the next launch")
                }
            } catch {
                store?.say("Couldn't install Conch \(v): \(error)", .err)
            }
        }
    }

    func later() {
        offer = nil
        sheetFor = nil
    }

    func skip() {
        if let v = offer?.latest { defaults.set(v, forKey: Key.skipped) }
        later()
    }

    /// Puts back the app the last update replaced, and relaunches onto it.
    func rollBack() {
        guard let bin = conch else { return }
        Task {
            do {
                _ = try await runConch(bin, ["app", "update", "rollback", "-app", bundlePath])
                relaunch()
            } catch {
                store?.say("Couldn't roll back: \(error)", .err)
            }
        }
    }

    func relaunch() {
        let argv = relaunchCommand(pid: ProcessInfo.processInfo.processIdentifier, app: bundlePath)
        let p = Process()
        p.executableURL = URL(fileURLWithPath: argv[0])
        p.arguments = Array(argv.dropFirst())
        do {
            try p.run()
        } catch {
            store?.say("Couldn't relaunch: quit Conch and open it again", .err)
            return
        }
        NSApp.terminate(nil)
    }

    /// The server reloaded onto this app's conch, keeping its panes.
    func reloadServer() {
        reloadOffer = nil
        store?.reloadServer("local", onto: conch)
    }
}

/// The status bar's word about the app's own update: one found, one being
/// installed, one installed and waiting for a relaunch, or the first launch
/// of one offering to reload the server.
struct AppUpdateBarItem: View {
    @ObservedObject var updater: AppUpdater
    @ObservedObject var store: Store

    var body: some View {
        Group {
            if let v = updater.installing {
                HStack(spacing: 4) {
                    ProgressView().controlSize(.mini)
                    Text("Installing Conch \(v)…").foregroundStyle(Theme.muted)
                }
            } else if let v = updater.installed {
                pill("Conch \(v) installed · Relaunch", icon: "arrow.clockwise.circle.fill",
                     help: "Conch \(v) is installed and runs from the next launch; panes and agents keep running") { updater.relaunch() }
            } else if let c = updater.offer {
                pill(c.banner, icon: "arrow.down.circle.fill", help: "Click to install it, read what's new, or skip it") { updater.sheetFor = store.windowID }
            } else if let v = updater.reloadOffer, !store.serverVersion.isEmpty, store.serverVersion != v {
                pill("Updated to \(v) · Reload Server", icon: "arrow.triangle.2.circlepath",
                     help: "The server still runs conch \(store.serverVersion): reload it onto this app's \(v), keeping panes and agents") {
                    updater.reloadServer()
                }
            }
        }
        .sheet(isPresented: Binding(get: { updater.sheetFor == store.windowID },
                                    set: { if !$0, updater.sheetFor == store.windowID { updater.sheetFor = nil } })) {
            AppUpdateSheet(updater: updater)
        }
    }

    private func pill(_ text: String, icon: String, help: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: icon).font(.ui(size: 11))
                Text(text).lineLimit(1)
            }
            .font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.selectedText)
            .padding(.horizontal, 7).padding(.vertical, 1)
            .background(Capsule().fill(Theme.accentDeep))
        }
        .buttonStyle(.plain).fixedSize().help(help)
    }
}

/// Install, Later or Skip This Version, with the release notes a click away.
struct AppUpdateSheet: View {
    @ObservedObject var updater: AppUpdater
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let c = updater.offer
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 10) {
                Image(systemName: "arrow.down.circle.fill").font(.ui(size: 22)).foregroundStyle(Theme.accent)
                VStack(alignment: .leading, spacing: 2) {
                    Text(c?.banner ?? "Conch is up to date").font(.ui(size: 15, weight: .semibold))
                    if let c { Text("You have \(c.current).").font(.ui(size: 12)).foregroundStyle(.secondary) }
                }
            }
            if let n = c?.notes, let url = URL(string: n) {
                Link("What's new in \(c?.latest ?? "it")", destination: url).font(.ui(size: 12))
            }
            if let why = c?.unavailable {
                Text(why + " — download it from the release page.").font(.ui(size: 12)).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Text("Install downloads it, checks that Apple notarized it and that it is signed by the same developer as this one, puts it in place of this app and relaunches. Panes and agents keep running; the app before is kept, to roll back to.")
                    .font(.ui(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Button("Skip This Version") { updater.skip() }
                Spacer()
                Button("Later") { updater.later() }.keyboardShortcut(.cancelAction)
                Button("Install") { if let v = c?.latest { updater.install(v, relaunch: true) } }
                    .keyboardShortcut(.defaultAction).disabled(!(c?.canInstall ?? false) || updater.installing != nil)
            }
        }
        .padding(20).frame(width: 460)
    }
}
