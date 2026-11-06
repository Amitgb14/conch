"use client"

import { ArrowRightIcon, BotIcon, MessageSquareTextIcon, PanelsTopLeftIcon } from "lucide-react"
import Image from "next/image"
import Link from "next/link"
import { useState } from "react"

import { CopyButton } from "@/components/copy-button"
import { CommandLine, TerminalWindow } from "@/components/terminal"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import skillsShot from "@/images/desktop-skills.png"
import { cn } from "@/lib/utils"

// conch's skills, as internal/agentsetup/skills ships them: what each one
// teaches an agent, what happens when it runs, and what you would say.
type Skill = {
  name: string
  tagline: string
  body: string
  steps: string[]
  asks: string[]
  slash: string
}

const skills: Skill[] = [
  {
    name: "conch",
    tagline: "Hand work to a helper",
    body: "Start another agent on a branch of its own, prompt it, wait for it and read its answer. The one the others build on.",
    steps: ["Starts a helper on its own branch and worktree", "Waits for it to finish", "Reads its answer or the file it wrote", "Tells you what it found"],
    asks: ["Get a second opinion on this branch from another agent.", "Have Codex write tests for the parser and run them."],
    slash: "/conch",
  },
  {
    name: "conch-team",
    tagline: "Several agents at once",
    body: "One task tried by two or three agents to keep the best, a task split into parts that run side by side, or a chain where each starts from the last one's branch.",
    steps: ["Picks a race, a split or a chain", "Starts each agent on a branch of its own", "Waits for all of them", "Compares the results and recommends one"],
    asks: ["Have Claude and Codex each try the parser fix, and tell me which to keep.", "Split the export feature: one agent on the API, one on the UI."],
    slash: "/conch-team",
  },
  {
    name: "conch-review",
    tagline: "Review until clean",
    body: "Another agent reviews the branch against what was asked and comments on its lines; the author fixes or answers each one, and the reviewer looks again.",
    steps: ["A reviewer checks the branch against its spec or issue", "Comments go on the lines that need them", "The author fixes or answers each one", "Review again — until clean, or three rounds"],
    asks: ["Get this branch reviewed by Codex and fixed until the review is clean.", "Review the login change against issue 42 and send the findings to its author."],
    slash: "/conch-review",
  },
  {
    name: "conch-ship",
    tagline: "Tests to a merged PR",
    body: "Runs the project's tests, pushes, opens the pull request, watches CI and sends a failure back to the branch's agent — and merges once it is green, only when you asked.",
    steps: ["Runs the project's tests", "Pushes and opens the pull request", "Watches CI; a failure goes back to the agent", "Merges when green, if you asked"],
    asks: ["Run the tests, open the PR and merge it once CI is green.", "Get CI green on this branch."],
    slash: "/conch-ship",
  },
  {
    name: "conch-factory",
    tagline: "A ticket to a merged PR",
    body: "Agrees a spec with you, then one agent builds it test-first, another reviews against the spec, the builder fixes, a tester tries it, and the pull request goes through CI — with a retro at the end.",
    steps: ["Reads the ticket and agrees a spec with you", "A builder works test-first; a reviewer checks it", "A tester tries it; the pull request goes through CI", "Merges if you asked, then a short retro"],
    asks: ["Take issue 123 to a merged pull request.", "Work through the issues labelled ready, two at a time."],
    slash: "/conch-factory 123",
  },
  {
    name: "conch-autopilot",
    tagline: "Automate the workflow",
    body: "Finds the project's check and runs it, proposes the policy — a reviewer, failures sent back, pull requests opened, merging off until you trust it — and guardrails, and applies only what you agree to.",
    steps: ["Finds the project's check and runs it once", "Proposes the policy and guardrails", "Applies only what you agree to", "Shows how to feed it work"],
    asks: ["Set up an automated workflow for this project.", "Every morning, have an agent update dependencies and fix what breaks."],
    slash: "/conch-autopilot",
  },
]

const line = ["ticket", "spec", "build", "review", "fix", "test", "pull request", "CI", "merge", "retro"]

const use = [
  { icon: PanelsTopLeftIcon, title: "Start an agent in conch", body: "In Conch.app, Start an agent on a project; in the TUI, c. The skills act only inside a conch pane." },
  { icon: MessageSquareTextIcon, title: "Ask in plain words", body: "The agent picks the skill. In Claude Code you can also type its name, like /conch-factory 123." },
  { icon: BotIcon, title: "Watch the helpers work", body: "Each one appears in the sidebar under the agent that started it. A question it asks is yours to answer." },
]

