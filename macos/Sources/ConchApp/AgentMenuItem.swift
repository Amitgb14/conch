import AppKit
import SwiftUI

/// An agent in a menu: its mark beside its name, as the tree shows it.
/// A menu is drawn by AppKit, which shows a Label's image but not a
/// SwiftUI view, so the mark is drawn into an image first.
struct AgentMenuButton: View {
    let agent: String
    let title: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            if let img = AgentMenuIcon.image(agent) {
                Label { Text(title) } icon: { Image(nsImage: img) }
            } else {
                Text(title)
            }
        }
    }
}

@MainActor
enum AgentMenuIcon {
    /// The agent's mark, 16 points square in its own colours, or nil when
    /// marks are off ([ui] icons = "off") or it could not be drawn.
    static func image(_ agent: String) -> NSImage? {
        guard Theme.icons != .off else { return nil }
        let scale = NSScreen.main?.backingScaleFactor ?? 2
        // The same image each time it is asked for: a new one every time a
        // menu was drawn told AppKit its items had changed, so an open menu
        // was redrawn — flickering — at every change in the store.
        let key = "\(agent)|\(Theme.icons)|\(scale)|\(Theme.agent(agent).description)"
        if let img = cache[key] { return img }
        let r = ImageRenderer(content: Mark(agent: agent).frame(width: 16, height: 16))
        r.scale = scale
        guard let img = r.nsImage else { return nil }
        img.isTemplate = false // keep the agent's colour rather than the menu's
        cache[key] = img
        return img
    }

    private static var cache: [String: NSImage] = [:]
}
