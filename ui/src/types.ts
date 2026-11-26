// What the host sends each view: one JSON object, the whole view, on
// every change. The Mac app writes these from Swift (ConchKit's
// WebUI.swift, InspectorView, ChangesWebView, AgentsWebView); a field
// added here is added there.

export type Colors = {
  bg: string; fg: string; muted: string; border: string; raised: string; accent: string
  ok: string; warn: string; err: string; work: string; light: boolean
}

export type Tick = "on" | "mixed" | "off"

// --- inspector

export type InspectorState = {
  colors: Colors
  empty?: string
  project: string
  branch: string
  base?: string
  ahead?: number
  loading: boolean
  error?: string
  added: number
  deleted: number
  files: { path: string; code: string; added: number; deleted: number }[]
  moreFiles: number
  notes: { id: string; file: string; line?: number; note: string; by?: string; github: boolean; replies: number; sent: boolean; canResolve: boolean }[]
  moreNotes: number
  pr?: {
    number: number; title?: string; state?: string; url?: string; checks?: string; passed?: number; total?: number
    review?: string; runs: { name: string; state: string; url?: string }[]; moreRuns: number
  }
}

// --- a branch's changes

export type DiffLine = { kind: "context" | "added" | "removed" | "hunk" | "meta"; text: string; old?: number; new?: number; cl?: number }

export type ChangedFile = {
  path: string; code: string; added: number; deleted: number; binary: boolean
  stage: Tick; tick?: Tick; notes: number; live: boolean
}

export type Tone = "agent" | "work" | "ok" | "muted" | "text" | "warn"

export type Comment = {
  id: string; line: number; by: string; note: string; ago?: string
  chip?: { text: string; tone: Tone }
  replies: { by: string; note: string; ago?: string; agent: boolean }[]
  github: boolean; url?: string; outdated: boolean
  waiting: boolean; sentPane?: string; sentTo?: string; noAnswer: boolean
  canReply: boolean; canResolve: boolean
}

export type ChangesState = {
  colors: Colors
  branch: string
  base: string
  worktree: boolean
  loading: boolean
  busy?: string
  files: ChangedFile[]
  committed: ChangedFile[]
  commits: { hash: string; subject: string }[]
  allTick?: Tick
  commitWhat: string
  selected?: { path: string; committed: boolean; live: boolean }
  split: boolean
  hunks: { index: number; header: string; ticked: boolean; lines: DiffLine[] }[]
  diff: DiffLine[]
  comments: Comment[]
  pending: { id: string; line: number; note: string }[]
  sent: { id: string; line?: number; note: string; to: string; pane: string; ago: string }[]
  composing?: number
  commit: { files: number; message: string; writing: boolean; canStage: boolean; canStageNow: boolean; canCommit: boolean; agent?: string; credit: boolean }
  review: {
    notes: number; prOpen: boolean; prNumber?: number; pending: number; reviewed?: string
    event: string; body: string; canSubmit: boolean; events: { id: string; title: string; help: string }[]
  }
  send: { canPick: boolean; agents: { id: string; label: string; disabled: boolean }[]; startable: { name: string; title: string }[]; enabled: boolean; branch: string }
  checks?: {
    number: number; text: string; status: string; progress?: number
    runs: { name: string; state: string; url?: string; where: string }[]
    failing: boolean; fixLabel: string; canRerun: boolean; url?: string
    card?: { text: string; install?: string; open?: string }
  }
}

// --- every agent

export type AgentCard = {
  key: string; title: string; agent?: string; color: string; state: string; running: boolean
  place: string; ago?: string; focused: boolean; answering: boolean
  question?: { text: string; kind: string; choices: { index: number; label: string; default: boolean }[] }
}

export type AgentsState = {
  colors: Colors
  agents: AgentCard[]
  limits: { label: string; pct: number; agent: string; color: string }[]
  activity: { key: string; agent: string; text: string; ago: string; color: string }[]
}
