import AppKit
import ConchKit
import SwiftUI

// A branch's Changes drawn by conch-ui (ConchKit's ChangesWeb.swift, ui/
// src/views/Changes.tsx) in place of GitChanges — [app] web_views = false
// brings that back. The model is ChangesModel as before: the view is sent
// what it holds, and each thing clicked is the model's own method, run
// only for a file, note, agent or link the branch has.

struct GitChangesWeb: View {
    @ObservedObject var store: Store
    @ObservedObject var model: ChangesModel
    let info: BranchInfo?

    var body: some View {
        ConchUIView(host: model.webHost, script: webUpdateScript(changesWebView(input, theme: Theme.current))) { body in
            if let i = changesIntent(body) { act(i) }
        }
    }

    private var input: ChangesWebInput {
        var i = ChangesWebInput()
        i.changes = model.changes
        i.error = model.error
        i.busy = model.busy
        i.selected = model.selected
        i.selectedCommitted = model.selectedCommitted
        i.split = model.split
        i.diff = model.diff
        i.hunks = model.fileHunks.hunks
        i.hunkOrder = model.hunkOrder
        i.picks = model.picks
        i.commitWhat = model.commitWhat
        i.live = model.live
        i.comments = model.comments
        i.pending = model.pending
        i.sent = model.sent.map { WebSentNote(id: $0.id.uuidString, file: $0.file, line: $0.line, note: $0.note, to: $0.to, pane: $0.pane, at: $0.at) }
        i.composingFile = model.composingFile
        i.composingLine = model.composingLine
        i.message = model.message
        i.writing = model.writing
        i.credit = model.credit
        i.canStage = model.canStage
        i.canThread = model.canThread
        i.agentName = model.agent?.agent?.name
        let m = model
        i.agentStopped = { m.agentStopped($0) }
        i.notes = model.notes.count
        i.pr = info?.pr
        i.reviewed = model.reviewed
        i.event = model.event
        i.reviewBody = model.reviewBody
        i.canPick = model.canPick
        i.branchAgents = model.branchAgents
        i.startable = (store.agents[model.machine] ?? []).filter(\.installed).map { ($0.name, $0.title) }
        i.hasAgent = model.agent != nil
        i.conflicts = model.baseConflicts
        i.baseUnpushed = model.baseUnpushed
        i.ciCard = model.ciCard
        i.canInstallCI = model.canInstallCI
        i.canRerun = ciCanRerun(info?.pr, checksSay: store.connection(model.machine)?.hello?.capabilities?.contains("pr.check.rerun.v1") == true)
        i.fixLabel = model.agent.map { "Send the failure to \($0.name.isEmpty ? ($0.agent?.name ?? "the agent") : $0.name)" } ?? "Start an agent on the failure"
        return i
    }

    /// The files the view lists: what a path from it may name.
    private var listed: (uncommitted: Set<String>, committed: Set<String>) {
        guard let ch = model.changes else { return ([], []) }
        if ch.worktree == nil { return ([], Set(ch.files.map(\.path))) }
        return (Set(ch.files.map(\.path)), Set((ch.committedFiles ?? []).map(\.path)))
    }

    /// The links the view shows: the pull request's, its checks', its
    /// comments' on GitHub, and the CI card's service.
    private var links: Set<String> {
        let pr = info?.pr
        var all = ([pr?.url] + (pr?.runs ?? []).map(\.url)).compactMap { $0 }
        all += model.comments.compactMap(\.url)
        if let u = model.ciCard?.check.url { all.append(u) }
        return Set(all)
    }

