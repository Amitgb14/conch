import {
  BotIcon,
  CloudIcon,
  CornerDownLeftIcon,
  FileDiffIcon,
  FolderTreeIcon,
  GitBranchIcon,
  GlobeIcon,
  HistoryIcon,
  MessageSquareIcon,
  MonitorIcon,
  PanelsTopLeftIcon,
  RadioIcon,
  RefreshCwIcon,
  RotateCwIcon,
  ShieldCheckIcon,
  SmartphoneIcon,
  SparklesIcon,
  SquareTerminalIcon,
  type LucideIcon,
} from "lucide-react"
import Link from "next/link"

import { TuiScreen, TuiWindow } from "@/components/landing/tui-screen"
import { cn } from "@/lib/utils"

// The pictures a docs page draws rather than shows: diagrams built from the
// page's own type and colours, so they stay sharp, follow the light and dark
// themes, and say nothing a screenshot would have to be retaken for.

// Icons by name, since an MDX page cannot import them.
const icons: Record<string, LucideIcon> = {
  agents: BotIcon,
  browser: GlobeIcon,
  chat: MessageSquareIcon,
  cloud: CloudIcon,
  desktop: MonitorIcon,
  diff: FileDiffIcon,
  files: FolderTreeIcon,
  git: GitBranchIcon,
  loop: RefreshCwIcon,
  phone: SmartphoneIcon,
  reload: RotateCwIcon,
  scope: ShieldCheckIcon,
  sessions: HistoryIcon,
  signal: RadioIcon,
  sparkles: SparklesIcon,
  terminal: SquareTerminalIcon,
  workbench: PanelsTopLeftIcon,
}

function Icon({ name, className }: { name: string; className?: string }) {
  const I = icons[name]
  if (!I) throw new Error(`no such icon: ${name} (add it to docs-visuals.tsx)`)
  return <I className={className} />
}

/** Cards in two columns: `<FeatureGrid><Feature icon="loop" title="…" href="…">…</Feature></FeatureGrid>` */
export function FeatureGrid({ children }: { children: React.ReactNode }) {
  return <div className="not-prose my-6 grid gap-3 sm:grid-cols-2">{children}</div>
}

export function Feature({
  icon,
  title,
  href,
  badge,
  children,
}: {
  icon: string
  title: string
  href?: string
  badge?: string
  children: React.ReactNode
}) {
  const body = (
    <>
      <div className="flex items-center gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-brand/25 bg-brand-soft/60 text-brand">
          <Icon name={icon} className="size-4" />
        </span>
        <p className="font-medium leading-snug">{title}</p>
        {badge && (
          <span className="ml-auto rounded-full border px-2 py-0.5 font-mono text-[0.65rem] text-muted-foreground">
            {badge}
          </span>
        )}
      </div>
      <div className="text-sm leading-6 text-muted-foreground [&_code]:text-[0.8em] [&_p]:my-0">{children}</div>
    </>
  )
  const cls = "flex flex-col gap-2.5 rounded-xl border bg-card p-4"
  return href ? (
    <Link href={href} className={cn(cls, "transition-colors hover:border-brand/40 hover:bg-brand-soft/20")}>
      {body}
    </Link>
  ) : (
    <div className={cls}>{body}</div>
  )
}

type Node = { icon: string; title: string; sub: string; tone?: "brand" | "done" | "waiting" }

const toneRing = {
  brand: "border-brand/30 bg-brand-soft/50 text-brand",
  done: "border-done/30 bg-done/10 text-done",
  waiting: "border-waiting/30 bg-waiting/10 text-waiting",
}

function Flow({ nodes, back, label }: { nodes: Node[]; back?: React.ReactNode; label: string }) {
  return (
    <figure className="my-6 rounded-2xl border bg-card p-4 sm:p-5" aria-label={label}>
      <ol className="grid gap-2 sm:grid-flow-col sm:auto-cols-fr">
        {nodes.map((n, i) => (
          <li key={n.title} className="relative flex items-start gap-3 sm:flex-col sm:items-center sm:text-center">
            <span
              className={cn(
                "z-10 flex size-10 shrink-0 items-center justify-center rounded-full border",
                toneRing[n.tone ?? "brand"],
              )}
            >
              <Icon name={n.icon} className="size-4.5" />
            </span>
            {i < nodes.length - 1 && (
              <span aria-hidden className="absolute top-5 left-[calc(50%+1.5rem)] hidden h-px w-[calc(100%-3rem)] bg-border sm:block" />
            )}
            <span className="flex flex-col gap-0.5">
              <span className="text-sm font-medium leading-snug">{n.title}</span>
              <span className="text-xs leading-5 text-muted-foreground">{n.sub}</span>
            </span>
          </li>
        ))}
      </ol>
      {back && (
        <div className="mt-4 flex items-start gap-2 rounded-lg border border-dashed border-waiting/40 bg-waiting/5 px-3 py-2 text-xs leading-5 text-muted-foreground">
          <CornerDownLeftIcon className="mt-0.5 size-3.5 shrink-0 text-waiting" />
          <span>{back}</span>
        </div>
      )}
      <figcaption className="sr-only">{label}</figcaption>
    </figure>
  )
}

