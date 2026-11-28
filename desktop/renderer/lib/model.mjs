// What the window draws, from what the server says: the tree of projects
// and their panes, a pane's name and state, and the Agents overview's
// state for conch-ui. Plain functions, tested with node.

/** A pane's name as the sidebar says it: a name given it, else its title,
 *  else its agent's or program's name. */
export function paneLabel(p) {
  if (p.custom_name && p.name) return p.name
  return p.title || p.name || p.agent?.name || p.id
}

/** What a pane is doing, in one word. */
export function paneState(p) {
  if (p.state && p.state !== "running") return "exited"
  if (!p.agent) return "terminal"
  return { blocked: "waiting", working: "working", done: "done" }[p.agent.state] ?? "idle"
}

/** Projects with their panes, by name, then the panes in no project. */
export function buildTree(panes, projects) {
  const byID = new Map((projects ?? []).map((p) => [p.id, { project: p, panes: [] }]))
  const loose = []
  for (const p of panes ?? []) {
    const g = p.project_id && byID.get(p.project_id)
    if (g) g.panes.push(p)
    else loose.push(p)
  }
  const order = (a, b) => (rank(a) - rank(b)) || paneLabel(a).localeCompare(paneLabel(b))
  const groups = [...byID.values()].filter((g) => g.panes.length).sort((a, b) => a.project.name.localeCompare(b.project.name))
  for (const g of groups) g.panes.sort(order)
  loose.sort(order)
  return { groups, loose }
}

const rank = (p) => ({ waiting: 0, working: 1, done: 2, idle: 3, terminal: 4, exited: 5 })[paneState(p)]

/** The panes waiting on somebody, longest waiting first. */
export function waiting(panes) {
  return (panes ?? []).filter((p) => paneState(p) === "waiting")
    .sort((a, b) => Math.max(0, Date.parse(a.agent?.since ?? "") || 0) - Math.max(0, Date.parse(b.agent?.since ?? "") || 0))
}

/** How long ago, as the overview says it: now, 4m, 3h, 2d. */
export function ago(since, now) {
  // Go writes an unset time as the year 1: that is no time at all.
  const t = Date.parse(since ?? "")
  if (!(t > 0)) return undefined
  const s = Math.max(0, Math.round((now - t) / 1000))
  if (s < 45) return "now"
  if (s < 3600) return `${Math.round(s / 60)}m`
  if (s < 86400) return `${Math.round(s / 3600)}h`
  return `${Math.round(s / 86400)}d`
}

/** conch's own theme, as the Mac app's views are given it. */
export const colors = {
  bg: "#1B1F24", fg: "#C9D1D9", muted: "#8B949E", border: "#444C56", raised: "#262C33", accent: "#2AA5B5",
  ok: "#3FB950", warn: "#D29922", err: "#F85149", work: "#58A6FF", light: false,
}

const agentColors = { claude: "#D97757", codex: "#10A37F", gemini: "#4285F4", opencode: "#F5A623", qwen: "#615CED", goose: "#9B59B6", copilot: "#8250DF" }

/** The Agents overview's state (conch-ui's AgentsState), from the panes:
 *  agents only, waiting first. Questions are answered in the pane here. */
export function agentsState(panes, projects, now, focused) {
  const names = new Map((projects ?? []).map((p) => [p.id, p.name]))
  const agents = (panes ?? []).filter((p) => p.agent)
  const card = (p) => ({
    key: p.id,
    title: paneLabel(p),
    agent: p.agent.name,
    color: agentColors[p.agent.name] ?? colors.muted,
    state: p.agent.state ?? "idle",
    running: !p.state || p.state === "running",
    place: [names.get(p.project_id), p.branch].filter(Boolean).join(" · "),
    ago: ago(p.agent.since, now),
    focused: p.id === focused,
    answering: false,
    question: paneState(p) === "waiting" ? { text: p.agent.message || "", kind: "waiting", choices: [] } : undefined,
  })
  const cards = agents.map(card).sort((a, b) => rankCard(a) - rankCard(b) || a.title.localeCompare(b.title))
  const words = { blocked: "is waiting on you", working: "started working", done: "is done" }
  const activity = cards.filter((c) => Date.parse(agents.find((p) => p.id === c.key)?.agent?.since ?? "") > 0)
    .map((c) => {
      const p = agents.find((x) => x.id === c.key)
      return { key: c.key, agent: c.agent, text: `${c.title} ${c.running ? words[c.state] ?? "went idle" : "exited"}`, ago: c.ago ?? "", color: c.color, t: Date.parse(p.agent.since) }
    })
    .sort((a, b) => b.t - a.t).slice(0, 8).map(({ t, ...e }) => e)
  return { colors, agents: cards, limits: [], activity }
}
const rankCard = (c) => !c.running ? 4 : ({ blocked: 0, working: 1, done: 2 })[c.state] ?? 3

/** Bytes the server sent as base64 (Go's []byte in JSON). */
export function bytes(b64) {
  if (!b64) return new Uint8Array(0)
  const s = atob(b64)
  const out = new Uint8Array(s.length)
  for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i)
  return out
}
