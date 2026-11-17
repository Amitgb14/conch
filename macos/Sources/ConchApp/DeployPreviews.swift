import AppKit
import ConchKit
import SwiftUI

// A branch's deploy previews in its git tab (the deploy mock's third
// screen): the newest deploy's state as a chip in the header, the preview
// on the pull request's card, and a card of its deploys on the overview —
// state, commit, the preview's link or why it failed, when — with Build
// log, Send to the branch's agent, Redeploy, and Promote/Publish to
// production behind a sheet in the service's own words. What may be
// released, and how it is put, is the server's (branch.previews.v1); the
// decisions are ConchKit's Deploy.swift. Nothing here exists against an
// older server.

/// How often the previews are asked again while the tab reloads: the
/// server keeps them a moment itself.
private let previewsEvery: TimeInterval = 15

extension ChangesModel {
    func loadPreviews(force: Bool = false) async {
        guard let c = connection, can(branchPreviewsCapability) else { return }
        if !force, let at = previewsAt, Date().timeIntervalSince(at) < previewsEvery { return }
        previewsAt = Date()
        struct P: Encodable { var project_id: String; var branch: String }
        if let p = try? await c.call("branch.previews", P(project_id: project, branch: branch), as: BranchPreviews.self) {
            previews = p
        }
    }

    struct DeployRef: Encodable {
        var project_id: String
        var branch: String
        var source: String
        var deploy: String
    }

    func deployRef(_ s: DeploySource, _ d: DeployInfo) -> DeployRef {
        DeployRef(project_id: project, branch: branch, source: s.source, deploy: d.id)
    }

    /// Build log: read once, then shown or hidden.
    func toggleDeployLog(_ s: DeploySource, _ d: DeployInfo) {
        let k = s.source + "|" + d.id
        if deployLogShown == k { deployLogShown = nil; return }
        deployLogShown = k
        guard deployLogs[k] == nil, let c = connection else { return }
        struct Log: Decodable { var log: String }
        Task {
            do {
                let lg = try await c.call("branch.deploy_log", deployRef(s, d), as: Log.self)
                deployLogs[k] = lg.log.isEmpty ? "It printed nothing." : lg.log
            } catch {
                deployLogs[k] = "Could not read it: \(error)"
            }
        }
    }

    func sendDeploy(_ s: DeploySource, _ d: DeployInfo) {
        store?.fixDeploy(machine: machine, project: project, branch: branch, source: s.source, deploy: d.id)
        handed = Handed(what: "fixing the failed build", pane: nil, at: Date())
    }

    func redeploy(_ s: DeploySource, _ d: DeployInfo) {
        act("branch.redeploy", s, d, doing: "Redeploying", done: "Building \(Deploys.short(d.commit).isEmpty ? d.id : Deploys.short(d.commit)) again on \(s.service)")
    }

    /// After the sheet: the person has said yes.
    func release(_ c: DeployConfirm) {
        releasing = nil
        act("branch.promote", c.source, c.deploy, doing: c.verb == "Publish" ? "Publishing" : "Promoting",
            done: "\(c.verb == "Publish" ? "Published" : "Promoting") \(c.what) to production on \(c.source.service)")
    }

    private func act(_ method: String, _ s: DeploySource, _ d: DeployInfo, doing: String, done: String) {
        guard let c = connection else { return }
        busy = doing
        Task {
            defer { busy = nil }
            do {
                try await c.call(method, deployRef(s, d))
                store?.say(done)
                await loadPreviews(force: true)
            } catch {
                self.error = "\(s.service): \(error)"
            }
        }
    }
}

@MainActor private func toneColor(_ t: DeployTone) -> Color {
    switch t {
    case .ok: return Theme.ok
    case .bad: return Theme.err
    case .busy: return Theme.work
    case .quiet: return Theme.muted
    }
}

/// The header's chip: the newest deploy's state; a click opens its preview,
/// or a failed one's page on its service.
struct DeployChip: View {
    @Environment(\.conchTheme) private var look
    let previews: BranchPreviews?

