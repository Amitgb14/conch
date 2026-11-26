import SwiftUI

extension View {
    /// No keyboard focus rings. With Keyboard navigation on in System
    /// Settings, macOS gives every button a place in the Tab order and a
    /// sheet opens with a ring round its first one ("Copy Details" in About
    /// Browser, a row of the Settings sidebar), which reads as that one
    /// being selected beside the real selection. The keys still move focus;
    /// only the ring is gone. Every window's, toolbar view's and popover's
    /// root takes it, and their sheets inherit it — a test checks the roots.
    func noFocusRings() -> some View { focusEffectDisabled() }
}
