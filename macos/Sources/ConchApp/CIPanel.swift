import AppKit
import ConchKit
import SwiftUI

// The commit graph's CI panel: the project's GitHub Actions over the last
// week, two or a month — how often it passes, how long it takes and waits,
// how long a branch stays red, runs per day, each job's time, the jobs
// that fail and pass with nothing changed, and the failures that can be
// run again. All of it read through gh on the project's machine (ci.stats).

struct CIPanel: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel

    var body: some View {
        let _ = look
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                VStack(alignment: .leading, spacing: 1) {
                    Text("CI pipeline").font(.ui(size: 13.5, weight: .semibold))
                    Text("GitHub Actions · last \(model.ciDays) days · read with gh")
                        .font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1)
                }
                Spacer(minLength: 4)
                if model.ciLoading { ProgressView().controlSize(.small) }
                Picker("Range", selection: $model.ciDays) {
                    Text("7d").tag(7)
                    Text("14d").tag(14)
                    Text("30d").tag(30)
                }
                .pickerStyle(.segmented).labelsHidden().fixedSize().controlSize(.small)
                .onChange(of: model.ciDays) { _, _ in model.loadCI() }
            }
            .padding(.horizontal, 14).frame(height: 52)
            Rectangle().fill(Theme.line).frame(height: 1)
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if !model.ciError.isEmpty {
                        Text(model.ciError).font(.ui(size: 12)).foregroundStyle(Theme.err)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if let s = model.ci {
                        Tiles(s: s)
                        if s.runs == 0 {
                            Text("No finished runs in the last \(s.days) days.").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                        } else {
                            RunsPerDay(s: s)
                        }
                        if !(s.failing ?? []).isEmpty { Failing(model: model, runs: s.failing ?? []) }
                        if !(s.jobs ?? []).isEmpty || s.jobsPending > 0 { JobTimes(s: s) }
                        if !(s.flaky ?? []).isEmpty { Flaky(items: s.flaky ?? []) }
                    } else if model.ciError.isEmpty {
                        ProgressView().controlSize(.small).frame(maxWidth: .infinity)
                    }
                }
                .padding(14)
            }
        }
        .background(Theme.side)
    }
}

/// A heading and a line saying how to read what follows.
private struct CISection<Content: View>: View {
    let title: String
    var note = ""
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(title).font(.ui(size: 12.5, weight: .semibold))
                Spacer(minLength: 6)
                if !note.isEmpty { Text(note).font(.ui(size: 10.5)).foregroundStyle(Theme.muted).lineLimit(1) }
            }
            content
        }
    }
}

private struct Tiles: View {
    @Environment(\.conchTheme) private var look
    let s: CIStats

    var body: some View {
        let _ = look
        LazyVGrid(columns: [GridItem(.flexible(), spacing: 8), GridItem(.flexible(), spacing: 8)], spacing: 8) {
            tile("PASS RATE", s.passRate.map { "\($0)%" } ?? "—",
                 s.passRateChange.map { $0 == 0 ? "same as before" : "\($0 > 0 ? "▲" : "▼") \(abs($0)) pts vs before" } ?? "\(s.runs) runs",
                 (s.passRateChange ?? 0) > 0 ? Theme.ok : (s.passRateChange ?? 0) < 0 ? Theme.err : Theme.muted)
            tile("MEDIAN RUN", s.medianSeconds > 0 ? ciDuration(s.medianSeconds) : "—",
                 s.medianChange.map { $0 == 0 ? "same as before" : "\($0 < 0 ? "▼" : "▲") \(ciDuration(abs($0))) \($0 < 0 ? "faster" : "slower")" } ?? "start to finish",
                 (s.medianChange ?? 0) < 0 ? Theme.ok : (s.medianChange ?? 0) > 0 ? Theme.warn : Theme.muted)
            tile("RED → GREEN", s.redToGreenCount > 0 ? ciDuration(s.redToGreenSeconds) : "—",
                 s.redToGreenCount > 0 ? "median of \(s.redToGreenCount)" : "nothing went red", Theme.muted)
            tile("IN QUEUE", s.runs > 0 ? ciDuration(s.queueSeconds) : "—", "median wait to start", Theme.muted)
        }
    }

