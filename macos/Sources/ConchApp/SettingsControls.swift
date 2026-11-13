import AppKit
import ConchKit
import SwiftUI

// The Settings window's controls, drawn in conch's own look rather than
// the system's grey ones: a card of rows, a switch, segments with a sliding
// pill, a menu with pictures and a search, a stepper, a slider, swatches,
// pictures of a layout, a key recorder, fields that say what is wrong as
// you type, chips, the quiet hours as a day, and the autopilot as a line.
// Which one a setting gets is ConchKit's settingsControl.

/// Settings in a rounded card, a hairline between rows.
struct SettingsCard<Content: View>: View {
    var title: String?
    var trailing: AnyView?
    @ViewBuilder var content: Content
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 0) {
            if let title {
                HStack {
                    Text(title.uppercased()).font(.ui(size: 11, weight: .semibold)).tracking(0.6).foregroundStyle(Theme.text2)
                    Spacer()
                    trailing
                }
                .padding(.horizontal, 16).padding(.top, 12).padding(.bottom, 2)
            }
            content
        }
        .background(RoundedRectangle(cornerRadius: 10).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Theme.line, lineWidth: 1))
    }
}

/// A row of a card: what the setting is, an edited mark while it differs
/// from what was saved, and its control at the right.
struct SettingsRow<Control: View>: View {
    let label: String
    var help: String?
    var changed = false
    var first = false
    var wide = false
    /// Drawn under the row at its full width: the chosen theme's prompt.
    var below: AnyView?
    @ViewBuilder var control: Control
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .center, spacing: 16) {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 8) {
                        Text(label).font(.ui(size: 13, weight: .medium)).foregroundStyle(Theme.text)
                        if changed {
                            Text("edited").font(.ui(size: 10, weight: .semibold)).foregroundStyle(Theme.warn)
                                .padding(.horizontal, 5).padding(.vertical, 1)
                                .overlay(RoundedRectangle(cornerRadius: 4).stroke(Theme.warn.opacity(0.45)))
                                .transition(.opacity.combined(with: .scale(scale: 0.8)))
                        }
                    }
                    if let h = help, !h.isEmpty {
                        Text(h).font(.ui(size: 12)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 12)
                if !wide { control }
            }
            if wide { control }
            if let below { below }
        }
        .padding(.horizontal, 16).padding(.vertical, 12)
        .overlay(alignment: .top) { if !first { Rectangle().fill(Theme.line).frame(height: 1) } }
        .animation(.easeOut(duration: 0.15), value: changed)
    }
}

/// An on/off switch whose knob springs across.
struct ConchSwitch: View {
    @Binding var isOn: Bool
    var tint: Color? = nil
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        Button { withAnimation(.spring(response: 0.25, dampingFraction: 0.7)) { isOn.toggle() } } label: {
            ZStack(alignment: isOn ? .trailing : .leading) {
                Capsule().fill(isOn ? (tint ?? Theme.accent) : Theme.line2).frame(width: 38, height: 22)
                Circle().fill(.white).frame(width: 18, height: 18).shadow(color: .black.opacity(0.35), radius: 1.5, y: 1).padding(2)
            }
        }
        .buttonStyle(.plain)
        .accessibilityRepresentation { Toggle("", isOn: $isOn) }
    }
}

/// A few choices side by side; a pill slides to the chosen one.
struct Segments: View {
    let options: [SettingsOption]
    @Binding var value: String
    @Namespace private var ns
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        HStack(spacing: 0) {
            ForEach(options, id: \.value) { o in
                let on = o.value == value
                Button { withAnimation(.spring(response: 0.28, dampingFraction: 0.8)) { value = o.value } } label: {
                    Text(o.label).font(.ui(size: 12, weight: on ? .medium : .regular))
                        .foregroundStyle(on ? Theme.text : Theme.text2)
                        .padding(.horizontal, 12).padding(.vertical, 5)
                        .background {
                            if on {
                                RoundedRectangle(cornerRadius: 6).fill(Theme.raise2)
                                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line2))
                                    .shadow(color: .black.opacity(0.3), radius: 1, y: 1)
                                    .matchedGeometryEffect(id: "pill", in: ns)
                            }
                        }
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(on ? .isSelected : [])
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
    }
}

