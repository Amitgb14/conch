import AppKit
import ConchKit
import SwiftUI

// The Artifacts tab's views: its list (the first tab of an Artifacts
// browser), the bar over an artifact open beside it, the toast a publish
// raises, and the counts on an agent's header and its tree row. What they
// show is decided in ConchKit's Artifacts.swift.

/// An agent's artifacts, by scope, newest first in days; a row opens the
/// artifact in a tab of this browser.
struct ArtifactsListView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    @ObservedObject var store: Store
    /// The agent, key(machine, pane).
    let pane: String
    @FocusState private var filtering: Bool

    var machine: String { unkey(pane).machine }
    var info: PaneInfo? { store.pane(pane) }
    var agentName: String { info?.agent?.name ?? "the agent" }

    var body: some View {
        let _ = look
        VStack(spacing: 0) {
            header
            filter
            openingNote
            Rectangle().fill(Theme.line).frame(height: 1)
            TimelineView(.periodic(from: .now, by: 30)) { ctx in
                content(now: ctx.date)
            }
            Rectangle().fill(Theme.line).frame(height: 1)
            footer
        }
        .background(Theme.window)
        .onAppear {
            store.loadArtifacts(machine)
            store.checkClaudeSignIn()
        }
    }

    var header: some View {
        // Its title and close are the browser tab's, just above.
        HStack(spacing: 8) {
            Picker("Whose artifacts", selection: $model.artifactScope) {
                ForEach(ArtifactScope.allCases, id: \.self) { s in Text(s.label(machine: machine)).tag(s) }
            }
            .pickerStyle(.segmented).labelsHidden().fixedSize()
            Spacer()
            Menu {
                Picker("Open Artifacts", selection: Binding(get: { store.artifactOpening }, set: { store.artifactOpening = $0 })) {
                    ForEach(ArtifactOpening.allCases, id: \.self) { o in Text(o.label).tag(o) }
                }
                .pickerStyle(.inline)
            } label: {
                LucideIcon("external-link", color: Theme.text2, size: 12)
            }
            .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
            .help("Where an artifact opens: in Conch, or in your own browser")
            Button { store.loadArtifacts(machine) } label: { LucideIcon("rotate-cw", color: Theme.text2, size: 12) }
                .buttonStyle(.plain).help("List Again").accessibilityLabel("List artifacts again")
        }
        .padding(.horizontal, 10).frame(height: 34)
    }

    /// Said while artifacts open outside because this browser is not
    /// signed in: why, and the way to sign in here instead.
    @ViewBuilder var openingNote: some View {
        if store.artifactOpening == .automatic, store.claudeSignedIn == false {
            HStack(spacing: 6) {
                LucideIcon("external-link", color: Theme.text2, size: 11)
                Text("Artifacts open in your browser: Conch's isn't signed in to claude.ai.")
                    .font(.ui(size: 11.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 4)
                Button("Sign In Here") { model.openTab("https://claude.ai/login") }
                    .buttonStyle(.link).font(.ui(size: 11.5)).fixedSize()
                    .help("Sign in with a password or an email code: a passkey cannot be used in Conch's browser")
            }
            .padding(.horizontal, 12).padding(.bottom, 8)
        }
    }

    var filter: some View {
        HStack(spacing: 6) {
            LucideIcon("search", color: Theme.muted, size: 12)
            TextField("Filter by title", text: $model.artifactQuery)
                .textFieldStyle(.plain).font(.ui(size: 12)).focused($filtering)
                .onChange(of: filtering) { _, f in model.chromeEditing = f }
            if !model.artifactQuery.isEmpty {
                Button { model.artifactQuery = "" } label: { LucideIcon("x", color: Theme.muted, size: 10) }
                    .buttonStyle(.plain).accessibilityLabel("Clear the filter")
            }
        }
        .padding(.horizontal, 8).padding(.vertical, 5)
        .background(RoundedRectangle(cornerRadius: 6).stroke(Theme.line))
        .padding(.horizontal, 10).padding(.bottom, 8)
    }

    @ViewBuilder func content(now: Date) -> some View {
        let shown = artifactsShown(store.artifactShelf.list(machine), scope: model.artifactScope, pane: unkey(pane).id,
                                   project: info?.projectID, query: model.artifactQuery)
        if let note = store.artifactShelf.notes[machine] {
            empty("circle-alert", "Artifacts can't be listed", note, tone: Theme.warn)
        } else if model.artifactScope == .agent, info == nil {
            empty("layers", "This agent has closed", "What it published is still listed under \(ArtifactScope.machine.label(machine: machine)).",
                  action: ("Show \(ArtifactScope.machine.label(machine: machine))", { model.artifactScope = .machine }))
        } else if model.artifactScope == .agent, let a = info?.agent, a.name != "claude" {
            empty("layers", "\(a.name) keeps no record of artifacts",
                  "Only Claude Code records the artifacts it publishes. A claude.ai link \(a.name) prints still opens here with ⌘-click.")
        } else if model.artifactScope == .project, info?.projectID == nil {
            empty("layers", "This agent is in no project", "Its own artifacts are under This Agent.")
        } else if shown.isEmpty, !model.artifactQuery.isEmpty {
            empty("search", "Nothing matches “\(model.artifactQuery)”", "")
        } else if shown.isEmpty {
            empty("layers", model.artifactScope == .agent ? "\(agentName) has published no artifacts here" : "No artifacts yet",
                  "Ask it for a mock, a page or a doc, and it shows up here as soon as it is published.",
                  action: model.artifactScope == .machine ? nil
                      : ("Show Every Artifact on \(ArtifactScope.machine.label(machine: machine))", { model.artifactScope = .machine }))
        } else {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(artifactSections(shown, now: now), id: \.title) { sec in
                        Text(sec.title.uppercased()).font(.ui(size: 10, weight: .semibold)).foregroundStyle(Theme.muted)
                            .padding(.horizontal, 12).padding(.top, 10).padding(.bottom, 4)
                        ForEach(sec.items) { a in row(a, now: now) }
                    }
                }
            }
        }
    }

    func row(_ a: Artifact, now: Date) -> some View {
        let unseen = store.artifactShelf.isUnseen(machine, a.url)
        return Button { open(a) } label: {
            HStack(spacing: 10) {
                LucideIcon(artifactIcon(kind: a.kind), color: Theme.accent, size: 15)
                    .frame(width: 30, height: 30)
                    .background(RoundedRectangle(cornerRadius: 7).fill(Theme.accent.opacity(0.12)))
                VStack(alignment: .leading, spacing: 2) {
                    Text(a.title.isEmpty ? a.url : a.title).font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(Theme.text)
                        .lineLimit(1).truncationMode(.tail)
                    Text(artifactDetail(a, now: now)).font(.ui(size: 11)).foregroundStyle(Theme.text2).lineLimit(1)
                }
                Spacer(minLength: 4)
                if unseen {
                    Circle().fill(Theme.accent).frame(width: 7, height: 7).accessibilityLabel("New")
                }
                LucideIcon("chevron-right", color: Theme.muted, size: 12)
            }
            .padding(.horizontal, 12).frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowButtonStyle())
        .help(a.url)
        .contextMenu {
            Button("Open") { open(a) }
            Button("Open in Browser") { if let u = URL(string: a.url) { NSWorkspace.shared.open(u) } }
            Button("Copy Link") { copyArtifactLink(a.url) }
        }
    }

    func open(_ a: Artifact) {
        store.artifactShelf.opened(machine, a.url)
        store.pickArtifact(a.url) { model.openArtifact(a.url) }
    }

    func empty(_ icon: String, _ title: String, _ detail: String, tone: Color? = nil,
               action: (String, () -> Void)? = nil) -> some View {
        VStack(spacing: 8) {
            LucideIcon(icon, color: tone ?? Theme.muted, size: 20)
                .frame(width: 36, height: 36).background(RoundedRectangle(cornerRadius: 9).fill(Theme.raise))
            Text(title).font(.ui(size: 13, weight: .semibold)).foregroundStyle(Theme.text).multilineTextAlignment(.center)
            if !detail.isEmpty {
                Text(detail).font(.ui(size: 12)).foregroundStyle(Theme.text2).multilineTextAlignment(.center)
                    .frame(maxWidth: 320).textSelection(.enabled)
            }
            if let action { Button(action.0, action: action.1).buttonStyle(.link) }
        }
        .padding(24).frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    var footer: some View {
        HStack(spacing: 6) {
            LucideIcon("history", color: Theme.muted, size: 11)
            Text("Read from Claude Code's session transcripts").font(.ui(size: 10.5)).foregroundStyle(Theme.muted).lineLimit(1)
            Spacer()
            Text("⌥⌘A").font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
        }
        .padding(.horizontal, 12).frame(height: 24)
    }
}

