"use client"

import { useEffect, useState } from "react"

import { cn } from "@/lib/utils"

import data from "./tui-frames.json"

// Real conch TUI screens, captured from a staged demo project and converted
// from the server's ANSI frames into styled runs.
type Style = { fg?: string; bg?: string; b?: number; d?: number; i?: number; u?: number }
type Run = [string, number]

const styles = data.styles as unknown as Style[]
const frames = data.frames as unknown as Record<string, Run[][]>

const views = [
  { id: "working", label: "Agent working", caption: "Selecting an agent shows its live terminal, with state and token usage in the title." },
  { id: "waiting", label: "Waiting for you", caption: "A permission prompt turns the agent's state to waiting — ⚑ counts them and ! jumps to the next." },
  { id: "project", label: "Project", caption: "A project page lists its agents, worktrees and uncommitted changes." },
  { id: "branch", label: "Branch and PR", caption: "A branch shows its pull request checks, the files it changed and its commits ahead." },
  { id: "terminal", label: "Terminal", caption: "Plain shells live in the tree next to the agents, and keep running when you detach." },
] as const

const VERTICAL = new Set("│┃║╭╮╰╯├┤┌┐└┘")

// Fonts disagree on the width of symbols such as ⏺ ⎿ ⠏ and box drawing, so
// every non-ASCII character gets exactly one cell; vertical box lines are
// stretched to meet the rows above and below.
function Cells({ text }: { text: string }) {
  const parts: React.ReactNode[] = []
  let ascii = ""
  let i = 0
  for (const ch of text) {
    if (ch.charCodeAt(0) < 0x7f) {
      ascii += ch
      continue
    }
    if (ascii) parts.push(ascii)
    ascii = ""
    parts.push(
      <span
        key={i++}
        className="inline-block w-[1ch] text-center"
        style={VERTICAL.has(ch) ? { transform: "scaleY(1.35)" } : undefined}
      >
        {ch}
      </span>,
    )
  }
  if (ascii) parts.push(ascii)
  return <>{parts}</>
}

function Line({ runs }: { runs: Run[] }) {
  return (
    <div className="h-[1.22em] whitespace-pre">
      {runs.map(([text, s], i) => {
        const st = styles[s]
        return (
          <span
            key={i}
            style={{
              color: st.fg,
              backgroundColor: st.bg,
              fontWeight: st.b ? 700 : undefined,
              opacity: st.d ? 0.6 : undefined,
              fontStyle: st.i ? "italic" : undefined,
              textDecoration: st.u ? "underline" : undefined,
            }}
          >
            <Cells text={text} />
          </span>
        )
      })}
      {runs.length === 0 && " "}
    </div>
  )
}

export function TuiDemo() {
  const [active, setActive] = useState<(typeof views)[number]["id"]>("working")
  const [auto, setAuto] = useState(true)

  useEffect(() => {
    if (!auto) return
    const t = setInterval(() => {
      setActive((a) => views[(views.findIndex((v) => v.id === a) + 1) % views.length].id)
    }, 5000)
    return () => clearInterval(t)
  }, [auto])

  const view = views.find((v) => v.id === active)!

  return (
    <div className="flex flex-col gap-3">
      <div className="overflow-hidden rounded-xl border border-term-border bg-[#0d1117] shadow-[0_40px_100px_-30px_rgb(0_0_0/0.45)]">
        <div className="flex items-center gap-2 border-b border-white/10 bg-[#161b22] px-3.5 py-2.5">
          <span className="flex gap-1.5">
            <i className="size-2.5 rounded-full bg-[#ff5f57]" />
            <i className="size-2.5 rounded-full bg-[#febc2e]" />
            <i className="size-2.5 rounded-full bg-[#28c840]" />
          </span>
          <div role="tablist" aria-label="conch screens" className="ml-3 flex min-w-0 gap-1 overflow-x-auto">
            {views.map((v) => (
              <button
                key={v.id}
                role="tab"
                type="button"
                aria-selected={active === v.id}
                onClick={() => {
                  setActive(v.id)
                  setAuto(false)
                }}
                className={cn(
                  "rounded-md px-2.5 py-1 font-mono text-[0.7rem] whitespace-nowrap transition-colors",
                  active === v.id ? "bg-white/10 text-white" : "text-[#8b949e] hover:text-white",
                )}
              >
                {v.label}
              </button>
            ))}
          </div>
        </div>
        <div className="overflow-x-auto px-3 py-3">
          <div
            key={active}
            className="w-max font-mono text-[#e6edf3] animate-in fade-in duration-300"
            style={{ fontSize: "clamp(8px, 0.93vw, 12.5px)", lineHeight: 1.22 }}
          >
            {frames[active].map((runs, i) => (
              <Line key={i} runs={runs} />
            ))}
          </div>
        </div>
      </div>
      <p className="text-center text-sm text-muted-foreground">{view.caption}</p>
    </div>
  )
}