/// A menu drawn as a field: the choice with its mark, and a popover of
/// every choice — marks, a search once there are many, a check on the
/// chosen one. allowsOther lets a value the list lacks be typed.
struct RichMenu: View {
    let options: [SettingsOption]
    @Binding var value: String
    var agents = false
    var placeholder = "Choose…"
    /// Its width; nil fills what it is given (a side panel).
    var width: CGFloat? = 240
    @State private var open = false
    @State private var query = ""
    @State private var hover: String?
    @Environment(\.conchTheme) private var theme

    var current: SettingsOption? { options.first { $0.value == value } }
    var shown: [SettingsOption] {
        let q = query.lowercased()
        return q.isEmpty ? options : options.filter { ($0.label + " " + $0.value).lowercased().contains(q) }
    }

    @ViewBuilder func mark(_ v: String) -> some View {
        if agents && !v.isEmpty {
            Mark(agent: v).frame(width: 22, height: 22)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.agent(v).opacity(0.14)))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.agent(v).opacity(0.35)))
        } else if agents {
            Image(systemName: "nosign").font(.system(size: 11)).foregroundStyle(Theme.muted).frame(width: 22, height: 22)
        }
    }

    var body: some View {
        let _ = theme
        Button { open.toggle() } label: {
            HStack(spacing: 8) {
                mark(value)
                Text(current?.label ?? (value.isEmpty ? placeholder : value)).font(.ui(size: 12.5)).foregroundStyle(Theme.text).lineLimit(1)
                Spacer(minLength: 6)
                Image(systemName: "chevron.down").font(.system(size: 9, weight: .bold)).foregroundStyle(Theme.muted)
                    .rotationEffect(.degrees(open ? 180 : 0))
            }
            .padding(.horizontal, 10).frame(width: width, height: 32)
            .frame(maxWidth: width == nil ? .infinity : nil)
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.raise2))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(open ? Theme.accent : Theme.line2, lineWidth: open ? 1.5 : 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .animation(.easeOut(duration: 0.15), value: open)
        .popover(isPresented: $open, arrowEdge: .bottom) {
            VStack(alignment: .leading, spacing: 2) {
                if options.count > 6 {
                    TextField("Search…", text: $query).textFieldStyle(.plain).font(.ui(size: 12.5))
                        .padding(.horizontal, 8).frame(height: 28)
                        .background(RoundedRectangle(cornerRadius: 6).fill(Theme.well))
                        .padding(.bottom, 4)
                }
                ScrollView {
                    LazyVStack(spacing: 2) {
                        ForEach(shown, id: \.value) { o in
                            Button {
                                value = o.value
                                open = false
                                query = ""
                            } label: {
                                HStack(spacing: 10) {
                                    mark(o.value)
                                    Text(o.label).font(.ui(size: 12.5)).foregroundStyle(Theme.text)
                                    Spacer()
                                    if o.value == value { Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Theme.accent) }
                                }
                                .padding(.horizontal, 8).frame(height: agents ? 34 : 28)
                                .background(RoundedRectangle(cornerRadius: 6).fill(hover == o.value ? Theme.accentDeep.opacity(0.35) : .clear))
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .onHover { hover = $0 ? o.value : (hover == o.value ? nil : hover) }
                        }
                        if shown.isEmpty { Text("Nothing matches").font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(8) }
                    }
                }
                .frame(maxHeight: 320)
            }
            .padding(6).frame(width: 280)
            .background(Theme.raise)
        }
    }
}

/// A number with − and + either side, typed into as well.
struct StepperField: View {
    @Binding var value: Double?
    let step: Double
    let field: SettingsField
    var fallback: Double = 0
    @State private var text = ""
    @State private var problem: String?
    @Environment(\.conchTheme) private var theme

    func show(_ v: Double?) -> String {
        guard let v else { return "" }
        return field.kind == "int" ? String(Int(v)) : String(format: "%.1f", v)
    }

