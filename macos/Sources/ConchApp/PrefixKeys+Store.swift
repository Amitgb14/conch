import AppKit
import ConchKit

// The TUI's prefix keys in the app (ConchKit/PrefixKeys.swift): the table
// read from conch keys -json, a key caught before the pane gets it, and
// each action done the way the app's own menu item does it.

/// What the key monitor does with a key.
enum PrefixOutcome { case taken, typed, pass }

extension Store {
    /// Reads the table again when config.toml or [app] prefix_keys changed;
    /// off, there is none and every key is the program's.
    func loadKeys(on: Bool, stamp: String) {
        let s = "\(on)\n\(stamp)"
        guard s != keysStamp else { return }
        keysStamp = s
        guard on, let b = binary else {
            prefixKeys = PrefixKeys()
            return
        }
        Task {
            do {
                let out = try await runConch(b, ["keys", "-json"])
                let t = try JSONDecoder().decode(KeyTable.self, from: out)
                guard keysStamp == s else { return } // read again meanwhile
                prefixKeys.table = t
            } catch {
                // A conch from before -json: no prefix keys, said once.
                prefixKeys = PrefixKeys()
                say("Prefix keys are off: conch keys -json failed (\(error))", .err)
            }
        }
    }

    /// A key, before the window routes it. The prefix is caught wherever
    /// the keyboard is in the main window — a pane or the sidebar — but
    /// never while something is being typed into: a field, a sheet, the
    /// palette, a task.
    func prefixKey(_ e: NSEvent) -> PrefixOutcome {
        guard prefixKeys.table != nil else { return .pass }
        guard let win = NSApp.keyWindow, win === mainWindow, win.attachedSheet == nil, !paletteOpen, taskDraft == nil,
              !(win.firstResponder is NSText), !(win.firstResponder is NSTextField) else {
            prefixKeys.disarm()
            return .pass
        }
        let f = e.modifierFlags
        let name = conchKeyName(keyCode: e.keyCode, characters: e.charactersIgnoringModifiers,
                                ctrl: f.contains(.control), alt: f.contains(.option), shift: f.contains(.shift), command: f.contains(.command))
        switch prefixKeys.press(name) {
        case .pass: return .pass
        case .arm: return .taken
        case .typePrefix: return terminalHasKeyboard ? .typed : .taken
        case .tab(let i):
            if layout.tabs.indices.contains(i) {
                layout.select(i)
                focusTerminal()
            }
            return .taken
        case .sidebar:
            focusSidebar()
            return .taken
        case .run(let a):
            runPrefixAction(a)
            return .taken
        }
    }

    /// An action from [keys.bind], done as the app's menus do it.
    func runPrefixAction(_ a: String) {
        guard prefixActionsInApp.contains(a) else {
            let what = prefixKeys.table?.keys.first { $0.action == a }?.what ?? a
            say("\(what) is the TUI's: not in the app yet", .info)
            return
        }
        switch a {
        case "split_right": command("splitRight")
        case "split_down": command("splitDown")
        case "close_split": command("closeSplit")
        case "focus_left": command("splitLeft")
        case "focus_right": command("splitRightward")
        case "focus_up": command("splitAbove")
        case "focus_down": command("splitBelow")
        case "next_split": layout.nextSplit(); focusTerminal()
        case "equalize": layout.equalize()
        case "zoom": command("zoomSplit")
        case "new_tab": command("newTab")
        case "next_tab": command("nextTab")
        case "prev_tab": command("previousTab")
        case "move_tab_left": layout.moveTab(from: layout.active, to: layout.active - 1)
        case "move_tab_right": layout.moveTab(from: layout.active, to: layout.active + 1)
        case "close_tab": closeTab()
        case "rename_tab": renameTab(layout.active)
        case "changes_tab": changesOfFocused()
        case "tab_picker", "palette": paletteOpen = true
        case "ask": askOpen = true
        case "new_workspace": newWorkspace()
        case "prev_workspace": if workspaces.count > 1 { workspace = (workspace + workspaces.count - 1) % workspaces.count }
        case "next_workspace": if workspaces.count > 1 { workspace = (workspace + 1) % workspaces.count }
        case "rename_workspace": renameWorkspace(workspace)
        case "close_workspace": closeWorkspace(workspace)
        case "monitor_quiet", "monitor_output":
            if let k = layout.focused, pane(k) != nil { toggleMonitor(k, quiet: a == "monitor_quiet") }
        case "next_waiting": nextWaiting()
        case "help": helpOpen = true
        default: break
        }
    }

    /// The focused pane's branch, its changes in a tab — the TUI's ctrl+b C.
    private func changesOfFocused() {
        guard let k = layout.focused, let p = pane(k), let pid = p.projectID, let br = p.branch, !br.isEmpty else {
            say("This split is not on a branch", .info)
            return
        }
        openChanges(project: key(unkey(k).machine, pid), branch: br)
    }
}
