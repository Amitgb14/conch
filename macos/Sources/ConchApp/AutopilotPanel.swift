import ConchKit
import SwiftUI

// The commit graph's Autopilot panel: what the server's autopilot does on
// its own in this project (its rules), what it is minding now, and what
// it did — kept by the server (autopilot.activity), so it says what
// happened while nobody was looking, and read again as it happens.

struct AutopilotPanel: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel
    @ObservedObject var store: Store

    var body: some View {
        let _ = look
        let on = model.autopilot?.policy.anyOn == true
        let minding = (model.autopilot?.branches ?? []).filter { $0.projectID == model.project }
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Text("Autopilot").font(.ui(size: 13.5, weight: .semibold))
                Text(on ? "On" : "Off").font(.ui(size: 10.5, weight: .semibold))
                    .foregroundStyle(on ? Theme.ok : Theme.muted)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .background(Capsule().fill((on ? Theme.ok : Theme.muted).opacity(0.15)))
                Spacer(minLength: 4)
                Button("Settings…") { store.openSettings("git") }
                    .controlSize(.small)
                    .help("Checks & autopilot: what it does on its own, for every project")
            }
            .padding(.horizontal, 14).frame(height: 52)
            Rectangle().fill(Theme.line).frame(height: 1)
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if let p = model.autopilot?.policy {
                        VStack(alignment: .leading, spacing: 5) {
                            ForEach(Array(autopilotRules(p, project: model.project).enumerated()), id: \.offset) { _, r in
                                HStack(alignment: .firstTextBaseline, spacing: 7) {
                                    Text(r.on ? "✓" : "–").font(.ui(size: 11, weight: .bold))
                                        .foregroundStyle(r.on ? Theme.ok : Theme.muted).frame(width: 10)
                                    Text(r.text).font(.ui(size: 12)).foregroundStyle(r.on ? Theme.text : Theme.muted)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                                .accessibilityElement(children: .combine)
                            }
                        }
                    }
                    if !minding.isEmpty {
                        VStack(alignment: .leading, spacing: 6) {
                            Text("MINDING NOW").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.muted)
                            ForEach(minding, id: \.branch) { b in
                                Button { model.focus(branch: b.branch) } label: {
                                    HStack(spacing: 8) {
                                        Text(b.branch).font(.ui(size: 12, weight: .medium)).lineLimit(1).truncationMode(.middle)
                                        Spacer(minLength: 6)
                                        Text(b.status).font(.ui(size: 11)).foregroundStyle(b.conflicting == true || b.checkFailed == true ? Theme.warn : Theme.text2)
                                            .lineLimit(1).truncationMode(.tail)
                                    }
                                    .contentShape(Rectangle())
                                }
                                .buttonStyle(.plain).help("Show \(b.branch) in the graph")
                            }
                        }
                    }
                    Feed(model: model)
                }
                .padding(14)
            }
        }
        .background(Theme.side)
        .onChange(of: store.autopilotTick[model.machine] ?? 0) { _, _ in model.loadAutopilot() }
    }
}

/// What it did, a day at a time, newest first.
private struct Feed: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel

    var body: some View {
        let _ = look
        VStack(alignment: .leading, spacing: 10) {
            Text("WHAT IT DID").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.muted)
            if !model.activityError.isEmpty {
                Text(model.activityError).font(.ui(size: 12)).foregroundStyle(Theme.err)
                    .fixedSize(horizontal: false, vertical: true)
            } else if model.activity.isEmpty {
                Text("Nothing yet. What the autopilot does on its own in this project — a failed check sent to an agent, a pull request opened or merged, a conflict handed back — shows here, and stays after a restart.")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
            }
            ForEach(activityDays(model.activity), id: \.title) { day in
                VStack(alignment: .leading, spacing: 2) {
                    Text(day.title).font(.ui(size: 11.5, weight: .semibold)).foregroundStyle(Theme.text2).padding(.bottom, 4)
                    ForEach(Array(day.notes.enumerated()), id: \.offset) { _, n in Entry(model: model, note: n) }
                }
            }
        }
    }
}

private struct Entry: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel
    let note: AutopilotNote

    var body: some View {
        let _ = look
        let tone: Color = switch note.tone {
        case .done: Theme.ok
        case .failed: Theme.err
        case .handed: Theme.work
        case .info: Theme.muted
        }
        HStack(alignment: .top, spacing: 9) {
            Text(note.at.map { Self.time.string(from: $0) } ?? "").font(.ui(size: 10.5)).monospacedDigit()
                .foregroundStyle(Theme.muted).frame(width: 36, alignment: .leading).padding(.top, 1)
            Circle().fill(tone).frame(width: 7, height: 7).padding(.top, 4)
            VStack(alignment: .leading, spacing: 3) {
                Text(note.text).font(.ui(size: 12)).foregroundStyle(Theme.text).fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 6) {
                    if !note.kindLabel.isEmpty {
                        Text(note.kindLabel).font(.ui(size: 10, weight: .semibold)).foregroundStyle(tone)
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(RoundedRectangle(cornerRadius: 3).fill(tone.opacity(0.14)))
                    }
                    if let b = note.branch, !b.isEmpty {
                        Button(b) { model.focus(branch: b) }
                            .buttonStyle(.link).font(.ui(size: 11)).lineLimit(1)
                            .help("Show \(b) in the graph")
                    }
                }
            }
        }
        .padding(.vertical, 5)
    }

    static let time: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "HH:mm"
        return f
    }()
}
