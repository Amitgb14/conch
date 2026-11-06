"use client"

import { useEffect, useState } from "react"

import { cn } from "@/lib/utils"

import { TuiScreen, TuiWindow } from "./tui-screen"

const views = [
  { id: "working", label: "Agent working", caption: "Selecting an agent shows its live terminal, with state and token usage in the title." },
  { id: "waiting", label: "Waiting for you", caption: "A permission prompt turns the agent's state to waiting — ⚑ counts them and ! jumps to the next." },
  { id: "project", label: "Project", caption: "A project page lists its agents, worktrees and uncommitted changes." },
  { id: "branch", label: "Branch and PR", caption: "A branch shows its pull request checks, the files it changed and its commits ahead." },
  { id: "terminal", label: "Terminal", caption: "Plain shells live in the tree next to the agents, and keep running when you detach." },
] as const

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
      <TuiWindow
        title={
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
        }
      >
        <TuiScreen key={active} id={active} className="animate-in fade-in duration-300" />
      </TuiWindow>
      <p className="text-center text-sm text-muted-foreground">{view.caption}</p>
    </div>
  )
}
