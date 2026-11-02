import Foundation

/// An agent's own colour, for its dot in the sidebar, tabs and pane
/// headers: the TUI's agentColors (internal/tui/view.go), the brand's where
/// one is published. nil for an agent conch has none for.
public func agentColor(_ name: String?) -> RGB? {
    switch name {
    case "claude": return RGB("#D97757") // Anthropic's terracotta
    case "codex": return RGB("#10A37F") // OpenAI's green
    case "gemini": return RGB("#4285F4") // Google's blue
    case "opencode": return RGB("#C792EA")
    case "devin": return RGB("#E3B341")
    case "aider": return RGB("#4CAF50") // chosen: aider publishes none
    case "amp": return RGB("#FF6B35") // chosen
    case "cursor": return RGB("#C9D1D9") // its mark is white
    case "grok": return RGB("#E8E8E8") // xAI is black and white
    case "kilo": return RGB("#FEE685") // Kilo Code's own
    case "pi": return RGB("#4D9ABF") // the blue of its three blocks
    default: return nil
    }
}

/// How a tree row shares its width between the name and what is at its
/// right: both whole when they fit; otherwise the right side keeps what
/// the name leaves, but never less than its share of the room (or its
/// whole width, if smaller), and the name has the rest.
public func rowWidths(name: Double, right: Double, room: Double, share: Double = 0.4) -> (name: Double, right: Double) {
    let room = max(room, 0)
    var r = right
    if name + right > room { r = min(right, max(room * share, room - name)) }
    return (max(room - r, 0), r)
}