    var body: some View {
        let _ = theme
        VStack(alignment: .trailing, spacing: 3) {
            HStack(spacing: 0) {
                button("minus", -step)
                TextField(field.placeholder ?? show(fallback), text: $text)
                    .textFieldStyle(.plain).multilineTextAlignment(.center).font(.ui(size: 12.5, design: .monospaced))
                    .frame(width: 58, height: 30)
                    .overlay(alignment: .leading) { Rectangle().fill(Theme.line).frame(width: 1) }
                    .overlay(alignment: .trailing) { Rectangle().fill(Theme.line).frame(width: 1) }
                    .onSubmit(commit)
                    .onChange(of: text) { _, t in
                        problem = settingsProblem(.stepper(step: step), field, t)
                        if problem == nil { value = t.trimmingCharacters(in: .whitespaces).isEmpty ? nil : Double(t) }
                    }
                button("plus", step)
            }
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(problem == nil ? Theme.line2 : Theme.err))
            if let problem { Text(problem).font(.ui(size: 11)).foregroundStyle(Theme.err) }
        }
        .onAppear { text = show(value) }
        .onChange(of: value) { _, v in if Double(text) != v { text = show(v) } }
    }

    func commit() { text = show(value) }

    func button(_ sym: String, _ d: Double) -> some View {
        Button {
            value = stepped(value, by: d, field, fallback: fallback)
            text = show(value)
            problem = nil
        } label: {
            Image(systemName: sym).font(.system(size: 10, weight: .bold)).foregroundStyle(Theme.text2).frame(width: 30, height: 30).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// A number on a slider between two sizes of A, its value beside it.
struct SliderControl: View {
    @Binding var value: Double?
    let min: Double, max: Double, step: Double, unit: String
    var fallback: Double = 100
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        HStack(spacing: 8) {
            Text("A").font(.system(size: 10)).foregroundStyle(Theme.muted)
            Slider(value: Binding(get: { value ?? fallback }, set: { value = ($0 / step).rounded() * step }), in: min...max)
                .frame(width: 170).tint(Theme.accent)
            Text("A").font(.system(size: 16)).foregroundStyle(Theme.muted)
            Text("\(Int(value ?? fallback))\(unit)").font(.ui(size: 12, design: .monospaced)).foregroundStyle(Theme.text2).frame(width: 44, alignment: .trailing)
        }
    }
}

/// The accent: a dot of each colour, and the theme's own.
struct Swatches: View {
    let options: [SettingsOption]
    @Binding var value: String
    @State private var hover: String?
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        HStack(spacing: 8) {
            ForEach(options, id: \.value) { o in
                let on = o.value == value
                let color = accentColors[o.value].map { Theme.color(RGB($0)) }
                Button { value = o.value } label: {
                    ZStack {
                        if let color {
                            Circle().fill(color)
                        } else {
                            // The theme's own: half its accent, half its window.
                            Circle().fill(LinearGradient(colors: [Theme.accentDeep, Theme.raise2], startPoint: .topLeading, endPoint: .bottomTrailing))
                        }
                        if on { Image(systemName: "checkmark").font(.system(size: 9, weight: .heavy)).foregroundStyle(.white) }
                    }
                    .frame(width: 22, height: 22)
                    .overlay(Circle().stroke(on ? Theme.text : .clear, lineWidth: 2).padding(-3))
                    .scaleEffect(hover == o.value ? 1.12 : 1)
                    .animation(.easeOut(duration: 0.12), value: hover)
                }
                .buttonStyle(.plain)
                .help(o.label)
                .onHover { hover = $0 ? o.value : nil }
            }
        }
    }
}

/// [app] tabs: a picture of where the tabs go, for each choice.
struct LayoutPictures: View {
    let options: [SettingsOption]
    @Binding var value: String
    @Environment(\.conchTheme) private var theme

    @ViewBuilder func picture(_ v: String) -> some View {
        let tab = RoundedRectangle(cornerRadius: 3).fill(Theme.accentDeep)
        let pane = RoundedRectangle(cornerRadius: 3).fill(Theme.raise2)
        switch v {
        case "left": HStack(spacing: 4) { tab.frame(width: 20); pane }
        case "right": HStack(spacing: 4) { pane; tab.frame(width: 20) }
        default: VStack(spacing: 4) { tab.frame(height: 8); pane }
        }
    }

