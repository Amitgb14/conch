"use client"

import Link from "next/link"
import { useState } from "react"

import { Badge } from "@/components/ui/badge"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

type Group = "agents" | "git" | "remote" | "workflow"

const groups: { value: Group | "all"; label: string }[] = [
  { value: "all", label: "Everything" },
  { value: "agents", label: "Agents" },
  { value: "git", label: "Git" },
  { value: "remote", label: "Remote" },
  { value: "workflow", label: "Workflow" },
]

const features: { group: Group; title: string; body: string; example: string; href: string }[] = [
  {
    group: "agents",
    title: "Agents outlive the UI",
    body: "A background server owns every pane. Detach, close the laptop lid, come back — the agents kept going.",
    example: "q  detach · conch  reattach",
    href: "/docs",
  },
  {
    group: "agents",
    title: "Know who needs you",
    body: "Working, waiting, done or idle for every agent — from hooks, screen rules and the foreground process, most reliable first.",
    example: "!  next agent waiting",
    href: "/docs/agents#agent-states",
  },
  {
    group: "agents",
    title: "Notifications, quiet hours, snooze",
    body: "Desktop notification, sound or bell when an agent waits or finishes. Silence them overnight or for an hour.",
    example: ",  settings → notifications",
    href: "/docs/interface#settings",
  },
  {
    group: "agents",
    title: "Token usage and plan limits",
    body: "Context, output and cost in each pane's title. Claude's and Codex's 5-hour and weekly windows in the status bar, with an alert once they pass 80% and 95%.",
    example: "Claude 5h 82% · 7d 31%",
    href: "/docs/agents#plan-limits",
  },
  {
    group: "agents",
    title: "Sessions: resume, search, share",
    body: "Every saved conversation for a project across all four agents. Search inside them, resume one, or hand one to a different agent to continue.",
    example: "/  search · s  share · I  resume all",
    href: "/docs/sessions",
  },
  {
    group: "workflow",
    title: "One worktree per task",
    body: "Describe the task: conch names a branch, checks it out into its own worktree and starts the agent with your prompt.",
    example: 'conch task "Fix the flaky tests"',
    href: "/docs/tasks",
  },
  {
    group: "workflow",
    title: "Your .env comes along",
    body: "New worktrees get the main checkout's gitignored local files — .env, .envrc, local agent settings — never untracked ones.",
    example: "F  edit the patterns",
    href: "/docs/tasks#local-files-in-worktrees",
  },
  {
    group: "workflow",
    title: "Ask in plain words",
    body: "“Start 3 agents on api: …” becomes a checked plan of tasks and messages. Nothing runs until you press enter.",
    example: ":  ask · conch ask \"…\"",
    href: "/docs/brain",
  },
  {
    group: "workflow",
    title: "Broadcast to a group",
    body: "One message to every agent in a project, branch or machine — or one command to its terminals. Waiting agents are left out unless you tick them.",
    example: "B  broadcast",
    href: "/docs/agents#broadcast",
  },
  {
    group: "workflow",
    title: "tmux keys, splits and tabs",
    body: "tmux's splits, tabs, layouts, split numbers and synchronized typing. Tabs follow the tree selection; 10,000 lines of scrollback with copy mode and OSC 52.",
    example: "ctrl+b % · ctrl+b space · ctrl+b q",
    href: "/docs/interface",
  },
  {
    group: "workflow",
    title: "Scriptable",
    body: "Everything in the TUI is a command, locally or on any machine. The server speaks newline-delimited JSON over a unix socket.",
    example: "conch send p1 \"…\" · conch read p1",
    href: "/docs/cli",
  },
  {
    group: "git",
    title: "Branches at a glance",
    body: "Uncommitted lines, conflicts, commits ahead and behind the base, and which agents work on each branch.",
    example: "+42 −7  ↑2 ↓1",
    href: "/docs/interface#the-sidebar",
  },
  {
    group: "git",
    title: "Pull requests and checks",
    body: "PR state and checks from your gh login, refreshed every minute while agents work. A changes view with diffs for every branch.",
    example: "#12✓  o  open PR",
    href: "/docs/interface#the-sidebar",
  },
  {
    group: "git",
    title: "See what an agent loads",
    body: "Instructions, skills, MCP servers and plugins each agent picks up in a folder — and what a worktree is missing from the main checkout.",
    example: "i  agent setup",
    href: "/docs/tasks#agent-setup",
  },
  {
    group: "remote",
    title: "Remote machines over ssh",
    body: "Add any host from your ssh config. conch installs itself there and its server keeps agents running when your network drops.",
    example: "conch machine add gpu-box",
    href: "/docs/remote-machines",
  },
  {
    group: "remote",
    title: "Update without stopping agents",
    body: "The server execs a new build in place: panes, scrollback and agent states survive, and every connected machine updates with one key.",
    example: "conch server reload",
    href: "/docs/installation",
  },
]

const groupLabel: Record<Group, string> = { agents: "agents", git: "git", remote: "remote", workflow: "workflow" }

export function Features() {
  const [filter, setFilter] = useState<Group | "all">("all")
  const shown = features.filter((f) => filter === "all" || f.group === filter)

  return (
    <div className="flex flex-col gap-8">
      <ToggleGroup
        value={[filter]}
        onValueChange={(v) => v.length && setFilter(v[v.length - 1] as Group | "all")}
        variant="outline"
        className="flex-wrap"
      >
        {groups.map((g) => (
          <ToggleGroupItem key={g.value} value={g.value} className="gap-2 px-3">
            {g.label}
            <span className="font-mono text-[0.7rem] text-muted-foreground">
              {g.value === "all" ? features.length : features.filter((f) => f.group === g.value).length}
            </span>
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {shown.map((f) => (
          <Link
            key={f.title}
            href={f.href}
            className="group flex flex-col gap-3 rounded-xl border bg-card p-5 transition-colors hover:border-foreground/20 hover:bg-muted/30"
          >
            <Badge variant="secondary" className="font-mono text-[0.65rem]">
              {groupLabel[f.group]}
            </Badge>
            <h3 className="font-semibold tracking-tight">{f.title}</h3>
            <p className="flex-1 text-sm leading-6 text-muted-foreground">{f.body}</p>
            <code className="truncate rounded-md border bg-muted/60 px-2 py-1 font-mono text-[0.72rem]">{f.example}</code>
          </Link>
        ))}
      </div>
    </div>
  )
}
