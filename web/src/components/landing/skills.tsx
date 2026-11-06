import { ArrowRightIcon } from "lucide-react"
import Link from "next/link"

import { CommandLine, TerminalWindow } from "@/components/terminal"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"

// conch's skills, as internal/agentsetup/skills ships them: what each one
// teaches an agent, and what you would say to use it.
const skills: { name: string; tagline: string; body: string; asks: string[] }[] = [
  {
    name: "conch",
    tagline: "Hand work to a helper",
    body: "Start another agent on a branch of its own, prompt it, wait for it and read its answer — the one the others build on.",
    asks: [
      "Get a second opinion on this branch from another agent.",
      "Have Codex write tests for the parser and run them.",
    ],
  },
  {
    name: "conch-team",
    tagline: "Several agents at once",
    body: "One task tried by two or three agents to keep the best, a task split into parts that run side by side, or a chain where each starts from the last one's branch.",
    asks: [
      "Have Claude and Codex each try the parser fix, and tell me which to keep.",
      "Split the export feature: one agent on the API, one on the UI.",
    ],
  },
  {
    name: "conch-review",
    tagline: "Review until clean",
    body: "Another agent reviews the branch against what was asked, comments on its lines, the author fixes or answers each one, and the reviewer looks again.",
    asks: [
      "Get this branch reviewed by Codex and fixed until the review is clean.",
      "Review the login change against issue 42 and send the findings to its author.",
    ],
  },
  {
    name: "conch-ship",
    tagline: "Tests to a merged PR",
    body: "Runs the project's tests, pushes, opens the pull request, watches CI and sends a failure back to the branch's agent — and merges once it is green, only when you asked.",
    asks: [
      "Run the tests, open the PR and merge it once CI is green.",
      "Get CI green on this branch.",
    ],
  },
  {
    name: "conch-factory",
    tagline: "A ticket to a merged PR",
    body: "Agrees a spec with you, then one agent builds it test-first, another reviews against the spec, the builder fixes, a tester tries it, and the pull request goes through CI — with a retro at the end.",
    asks: [
      "Take issue 123 to a merged pull request.",
      "Work through the issues labelled ready, two at a time.",
    ],
  },
  {
    name: "conch-autopilot",
    tagline: "Automate the workflow",
    body: "Finds the project's check and runs it, proposes the policy — a reviewer, failures sent back, pull requests opened, merging off until you trust it — and guardrails, and applies only what you agree to.",
    asks: [
      "Set up an automated workflow for this project.",
      "Every morning, have an agent update dependencies and fix what breaks.",
    ],
  },
]

const line = ["ticket", "spec", "build", "review", "fix", "test", "pull request", "CI", "merge", "retro"]

export function Skills() {
  return (
    <div className="flex flex-col gap-10">
      <div className="grid gap-6 md:grid-cols-2">
        <TerminalWindow title="With conch — every agent">
          <CommandLine command="conch agent skill -apply" />
          <p className="px-4 pb-3 font-mono text-xs text-term-muted">
            # Claude Code, Codex, Gemini CLI, OpenCode, Devin · or Settings → Skills &amp; MCP servers
          </p>
        </TerminalWindow>
        <TerminalWindow title="Claude Code — as a plugin">
          <div className="px-4 py-3">
            <code className="block overflow-x-auto whitespace-nowrap">/plugin marketplace add Amitgb14/conch</code>
            <code className="mt-1 block overflow-x-auto whitespace-nowrap">/plugin install conch@conch</code>
          </div>
        </TerminalWindow>
      </div>

      <div className="flex flex-col gap-3">
        <p className="font-mono text-xs text-muted-foreground">conch-factory&apos;s line</p>
        <ol className="flex flex-wrap items-center gap-2 text-sm">
          {line.map((step, i) => (
            <li key={step} className="flex items-center gap-2">
              <span className="rounded-full border bg-card px-3 py-1 font-mono text-xs">{step}</span>
              {i < line.length - 1 && <ArrowRightIcon className="size-3.5 text-muted-foreground" aria-hidden />}
            </li>
          ))}
        </ol>
      </div>

      <ul className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
        {skills.map((s) => (
          <li key={s.name} className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-5">
            <div className="flex flex-wrap items-center gap-2">
              <code className="font-mono text-sm font-semibold">{s.name}</code>
              <Badge variant="outline" className="text-[0.7rem]">{s.tagline}</Badge>
            </div>
            <p className="text-sm leading-6 text-muted-foreground">{s.body}</p>
            <div className="mt-auto flex flex-col gap-2">
              {s.asks.map((a) => (
                <p key={a} className="rounded-lg border-l-2 border-brand bg-muted/50 px-3 py-2 text-sm">
                  &ldquo;{a}&rdquo;
                </p>
              ))}
              <p className="font-mono text-xs text-muted-foreground">/{s.name}</p>
            </div>
          </li>
        ))}
      </ul>

      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <p className="max-w-2xl text-sm text-muted-foreground">
          Ask in plain words, from an agent in a conch pane. Every helper is an agent in a pane of its own that you can watch,
          answer or close; a question it asks is yours to answer, and nothing merges unless you asked.
        </p>
        <Button variant="outline" className="w-fit" nativeButton={false} render={<Link href="/docs/agents-together" />}>
          Agents working together <ArrowRightIcon data-icon="inline-end" />
        </Button>
      </div>
    </div>
  )
}