    var body: some View {
        let _ = theme
        HStack(spacing: 10) {
            ForEach(options, id: \.value) { o in
                let on = o.value == value
                Button { withAnimation(.easeOut(duration: 0.15)) { value = o.value } } label: {
                    VStack(spacing: 6) {
                        picture(o.value).padding(6).frame(width: 96, height: 60)
                            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
                            .overlay(RoundedRectangle(cornerRadius: 8).stroke(on ? Theme.accent : Theme.line2, lineWidth: on ? 2 : 1))
                        Text(o.label.components(separatedBy: ",").first ?? o.label).font(.ui(size: 11.5)).foregroundStyle(on ? Theme.text : Theme.text2).lineLimit(1)
                    }
                    .frame(width: 100)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(o.label)
            }
        }
    }
}

/// A key, shown as caps, recorded by pressing it.
struct KeyRecorder: View {
    @Binding var value: String
    var placeholder = "ctrl+b"
    @State private var recording = false
    @State private var monitor: Any?
    @State private var problem: String?
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        VStack(alignment: .trailing, spacing: 3) {
            HStack(spacing: 6) {
                ForEach(Array(keyCaps(value.isEmpty ? placeholder : value).enumerated()), id: \.offset) { _, k in
                    Text(k).font(.ui(size: 12, design: .monospaced)).foregroundStyle(value.isEmpty ? Theme.muted : Theme.text)
                        .padding(.horizontal, 8).frame(minWidth: 30, minHeight: 28)
                        .background(RoundedRectangle(cornerRadius: 6).fill(LinearGradient(colors: [Theme.raise2, Theme.raise], startPoint: .top, endPoint: .bottom)))
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(recording ? Theme.accent : Theme.line2))
                        .shadow(color: .black.opacity(0.35), radius: 0, y: 1.5)
                }
                Button(recording ? "Press keys…" : "Record") { recording ? stop() : start() }
                    .buttonStyle(.plain).font(.ui(size: 12, weight: .medium)).foregroundStyle(Theme.accent)
                    .padding(.horizontal, 8).padding(.vertical, 4)
                    .background(RoundedRectangle(cornerRadius: 6).fill(recording ? Theme.accentDeep.opacity(0.35) : .clear))
                if !value.isEmpty && !recording {
                    Button { value = "" } label: { Image(systemName: "arrow.uturn.backward").font(.system(size: 10, weight: .bold)) }
                        .buttonStyle(.plain).foregroundStyle(Theme.muted).help("Back to the default, \(placeholder)")
                }
            }
            if let problem { Text(problem).font(.ui(size: 11)).foregroundStyle(Theme.warn) }
        }
        .onDisappear(perform: stop)
    }

    func start() {
        recording = true
        problem = nil
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { e in
            if e.keyCode == 53 && e.modifierFlags.intersection(.deviceIndependentFlagsMask).isEmpty { stop(); return nil } // esc alone: cancel
            let m = e.modifierFlags
            let arrows: [UInt16: String] = [123: "left", 124: "right", 125: "down", 126: "up"]
            let key = arrows[e.keyCode] ?? (e.charactersIgnoringModifiers ?? "")
            if let n = keyNotation(ctrl: m.contains(.control), alt: m.contains(.option), shift: m.contains(.shift), key: key) {
                value = n
                stop()
            } else {
                problem = "hold ⌃ or ⌥ with it — a key alone would be taken from typing"
            }
            return nil
        }
    }

    func stop() {
        recording = false
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
    }
}

/// A one-line field that says what is wrong as it is typed: an icon
/// before it, a green edge once a checked value is right, a red one and
/// the reason under it when not. A secret has an eye to show it.
struct CheckedField: View {
    let control: SettingsControl
    let field: SettingsField
    @Binding var text: String
    var icon: String?
    var width: CGFloat = 260
    /// Something more to say about a right value (a variable that is set).
    var note: ((String) -> (ok: Bool, text: String)?)? = nil
    @State private var reveal = false
    @FocusState private var focused: Bool
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        let problem = settingsProblem(control, field, text)
        let said = problem == nil && !text.isEmpty ? note?(text) : nil
        let edge: Color = problem != nil ? Theme.err : said?.ok == false ? Theme.warn : focused ? Theme.accent : said?.ok == true ? Theme.ok.opacity(0.6) : Theme.line2
        VStack(alignment: .trailing, spacing: 3) {
            HStack(spacing: 7) {
                if let icon { Image(systemName: icon).font(.system(size: 11)).foregroundStyle(Theme.muted) }
                Group {
                    if control == .secret && !reveal {
                        SecureField(field.placeholder ?? "", text: $text)
                    } else {
                        TextField(field.placeholder ?? "", text: $text)
                    }
                }
                .textFieldStyle(.plain).font(.ui(size: 12.5, design: control == .time || control == .envVar ? .monospaced : .default))
                .focused($focused)
                if control == .secret {
                    Button { reveal.toggle() } label: { Image(systemName: reveal ? "eye.slash" : "eye").font(.system(size: 11)) }
                        .buttonStyle(.plain).foregroundStyle(Theme.muted).help(reveal ? "Hide" : "Show")
                }
            }
            .padding(.horizontal, 10).frame(width: width, height: 32)
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(edge, lineWidth: focused || problem != nil ? 1.5 : 1))
            .animation(.easeOut(duration: 0.12), value: focused)
            if let problem {
                Text(problem).font(.ui(size: 11)).foregroundStyle(Theme.err)
            } else if let said {
                Text((said.ok ? "✓ " : "") + said.text).font(.ui(size: 11)).foregroundStyle(said.ok ? Theme.ok : Theme.warn)
            }
        }
    }
}