    var body: some View {
        let _ = look
        if let c = Deploys.chip(previews) {
            let color = toneColor(c.tone)
            let link = c.deploy.state == "ready" ? c.deploy.url : c.deploy.inspect
            Button {
                if let u = link.flatMap(URL.init(string:)) { NSWorkspace.shared.open(u) }
            } label: {
                HStack(spacing: 4) {
                    Circle().fill(color).frame(width: 6, height: 6)
                    Text(c.text).font(.ui(size: 11, weight: .medium, design: .monospaced)).lineLimit(1)
                }
                .foregroundStyle(color)
                .padding(.horizontal, 7).padding(.vertical, 2)
                .background(RoundedRectangle(cornerRadius: 5).fill(color.opacity(0.10)))
                .overlay(RoundedRectangle(cornerRadius: 5).stroke(color.opacity(0.45)))
                .fixedSize()
            }
            .buttonStyle(.plain)
            .help(c.deploy.state == "ready" ? "Open the preview: \(Deploys.shown(c.deploy.url))"
                  : c.deploy.state == "failed" ? "\(c.source.service): \(c.deploy.error ?? "the build failed") — open it on \(c.source.service)"
                  : "\(c.source.service): \(Deploys.stateText(c.deploy.state).lowercased())")
        }
    }
}

/// The pull request's line for its preview: the newest ready one.
struct DeployPRLine: View {
    @Environment(\.conchTheme) private var look
    let previews: BranchPreviews?

    var body: some View {
        let _ = look
        if let r = Deploys.newestReady(previews), let u = r.deploy.url.flatMap(URL.init(string:)) {
            HStack(spacing: 8) {
                Image(systemName: "globe").font(.ui(size: 12)).foregroundStyle(Theme.ok).frame(width: 14)
                Text("Preview on \(r.source.service)").font(.ui(size: 12.5))
                Link(Deploys.shown(r.deploy.url), destination: u).font(.ui(size: 12, design: .monospaced)).lineLimit(1).truncationMode(.middle)
            }
        }
    }
}

/// The overview's card: each deploy integration's deploys of the branch.
struct DeployPreviewsCard: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel
    @ObservedObject var icons = DeployIcons.shared
    let previews: BranchPreviews

    var body: some View {
        let _ = look
        GitCard(title: "Deploy previews") {
            ForEach(previews.integrations) { s in
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: 7) {
                        IntegrationMark(label: s.service, icon: icons.icon(s.source), size: 18)
                        Text(s.service).font(.ui(size: 12.5, weight: .semibold))
                        if let pr = model.branchInfo?.pr { Text("· pull request #\(pr.number)").font(.ui(size: 12)).foregroundStyle(Theme.muted) }
                        Spacer()
                    }
                    .padding(.bottom, 6)
                    if let why = s.why, !why.isEmpty {
                        Text(why).font(.ui(size: 12)).foregroundStyle(Theme.warn).textSelection(.enabled)
                    } else if (s.deploys ?? []).isEmpty {
                        Text("No deploy of \(model.branch) yet: it is built once it is pushed.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    } else {
                        header
                        ForEach(s.deploys ?? []) { d in row(s, d) }
                    }
                }
            }
            Text("The newest ready preview is also on the pull request, and the newest deploy's state in the header.")
                .font(.ui(size: 11)).foregroundStyle(Theme.muted)
        }
        .task { await DeployIcons.shared.load(model.store?.binary) }
    }

    var header: some View {
        HStack(spacing: 12) {
            Text("State").frame(width: 84, alignment: .leading)
            Text("Commit").frame(minWidth: 120, maxWidth: .infinity, alignment: .leading)
            Text("Preview").frame(minWidth: 140, maxWidth: .infinity, alignment: .leading)
            Text("Built").frame(width: 70, alignment: .leading)
            Text("Actions").frame(width: 250, alignment: .trailing)
        }
        .font(.ui(size: 11)).foregroundStyle(Theme.muted)
        .padding(.vertical, 5)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

    func row(_ s: DeploySource, _ d: DeployInfo) -> some View {
        let a = Deploys.actions(d, in: s)
        let key = s.source + "|" + d.id
        return VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                HStack(spacing: 6) {
                    Circle().fill(toneColor(Deploys.tone(d.state))).frame(width: 7, height: 7)
                    Text(Deploys.stateText(d.state)).font(.ui(size: 12.5))
                }
                .frame(width: 84, alignment: .leading)
                VStack(alignment: .leading, spacing: 1) {
                    HStack(spacing: 5) {
                        Text(Deploys.short(d.commit).isEmpty ? d.id : Deploys.short(d.commit)).font(.ui(size: 12, design: .monospaced))
                        if d.atHead == true { Text("head").font(.ui(size: 10)).foregroundStyle(Theme.muted) }
                    }
                    let msg = Deploys.message(d)
                    if !msg.isEmpty { Text(msg).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.tail) }
                }
                .frame(minWidth: 120, maxWidth: .infinity, alignment: .leading)
                Group {
                    if d.state == "ready", let u = d.url.flatMap(URL.init(string:)) {
                        Link(Deploys.shown(d.url), destination: u).font(.ui(size: 11.5, design: .monospaced))
                    } else if d.state == "failed" {
                        Text(d.error ?? "The build failed").font(.ui(size: 11.5)).foregroundStyle(Theme.err)
                    } else {
                        Text("").font(.ui(size: 11.5))
                    }
                }
                .lineLimit(1).truncationMode(.middle)
                .frame(minWidth: 140, maxWidth: .infinity, alignment: .leading)
                Text(Deploys.when(d)).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).frame(width: 70, alignment: .leading)
                HStack(spacing: 6) {
                    if let why = a.notReleasable {
                        Text(why).font(.ui(size: 11)).foregroundStyle(Theme.muted).multilineTextAlignment(.trailing)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if a.release, let r = s.release {
                        Button("\(r.verb) to production") { model.releasing = DeployConfirm(source: s, deploy: d) }
                            .buttonStyle(.borderedProminent).controlSize(.small).disabled(model.busy != nil)
                    }
                    if a.log {
                        Button(model.deployLogShown == key ? "Hide log" : "Build log") { model.toggleDeployLog(s, d) }.controlSize(.small)
                    }
                    if a.redeploy {
                        Button("Redeploy") { model.redeploy(s, d) }.controlSize(.small).disabled(model.busy != nil)
                    }
                    if let u = d.inspect.flatMap(URL.init(string:)), d.state != "ready" || !a.release {
                        Button { NSWorkspace.shared.open(u) } label: {
                            Image(systemName: "arrow.up.right.square").font(.ui(size: 11)).foregroundStyle(Theme.muted).padding(3).contentShape(Rectangle())
                        }
                        .buttonStyle(.plain).help("Open it on \(s.service)")
                    }
                }
                .frame(width: 250, alignment: .trailing)
            }
            .padding(.vertical, 6)
            if model.deployLogShown == key {
                VStack(alignment: .leading, spacing: 10) {
                    ScrollView {
                        Text(model.deployLogs[key] ?? "Reading the build's log…")
                            .font(.ui(size: 11.5, design: .monospaced)).foregroundStyle(Theme.text2)
                            .frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
                    }
                    .frame(maxHeight: 220)
                    HStack(spacing: 10) {
                        Button(model.agent.map { "Send to \($0.name.isEmpty ? ($0.agent?.name ?? "the branch's agent") : $0.name)" } ?? "Send to the branch's agent") {
                            model.sendDeploy(s, d)
                        }
                        .buttonStyle(.borderedProminent).controlSize(.small).disabled(!a.send)
                        Text("As a failed CI run is: the end of the log, with the commit.").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                    }
                }
                .padding(12)
                .background(RoundedRectangle(cornerRadius: 8).fill(Theme.window))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
                .padding(.bottom, 8)
            }
        }
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line.opacity(0.6)).frame(height: 1) }
    }
}

