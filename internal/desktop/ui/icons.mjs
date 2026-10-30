// The window's icons: each agent's mark in its own colour, as the TUI draws
// it (agentColors, agentGlyphs in internal/tui/view.go), and the few the
// interface needs. All constant markup, never built from data.

// The agents' own colours; a shell is the interface's grey.
export const agentColors = {
  claude: "#D97757", codex: "#10A37F", gemini: "#4285F4", opencode: "#C792EA", devin: "#E3B341",
}

const glyphs = {
  // An asterisk, the shape of Anthropic's own mark.
  claude: `<path d="M12 3.5v17M3.5 12h17M6 6l12 12M18 6 6 18" stroke-width="2.4" stroke-linecap="round"/>`,
  // Six-fold, as OpenAI's Blossom is.
  codex: [0, 60, 120, 180, 240, 300].map((a) =>
    `<ellipse cx="12" cy="7.6" rx="2.9" ry="4.4" transform="rotate(${a} 12 12)" stroke-width="1.7"/>`).join(""),
  // A four-point sparkle: the TUI uses Google's logo, which is not ours to draw.
  gemini: `<path d="M12 2.5c.7 5 4.5 8.8 9.5 9.5-5 .7-8.8 4.5-9.5 9.5-.7-5-4.5-8.8-9.5-9.5 5-.7 8.8-4.5 9.5-9.5z" fill="currentColor" stroke="none"/>`,
  // A terminal prompt, which is opencode's whole identity.
  opencode: `<path d="m6 8 4 4-4 4M12.5 16.5H18" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`,
  // A rocket: chosen, since Cognition's mark is a wordmark.
  devin: `<path d="M14 4.5c3-1 5.5-1 5.5-1s0 2.5-1 5.5L12 15.5 8.5 12z" stroke-width="1.9" stroke-linejoin="round"/><circle cx="15" cy="9" r="1.3" stroke-width="1.9"/><path d="M8.5 12H5l2.5-3h3.5M12 15.5V19l3-2.5V13" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/>`,
  // Any other agent: a chip, as the TUI's default glyph.
  other: `<rect x="6" y="6" width="12" height="12" rx="2" stroke-width="1.8"/><path d="M9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3" stroke-width="1.8" stroke-linecap="round"/>`,
  shell: `<rect x="3.5" y="5" width="17" height="14" rx="2.5" stroke-width="2"/><path d="m7.5 10 2.5 2-2.5 2M12 14.5h4" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`,
}

// agentIcon is an agent's mark on a tinted tile, size px square.
export function agentIcon(agent, size = 22) {
  const known = agent in glyphs ? agent : "other"
  const color = agentColors[agent]
  const style = color
    ? `color:${color};background:color-mix(in srgb, ${color} 16%, transparent)`
    : "color:var(--muted);background:var(--tile)"
  const g = Math.round(size * 0.62)
  return `<span class="agent-icon" role="img" aria-label="${escapeAttr(agent)}" style="width:${size}px;height:${size}px;border-radius:${Math.round(size * 0.3)}px;${style}">` +
    `<svg width="${g}" height="${g}" viewBox="0 0 24 24" fill="none" stroke="currentColor" aria-hidden="true">${glyphs[known]}</svg></span>`
}

// The interface's icons, 24-unit strokes.
const ui = {
  plus: `<path d="M12 5v14M5 12h14"/>`,
  inbox: `<path d="M4 13h4l2 3h4l2-3h4"/><path d="M5 5h14l1 8v6H4v-6z"/>`,
  review: `<circle cx="6" cy="6" r="2.5"/><circle cx="18" cy="18" r="2.5"/><path d="M6 8.5V15a3 3 0 0 0 3 3h6.5M18 15.5V9a3 3 0 0 0-3-3H8.5"/>`,
  gear: `<circle cx="12" cy="12" r="3"/><path d="M12 2.8v2.4M12 18.8v2.4M2.8 12h2.4M18.8 12h2.4M5.5 5.5l1.7 1.7M16.8 16.8l1.7 1.7M5.5 18.5l1.7-1.7M16.8 7.2l1.7-1.7"/>`,
  rail: `<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><path d="M9 4.5v15"/>`,
  send: `<path d="M12 19V5M6 11l6-6 6 6"/>`,
  shield: `<path d="M12 3l7 3v5c0 4.5-3 8.2-7 10-4-1.8-7-5.5-7-10V6z"/>`,
  search: `<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`,
  close: `<path d="M6 6l12 12M18 6 6 18"/>`,
  conch: `<path d="M12 3c4.97 0 9 4.03 9 9 0 3.6-2.9 6.5-6.5 6.5S8 15.6 8 12.5 10.2 7 13 7s4.5 2 4.5 4.5S15.9 15 14 15s-2.5-1.3-2.5-2.5"/><path d="M4 20l3.5-3.5"/>`,
}

// icon is an interface icon, size px.
export function icon(name, size = 16, width = 1.8) {
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${width}" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ui[name] || ""}</svg>`
}

// escapeAttr and escapeText make data safe to put in markup: a pane's
// name and title come from programs, and from other machines.
export function escapeAttr(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c])
}
export const escapeText = escapeAttr