func copyArtifactLink(_ s: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(s, forType: .string)
}

/// Over an artifact open in an Artifacts tab: which version it is and
/// when, its link copied, the system's browser.
struct ArtifactBar: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel
    @ObservedObject var store: Store

    var body: some View {
        let _ = look
        let found = store.artifactShelf.find(model.address)
        HStack(spacing: 8) {
            Button { model.showArtifactList() } label: {
                HStack(spacing: 3) {
                    LucideIcon("chevron-left", color: Theme.accent, size: 11)
                    Text("Artifacts").font(.ui(size: 11.5, weight: .semibold)).foregroundStyle(Theme.accent)
                }
            }
            .buttonStyle(.plain).help("Back to the list").accessibilityLabel("Back to the artifacts list")
            if let a = found?.artifact {
                LucideIcon(artifactIcon(kind: a.kind), color: Theme.text2, size: 12)
                Text(a.title).font(.ui(size: 12)).foregroundStyle(Theme.text).lineLimit(1)
                TimelineView(.periodic(from: .now, by: 30)) { ctx in
                    Text(["v\(max(a.versions, 1))", artifactWhen(a.updated, now: ctx.date)].filter { !$0.isEmpty }.joined(separator: " · "))
                        .font(.ui(size: 11)).foregroundStyle(Theme.text2)
                        .padding(.horizontal, 6).padding(.vertical, 1).background(RoundedRectangle(cornerRadius: 5).fill(Theme.raise))
                }
            }
            Spacer()
            Button { copyArtifactLink(artifactBase(model.address)); model.say("Copied the link") } label: {
                HStack(spacing: 4) { LucideIcon("copy", color: Theme.text2, size: 11); Text("Copy Link") }
            }
            .buttonStyle(.plain).font(.ui(size: 11.5)).foregroundStyle(Theme.text2)
            Button { if let u = URL(string: model.address) { NSWorkspace.shared.open(u) } } label: {
                HStack(spacing: 4) { LucideIcon("external-link", color: Theme.text2, size: 11); Text("Open in Browser") }
            }
            .buttonStyle(.plain).font(.ui(size: 11.5)).foregroundStyle(Theme.text2)
        }
        .padding(.horizontal, 10).frame(height: 28)
        .background(Theme.well.opacity(0.4))
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
    }
}