/// Numbers as chips: × takes one away, return adds what is typed.
struct ChipsField: View {
    @Binding var values: [Double]
    var unit = "%"
    @State private var typed = ""
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        HStack(spacing: 6) {
            ForEach(Array(values.enumerated()), id: \.offset) { i, v in
                HStack(spacing: 3) {
                    Text("\(Int(v))\(unit)").font(.ui(size: 12))
                    Button { values.remove(at: i) } label: { Image(systemName: "xmark").font(.system(size: 8, weight: .bold)) }
                        .buttonStyle(.plain).foregroundStyle(Theme.muted)
                }
                .padding(.leading, 8).padding(.trailing, 6).padding(.vertical, 3)
                .background(RoundedRectangle(cornerRadius: 6).fill(Theme.raise2))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(Theme.line2))
            }
            TextField("add…", text: $typed).textFieldStyle(.plain).font(.ui(size: 12)).frame(minWidth: 40)
                .onSubmit {
                    if let n = Double(typed.trimmingCharacters(in: CharacterSet(charactersIn: " %"))), n > 0, n <= 100, !values.contains(n) {
                        values = (values + [n]).sorted()
                    }
                    typed = ""
                }
        }
        .padding(.horizontal, 6).frame(width: 260, height: 32, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line2))
    }
}

/// The quiet hours as a day: the hours they cover shaded, and now.
struct QuietDay: View {
    let start: String?
    let end: String?
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        GeometryReader { g in
            let w = g.size.width
            ZStack(alignment: .topLeading) {
                RoundedRectangle(cornerRadius: 8).fill(Theme.well)
                ForEach(Array(quietSpans(start, end).enumerated()), id: \.offset) { _, s in
                    Rectangle().fill(Theme.color(RGB("#A78BFA")).opacity(0.28))
                        .overlay(alignment: .leading) { if s.lowerBound > 0 { Rectangle().fill(Theme.color(RGB("#A78BFA"))).frame(width: 2) } }
                        .overlay(alignment: .trailing) { if s.upperBound < 1 { Rectangle().fill(Theme.color(RGB("#A78BFA"))).frame(width: 2) } }
                        .frame(width: max(0, CGFloat(s.upperBound - s.lowerBound) * w))
                        .offset(x: CGFloat(s.lowerBound) * w)
                }
                ForEach(Array(stride(from: 0, through: 24, by: 3)), id: \.self) { h in
                    Text(String(format: "%02d", h % 24)).font(.ui(size: 9.5, design: .monospaced)).foregroundStyle(Theme.muted)
                        .position(x: min(w - 10, max(10, CGFloat(h) / 24 * w)), y: g.size.height - 8)
                }
                let now = Calendar.current.dateComponents([.hour, .minute], from: Date())
                Rectangle().fill(Theme.accent).frame(width: 2)
                    .offset(x: CGFloat(Double(now.hour ?? 0) + Double(now.minute ?? 0) / 60) / 24 * w)
                    .help("now")
            }
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.line))
        }
        .frame(height: 34)
    }
}