// An agent's prompt box, with what you would type in it.
function Prompt({ text, agent = "claude" }: { text: string; agent?: string }) {
  return (
    <div className="group relative overflow-hidden rounded-lg border border-term-border bg-term text-term-foreground">
      <div className="flex items-center gap-2 border-b border-term-border px-3 py-1.5 font-mono text-[0.65rem] text-term-muted">
        <span className="size-1.5 rounded-full bg-brand" />
        {agent} · in a conch pane
      </div>
      <div className="flex items-start gap-2 px-3 py-2.5">
        <span className="font-mono text-sm text-brand select-none">❯</span>
        <p className="min-w-0 flex-1 text-sm leading-6">{text}</p>
        <CopyButton value={text} className="-my-1 shrink-0" />
      </div>
    </div>
  )
}

function Step({ n, children }: { n: number; children: React.ReactNode }) {
  return (
    <li className="flex items-start gap-3">
      <span className="mt-0.5 grid size-5 shrink-0 place-items-center rounded-full border font-mono text-[0.65rem] text-muted-foreground">
        {n}
      </span>
      <span className="text-sm leading-6">{children}</span>
    </li>
  )
}

export function Skills() {
  const [pick, setPick] = useState(skills[4].name)
  const s = skills.find((x) => x.name === pick) ?? skills[0]

  return (
    <div className="flex flex-col gap-14">
      {/* 1. Install */}
      <div className="flex flex-col gap-4">
        <h3 className="text-lg font-semibold tracking-tight">1 · Install them once</h3>
        <div className="rounded-2xl border bg-card p-2 shadow-sm">
          <Tabs defaultValue="app" className="gap-0">
            <div className="flex flex-wrap items-center justify-between gap-2 px-2 pt-1 pb-3">
              <TabsList>
                <TabsTrigger value="app" className="px-2.5">Conch.app</TabsTrigger>
                <TabsTrigger value="cli" className="px-2.5">Terminal</TabsTrigger>
                <TabsTrigger value="plugin" className="px-2.5">Claude Code plugin</TabsTrigger>
              </TabsList>
              <Badge variant="outline" className="font-mono">Claude Code · Codex · Gemini · OpenCode · Devin</Badge>
            </div>
            <TabsContent value="app">
              <div className="grid items-start gap-6 p-2 lg:grid-cols-[1fr_1.4fr]">
                <ol className="flex flex-col gap-3">
                  <Step n={1}>
                    Open <b>Settings</b> — <kbd className="rounded border px-1 font-mono text-xs">⌘,</kbd>, or <b>⚙ Settings</b> in the status bar.
                  </Step>
                  <Step n={2}>
                    Choose <b>Skills &amp; MCP servers</b> in the sidebar, and the machine at the top if you have more than one.
                  </Step>
                  <Step n={3}>
                    Click <b>Install all…</b> under <b>conch&apos;s skills</b>. It names every file it writes before writing it.
                  </Step>
                  <Step n={4}>
                    The ring is full when every agent has them all. Restart agents that were already running.
                  </Step>
                  <p className="pl-8 text-xs text-muted-foreground">
                    On the same page: your own skills and MCP servers, ticked per agent — or right-click a machine →{" "}
                    <b>Skills &amp; MCP servers…</b>
                  </p>
                </ol>
                <div className="overflow-hidden rounded-xl border border-term-border shadow-[0_24px_60px_-20px_rgb(0_0_0/0.35)]">
                  <Image
                    src={skillsShot}
                    alt="Conch.app's Settings → Skills & MCP servers: conch's skills installed for every agent, with the install button and a row per skill"
                    sizes="(min-width: 1024px) 40rem, 100vw"
                    className="h-auto w-full"
                  />
                </div>
              </div>
            </TabsContent>
            <TabsContent value="cli">
              <TerminalWindow title="Terminal — every agent on this machine">
                <CommandLine command="conch agent skill" className="pb-0" />
                <CommandLine command="conch agent skill -apply" className="pb-0" />
                <CommandLine command="conch -m gpu-box agent skill -apply" />
              </TerminalWindow>
              <p className="px-2 pt-3 text-xs text-muted-foreground">
                The first says where it would write and writes nothing; -apply writes them; -m installs on another machine. In the
                TUI: , then Agents → conch&apos;s skills.
              </p>
            </TabsContent>
            <TabsContent value="plugin">
              <TerminalWindow title="Claude Code">
                {["/plugin marketplace add Amitgb14/conch", "/plugin install conch@conch"].map((c) => (
                  <div key={c} className="flex items-center gap-2 px-4 py-2 first:pt-3 last:pb-3">
                    <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap">
                      <span className="text-term-muted select-none">❯ </span>
                      {c}
                    </code>
                    <CopyButton value={c} />
                  </div>
                ))}
              </TerminalWindow>
              <p className="px-2 pt-3 text-xs text-muted-foreground">
                The same six skills, named /conch:conch-factory and so on. Use either this or conch&apos;s own install for Claude
                Code, not both. They still need conch: they act inside a conch pane.
              </p>
            </TabsContent>
          </Tabs>
        </div>
      </div>

      {/* 2. Use */}
      <div className="flex flex-col gap-4">
        <h3 className="text-lg font-semibold tracking-tight">2 · Ask an agent</h3>
        <ol className="grid gap-4 md:grid-cols-3">
          {use.map((u, i) => (
            <li key={u.title} className="flex flex-col gap-2 rounded-xl border bg-card p-5">
              <div className="flex items-center gap-2">
                <u.icon className="size-4 text-brand" aria-hidden />
                <span className="font-mono text-xs text-muted-foreground">{String(i + 1).padStart(2, "0")}</span>
              </div>
              <h4 className="font-semibold tracking-tight">{u.title}</h4>
              <p className="text-sm leading-6 text-muted-foreground">{u.body}</p>
            </li>
          ))}
        </ol>
      </div>

      {/* 3. The skills */}
      <div className="flex flex-col gap-4">
        <h3 className="text-lg font-semibold tracking-tight">3 · Pick a skill to see it work</h3>
        <div className="grid gap-6 lg:grid-cols-[16rem_1fr]">
          <ul className="flex gap-2 overflow-x-auto lg:flex-col lg:overflow-visible" role="tablist" aria-label="conch's skills">
            {skills.map((x) => (
              <li key={x.name} className="shrink-0">
                <button
                  type="button"
                  role="tab"
                  aria-selected={x.name === pick}
                  onClick={() => setPick(x.name)}
                  className={cn(
                    "flex w-full flex-col items-start gap-0.5 rounded-xl border px-4 py-3 text-left transition-colors",
                    x.name === pick ? "border-brand bg-card shadow-sm" : "border-transparent hover:bg-muted",
                  )}
                >
                  <code className="font-mono text-sm font-semibold">{x.name}</code>
                  <span className="text-xs text-muted-foreground">{x.tagline}</span>
                </button>
              </li>
            ))}
          </ul>

          <div className="flex min-w-0 flex-col gap-6 rounded-2xl border bg-card p-6" role="tabpanel">
            <div className="flex flex-col gap-2">
              <div className="flex flex-wrap items-center gap-2">
                <code className="font-mono text-lg font-semibold">{s.name}</code>
                <Badge variant="outline">{s.tagline}</Badge>
              </div>
              <p className="text-sm leading-6 text-muted-foreground">{s.body}</p>
            </div>

            <div className="flex flex-col gap-3">
              <p className="font-mono text-xs text-muted-foreground">say to an agent</p>
              {s.asks.map((a) => (
                <Prompt key={a} text={a} />
              ))}
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <span>or, in Claude Code, type</span>
                <code className="rounded-md border bg-muted px-2 py-0.5 font-mono text-[0.8rem] text-foreground">{s.slash}</code>
                <CopyButton value={s.slash} className="-my-1" />
              </div>
            </div>

            <div className="flex flex-col gap-3">
              <p className="font-mono text-xs text-muted-foreground">what happens</p>
              <ol className="grid gap-2 sm:grid-cols-2">
                {s.steps.map((st, i) => (
                  <Step key={st} n={i + 1}>
                    {st}
                  </Step>
                ))}
              </ol>
              {s.name === "conch-factory" && (
                <ol className="mt-2 flex flex-wrap items-center gap-2" aria-label="conch-factory's line">
                  {line.map((step, i) => (
                    <li key={step} className="flex items-center gap-2">
                      <span className="rounded-full border bg-muted/50 px-3 py-1 font-mono text-xs">{step}</span>
                      {i < line.length - 1 && <ArrowRightIcon className="size-3.5 text-muted-foreground" aria-hidden />}
                    </li>
                  ))}
                </ol>
              )}
            </div>
          </div>
        </div>
      </div>

      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <p className="max-w-2xl text-sm text-muted-foreground">
          Every helper is an agent in a pane of its own that you can watch, answer or close. Each one spends your plan&apos;s
          usage, and nothing merges unless you asked.
        </p>
        <Button variant="outline" className="w-fit" nativeButton={false} render={<Link href="/docs/agents-together" />}>
          Agents working together <ArrowRightIcon data-icon="inline-end" />
        </Button>
      </div>
    </div>
  )
}
