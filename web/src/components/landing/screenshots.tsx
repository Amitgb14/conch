import { TuiScreen, TuiWindow } from "./tui-screen"

// The TUI itself, captured from a staged demo (web/scripts/tui-capture) and
// drawn as text, so every letter is as sharp as the page around it.
const shots = [
  {
    id: "diff",
    title: "conch — api/add-health-check · changes",
    caption:
      "Review an agent's work before it lands: a branch's uncommitted changes and commits ahead, and each file's diff, hunk by hunk.",
  },
  {
    id: "queue",
    title: "conch — review queue",
    caption:
      "The review queue, across every machine: the agent waiting for an answer, the pull request whose CI failed, the branch ready to push.",
  },
] as const

export function Screenshots() {
  return (
    <div className="flex flex-col gap-12">
      {shots.map((s) => (
        <figure key={s.id} className="flex flex-col gap-3">
          <TuiWindow title={<span className="ml-2 truncate font-mono text-[0.7rem] text-[#8b949e]">{s.title}</span>}>
            <TuiScreen id={s.id} />
          </TuiWindow>
          <figcaption className="text-sm text-muted-foreground">{s.caption}</figcaption>
        </figure>
      ))}
    </div>
  )
}