/// [verify] as the line a finished branch takes; a click turns a step on
/// or off.
struct AutopilotLine: View {
    let steps: [PipelineStep]
    let toggle: (PipelineStep) -> Void
    @State private var hover: Int?
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        HStack(spacing: 0) {
            ForEach(Array(steps.enumerated()), id: \.offset) { i, s in
                if i > 0 {
                    let lit = s.on && steps[i - 1].on
                    HStack(spacing: 0) {
                        Rectangle().fill(lit ? Theme.accent : Theme.line2).frame(height: 2)
                        Image(systemName: "arrowtriangle.right.fill").font(.system(size: 7)).foregroundStyle(lit ? Theme.accent : Theme.line2).offset(x: -2)
                    }
                    .frame(minWidth: 8, maxWidth: 22)
                }
                Button { toggle(s) } label: {
                    VStack(spacing: 3) {
                        Text(s.title).font(.ui(size: 12, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1).minimumScaleFactor(0.85)
                        Text(s.detail).font(.ui(size: 10.5)).foregroundStyle(Theme.muted).lineLimit(1)
                    }
                    .padding(.horizontal, 4).padding(.vertical, 10).frame(minWidth: 64, maxWidth: 104)
                    .background(RoundedRectangle(cornerRadius: 10).fill(s.on ? Theme.accentDeep.opacity(0.28) : Theme.raise2))
                    .overlay(RoundedRectangle(cornerRadius: 10).stroke(s.on ? Theme.accent : Theme.line2, lineWidth: hover == i && s.key != nil ? 2 : 1))
                    .overlay(alignment: .topTrailing) {
                        Text(s.key == nil ? "●" : s.on ? "ON" : "OFF").font(.ui(size: 9, weight: .bold))
                            .foregroundStyle(s.on ? Theme.selectedText : Theme.text2)
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(Capsule().fill(s.on ? Theme.accent : Theme.line2))
                            .offset(x: 6, y: -7)
                    }
                    .opacity(s.on ? 1 : 0.6)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(s.key == nil)
                .help(s.key == nil ? (i == 0 ? "Where the line starts" : "Set per project, under Check command per project") : "Click to turn \(s.on ? "off" : "on")")
                .onHover { hover = $0 ? i : nil }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity)
        .animation(.easeOut(duration: 0.18), value: steps)
    }
}

/// A short-lived note at the top right: what was just done.
struct SettingsToast: View {
    let text: String
    @Environment(\.conchTheme) private var theme

    var body: some View {
        let _ = theme
        HStack(spacing: 7) {
            Image(systemName: "checkmark").font(.system(size: 11, weight: .bold))
            Text(text).font(.ui(size: 12))
        }
        .foregroundStyle(Theme.ok)
        .padding(.horizontal, 12).padding(.vertical, 8)
        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.raise))
        .overlay(RoundedRectangle(cornerRadius: 9).stroke(Theme.ok.opacity(0.45)))
        .shadow(color: .black.opacity(0.35), radius: 10, y: 4)
    }
}

/// A name typed, or picked from a list under the chevron: a model the
/// provider lists, an Oh My Zsh theme installed here. What is typed
/// narrows the list; a name it does not have is kept as typed.
struct ComboField: View {
    let options: [SettingsOption]
    @Binding var text: String
    var placeholder = ""
    /// Said under the field: why the list is short, say.
    var note: String?
    var loading = false
    var width: CGFloat = 300
    /// Drawn under each choice's name in the list: a theme's prompt.
    var preview: ((String) -> AnyView)?
    @State private var open = false
    @State private var hover: String?
    @FocusState private var focused: Bool
    @Environment(\.conchTheme) private var theme

    var shown: [SettingsOption] {
        let q = text.lowercased()
        // Opened on a value it has, or nothing typed: everything.
        if q.isEmpty || options.contains(where: { $0.value.lowercased() == q }) { return options }
        return options.filter { ($0.label + " " + $0.value).lowercased().contains(q) }
    }

