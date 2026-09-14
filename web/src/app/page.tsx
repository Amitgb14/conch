import { ArrowRightIcon } from "lucide-react"
import Link from "next/link"

import { GithubIcon } from "@/components/github-icon"
import { Architecture } from "@/components/landing/architecture"
import { BrainDemo } from "@/components/landing/brain-demo"
import { Features } from "@/components/landing/features"
import { InstallCard } from "@/components/landing/install-card"
import { Section } from "@/components/landing/section"
import { TuiDemo } from "@/components/landing/tui-demo"
import { TerminalWindow } from "@/components/terminal"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { site } from "@/lib/site"

const stats = [
  { value: "4", label: "coding agents supported" },
  { value: "1", label: "branch and worktree per task" },
  { value: "0", label: "agents stopped when you quit the UI" },
  { value: "ssh", label: "is all a remote machine needs" },
]

const agents = [
  {
    name: "Claude Code",
    state: "hooks, the screen, and usage from the transcript",
    install: "curl -fsSL https://claude.ai/install.sh | bash",
  },
  {
    name: "Codex",
    state: "the terminal title and the screen",
    install: "curl -fsSL https://chatgpt.com/codex/install.sh | sh",
  },
  {
    name: "Gemini CLI",
    state: "hooks in trusted folders, the title and the screen",
    install: "npm install -g --prefix ~/.local @google/gemini-cli",
  },
  {
    name: "OpenCode",
    state: "plugin events (busy, idle, permission, question) and the screen",
    install: "curl -fsSL https://opencode.ai/install | bash",
  },
]

const states = [
  { glyph: "⠋", name: "working", meaning: "the agent is busy", color: "text-working" },
  { glyph: "!", name: "waiting", meaning: "blocked on you — a permission or question", color: "text-waiting" },
  { glyph: "✓", name: "done", meaning: "finished while you looked elsewhere", color: "text-done" },
  { glyph: "○", name: "idle", meaning: "waiting for a prompt", color: "text-muted-foreground" },
]

const workflow = [
  {
    title: "Start a task",
    body: "Press t on a project and describe the work. conch names a branch from the prompt, checks it out into its own worktree, copies your local files and starts the agent.",
    code: 'conch task -cwd ~/src/api "Add a health check endpoint"',
  },
  {
    title: "Start a few more",
    body: "Each task is isolated from the others, so agents never trip over each other's changes. Or ask the brain to split a list of work into tasks for you.",
    code: 'conch ask "start 3 agents on api: fix the flaky test, add rate limiting, update the README"',
  },
  {
    title: "Get on with your day",
    body: "Detach with q. The tree shows who is working and who is waiting; a notification fires when an agent you aren't watching needs you, and ! jumps straight to it.",
    code: "conch status",
  },
  {
    title: "Review and ship",
    body: "Select a branch to see its uncommitted files, commits ahead, diffs and pull request checks. o opens the PR; x removes the worktree when you're done.",
    code: "conch read p1",
  },
]