    func tile(_ k: String, _ v: String, _ d: String, _ tone: Color) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(k).font(.ui(size: 10, weight: .semibold)).foregroundStyle(Theme.muted)
            Text(v).font(.ui(size: 19, weight: .semibold)).monospacedDigit().lineLimit(1).minimumScaleFactor(0.7)
            Text(d).font(.ui(size: 10.5)).foregroundStyle(tone).lineLimit(1).minimumScaleFactor(0.8)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.window))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
        .accessibilityElement(children: .combine)
    }
}

/// Passed runs as green bars with failures stacked on top, one a day;
/// the top gridline is labelled with the count it stands for.
private struct RunsPerDay: View {
    @Environment(\.conchTheme) private var look
    let s: CIStats
    static let height: Double = 96

    var body: some View {
        let _ = look
        let bars = ciBars(s.daily, height: Self.height)
        let top = ciAxisTop(s.daily.map { $0.passed + $0.failed }.max() ?? 0)
        CISection(title: "Runs per day") {
            HStack(spacing: 10) {
                legend(Theme.ok, "✓ passed")
                legend(Theme.err, "✕ failed")
            }
            HStack(alignment: .bottom, spacing: 6) {
                VStack(alignment: .trailing) {
                    Text("\(top)")
                    Spacer()
                    Text("0")
                }
                .font(.ui(size: 9.5)).foregroundStyle(Theme.muted).frame(height: Self.height)
                ZStack(alignment: .bottom) {
                    VStack {
                        Rectangle().fill(Theme.line.opacity(0.5)).frame(height: 1)
                        Spacer()
                        Rectangle().fill(Theme.line).frame(height: 1)
                    }
                    HStack(alignment: .bottom, spacing: s.daily.count > 20 ? 2 : 4) {
                        ForEach(Array(s.daily.enumerated()), id: \.offset) { i, d in
                            VStack(spacing: 2) {
                                if d.failed > 0 {
                                    UnevenRoundedRectangle(topLeadingRadius: 3, topTrailingRadius: 3).fill(Theme.err)
                                        .frame(height: max(bars[i].failed - 1, 1))
                                }
                                if d.passed > 0 {
                                    UnevenRoundedRectangle(topLeadingRadius: d.failed > 0 ? 0 : 3, topTrailingRadius: d.failed > 0 ? 0 : 3)
                                        .fill(Theme.ok).frame(height: max(bars[i].passed - 1, 1))
                                }
                            }
                            .frame(maxWidth: .infinity, maxHeight: Self.height, alignment: .bottom)
                            .contentShape(Rectangle())
                            .help("\(d.date): \(d.passed) passed, \(d.failed) failed")
                        }
                    }
                }
                .frame(height: Self.height)
            }
            HStack {
                Text(s.daily.first.map { shortDate($0.date) } ?? "")
                Spacer()
                Text("today")
            }
            .font(.ui(size: 9.5)).foregroundStyle(Theme.muted).padding(.leading, 18)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Runs per day: \(s.passed) passed and \(s.failed) failed over \(s.days) days")
    }

    func legend(_ c: Color, _ t: String) -> some View {
        HStack(spacing: 4) {
            RoundedRectangle(cornerRadius: 2).fill(c).frame(width: 9, height: 9)
            Text(t).font(.ui(size: 10.5)).foregroundStyle(Theme.text2)
        }
    }

    func shortDate(_ iso: String) -> String {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        guard let d = f.date(from: iso) else { return iso }
        f.dateFormat = "MMM d"
        return f.string(from: d)
    }
}

/// Each job's median time as a bar and its slowest tenth as a tick.
private struct JobTimes: View {
    @Environment(\.conchTheme) private var look
    let s: CIStats

