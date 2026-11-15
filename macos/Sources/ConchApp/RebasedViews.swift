import ConchKit
import SwiftUI

// A branch rebased onto the base — an agent asked to resolve its conflict
// does this — and not pushed yet: said above the graph, shown beside the
// branch's commit with each commit before and after, and reviewed in a
// sheet with git range-diff's account of what resolving changed, before
// it is pushed over the old branch or put back.

/// Says the push a rebase needs, for its button.
func pushTitle(_ r: BranchRebase) -> String {
    switch r.push {
    case .fresh: "Push"
    case .over: "Push (with lease)"
    case .moved: "Push"
    }
}

func pushHelp(_ r: BranchRebase, base: String) -> String {
    switch r.push {
    case .fresh: "Pushes \(r.branch) for the first time"
    case .over(let h): "Replaces \(String(h.prefix(8))) on the remote with the rebased branch, and only that: anything pushed since is kept and the push refused"
    case .moved(let h): "Somebody pushed \(String(h.prefix(8))) to \(r.branch) since the rebase; pushing would throw it away. Bring it in first."
    }
}

func pushBlocked(_ r: BranchRebase) -> Bool {
    if case .moved = r.push { return true }
    return false
}

/// Above the graph when a branch is rebased and waiting to go up, and no
/// conflict has stopped anything (that banner comes first).
struct RebasedBanner: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel

    var body: some View {
        let _ = look
        let bs = model.branches
        if !bs.contains(where: \.stopped), let b = bs.first(where: { $0.rebase != nil }), let r = b.rebase {
            let base = model.projectInfo?.base ?? "the base"
            HStack(spacing: 10) {
                Image(systemName: "checkmark.circle.fill").foregroundStyle(Theme.ok)
                VStack(alignment: .leading, spacing: 1) {
                    Text("\(b.name) is rebased onto \(base)").font(.ui(size: 12.5, weight: .semibold)).lineLimit(1)
                    Text(r.summary + ". Not pushed yet.").font(.ui(size: 11.5)).foregroundStyle(Theme.text2).lineLimit(1)
                }
                Spacer(minLength: 8)
                Button("Review…") { model.review(r) }
                    .controlSize(.small).buttonStyle(.borderedProminent).tint(Theme.accentDeep)
                Button(pushTitle(r)) { model.pushRebase(r) }
                    .controlSize(.small)
                    .disabled(model.pushing.contains(r.branch) || pushBlocked(r))
                    .help(pushHelp(r, base: base))
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(Theme.ok.opacity(0.10))
            .overlay(alignment: .bottom) { Rectangle().fill(Theme.ok.opacity(0.35)).frame(height: 1) }
        }
    }
}

/// Beside a rebased branch's commit: when and onto what, each commit
/// before and after, and what to do with it.
struct RebasedCard: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel
    let branch: GraphBranch
    let rebase: BranchRebase

    var body: some View {
        let _ = look
        let base = model.projectInfo?.base ?? "the base"
        let busy = model.pushing.contains(rebase.branch)
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Text("REBASED ONTO \(base.uppercased())").font(.ui(size: 10.5, weight: .bold)).foregroundStyle(Theme.ok)
                if let at = rebase.at { Text("· \(agoText(at))").font(.ui(size: 10.5)).foregroundStyle(Theme.muted) }
            }
            Text("\(rebase.branch) is on \(base) again").font(.ui(size: 13.5, weight: .semibold))
                .fixedSize(horizontal: false, vertical: true)
            Text(rebase.summary + (branch.agent.map { " · \($0.agent?.name.capitalized ?? $0.displayName) is on this branch" } ?? ""))
                .font(.ui(size: 11.5)).foregroundStyle(Theme.text2).fixedSize(horizontal: false, vertical: true)
            if let cs = rebase.commits, !cs.isEmpty {
                VStack(alignment: .leading, spacing: 3) {
                    Text("REWRITTEN COMMITS").font(.ui(size: 10.5, weight: .semibold)).foregroundStyle(Theme.muted)
                    ForEach(Array(cs.prefix(12).enumerated()), id: \.offset) { _, c in RewrittenRow(commit: c) }
                    if cs.count > 12 {
                        Text("and \(cs.count - 12) more").font(.ui(size: 11)).foregroundStyle(Theme.muted)
                    }
                }
            }
            if case .moved(let h) = rebase.push {
                Text("Somebody pushed \(String(h.prefix(8))) to \(rebase.branch) since: pushing would throw it away. Bring it in first.")
                    .font(.ui(size: 11.5)).foregroundStyle(Theme.warn).fixedSize(horizontal: false, vertical: true)
            }
            Button { model.review(rebase) } label: {
                Text("Review the resolution").frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent).tint(Theme.accentDeep).controlSize(.regular)
            .help("What resolving the conflict changed in each commit (git range-diff)")
            HStack(spacing: 6) {
                Button { model.pushRebase(rebase) } label: { Text(pushTitle(rebase)).frame(maxWidth: .infinity) }
                    .disabled(busy || pushBlocked(rebase))
                    .help(pushHelp(rebase, base: base))
                Button { model.undoRebase(rebase) } label: { Text("Undo the rebase").frame(maxWidth: .infinity) }
                    .disabled(busy)
                    .help("Puts \(rebase.branch) back at \(String(rebase.before.prefix(8))), where it was before")
            }
            .controlSize(.small)
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.ok.opacity(0.08)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Theme.ok.opacity(0.30)))
    }
}

