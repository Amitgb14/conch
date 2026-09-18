import Image, { type StaticImageData } from "next/image"

import branchesAgents from "@/images/branches-agents.png"
import overview from "@/images/overview.png"

const shots: { src: StaticImageData; title: string; alt: string; caption: string }[] = [
  {
    src: overview,
    title: "conch — local",
    alt: "conch showing a machine: its projects in the tree, plan usage and the agents installed",
    caption:
      "A machine at a glance: what is running, how much of the plan window is left, and which agents are installed.",
  },
  {
    src: branchesAgents,
    title: "conch — conch/new-features",
    alt: "conch showing a project: branches, agents and terminals in the tree, a tab per agent, and an agent's diff in the pane",
    caption: "A project opens into branches, agents and terminals, with a tab per agent and its work live in the pane.",
  },
]

export function Screenshots() {
  return (
    <div className="flex flex-col gap-12">
      {shots.map((s) => (
        <figure key={s.title} className="flex flex-col gap-3">
          {/* The chrome of TerminalWindow, around a picture rather than text. */}
          <div className="overflow-hidden rounded-xl border border-term-border bg-term shadow-[0_24px_60px_-20px_rgb(0_0_0/0.35)]">
            <div className="flex items-center gap-2 border-b border-term-border px-3.5 py-2.5">
              <span className="flex gap-1.5">
                <i className="size-2.5 rounded-full bg-white/15" />
                <i className="size-2.5 rounded-full bg-white/15" />
                <i className="size-2.5 rounded-full bg-white/15" />
              </span>
              <span className="ml-2 truncate font-mono text-[0.7rem] text-term-muted">{s.title}</span>
            </div>
            <Image src={s.src} alt={s.alt} sizes="(min-width: 1024px) 72rem, 100vw" className="h-auto w-full" />
          </div>
          <figcaption className="text-sm text-muted-foreground">{s.caption}</figcaption>
        </figure>
      ))}
    </div>
  )
}
