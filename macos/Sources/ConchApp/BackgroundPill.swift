import ConchKit
import SwiftUI

/// What an agent left running when it stopped working — a dev server, a
/// watcher (agent.background.v1): "idle · 2 running", or "2 running" beside
/// a state pill that already says done or waiting. Hovering lists each job
/// with its pid, how long it has run and its ports; a click opens the
/// list with a Stop for each.
struct BackgroundPill: View {
    @ObservedObject var store: Store
    let pane: String
    /// The tab's: the count alone, ⟳2.
    var mark = false
    @State private var open = false

    var body: some View {
        if let a = store.pane(pane)?.agent, let full = a.backgroundPill {
            let jobs = a.jobs ?? []
            Group {
                if mark {
                    Text("⟳\(a.runningCount)").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.work)
                } else {
                    Pill(text: Pill.state(a.state) == nil ? full : "\(a.runningCount) running",
                         fg: Theme.work, bg: Theme.work.opacity(0.16))
                }
            }
            .fixedSize()
            .contentShape(Rectangle())
            .help(jobs.isEmpty ? full : backgroundTip(jobs))
            .onTapGesture { open = true }
            .accessibilityAddTraits(.isButton)
            .accessibilityLabel(full)
            .popover(isPresented: $open, arrowEdge: .bottom) {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Left running").font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text)
                    if jobs.isEmpty {
                        Text("\(a.runningCount) running; its server did not say what.")
                            .font(.ui(size: 12)).foregroundStyle(Theme.text2)
                    }
                    ForEach(jobs, id: \.pid) { j in
                        HStack(spacing: 10) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(j.command.isEmpty ? "pid \(j.pid)" : j.command)
                                    .font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.text)
                                    .lineLimit(1).truncationMode(.middle)
                                Text(backgroundTip([BackgroundJob(pid: j.pid, command: "", since: j.since, ports: j.ports)]))
                                    .font(.ui(size: 11)).foregroundStyle(Theme.muted)
                            }
                            Spacer(minLength: 12)
                            Button("Stop") { store.stopJob(pane, pid: j.pid) }
                                .help("End it, and what it started")
                        }
                    }
                }
                .padding(12).frame(minWidth: 280, maxWidth: 460)
            }
        }
    }
}