/// One commit before → after, and whether its change is what it was.
struct RewrittenRow: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let commit: RebasedCommit

    var body: some View {
        let _ = look
        HStack(spacing: 6) {
            Text(commit.old.map { String($0.prefix(7)) } ?? "—").strikethrough(commit.old != nil)
                .foregroundStyle(Theme.muted)
            Text("→").foregroundStyle(Theme.muted)
            Text(commit.new.map { String($0.prefix(7)) } ?? "—").foregroundStyle(Theme.work)
            Text(commit.subject).font(.ui(size: 11.5)).foregroundStyle(Theme.text2).lineLimit(1).truncationMode(.tail)
            Spacer(minLength: 2)
            if let (word, tone) = mark {
                Text(word).font(.ui(size: 9.5, weight: .semibold)).foregroundStyle(tone)
                    .padding(.horizontal, 4).padding(.vertical, 1)
                    .background(RoundedRectangle(cornerRadius: 3).fill(tone.opacity(0.14)))
                    .fixedSize()
            }
        }
        .font(.ui(size: 11, design: .monospaced))
        .accessibilityElement(children: .combine)
    }

    var mark: (String, Color)? {
        switch commit.change {
        case "changed": ("resolved", Theme.warn)
        case "dropped": ("dropped", Theme.muted)
        case "added": ("new", Theme.work)
        default: nil
        }
    }
}

/// The review: git range-diff's account of the rebase, the resolution's
/// own lines marked, with the push and the undo at hand.
struct RebaseReviewSheet: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    @ObservedObject var model: CommitsModel

    var body: some View {
        let _ = look
        let base = model.projectInfo?.base ?? "the base"
        VStack(alignment: .leading, spacing: 0) {
            if let r = model.reviewing {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Review the resolution").font(.ui(size: 15, weight: .semibold))
                    Text("\(r.branch) onto \(base) · \(r.summary)").font(.ui(size: 12)).foregroundStyle(Theme.text2)
                    Text("Lines marked + or − are what the rebase changed in a commit's own diff: resolving the conflict, mostly. A commit that came across as it was shows as one line.")
                        .font(.ui(size: 11.5)).foregroundStyle(Theme.muted).fixedSize(horizontal: false, vertical: true)
                }
                .padding(16)
                Rectangle().fill(Theme.line).frame(height: 1)
                // Scrolling both ways centres what is smaller than the view;
                // the diff starts at the top left, as text does.
                GeometryReader { geo in
                ScrollView([.vertical, .horizontal]) {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        if !model.reviewError.isEmpty {
                            Text(model.reviewError).font(.ui(size: 12)).foregroundStyle(Theme.err).padding(12)
                        } else if let d = r.diff {
                            ForEach(rangeDiffLines(d)) { l in DiffOfDiffLine(line: l) }
                        } else {
                            ProgressView().controlSize(.small).padding(16)
                        }
                    }
                    .padding(.vertical, 8)
                    .frame(minWidth: geo.size.width, minHeight: geo.size.height, alignment: .topLeading)
                }
                }
                .background(Theme.side)
                Rectangle().fill(Theme.line).frame(height: 1)
                HStack(spacing: 8) {
                    Button("Undo the Rebase…") { model.undoRebase(r) }
                        .disabled(model.pushing.contains(r.branch))
                    Spacer()
                    Button("Close") { model.reviewing = nil }.keyboardShortcut(.cancelAction)
                    Button(pushTitle(r)) { model.pushRebase(r) }
                        .keyboardShortcut(.defaultAction)
                        .disabled(model.pushing.contains(r.branch) || pushBlocked(r) || !model.reviewError.isEmpty)
                        .help(pushHelp(r, base: base))
                }
                .padding(12)
            }
        }
        .frame(minWidth: 640, idealWidth: 760, minHeight: 420, idealHeight: 560)
        .background(Theme.window)
    }
}

struct DiffOfDiffLine: View {
    /// Read so this is drawn again when the theme changes (ThemeLook).
    @Environment(\.conchTheme) private var look
    let line: RangeDiffLine

    var body: some View {
        let _ = look
        let (fg, bg): (Color, Color) = switch line.kind {
        case .commit: (Theme.text, Theme.raise)
        case .hunk: (Theme.work, .clear)
        case .added: (Theme.ok, Theme.ok.opacity(0.10))
        case .removed: (Theme.err, Theme.err.opacity(0.10))
        case .context: (Theme.text2, .clear)
        }
        Text(line.text.isEmpty ? " " : line.text)
            .font(.ui(size: 11.5, weight: line.kind == .commit ? .semibold : .regular, design: .monospaced))
            .foregroundStyle(fg)
            .lineLimit(1).fixedSize(horizontal: true, vertical: false)
            .padding(.horizontal, 12).padding(.vertical, line.kind == .commit ? 4 : 0)
            .frame(minWidth: 0, maxWidth: .infinity, alignment: .leading)
            .background(bg)
            .padding(.top, line.kind == .commit && line.id > 0 ? 6 : 0)
            .textSelection(.enabled)
    }
}