/** The autopilot's loop: an agent's work checked, sent back while red, merged once green. */
export function AutopilotLoop() {
  return (
    <Flow
      label="The autopilot: the agent finishes, the check and CI run, a failure goes back to the agent, and the pull request merges once green"
      nodes={[
        { icon: "agents", title: "Agent finishes", sub: "on its own branch and worktree" },
        { icon: "terminal", title: "Check runs", sub: "the project's own test command" },
        { icon: "git", title: "Pull request", sub: "opened when the agent is done" },
        { icon: "loop", title: "CI", sub: "watched by the server" },
        { icon: "sparkles", title: "Merged", sub: "at the commit that went green", tone: "done" },
      ]}
      back={
        <>
          <strong className="text-foreground">Red?</strong> The failed check or the tail of the CI log goes back to the
          branch&rsquo;s agent, which fixes it and finishes again — up to the limit you set, then it is yours. All of it runs
          in the machine&rsquo;s server, so it carries on with every window closed.
        </>
      }
    />
  )
}

/** A task's way from a prompt to a merge. */
export function TaskFlow() {
  return (
    <Flow
      label="A task: a prompt becomes a branch and worktree, an agent works there, you review its changes, and it lands as a pull request"
      nodes={[
        { icon: "chat", title: "A prompt", sub: "t on a project, or conch task" },
        { icon: "git", title: "Branch + worktree", sub: "named for you, your .env copied in" },
        { icon: "agents", title: "Agent works", sub: "in a pane the server owns" },
        { icon: "diff", title: "Review", sub: "the diff, hunk by hunk" },
        { icon: "sparkles", title: "Pull request, merge", sub: "by you, or by the autopilot", tone: "done" },
      ]}
    />
  )
}

const states = [
  { glyph: "⠋", name: "working", sub: "busy", cls: "text-working border-working/30 bg-working/10" },
  { glyph: "!", name: "waiting", sub: "needs you", cls: "text-waiting border-waiting/30 bg-waiting/10" },
  { glyph: "✓", name: "done", sub: "finished, unseen", cls: "text-done border-done/30 bg-done/10" },
  { glyph: "○", name: "idle", sub: "wants a prompt", cls: "text-muted-foreground border-border bg-muted/50" },
  { glyph: "✗", name: "failed", sub: "ended in an error", cls: "text-destructive border-destructive/30 bg-destructive/10" },
]

const sources = [
  { name: "The agent's own report", sub: "OSC 7501", width: "w-full" },
  { name: "Hooks", sub: "Claude Code and others", width: "w-[82%]" },
  { name: "Screen rules", sub: "the bottom of its screen", width: "w-[64%]" },
  { name: "Foreground process", sub: "is the agent running at all", width: "w-[46%]" },
]

/** The five states an agent is shown in, and where conch learns them, most reliable first. */
export function AgentStates() {
  return (
    <figure className="my-6 flex flex-col gap-5 rounded-2xl border bg-card p-4 sm:p-5">
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
        {states.map((s) => (
          <div key={s.name} className="flex flex-col items-center gap-1.5 rounded-xl border bg-background px-2 py-3 text-center">
            <span className={cn("flex size-9 items-center justify-center rounded-full border font-mono text-base", s.cls)}>
              {s.glyph}
            </span>
            <span className="font-mono text-xs font-medium">{s.name}</span>
            <span className="text-[0.7rem] leading-4 text-muted-foreground">{s.sub}</span>
          </div>
        ))}
      </div>
      <div className="flex flex-col gap-1.5">
        <p className="text-xs font-medium text-muted-foreground">How conch knows, most reliable first</p>
        {sources.map((s, i) => (
          <div key={s.name} className={cn("flex items-center gap-2 rounded-md border border-brand/25 bg-brand-soft/50 px-2.5 py-1.5", s.width)}>
            <span className="font-mono text-[0.7rem] text-brand">{i + 1}</span>
            <span className="text-xs font-medium">{s.name}</span>
            <span className="truncate text-[0.7rem] text-muted-foreground">{s.sub}</span>
          </div>
        ))}
      </div>
    </figure>
  )
}

/** A captured TUI screen in a docs page: `<TuiShot id="project" title="…" caption="…" />` */
export function TuiShot({ id, title, caption }: { id: string; title?: string; caption?: string }) {
  return (
    <figure className="my-6 flex flex-col gap-3">
      <TuiWindow title={title && <span className="ml-2 truncate font-mono text-[0.7rem] text-[#8b949e]">{title}</span>}>
        <TuiScreen id={id} className="text-[clamp(5px,0.62vw,9px)]!" />
      </TuiWindow>
      {caption && <figcaption className="text-sm text-muted-foreground">{caption}</figcaption>}
    </figure>
  )
}
