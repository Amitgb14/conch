import AppKit
import Charts
import Combine
import ConchKit
import SwiftUI

/// The System Monitor: the whole machine beside what conch uses of it —
/// CPU and each core, memory and where it goes, load, disk, the heaviest
/// processes with the pane each of conch's came from — for this computer
/// and every machine conch reaches (system.stats.v1). It only shows: it
/// never stops a process.

/// The window, made once and brought forward again.
@MainActor
final class SystemMonitorWindow {
    private var window: NSWindow?
    private let store: Store
    private var watch: [Any] = []
    private var look: AnyCancellable?

    init(store: Store) { self.store = store }

    func show() {
        if window == nil {
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1180, height: 820),
                             styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
            w.title = "System Monitor"
            w.isReleasedWhenClosed = false
            w.minSize = NSSize(width: 720, height: 480)
            w.contentView = NSHostingView(rootView: SystemMonitorView(store: store, look: store.look))
            look = store.look.$theme.sink { [weak w] t in
                w?.appearance = NSAppearance(named: t.light ? .aqua : .darkAqua)
                w?.backgroundColor = Theme.ns(t.chrome)
            }
            let store = store
            watch.append(NotificationCenter.default.addObserver(forName: NSWindow.willCloseNotification, object: w, queue: .main) { _ in
                MainActor.assumeIsolated { store.stopSystemPolling() }
            })
            w.center()
            w.setFrameAutosaveName("conch.system")
            window = w
        }
        store.startSystemPolling()
        window?.makeKeyAndOrderFront(nil)
    }
}

/// The colours the figures are drawn in: blue for the processor and
/// orange for memory, told apart by lightness as well as hue.
enum SysColor {
    static let cpu = Color(red: 0.30, green: 0.55, blue: 1.0)
    static let cpuSys = Color(red: 0.18, green: 0.37, blue: 0.72)
    static let mem = Color(red: 0.95, green: 0.64, blue: 0.23)
    static let memWired = Color(red: 0.78, green: 0.49, blue: 0.13)
    static let memComp = Color(red: 0.54, green: 0.35, blue: 0.11)
    static let cached = Color(red: 0.42, green: 0.46, blue: 0.53)
    static let conch = Color(red: 0.65, green: 0.55, blue: 0.98)

    /// A process's share of a core: as the resource monitor colours it.
    @MainActor static func level(_ pct: Double) -> Color {
        switch cpuLevel(pct) {
        case .hot: return Theme.err
        case .busy: return Theme.warn
        case .fine: return cpu
        }
    }
}

struct SystemMonitorView: View {
    @ObservedObject var store: Store
    @ObservedObject var look: ShownTheme
    @State private var range: TimeInterval = 900
    @State private var sort: SystemSort = .cpu
    @State private var filter = ""
    @State private var conchOnly = false
    @State private var selected: Int?

    var machine: String { store.systemMachine }
    var stats: SystemStats? { store.systemStats[machine] }

    var body: some View {
        HStack(spacing: 0) {
            sidebar.frame(width: 240)
            Rectangle().fill(Theme.line).frame(width: 1)
            VStack(spacing: 0) {
                toolbar
                Rectangle().fill(Theme.line).frame(height: 1)
                ScrollView { content.padding(20) }
            }
        }
        .background(Theme.window)
        .font(.ui(size: 13))
        .foregroundStyle(Theme.text)
        .preferredColorScheme(look.theme.light ? .light : .dark)
        .environment(\.conchTheme, look.theme.id)
    }

