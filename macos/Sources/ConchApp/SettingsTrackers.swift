import ConchKit
import SwiftUI

/// The top of a tracker's card in Settings → Trackers: whose the key is and
/// a badge — Connected, Off, Couldn't sign in — from ticket.check, with
/// Test again, then what it sees. The fields of the key stay below it.
struct TrackerStatus: View {
    @ObservedObject var model: SettingsModel
    let tracker: String
    @Environment(\.conchTheme) private var theme

    var state: TrackerState? { model.trackers?.trackers.first { $0.tracker == tracker } }

    var body: some View {
        let _ = theme
        let badge = trackerBadge(state)
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                VStack(alignment: .leading, spacing: 2) {
                    if let s = state, s.state == "connected", !trackerWho(s).isEmpty {
                        Text(trackerWho(s)).font(.ui(size: 12.5, weight: .medium)).foregroundStyle(Theme.text)
                    } else if let e = state?.error, !e.isEmpty {
                        Text(e).font(.ui(size: 12)).foregroundStyle(state?.state == "error" ? Theme.err : Theme.muted)
                            .fixedSize(horizontal: false, vertical: true).textSelection(.enabled)
                    } else if state?.state == "off" {
                        Text("Not set up").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    }
                    if let s = state, s.state == "connected", let e = s.error, !e.isEmpty {
                        // Connected, and the team or project it lists from is not.
                        Text(e).font(.ui(size: 11.5)).foregroundStyle(Theme.warn).fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 8)
                if model.trackersChecking {
                    ProgressView().controlSize(.small)
                }
                HStack(spacing: 5) {
                    Circle().fill(color(badge.tone)).frame(width: 7, height: 7)
                    Text(badge.text).font(.ui(size: 11.5, weight: .semibold)).foregroundStyle(color(badge.tone))
                }
                .padding(.horizontal, 9).padding(.vertical, 3)
                .background(Capsule().fill(color(badge.tone).opacity(0.12)))
                Button("Test again") { model.checkTrackers() }.controlSize(.small).disabled(model.trackersChecking)
            }
            .padding(.horizontal, 16).padding(.vertical, 11)
            if let s = state {
                let facts = trackerFacts(s)
                if !facts.isEmpty {
                    HStack(spacing: 0) {
                        ForEach(Array(facts.enumerated()), id: \.offset) { i, f in
                            VStack(alignment: .leading, spacing: 1) {
                                Text(f.value).font(.ui(size: 16, weight: .semibold)).foregroundStyle(Theme.text)
                                Text(f.label).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, 16).padding(.vertical, 8)
                            if i < facts.count - 1 { Divider() }
                        }
                    }
                    .fixedSize(horizontal: false, vertical: true)
                    .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
                }
                if let note = trackerKeyNote(s) {
                    Text(note).font(.ui(size: 11.5)).foregroundStyle(Theme.muted)
                        .padding(.horizontal, 16).padding(.vertical, 7)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
                }
            }
        }
    }

    func color(_ t: TrackerTone) -> Color {
        switch t {
        case .ok: return Theme.ok
        case .err: return Theme.err
        case .off: return Theme.muted
        }
    }
}

/// Each [[trackers.projects]] entry and how many are open there.
struct TrackerProjectsCard: View {
    @ObservedObject var model: SettingsModel
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        if let ps = model.trackers?.projects, !ps.isEmpty {
            SettingsCard(title: "Open tickets by project") {
                ForEach(Array(ps.enumerated()), id: \.offset) { i, p in
                    let note = trackerProjectNote(p)
                    HStack(spacing: 12) {
                        Text(p.project).font(.ui(size: 12.5, weight: .medium)).frame(width: 170, alignment: .leading).lineLimit(1)
                        Text(p.tracker.capitalized + (p.area.map { " · \($0)" } ?? "")).font(.ui(size: 12)).foregroundStyle(Theme.text2)
                            .frame(width: 130, alignment: .leading)
                        Text(note.text).font(.ui(size: 12)).foregroundStyle(note.ok ? Theme.ok : Theme.err).lineLimit(2)
                        Spacer(minLength: 0)
                    }
                    .padding(.horizontal, 16).padding(.vertical, 8)
                    .overlay(alignment: .top) { if i > 0 { Rectangle().fill(Theme.line).frame(height: 1) } }
                }
            }
        }
    }
}