/// A publish said in the window's corner: whose, what, and Open.
struct ArtifactToastView: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var store: Store
    let toast: ArtifactToast

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Mark(agent: "claude")
                Text(toast.who).font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1)
                Spacer()
                Button { store.artifactToast = nil } label: { LucideIcon("x", color: Theme.muted, size: 10) }
                    .buttonStyle(.plain).accessibilityLabel("Dismiss")
            }
            Text(toast.version > 1 ? "published version \(toast.version) of “\(toast.title)”" : "published “\(toast.title)”")
                .font(.ui(size: 12)).foregroundStyle(Theme.text2).lineLimit(2)
            HStack(spacing: 8) {
                Button("Open") { store.openToast(toast) }.keyboardShortcut(.defaultAction).controlSize(.small)
                Button("Later") { store.artifactToast = nil }.controlSize(.small)
            }
        }
        .padding(12).frame(width: 300)
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.window).shadow(color: .black.opacity(0.15), radius: 12, y: 4))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }
}

/// An agent's header: its Artifacts tab, with how many it published and a
/// dot for one not opened yet.
struct ArtifactsChip: View {
    @ObservedObject var store: Store
    let pane: String

    var body: some View {
        let (m, id) = unkey(pane)
        let c = store.artifactShelf.count(m, pane: id)
        Button { store.showArtifacts(pane: pane) } label: {
            HStack(spacing: 4) {
                LucideIcon("layers", color: Theme.accent, size: 11)
                Text(c.count > 0 ? "Artifacts \(c.count)" : "Artifacts").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.accent)
                if c.unseen { Circle().fill(Theme.accent).frame(width: 5, height: 5) }
            }
            .padding(.horizontal, 7).padding(.vertical, 2)
            .background(RoundedRectangle(cornerRadius: 5).fill(Theme.accent.opacity(0.12)))
        }
        .buttonStyle(.plain).fixedSize()
        .help("What this agent published on claude.ai, beside it (⌥⌘A)")
        .accessibilityLabel(c.count > 0 ? "Artifacts, \(c.count)" + (c.unseen ? ", one new" : "") : "Artifacts")
    }
}