    var body: some View {
        let _ = theme
        VStack(alignment: .trailing, spacing: 3) {
            HStack(spacing: 6) {
                TextField(placeholder, text: $text).textFieldStyle(.plain).font(.ui(size: 12.5)).focused($focused)
                if loading { ProgressView().controlSize(.mini) }
                Button { open.toggle() } label: {
                    Image(systemName: "chevron.down").font(.system(size: 9, weight: .bold)).foregroundStyle(Theme.muted)
                        .rotationEffect(.degrees(open ? 180 : 0)).frame(width: 18, height: 24).contentShape(Rectangle())
                }
                .buttonStyle(.plain).help("Choose from the list")
            }
            .padding(.leading, 10).padding(.trailing, 6).frame(width: width, height: 32)
            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(open || focused ? Theme.accent : Theme.line2, lineWidth: open || focused ? 1.5 : 1))
            .animation(.easeOut(duration: 0.12), value: open)
            .popover(isPresented: $open, arrowEdge: .bottom) {
                ScrollView {
                    VStack(spacing: 2) {
                        ForEach(shown, id: \.value) { o in
                            Button {
                                text = o.value
                                open = false
                            } label: {
                                VStack(alignment: .leading, spacing: 6) {
                                    HStack(spacing: 10) {
                                        Text(o.label).font(.ui(size: 12.5)).foregroundStyle(o.value.isEmpty ? Theme.text2 : Theme.text).lineLimit(1)
                                        Spacer()
                                        if o.value == text { Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Theme.accent) }
                                    }
                                    .frame(height: preview == nil ? 28 : 18)
                                    if let preview { preview(o.value) }
                                }
                                .padding(.horizontal, 8).padding(.vertical, preview == nil ? 0 : 7)
                                .background(RoundedRectangle(cornerRadius: 6).fill(hover == o.value ? Theme.accentDeep.opacity(0.35) : .clear))
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .onHover { hover = $0 ? o.value : (hover == o.value ? nil : hover) }
                        }
                        if shown.isEmpty {
                            Text(text.isEmpty ? "Nothing to choose from" : "None like “\(text)” — it is kept as typed")
                                .font(.ui(size: 12)).foregroundStyle(Theme.muted).padding(8)
                        }
                    }
                }
                .frame(maxHeight: preview == nil ? 340 : 520)
                .padding(6).frame(width: preview == nil ? Swift.max(width, 300) : 500)
                .background(Theme.raise)
            }
            if let note, !note.isEmpty {
                Text(note).font(.ui(size: 11)).foregroundStyle(Theme.muted).multilineTextAlignment(.trailing)
                    .frame(maxWidth: width, alignment: .trailing).fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}

/// [app] icons as a card per set, as Appearance → Icons draws them: the
/// sections' icons in the set and the style chosen, its name and what it
/// is. The pointer on one shows it in the sidebar; a click chooses it,
/// saved with the rest.
struct IconSetGrid: View {
    let chosen: SettingsIconSet
    let style: SettingsIconStyle
    let choose: (SettingsIconSet) -> Void
    var hover: (SettingsIconSet?) -> Void = { _ in }
    @Environment(\.conchTheme) private var theme
    @State private var under: SettingsIconSet?

    static let sample = ["palette", "keyboard", "bell", "bot", "shield-check", "git-merge", "sparkles", "terminal"]

    var body: some View {
        let _ = theme
        LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 12), count: 4), alignment: .leading, spacing: 12) {
            ForEach(SettingsIconSet.allCases, id: \.self) { s in card(s) }
        }
        .onDisappear { hover(nil) }
    }

    func card(_ s: SettingsIconSet) -> some View {
        let on = s == chosen
        return Button { choose(s) } label: {
            VStack(alignment: .leading, spacing: 0) {
                LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 4), count: 4), spacing: 9) {
                    ForEach(Self.sample, id: \.self) { icon in
                        SectionIcon(icon: icon, set: s, style: style, color: Theme.text, size: style == .tiles ? 15 : 18)
                            .frame(height: 26)
                    }
                }
                .padding(.top, 6).padding(.bottom, 10)
                HStack(spacing: 6) {
                    Text(s.name).font(.ui(size: 12.5, weight: .semibold)).foregroundStyle(Theme.text).lineLimit(1)
                    if s == .lucide {
                        Text("default").font(.ui(size: 9.5, weight: .semibold)).foregroundStyle(Theme.muted)
                            .padding(.horizontal, 6).padding(.vertical, 0.5)
                            .overlay(Capsule().stroke(Theme.line2))
                    }
                }
                HStack {
                    Text(s.detail).font(.ui(size: 11)).foregroundStyle(Theme.muted).lineLimit(1)
                    Spacer(minLength: 4)
                    if on { Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Theme.accent) }
                }
            }
            .padding(.horizontal, 12).padding(.top, 8).padding(.bottom, 10)
            .background(RoundedRectangle(cornerRadius: 10).fill(Theme.well))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(on ? Theme.accent : under == s ? Theme.muted : Theme.line2, lineWidth: on ? 1.5 : 1))
            .shadow(color: on ? Theme.accent.opacity(0.18) : .clear, radius: 3)
            .offset(y: under == s && !on ? -1 : 0)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { inside in
            withAnimation(.easeOut(duration: 0.12)) { under = inside ? s : (under == s ? nil : under) }
            hover(inside ? s : nil)
        }
        .accessibilityLabel(s.name)
    }
}
