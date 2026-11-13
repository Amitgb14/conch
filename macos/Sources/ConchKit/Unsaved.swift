import Foundation

// Edits not saved — a file open in a Files tab, a task's brief — and what
// is asked before they could be lost: switching a Files tab to another
// checkout, quitting the app.

/// An open file's state while another is shown.
public struct FileBuffer: Sendable {
    public var preview: FSRead?
    public var previewError = ""
    public var text = ""
    public var original = ""
    public var baseHash: String?
    public var diskChanged = false
    public var dirty: Bool { preview?.editable == true && text != original }

    public init(preview: FSRead? = nil, previewError: String = "", text: String = "", original: String = "",
                baseHash: String? = nil, diskChanged: Bool = false) {
        self.preview = preview
        self.previewError = previewError
        self.text = text
        self.original = original
        self.baseHash = baseHash
        self.diskChanged = diskChanged
    }

    /// A save of data came back with the file's new fingerprint. Typing
    /// done since the save was asked for is kept, and still not saved.
    public mutating func saved(_ data: String, hash: String) {
        original = data
        baseHash = hash
        diskChanged = false
    }
}

/// Where the answer to a save belongs: the file it was for, wherever that
/// file is now. A save is answered after the person may have picked
/// another file, or another checkout — and the answer applied to whatever
/// was on screen then gave that file the saved one's text and fingerprint.
public enum SavedFileAt: Equatable, Sendable {
    /// Still the file shown.
    case shown
    /// Open in a tab, but put aside while another is shown.
    case aside
    /// Closed, or in a checkout no longer shown: nothing to update.
    case gone
}

public func savedFileAt(path: String, root: String, nowRoot: String, selected: String?, aside: Bool) -> SavedFileAt {
    guard root == nowRoot else { return .gone }
    if selected == path { return .shown }
    return aside ? .aside : .gone
}

/// A task's brief (CONCH-TASK.md) as it is being edited: kept by the app
/// rather than by its page, so switching the page's view or the window's
/// tab does not throw the edits away.
public struct BriefDraft: Equatable, Sendable {
    public var text = ""
    public var original = ""
    public var hash: String?
    /// Whether it was read whole, so it may be saved.
    public var loaded = false
    public var dirty: Bool { loaded && text != original }

    public init() {}

    /// The brief as read from disk. Edits not saved are kept: the read
    /// only moves what they would be saved against when the file is still
    /// the one they began from. Returns whether the file on disk differs
    /// from what the edits began from — a save would then be refused.
    @discardableResult
    public mutating func read(_ data: String, hash: String?, truncated: Bool) -> Bool {
        if dirty {
            return hash != self.hash
        }
        text = data
        original = data
        self.hash = hash
        loaded = !truncated
        return false
    }

    /// A save of data came back with the brief's new fingerprint.
    public mutating func saved(_ data: String, hash: String) {
        original = data
        self.hash = hash
    }
}

/// One thing with edits not saved, as the question before quitting lists it.
public struct Unsaved {
    /// What it is called in the question: a file's name, "the brief of fix-login".
    public var name: String
    /// Writes it; false when it was not written (said by the saver itself).
    public var save: @MainActor () async -> Bool

    public init(name: String, save: @escaping @MainActor () async -> Bool) {
        self.name = name
        self.save = save
    }
}

/// Saves each in turn and stops at the first that fails: quitting goes on
/// only when everything was written, and whatever failed is left open to
/// be looked at rather than lost.
@MainActor
public func saveAll(_ items: [Unsaved]) async -> Bool {
    for i in items where !(await i.save()) {
        return false
    }
    return true
}

/// The question asked before quitting with edits not saved.
public func unsavedQuestion(_ names: [String]) -> (title: String, info: String) {
    let title = names.count == 1 ? "Save your changes to \(names[0]) before quitting?"
        : "\(names.count) open files have changes not saved. Save them before quitting?"
    var list = names.prefix(6).joined(separator: "\n")
    if names.count > 6 { list += "\n…and \(names.count - 6) more" }
    let info = "Your changes are lost if you don't save them."
    return (title, names.count == 1 ? info : info + "\n\n" + list)
}