/// An agent's tree row: how many artifacts it published, solid with a
/// dot when one is new.
struct ArtifactCount: View {
    @ObservedObject var store: Store
    let pane: String
    var dim: Color

    var body: some View {
        let (m, id) = unkey(pane)
        let c = store.artifactShelf.count(m, pane: id)
        if c.count > 0 {
            HStack(spacing: 3) {
                LucideIcon("layers", color: c.unseen ? Theme.window : dim, size: 10).frame(width: 10)
                Text("\(c.count)").font(.tree(size: 10.5, weight: c.unseen ? .bold : .regular))
                    .foregroundStyle(c.unseen ? Theme.window : dim)
                if c.unseen { Circle().fill(Theme.window).frame(width: 4, height: 4) }
            }
            .padding(.horizontal, 4).padding(.vertical, 1)
            .background(RoundedRectangle(cornerRadius: 4).fill(c.unseen ? Theme.accent : Theme.raise))
            .fixedSize()
            .help(c.count == 1 ? "1 artifact" + (c.unseen ? ", new" : "") : "\(c.count) artifacts" + (c.unseen ? ", one new" : ""))
            .onTapGesture { store.showArtifacts(pane: pane) }
        }
    }
}

/// Over Google's passkey step in Conch's browser, which always fails
/// there (isPasskeyChallenge): what to do instead.
struct PasskeyNotice: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: BrowserModel

    var body: some View {
        let _ = look
        HStack(spacing: 8) {
            LucideIcon("circle-alert", color: Theme.warn, size: 13)
            Text("Passkeys can't be used in Conch's browser. Choose **Try another way** (a password or your phone), or sign in to claude.ai with email.")
                .font(.ui(size: 12)).foregroundStyle(Theme.text).fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 6)
            if let a = model.lastArtifact, let u = URL(string: a) {
                Button { NSWorkspace.shared.open(u) } label: {
                    HStack(spacing: 4) { LucideIcon("external-link", color: Theme.accent, size: 11); Text("Open in Browser") }
                }
                .buttonStyle(.plain).font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.accent).fixedSize()
                .help("The artifact in your own browser, where your passkey works")
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 7)
        .background(Theme.warn.opacity(0.12))
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
        .accessibilityElement(children: .combine)
    }
}