    private func act(_ intent: ChangesIntent) {
        let files = listed
        switch intent {
        case .ready:
            break
        case let .select(path, committed):
            guard (committed ? files.committed : files.uncommitted).contains(path) else { return }
            model.select(path, committed: committed)
        case .tickAll:
            guard let paths = model.changes?.files.map(\.path), !paths.isEmpty, model.changes?.worktree != nil else { return }
            model.picks.set(paths, on: model.picks.allState(paths, hunks: model.hunkOrder) != .on)
            model.tickAnchor = nil
        case let .tickFile(path, shift):
            guard files.uncommitted.contains(path) else { return }
            // Shift ticks the run from the box clicked last, as Finder does.
            if shift {
                model.picks.extend(to: path, from: model.tickAnchor, in: model.changes?.files.map(\.path) ?? [])
            } else {
                model.picks.toggleFile(path)
            }
            model.tickAnchor = path
        case .tickHunk(let index):
            guard let path = model.selected, !model.selectedCommitted,
                  let h = model.fileHunks.hunks.first(where: { $0.index == index }) else { return }
            model.picks.toggleHunk(path, header: model.fileHunks.header, hunk: h.text, all: model.fileHunks.hunks)
        case let .stageFile(path, unstage):
            guard files.uncommitted.contains(path), model.canStage else { return }
            model.stage(file: path, unstage: unstage)
        case .copyPath(let path):
            guard files.uncommitted.contains(path) || files.committed.contains(path) else { return }
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(path, forType: .string)
        case .split(let on):
            model.split = on
        case .compose(let line):
            guard let path = model.selected else { return }
            model.compose(file: path, line: line)
        case .cancelCompose:
            model.composingFile = nil
        case let .addComment(line, note, forPR):
            guard let path = model.selected else { return }
            model.addComment(note, file: path, line: line, forPR: forPR && model.prOpen)
        case let .reply(id, note):
            guard model.canThread, let c = model.comments.first(where: { $0.id == id }) else { return }
            model.reply(c, note)
        case .resolve(let id):
            guard let c = model.comments.first(where: { $0.id == id }) else { return }
            model.resolve(c)
        case .dropPending(let id):
            guard let p = model.pending.first(where: { $0.id.uuidString == id }) else { return }
            model.dropPending(p)
        case .dismissSent(let id):
            model.sent.removeAll { $0.id.uuidString == id }
        case .openPane(let pane):
            // Only an agent a note went to.
            let known = Set(model.comments.compactMap(\.sentPane) + model.sent.map(\.pane))
            guard known.contains(pane) else { return }
            store.open(key(model.machine, pane))
        case .openURL(let u):
            guard links.contains(u.absoluteString) else { return }
            NSWorkspace.shared.open(u)
        case let .sendNotes(to, start):
            if let to {
                guard model.canPick, model.branchAgents.contains(where: { $0.id == to }) else { return }
                model.sendNotes(to: to)
            } else if let start {
                guard model.canPick, (store.agents[model.machine] ?? []).contains(where: { $0.installed && $0.name == start }) else { return }
                model.sendNotes(start: start)
            } else {
                model.sendNotes()
            }
        case .message(let text):
            if model.message != text { model.message = text }
        case .credit(let on):
            model.credit = on
        case .writeMessage:
            model.writeMessage()
        case .stage:
            guard model.canStage, !model.picks.isEmpty else { return }
            model.stage()
        case .commit:
            guard !(model.changes?.files.isEmpty ?? true),
                  !model.message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, model.busy == nil else { return }
            model.commit()
        case .event(let e):
            model.event = e
        case .reviewBody(let text):
            if model.reviewBody != text { model.reviewBody = text }
        case .submitReview:
            guard model.event.canSubmit(body: model.reviewBody, comments: model.pending.count), model.busy == nil else { return }
            model.submitReview()
        case .reviewAgain:
            model.reviewed = nil
        case .openPR:
            model.openPR()
        case .fixCI:
            model.fixCI()
        case .rerunCI:
            model.rerunCI()
        case .installCI:
            model.installCI()
        case .openCIService:
            if let s = model.ciCard?.check.url, let u = URL(string: s) { NSWorkspace.shared.open(u) }
        }
    }
}