    var body: some View {
        let _ = look
        let jobs = s.jobs ?? []
        let longest = Double(max(jobs.map(\.p90Seconds).max() ?? 1, 1))
        CISection(title: "Time per job", note: "bar median · tick slowest 10%") {
            ForEach(jobs.prefix(10), id: \.name) { j in
                HStack(spacing: 8) {
                    Text(j.name).font(.ui(size: 11.5)).foregroundStyle(Theme.text2).lineLimit(1).truncationMode(.middle)
                        .frame(width: 92, alignment: .leading)
                    GeometryReader { g in
                        ZStack(alignment: .leading) {
                            UnevenRoundedRectangle(bottomTrailingRadius: 3, topTrailingRadius: 3).fill(Theme.accent)
                                .frame(width: max(g.size.width * Double(j.medianSeconds) / longest, 2), height: 9)
                            RoundedRectangle(cornerRadius: 1).fill(Theme.muted)
                                .frame(width: 2, height: 15)
                                .offset(x: min(g.size.width * Double(j.p90Seconds) / longest, g.size.width - 2))
                        }
                        .frame(height: g.size.height)
                    }
                    .frame(height: 16)
                    Text(ciDuration(j.medianSeconds)).font(.ui(size: 11)).monospacedDigit().frame(width: 50, alignment: .trailing)
                    if j.failed > 0 {
                        Text("✕\(j.failed)").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.err).frame(width: 26, alignment: .trailing)
                    } else {
                        Color.clear.frame(width: 26)
                    }
                }
                .help("\(j.name): median \(ciDuration(j.medianSeconds)), slowest 10% \(ciDuration(j.p90Seconds)), failed \(j.failed) of \(j.runs)")
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("\(j.name): median \(ciDuration(j.medianSeconds)), slowest tenth \(ciDuration(j.p90Seconds)), failed \(j.failed) of \(j.runs)")
            }
            if s.jobsPending > 0 {
                Text("Reading the jobs of \(s.jobsPending) more run\(s.jobsPending == 1 ? "" : "s")…")
                    .font(.ui(size: 11)).foregroundStyle(Theme.muted)
            }
        }
    }
}

private struct Flaky: View {
    @Environment(\.conchTheme) private var look
    let items: [CIFlaky]

    var body: some View {
        let _ = look
        CISection(title: "Flaky", note: "failed, then passed with nothing changed") {
            ForEach(items.prefix(6), id: \.name) { f in
                VStack(alignment: .leading, spacing: 1) {
                    HStack {
                        Text(f.name).font(.ui(size: 11.5, design: .monospaced)).lineLimit(1).truncationMode(.middle)
                        Spacer(minLength: 6)
                        Text("\(f.times)×").font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.warn)
                    }
                    Text([f.how ?? "", f.last.map { agoText($0) } ?? ""].filter { !$0.isEmpty }.joined(separator: " · "))
                        .font(.ui(size: 10.5)).foregroundStyle(Theme.muted).lineLimit(1)
                }
                .accessibilityElement(children: .combine)
            }
        }
    }
}

/// The failures that can be run again: each workflow's newest run on a
/// branch, where it failed.
private struct Failing: View {
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel
    let runs: [CIRun]

    var body: some View {
        let _ = look
        CISection(title: "Failing now", note: "the newest run of each workflow, per branch") {
            ForEach(runs.prefix(8)) { r in
                HStack(spacing: 8) {
                    Circle().fill(Theme.err).frame(width: 7, height: 7)
                    VStack(alignment: .leading, spacing: 1) {
                        Text("\(r.workflow) · \(r.branch ?? "")").font(.ui(size: 12, weight: .medium)).lineLimit(1).truncationMode(.middle)
                        Text([r.conclusion == "timed_out" ? "timed out" : "failed", (r.attempt ?? 1) > 1 ? "attempt \(r.attempt ?? 1)" : "",
                              r.at.map { agoText($0) } ?? ""].filter { !$0.isEmpty }.joined(separator: " · "))
                            .font(.ui(size: 10.5)).foregroundStyle(Theme.muted)
                    }
                    Spacer(minLength: 4)
                    if let u = r.url, let url = URL(string: u) {
                        Button { NSWorkspace.shared.open(url) } label: { LucideIcon("globe", color: Theme.muted, size: 11).padding(3).contentShape(Rectangle()) }
                            .buttonStyle(.plain).help("Open the run on GitHub").accessibilityLabel("Open the run on GitHub")
                    }
                    if model.canRerun {
                        let busy = model.rerunning[r.id] == (r.attempt ?? 1)
                        Button(busy ? "Re-running…" : "Re-run Failed Jobs") { model.rerun(run: r.id, attempt: r.attempt ?? 1) }
                            .controlSize(.small).disabled(busy)
                            .help("Runs this run's failed jobs again on GitHub, as a new attempt")
                    }
                }
            }
        }
    }
}