export default function Home() {
  return (
    <>
      <section className="relative overflow-hidden">
        <div className="bg-blueprint pointer-events-none absolute inset-0 opacity-80 [mask-image:radial-gradient(80%_60%_at_50%_0%,black,transparent)]" />
        <div className="relative mx-auto w-full max-w-6xl px-5 pt-14 pb-10 sm:px-6 lg:pt-20">
          <div className="grid grid-cols-1 items-start gap-10 lg:grid-cols-[1.05fr_0.95fr] lg:gap-14">
            <div className="flex min-w-0 flex-col items-start">
              <Badge variant="outline" className="h-6 gap-2 bg-background px-2.5 font-mono text-[0.7rem]">
                <span className="relative flex size-1.5">
                  <span className="absolute inline-flex size-full animate-ping rounded-full bg-working opacity-60" />
                  <span className="relative inline-flex size-1.5 rounded-full bg-working" />
                </span>
                agents keep working after you detach
              </Badge>
              <h1 className="mt-5 text-[2.4rem] leading-[1.06] font-semibold tracking-[-0.032em] text-balance sm:text-[3rem] lg:text-[3.25rem]">
                Run a fleet of coding agents.{" "}
                <span className="text-muted-foreground">From one terminal.</span>
              </h1>
              <p className="mt-5 max-w-xl text-pretty text-muted-foreground sm:text-lg">
                conch is a terminal orchestrator for Claude Code, Codex, Gemini CLI and OpenCode. A
                background server owns their terminals, and one tree shows every machine, project,
                branch and agent — and which of them is waiting for you.
              </p>
              <div className="mt-8 flex flex-wrap gap-3">
                <Button size="lg" className="h-10 px-4" nativeButton={false} render={<Link href="/docs/quick-start" />}>
                  Get started <ArrowRightIcon data-icon="inline-end" />
                </Button>
                <Button size="lg" variant="outline" className="h-10 px-4" nativeButton={false} render={<a href={site.repo} />}>
                  <GithubIcon className="size-4" data-icon="inline-start" /> Source on GitHub
                </Button>
              </div>
              <p className="mt-6 font-mono text-xs text-muted-foreground">
                macOS · Linux · amd64 and arm64 · Apache 2.0 · written in Go
              </p>
            </div>
            <div className="min-w-0">
              <InstallCard />
            </div>
          </div>
          <div className="mt-16">
            <TuiDemo />
          </div>
          <dl className="mt-10 grid grid-cols-2 gap-px overflow-hidden rounded-xl border bg-border lg:grid-cols-4">
            {stats.map((s) => (
              <div key={s.label} className="bg-background px-5 py-5">
                <dt className="font-mono text-2xl font-semibold tracking-tight">{s.value}</dt>
                <dd className="mt-1 text-sm text-muted-foreground">{s.label}</dd>
              </div>
            ))}
          </dl>
        </div>
      </section>

      <Section
        id="features"
        label="what you get"
        title="Everything your agents are doing, in one tree"
        lede="Machines, projects, git branches, agents and terminals sit beside a live view of whatever you select. Every card links to its docs."
      >
        <Features />
      </Section>

      <Section
        id="workflow"
        label="a day with conch"
        title="Hand out the work, then stop babysitting it"
        lede="The loop conch is built for: many small tasks, each on its own branch, and you only step in when an agent asks."
        className="bg-surface"
      >
        <ol className="grid grid-cols-1 gap-6 md:grid-cols-2">
          {workflow.map((step, i) => (
            <li key={step.title} className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-5">
              <div className="flex items-center gap-3">
                <span className="grid size-7 place-items-center rounded-full border font-mono text-xs text-muted-foreground">
                  {String(i + 1).padStart(2, "0")}
                </span>
                <h3 className="font-semibold tracking-tight">{step.title}</h3>
              </div>
              <p className="flex-1 text-sm leading-6 text-muted-foreground">{step.body}</p>
              <TerminalWindow className="shadow-none">
                <code className="block overflow-x-auto px-4 py-3 whitespace-nowrap">
                  <span className="text-term-muted select-none">$ </span>
                  {step.code}
                </code>
              </TerminalWindow>
            </li>
          ))}
        </ol>
      </Section>

      <Section
        id="how-it-works"
        label="how it works"
        title="The UI is disposable. The server is not."
        lede="Every machine runs its own conch server, which owns the terminals your agents run in. The TUI is only a client: quit it, lose the network, reboot your laptop — the agents on the other side keep going."
      >
        <Architecture />
        <div className="mt-6 grid gap-4 md:grid-cols-3">
          {[
            {
              title: "Panes live in the server",
              body: "A PTY and terminal emulator per pane, with 10,000 lines of scrollback. Reattach from any terminal and pick up where you were.",
            },
            {
              title: "State from the best evidence",
              body: "Hooks first, then regexes over the screen, then the foreground process. conch agent explain shows why an agent is in its state.",
            },
            {
              title: "Reload in place",
              body: "conch server reload execs a new build keeping its process ID, terminals and socket — agents never notice the upgrade.",
            },
          ].map((c) => (
            <div key={c.title} className="rounded-xl border bg-card p-5">
              <h3 className="font-semibold tracking-tight">{c.title}</h3>
              <p className="mt-2 text-sm leading-6 text-muted-foreground">{c.body}</p>
            </div>
          ))}
        </div>
      </Section>

      <Section
        id="agents"
        label="bring your own agents"
        title="Works with the agents you already use"
        lede="conch launches each agent with its own login and configuration, and never edits that configuration or answers its trust prompts for you. A missing agent installs from the same menu — no root needed."
        className="bg-surface"
      >
        <div className="grid grid-cols-1 gap-8 lg:grid-cols-[1.4fr_1fr]">
          <div className="overflow-x-auto rounded-xl border bg-card">
            <table className="w-full text-sm">
              <thead className="bg-muted/60 text-left">
                <tr>
                  <th className="px-4 py-2.5 font-medium">Agent</th>
                  <th className="px-4 py-2.5 font-medium">How conch knows its state</th>
                </tr>
              </thead>
              <tbody>
                {agents.map((a) => (
                  <tr key={a.name} className="border-t align-top">
                    <td className="px-4 py-3 font-medium whitespace-nowrap">{a.name}</td>
                    <td className="px-4 py-3">
                      <p className="text-muted-foreground">{a.state}</p>
                      <code className="mt-1.5 block truncate font-mono text-[0.7rem] text-muted-foreground/80">{a.install}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex flex-col gap-3">
            <p className="font-mono text-xs text-muted-foreground">agent states</p>
            {states.map((s) => (
              <div key={s.name} className="flex items-center gap-4 rounded-xl border bg-card px-4 py-3">
                <span className={`w-4 text-center font-mono text-lg ${s.color}`}>{s.glyph}</span>
                <div>
                  <p className="font-medium">{s.name}</p>
                  <p className="text-sm text-muted-foreground">{s.meaning}</p>
                </div>
              </div>
            ))}
          </div>
        </div>
      </Section>

      <Section
        id="brain"
        label="the brain"
        title="Say what you want. Approve the plan."
        lede="Press : and ask in plain words. The brain sees every machine, project, branch and agent, proposes actions with checkboxes and marks the ones that can't run. Nothing runs until you press enter."
      >
        <div className="grid grid-cols-1 items-start gap-10 lg:grid-cols-[1fr_1.2fr]">
          <div className="flex flex-col gap-4 text-sm leading-6 text-muted-foreground">
            <p>
              <strong className="text-foreground">Providers:</strong> the Claude Code CLI with your existing
              login (the default), the Anthropic API, or any OpenAI-compatible endpoint — including a local
              Ollama.
            </p>
            <p>
              <strong className="text-foreground">Summaries:</strong> press S on an agent for a one-line
              summary of what it is doing and what it needs from you, or turn on automatic summaries.
            </p>
            <Button variant="outline" className="w-fit" nativeButton={false} render={<Link href="/docs/brain" />}>
              Read about the brain <ArrowRightIcon data-icon="inline-end" />
            </Button>
          </div>
          <BrainDemo />
        </div>
      </Section>

      <section className="border-t">
        <div className="relative mx-auto w-full max-w-6xl overflow-hidden px-5 py-20 sm:px-6">
          <div className="relative flex flex-col items-center gap-6 rounded-2xl border bg-card px-6 py-14 text-center">
            <div className="bg-blueprint pointer-events-none absolute inset-0 rounded-2xl opacity-70 [mask-image:radial-gradient(60%_70%_at_50%_100%,black,transparent)]" />
            <h2 className="relative max-w-2xl text-3xl font-semibold tracking-[-0.025em] text-balance sm:text-4xl">
              Give your agents a place to work
            </h2>
            <p className="relative max-w-xl text-muted-foreground">
              Install conch, run <code className="rounded border bg-muted px-1.5 font-mono text-sm">conch</code>, and
              press <code className="rounded border bg-muted px-1.5 font-mono text-sm">t</code> on a project.
            </p>
            <div className="relative flex flex-wrap justify-center gap-3">
              <Button size="lg" className="h-10 px-4" nativeButton={false} render={<Link href="/#install" />}>
                Install conch
              </Button>
              <Button size="lg" variant="outline" className="h-10 px-4" nativeButton={false} render={<Link href="/docs" />}>
                Read the docs
              </Button>
            </div>
          </div>
        </div>
      </section>
    </>
  )
}