/// The confirmation before a release, in the service's own words.
struct DeployReleaseSheet: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: ChangesModel
    let confirm: DeployConfirm

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 14) {
            Text(confirm.title).font(.ui(size: 16, weight: .semibold))
            Text(confirm.body).font(.ui(size: 12.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            VStack(alignment: .leading, spacing: 5) {
                Text(confirm.change.from).foregroundStyle(Theme.muted)
                Text("→ " + confirm.change.to)
            }
            .font(.ui(size: 12, design: .monospaced))
            .padding(12).frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.window))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
            if let n = confirm.note {
                Text(n).font(.ui(size: 11.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button("Cancel") { model.releasing = nil }.keyboardShortcut(.cancelAction)
                Button(confirm.verb) { model.release(confirm) }.buttonStyle(.borderedProminent)
            }
        }
        .padding(22)
        .frame(width: 480)
    }
}

/// The deploy services' icons, as conch downloaded them from the registry
/// (conch integration icons): asked once for the app, since the git tab
/// has no Integrations window to borrow them from.
@MainActor
final class DeployIcons: ObservableObject {
    static let shared = DeployIcons()
    @Published private(set) var icons: [String: String] = [:]
    private var asked = false

    func icon(_ name: String) -> String? { icons[name] }

    func load(_ binary: String?) async {
        guard !asked, let b = binary else { return }
        asked = true
        if let out = try? await runConch(b, ["integration", "icons", "-json"]),
           let got = try? JSONDecoder().decode(IntegrationIcons.self, from: out) {
            icons = got.icons ?? [:]
        }
    }
}
