import { flushSync } from "react-dom"
import { createRoot, type Root } from "react-dom/client"
import { useSyncExternalStore } from "react"
import "./styles.css"
import { post } from "./bridge"
import type { Colors } from "./types"
import { Inspector } from "./views/Inspector"
import { Changes } from "./views/Changes"
import { Agents } from "./views/Agents"

// conch-ui's entry: the host mounts one view on the page and then sends
// it the whole of what it shows, again on every change.
//   conchUI.mount("inspector" | "changes" | "agents")
//   conchUI.update(state)

let state: any = null
const listeners = new Set<() => void>()
const subscribe = (f: () => void) => { listeners.add(f); return () => listeners.delete(f) }
const read = () => state

function applyColors(c?: Colors) {
  if (!c) return
  const s = document.documentElement.style
  for (const k of ["bg", "fg", "muted", "border", "raised", "accent", "ok", "warn", "err", "work"] as const) {
    if (c[k]) s.setProperty("--" + k, c[k])
  }
  s.colorScheme = c.light ? "light" : "dark"
}

function View({ kind }: { kind: string }) {
  const s = useSyncExternalStore(subscribe, read)
  if (!s) return null
  if (kind === "inspector") return <Inspector s={s} />
  if (kind === "changes") return <Changes s={s} />
  if (kind === "agents") return <Agents s={s} />
  return null
}

let root: Root | null = null
let mounted = ""

const api = {
  mount(kind: string) {
    if (root && mounted === kind) return
    mounted = kind
    const el = document.getElementById("root")
    if (!el) return
    root?.unmount()
    root = createRoot(el)
    // Drawn at once rather than on React's next turn: the host sends the
    // view and may ask the page about it straight away.
    const r = root
    flushSync(() => r.render(<View kind={kind} />))
    post(kind, "ready")
  },
  update(next: any) {
    state = next
    applyColors(next?.colors)
    flushSync(() => listeners.forEach((f) => f()))
  },
}

;(window as any).conchUI = api
