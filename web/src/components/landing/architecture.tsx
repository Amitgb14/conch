import { LaptopIcon, ServerIcon } from "lucide-react"

import { cn } from "@/lib/utils"

function Box({ title, sub, children, className }: { title: string; sub?: string; children?: React.ReactNode; className?: string }) {
  return (
    <div className={cn("rounded-lg border bg-background px-3 py-2.5", className)}>
      <p className="font-mono text-[0.78rem] font-medium">{title}</p>
      {sub && <p className="mt-0.5 text-[0.72rem] text-muted-foreground">{sub}</p>}
      {children}
    </div>
  )
}

function Pane({ state, name }: { state: "working" | "waiting" | "idle"; name: string }) {
  const glyph = { working: "⠋", waiting: "!", idle: "○" }[state]
  const color = { working: "text-working", waiting: "text-waiting", idle: "text-muted-foreground" }[state]
  return (
    <div className="flex items-center gap-2 rounded-md border bg-card px-2 py-1 font-mono text-[0.7rem]">
      <span className={color}>{glyph}</span>
      <span className="truncate">{name}</span>
    </div>
  )
}

function Machine({ icon: Icon, name, children }: { icon: typeof LaptopIcon; name: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-dashed bg-surface p-4">
      <p className="flex items-center gap-2 font-mono text-xs text-muted-foreground">
        <Icon className="size-3.5" /> {name}
      </p>
      {children}
    </div>
  )
}

function Link({ label, className }: { label: string; className?: string }) {
  return (
    <div className={cn("flex items-center justify-center gap-2 py-2 font-mono text-[0.68rem] text-muted-foreground", className)}>
      <span className="h-px w-6 bg-border lg:w-10" />
      {label}
      <span className="h-px w-6 bg-border lg:w-10" />
    </div>
  )
}

// A drawn picture of conch's architecture: the TUI is a client; each
// machine's server owns the panes the agents run in.
export function Architecture() {
  return (
    <div className="grid grid-cols-1 items-center gap-2 rounded-2xl border bg-card p-4 sm:p-6 lg:grid-cols-[1fr_auto_1fr]">
      <Machine icon={LaptopIcon} name="your laptop">
        <Box title="conch" sub="the TUI — a client you can quit any time" />
        <Link label="unix socket · NDJSON" />
        <Box title="conch server" sub="owns panes, tracks git, PRs and agent states">
          <div className="mt-2.5 grid gap-1.5">
            <Pane state="working" name="claude · add-health-check" />
            <Pane state="waiting" name="codex · fix/flaky" />
            <Pane state="idle" name="zsh · dev-server" />
          </div>
        </Box>
      </Machine>
      <Link label="ssh · conch bridge" className="lg:flex-col lg:[&>span]:h-10 lg:[&>span]:w-px" />
      <Machine icon={ServerIcon} name="gpu-box (remote)">
        <Box title="conch bridge" sub="started over your normal ssh; no secrets stored" />
        <Link label="unix socket · NDJSON" />
        <Box title="conch server" sub="keeps running when your laptop sleeps">
          <div className="mt-2.5 grid gap-1.5">
            <Pane state="working" name="claude · train-eval" />
            <Pane state="working" name="opencode · data-loader" />
          </div>
        </Box>
      </Machine>
    </div>
  )
}