    // MARK: sidebar

    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 6) {
            label("Machines").padding(.horizontal, 8).padding(.bottom, 2)
            ScrollView {
                VStack(spacing: 4) {
                    ForEach(store.machines) { m in machineRow(m) }
                }
            }
            Rectangle().fill(Theme.line).frame(height: 1).padding(.vertical, 8)
            label("Show").padding(.horizontal, 8)
            Toggle("Only what conch started", isOn: $conchOnly).toggleStyle(.checkbox).padding(.horizontal, 8)
            Spacer(minLength: 0)
            Text("CPU 100% = the whole machine in the graphs, one core in the table.")
                .font(.ui(size: 11)).foregroundStyle(Theme.muted).padding(8)
        }
        .padding(.vertical, 14).padding(.horizontal, 8)
        .background(Theme.side)
    }

    private func machineRow(_ m: MachineLink) -> some View {
        let s = store.systemStats[m.id]
        let on = m.id == machine
        return Button {
            store.systemMachine = m.id
            selected = nil
            Task { await store.readSystem(m.id, summary: false) }
        } label: {
            VStack(alignment: .leading, spacing: 5) {
                HStack(spacing: 6) {
                    Circle().fill(m.online ? Theme.ok : Theme.muted).frame(width: 7, height: 7)
                    Text(m.id == "local" ? "This computer" : m.label).font(.ui(size: 13, weight: .semibold)).lineLimit(1)
                    Spacer()
                }
                if let s, s.host.cores > 0 {
                    Text(s.host.summary(memory: s.memory?.total)).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(2)
                    miniBar("CPU", s.cpu.map { $0.total / 100 }, SysColor.cpu)
                    miniBar("Mem", s.memory?.usedFraction, SysColor.mem)
                } else if let why = store.systemWhy[m.id] {
                    Text(why).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(3)
                }
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 8).fill(on ? Theme.raise : .clear))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(on ? Theme.line2 : .clear))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private func miniBar(_ name: String, _ f: Double?, _ color: Color) -> some View {
        HStack(spacing: 6) {
            Text(name).font(.ui(size: 11)).foregroundStyle(Theme.muted).frame(width: 30, alignment: .leading)
            bar(f ?? 0, color, height: 4)
            Text(f.map { "\(Int(($0 * 100).rounded()))%" } ?? "…").font(.ui(size: 11, design: .monospaced))
                .foregroundStyle(Theme.text2).frame(width: 36, alignment: .trailing)
        }
    }

    // MARK: toolbar

    private var toolbar: some View {
        HStack(spacing: 12) {
            Text(machineName).font(.ui(size: 15, weight: .semibold))
            if let s = stats {
                Text(s.host.summary(memory: s.memory?.total)).foregroundStyle(Theme.muted).lineLimit(1)
                if let up = s.host.uptime, up > 0 { Text("· up \(formatSpan(TimeInterval(up)))").foregroundStyle(Theme.muted) }
            }
            Spacer()
            Picker("", selection: $range) {
                ForEach(systemRanges, id: \.seconds) { r in Text(r.label).tag(r.seconds) }
            }
            .pickerStyle(.segmented).frame(width: 200).labelsHidden()
            Button(store.systemPaused ? "Resume" : "Pause") { store.systemPaused.toggle() }
            Text("every 2 s").font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted)
        }
        .padding(.horizontal, 20).padding(.vertical, 10)
        .background(Theme.chrome)
    }

    private var machineName: String {
        if let s = stats, let n = s.host.name, !n.isEmpty, machine == "local" { return n }
        return store.machines.first { $0.id == machine }.map { $0.id == "local" ? "This computer" : $0.label } ?? machine
    }

    // MARK: content

    @ViewBuilder private var content: some View {
        if let s = stats {
            VStack(alignment: .leading, spacing: 14) {
                tiles(s)
                let notes = heavyNotes(s)
                if !notes.isEmpty { heavy(notes) }
                charts(s)
                breakdown(s)
                processes(s)
            }
        } else {
            VStack(spacing: 10) {
                if let why = store.systemWhy[machine] {
                    LucideIcon("triangle-alert", color: Theme.warn, size: 22)
                    Text(why).foregroundStyle(Theme.text2).multilineTextAlignment(.center)
                } else {
                    ProgressView().controlSize(.small)
                    Text("Reading the machine…").foregroundStyle(Theme.muted)
                }
            }
            .frame(maxWidth: .infinity, minHeight: 300)
        }
    }

    private func tiles(_ s: SystemStats) -> some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 220), spacing: 12, alignment: .top)], spacing: 12) {
            card {
                label("CPU")
                bigFigure(s.cpu.map { "\(Int($0.total.rounded()))" } ?? "…", unit: "%")
                Text(cpuLine(s)).font(.ui(size: 12)).foregroundStyle(Theme.muted)
                sparkline(store.systemHistory[machine]?.window(120, now: Date()) ?? [])
            }
            card {
                label("Memory")
                if let m = s.memory {
                    bigFigure(formatBytes(m.used).components(separatedBy: " ").first ?? "", unit: "\(formatBytes(m.used).components(separatedBy: " ").last ?? "") of \(formatBytes(m.total, whole: true))")
                    Text(memLine(m)).font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    pressure(m.pressure).padding(.top, 8)
                } else {
                    bigFigure("…", unit: "")
                }
            }
            card {
                label("Load average")
                if s.load.count == 3 {
                    HStack(alignment: .firstTextBaseline, spacing: 10) {
                        bigFigure(String(format: "%.2f", s.load[0]), unit: "")
                        Text(String(format: "%.2f · %.2f", s.load[1], s.load[2])).font(.ui(size: 13, design: .monospaced)).foregroundStyle(Theme.muted)
                            .lineLimit(1).fixedSize()
                    }
                    Text("1 · 5 · 15 min · \(s.procs) processes").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                    if s.host.cores > 0 { bar(min(s.load[0] / Double(s.host.cores), 1), SysColor.cpu, height: 8).padding(.top, 8) }
                } else {
                    bigFigure("—", unit: "")
                    Text("\(s.procs) processes").font(.ui(size: 12)).foregroundStyle(Theme.muted)
                }
            }
            card {
                label("Disk")
                if let d = s.disk, d.total > 0 {
                    bigFigure(formatBytes(d.free).components(separatedBy: " ").first ?? "", unit: "\(formatBytes(d.free).components(separatedBy: " ").last ?? "") free of \(formatBytes(d.total, whole: true))")
                    Text(d.path).font(.ui(size: 12)).foregroundStyle(Theme.muted).lineLimit(1).truncationMode(.middle)
                    bar(1 - Double(d.free) / Double(d.total), SysColor.cached, height: 8).padding(.top, 8)
                } else {
                    bigFigure("—", unit: "")
                }
            }
        }
    }

    private func cpuLine(_ s: SystemStats) -> String {
        var parts: [String] = []
        if let c = s.cpu, c.split { parts.append("user \(Int(c.user.rounded()))% · system \(Int(c.system.rounded()))%") }
        if s.host.cores > 0 { parts.append("\(s.host.cores) cores") }
        return parts.joined(separator: " · ")
    }

    private func memLine(_ m: SystemMemory) -> String {
        var parts = ["pressure \(m.pressure ?? "unknown")"]
        if m.swapTotal > 0 { parts.append("swap \(formatBytes(m.swapUsed)) of \(formatBytes(m.swapTotal))") }
        return parts.joined(separator: " · ")
    }

    private func pressure(_ p: String?) -> some View {
        let n = p == "critical" ? 6 : p == "warn" ? 4 : p == "normal" ? 2 : 0
        let color = p == "critical" ? Theme.err : p == "warn" ? Theme.warn : Theme.ok
        return HStack(spacing: 3) {
            ForEach(0..<6, id: \.self) { i in
                RoundedRectangle(cornerRadius: 2).fill(i < n ? color : Theme.raise).frame(height: 8)
            }
        }
        .accessibilityLabel("Memory pressure: \(p ?? "unknown")")
    }

    private func sparkline(_ pts: [SystemPoint]) -> some View {
        Chart(pts, id: \.at) { p in
            AreaMark(x: .value("Time", p.at), y: .value("CPU", p.cpu)).foregroundStyle(SysColor.cpu.opacity(0.2))
            LineMark(x: .value("Time", p.at), y: .value("CPU", p.cpu)).foregroundStyle(SysColor.cpu)
        }
        .chartYScale(domain: 0...100)
        .chartXAxis(.hidden).chartYAxis(.hidden)
        .frame(height: 34).padding(.top, 6)
    }

    private func heavy(_ notes: [HeavyNote]) -> some View {
        card {
            HStack(spacing: 8) {
                LucideIcon("triangle-alert", color: Theme.warn, size: 14)
                Text("Heavy right now").font(.ui(size: 13, weight: .semibold))
            }
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 280), spacing: 10)], spacing: 10) {
                ForEach(Array(notes.enumerated()), id: \.offset) { _, n in
                    Button {
                        if let pid = n.pid { selected = pid; filter = "" }
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(n.title).foregroundStyle(n.kind == .cpu ? SysColor.level(400) : n.kind == .pressure ? Theme.warn : Theme.text)
                                .font(.ui(size: 13, weight: .medium)).lineLimit(2)
                            Text(n.detail).font(.ui(size: 12)).foregroundStyle(Theme.muted).lineLimit(1)
                        }
                        .padding(10).frame(maxWidth: .infinity, alignment: .leading)
                        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise))
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.top, 4)
        }
    }

    private func charts(_ s: SystemStats) -> some View {
        let now = Date()
        let pts = store.systemHistory[machine]?.window(range, now: now) ?? []
        // From the first point held, not the range's start, while there is
        // less than the range: a minute squeezed into the last fifteenth of
        // a graph says nothing.
        let from = max(now.addingTimeInterval(-range), pts.first?.at ?? now.addingTimeInterval(-60))
        let split = s.cpu?.split ?? false
        let total = Double(s.memory?.total ?? 0)
        return LazyVGrid(columns: [GridItem(.adaptive(minimum: 420), spacing: 12)], spacing: 12) {
            card {
                HStack {
                    label("CPU history")
                    Spacer()
                    if split { legend([("user", SysColor.cpu.opacity(0.6)), ("system", SysColor.cpuSys)]) }
                }
                Chart {
                    ForEach(pts, id: \.at) { p in
                        if split {
                            AreaMark(x: .value("Time", p.at), y: .value("%", p.system), stacking: .standard)
                                .foregroundStyle(by: .value("Kind", "system"))
                            AreaMark(x: .value("Time", p.at), y: .value("%", p.user), stacking: .standard)
                                .foregroundStyle(by: .value("Kind", "user"))
                        } else {
                            AreaMark(x: .value("Time", p.at), y: .value("%", p.cpu)).foregroundStyle(SysColor.cpu.opacity(0.35))
                            LineMark(x: .value("Time", p.at), y: .value("%", p.cpu)).foregroundStyle(SysColor.cpu)
                        }
                    }
                }
                .chartForegroundStyleScale(["system": SysColor.cpuSys, "user": SysColor.cpu.opacity(0.6)])
                .chartLegend(.hidden)
                .chartYScale(domain: 0...100)
                .chartXScale(domain: from...now)
                .chartYAxis { AxisMarks(values: [0, 50, 100]) { v in AxisGridLine(); AxisValueLabel { Text("\(v.as(Int.self) ?? 0)%") } } }
                .frame(height: 180)
                waiting(pts)
            }
            card {
                HStack {
                    label("Memory history")
                    Spacer()
                    legend([("used", SysColor.mem.opacity(0.6)), ("compressed", SysColor.memComp)])
                }
                Chart {
                    ForEach(pts, id: \.at) { p in
                        // Unstacked: compressed is part of used, not on top of it.
                        AreaMark(x: .value("Time", p.at), y: .value("GB", Double(p.memUsed) / 1_073_741_824),
                                 series: .value("s", "used"), stacking: .unstacked)
                            .foregroundStyle(SysColor.mem.opacity(0.3))
                        LineMark(x: .value("Time", p.at), y: .value("GB", Double(p.memUsed) / 1_073_741_824), series: .value("s", "used line"))
                            .foregroundStyle(SysColor.mem)
                        if p.compressed > 0 {
                            AreaMark(x: .value("Time", p.at), y: .value("GB", Double(p.compressed) / 1_073_741_824),
                                     series: .value("s", "compressed"), stacking: .unstacked)
                                .foregroundStyle(SysColor.memComp)
                        }
                    }
                }
                .chartYScale(domain: 0...max(total / 1_073_741_824, 1))
                .chartXScale(domain: from...now)
                .chartYAxis { AxisMarks { v in AxisGridLine(); AxisValueLabel { Text("\(v.as(Int.self) ?? 0) GB") } } }
                .chartPlotStyle { $0.clipped() }
                .frame(height: 180)
                waiting(pts)
            }
        }
    }

    /// Said under an empty graph: the server keeps history only while
    /// somebody asks, so a window just opened starts with none.
    @ViewBuilder private func waiting(_ pts: [SystemPoint]) -> some View {
        if pts.count < 2 {
            Text("Collecting — conch keeps an hour of history while this window or another asks.")
                .font(.ui(size: 11)).foregroundStyle(Theme.muted)
        }
    }

    private func breakdown(_ s: SystemStats) -> some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 420), spacing: 12)], spacing: 12) {
            card {
                HStack {
                    label("Per core")
                    Spacer()
                    if let p = s.host.pCores, let e = s.host.eCores { Text("\(p) performance · \(e) efficiency").font(.ui(size: 11)).foregroundStyle(Theme.muted) }
                }
                if let cores = s.cpu?.cores, !cores.isEmpty {
                    let pc = s.host.pCores ?? 0
                    Chart(Array(cores.enumerated()), id: \.offset) { i, v in
                        BarMark(x: .value("Core", coreName(i, pc)), y: .value("%", v))
                            .foregroundStyle(v >= 90 ? Theme.err : SysColor.cpu.opacity(pc > 0 && i >= pc ? 0.6 : 1))
                            .cornerRadius(3)
                    }
                    .chartYScale(domain: 0...100)
                    .chartYAxis { AxisMarks(values: [0, 50, 100]) { v in AxisGridLine(); AxisValueLabel { Text("\(v.as(Int.self) ?? 0)%") } } }
                    .frame(height: 150)
                } else {
                    Text(s.cpu == nil ? "Measuring…" : "This machine does not say how each core is used.")
                        .font(.ui(size: 12)).foregroundStyle(Theme.muted).frame(maxWidth: .infinity, minHeight: 150)
                }
            }
            card {
                HStack {
                    label("Where memory goes")
                    Spacer()
                    if let m = s.memory { Text(formatBytes(m.total, whole: true)).font(.ui(size: 11, design: .monospaced)).foregroundStyle(Theme.muted) }
                }
                if let m = s.memory, m.total > 0 {
                    let parts = m.parts
                    GeometryReader { g in
                        HStack(spacing: 2) {
                            ForEach(Array(parts.enumerated()), id: \.offset) { i, part in
                                Rectangle().fill(partColor(part.name))
                                    .frame(width: max(g.size.width * CGFloat(Double(part.bytes) / Double(m.total)) - 2, 0))
                            }
                        }
                    }
                    .frame(height: 18).clipShape(RoundedRectangle(cornerRadius: 5)).padding(.top, 8)
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), spacing: 16)], alignment: .leading, spacing: 8) {
                        ForEach(Array(parts.enumerated()), id: \.offset) { _, part in legendRow(part.name, partColor(part.name), formatBytes(part.bytes)) }
                        let ours = s.processes.filter(\.isConch).reduce(UInt64(0)) { $0 + $1.rss }
                        if ours > 0 { legendRow("of which conch", SysColor.conch, formatBytes(ours)) }
                    }
                    .padding(.top, 10)
                }
            }
        }
    }

    private func coreName(_ i: Int, _ pCores: Int) -> String {
        guard pCores > 0 else { return "\(i + 1)" }
        return i < pCores ? "P\(i + 1)" : "E\(i - pCores + 1)"
    }

    private func partColor(_ name: String) -> Color {
        switch name {
        case "Apps", "Used": return SysColor.mem
        case "Wired": return SysColor.memWired
        case "Compressed": return SysColor.memComp
        case "Cached files": return SysColor.cached
        default: return Theme.raise
        }
    }

    // MARK: processes

    private func processes(_ s: SystemStats) -> some View {
        let rows = systemRows(s.processes, sort: sort, filter: filter, conchOnly: conchOnly)
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                Text("Processes").font(.ui(size: 14, weight: .semibold))
                Text("\(rows.count) of \(s.procs) · the busiest, the largest and all of conch's · CPU is % of one core")
                    .font(.ui(size: 12)).foregroundStyle(Theme.muted).lineLimit(1)
                Spacer()
                TextField("Filter by name, pid, pane…", text: $filter).textFieldStyle(.roundedBorder).frame(width: 220)
                Picker("Sort by", selection: $sort) {
                    ForEach(SystemSort.allCases, id: \.self) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented).frame(width: 220).labelsHidden()
            }
            .padding(14)
            processHeader
            LazyVStack(spacing: 0) {
                ForEach(rows) { p in processRow(p, cores: s.host.cores) }
            }
            if let pid = selected, let p = s.processes.first(where: { $0.pid == pid }) {
                detail(p)
            }
        }
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.chrome))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
    }

    private var processHeader: some View {
        HStack(spacing: 12) {
            Text("Name").frame(maxWidth: .infinity, alignment: .leading)
            Text("PID").frame(width: 70, alignment: .leading)
            Text("CPU").frame(width: 170, alignment: .leading)
            Text("Memory").frame(width: 90, alignment: .trailing)
            Text("Started by").frame(width: 260, alignment: .leading)
        }
        .font(.ui(size: 11, weight: .semibold)).foregroundStyle(Theme.muted)
        .padding(.horizontal, 14).padding(.vertical, 8)
        .background(Theme.side)
    }

    private func processRow(_ p: SystemProcess, cores: Int) -> some View {
        let on = selected == p.pid
        return Button { selected = on ? nil : p.pid } label: {
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 1) {
                    Text(p.name).font(.ui(size: 13, weight: .medium, design: .monospaced)).lineLimit(1).truncationMode(.tail)
                    if let u = p.user { Text(u).font(.ui(size: 11)).foregroundStyle(Theme.muted) }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                Text(String(p.pid)).font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.muted).frame(width: 70, alignment: .leading)
                HStack(spacing: 8) {
                    bar(min((p.cpu ?? 0) / 400, 1), SysColor.level(p.cpu ?? 0), height: 6)
                    Text(formatCorePct(p.cpu)).font(.ui(size: 12, design: .monospaced))
                        .foregroundStyle((p.cpu ?? 0) >= 80 ? SysColor.level(p.cpu ?? 0) : Theme.text).frame(width: 56, alignment: .trailing)
                }
                .frame(width: 170)
                Text(formatBytes(p.rss)).font(.ui(size: 12, design: .monospaced)).frame(width: 90, alignment: .trailing)
                Group {
                    if let o = p.origin {
                        Text(o).font(.ui(size: 12)).foregroundStyle(SysColor.conch).lineLimit(1).truncationMode(.middle)
                            .padding(.horizontal, 8).padding(.vertical, 3)
                            .background(RoundedRectangle(cornerRadius: 5).fill(SysColor.conch.opacity(0.14)))
                    } else {
                        Text("—").foregroundStyle(Theme.muted)
                    }
                }
                .frame(width: 260, alignment: .leading)
            }
            .padding(.horizontal, 14).padding(.vertical, 7)
            .frame(minHeight: 40)
            .background(on ? Theme.raise : Color.clear)
            .overlay(alignment: .leading) { if on { Rectangle().fill(SysColor.cpu).frame(width: 2) } }
            .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(p.name), pid \(p.pid), CPU \(formatCorePct(p.cpu)), memory \(formatBytes(p.rss))\(p.origin.map { ", " + $0 } ?? "")")
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    /// The selected process: its whole command, which a copy icon puts on
    /// the clipboard, where it came from, and — for one of conch's — the
    /// pane it runs in.
    private func detail(_ p: SystemProcess) -> some View {
        HStack(alignment: .center, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(p.command ?? p.name).font(.ui(size: 13, weight: .semibold, design: .monospaced))
                        .lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                    Button {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(p.command ?? p.name, forType: .string)
                    } label: {
                        LucideIcon("copy", color: Theme.text2, size: 13)
                            .frame(width: 26, height: 26)
                            .background(RoundedRectangle(cornerRadius: 6).fill(Theme.raise))
                    }
                    .buttonStyle(.plain)
                    .help("Copy command")
                    .accessibilityLabel("Copy command")
                }
                Text(detailLine(p)).font(.ui(size: 12)).foregroundStyle(Theme.muted)
            }
            Spacer()
            if let pane = p.pane, !pane.isEmpty {
                Button("Show pane") { store.showPaneOf(machine: machine, pane: pane) }
                    .help("Open the pane this process runs in")
            }
        }
        .padding(14)
        .background(Theme.side)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

    private func detailLine(_ p: SystemProcess) -> String {
        var parts = ["pid \(p.pid)", "parent \(p.ppid)"]
        if let s = p.started, s.timeIntervalSince1970 > 0 { parts.append("started \(formatSpan(Date().timeIntervalSince(s))) ago") }
        parts.append(p.origin ?? "not started by conch")
        return parts.joined(separator: " · ")
    }

    // MARK: pieces

    private func card<C: View>(@ViewBuilder _ c: () -> C) -> some View {
        VStack(alignment: .leading, spacing: 4, content: c)
            .padding(16)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .background(RoundedRectangle(cornerRadius: 10).fill(Theme.chrome))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line))
    }

    private func label(_ s: String) -> some View {
        Text(s.uppercased()).font(.ui(size: 11, weight: .semibold)).tracking(0.6).foregroundStyle(Theme.muted)
    }

    private func bigFigure(_ v: String, unit: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(v).font(.ui(size: 28, weight: .semibold, design: .monospaced)).lineLimit(1).fixedSize()
            Text(unit).foregroundStyle(Theme.muted).lineLimit(1)
        }
        .padding(.top, 4)
    }

    private func bar(_ f: Double, _ color: Color, height: CGFloat) -> some View {
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(Theme.raise)
                Capsule().fill(color).frame(width: g.size.width * CGFloat(min(max(f, 0), 1)))
            }
        }
        .frame(height: height)
    }

    private func legend(_ items: [(String, Color)]) -> some View {
        HStack(spacing: 12) {
            ForEach(items, id: \.0) { name, c in
                HStack(spacing: 5) {
                    RoundedRectangle(cornerRadius: 2).fill(c).frame(width: 10, height: 10)
                    Text(name).font(.ui(size: 11)).foregroundStyle(Theme.muted)
                }
            }
        }
    }

    private func legendRow(_ name: String, _ c: Color, _ value: String) -> some View {
        HStack(spacing: 8) {
            RoundedRectangle(cornerRadius: 2).fill(c).frame(width: 10, height: 10)
                .overlay(RoundedRectangle(cornerRadius: 2).stroke(Theme.line2))
            Text(name).font(.ui(size: 12))
            Spacer()
            Text(value).font(.ui(size: 12, design: .monospaced))
        }
    }
}
