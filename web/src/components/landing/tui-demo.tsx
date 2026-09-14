"use client"

import { useState } from "react"

import { cn } from "@/lib/utils"

type Row = {
  id: string
  depth: number
  label: React.ReactNode
  meta?: React.ReactNode
  header?: boolean
  view?: ViewId
}

type ViewId = "health" | "flaky" | "branch" | "login" | "terminal" | "sessions" | "project" | "gpu"

const W = ({ children }: { children: React.ReactNode }) => <span className="text-working">{children}</span>
const Y = ({ children }: { children: React.ReactNode }) => <span className="text-waiting">{children}</span>
const G = ({ children }: { children: React.ReactNode }) => <span className="text-done">{children}</span>
const M = ({ children }: { children: React.ReactNode }) => <span className="text-term-muted">{children}</span>
const R = ({ children }: { children: React.ReactNode }) => <span className="text-[oklch(0.7_0.17_20)]">{children}</span>

const rows: Row[] = [
  { id: "h", depth: 0, header: true, label: <M>MACHINES</M> },
  { id: "local", depth: 0, label: <>▾ <G>●</G> local</>, meta: <><W>⠋1</W> <Y>⚑1</Y></>, view: "project" },
  { id: "api", depth: 1, label: <>▾ <W>◆</W> api</>, meta: <><W>⠋1</W> <Y>⚑1</Y></>, view: "project" },
  { id: "branches", depth: 2, label: "▾ Branches", meta: <M>7</M> },
  { id: "main", depth: 3, label: <><G>●</G> main</>, meta: <G>+1</G>, view: "branch" },
  { id: "b-health", depth: 3, label: <>◇ add-health-check</>, meta: <><W>⠋</W> <M>↑3</M></>, view: "health" },
  { id: "b-login", depth: 3, label: <><M>·</M> feat/login</>, meta: <><G>#12✓</G> <M>↑2</M></>, view: "login" },
  { id: "more", depth: 3, label: <M>… 18 more</M> },
  { id: "agents", depth: 2, label: "▾ Agents", meta: <M>2</M> },
  { id: "a-health", depth: 3, label: <><W>⠋</W> Add health check</>, view: "health" },
  { id: "a-flaky", depth: 3, label: <><Y>!</Y> Fix flaky tests</>, view: "flaky" },
  { id: "terms", depth: 2, label: "▾ Terminals", meta: <M>1</M> },
  { id: "t-dev", depth: 3, label: <><M>›</M> dev-server</>, view: "terminal" },
  { id: "sessions", depth: 2, label: "▸ Sessions", meta: <Y>⚠1</Y>, view: "sessions" },
  { id: "gpu", depth: 0, label: <>▸ <G>●</G> gpu-box</>, meta: <W>⠋2</W>, view: "gpu" },
]

const views: Record<ViewId, { title: React.ReactNode; body: React.ReactNode }> = {
  health: {
    title: <>Claude Code · add-health-check · <M>ctx 45k/200k · out 12k · $0.42</M></>,
    body: (
      <>
        <W>⏺</W> I&apos;ll add a <Y>/healthz</Y> endpoint that checks the database{"\n"}  connection and returns build info.{"\n\n"}
        <W>⏺</W> Update(internal/http/routes.go){"\n"}  ⎿  Added 14 lines{"\n"}     <G>+ r.Get(&quot;/healthz&quot;, s.health)</G>{"\n\n"}
        <W>⏺</W> Bash(go test ./internal/http/...){"\n"}  ⎿  <G>ok</G>   api/internal/http   0.412s{"\n\n"}
        <W>⠋</W> Writing the handler test…
      </>
    ),
  },
  flaky: {
    title: <>Claude Code · fix/flaky · <Y>waiting for you</Y></>,
    body: (
      <>
        <W>⏺</W> The login test races the session cleanup goroutine.{"\n"}  I want to run it 50 times to confirm.{"\n\n"}
        <span className="block rounded-md border border-term-border p-3">
          <Y>Bash command</Y>{"\n\n"}  go test -run TestLogin -count=50 ./auth/...{"\n\n"}Do you want to proceed?{"\n"}<W>❯ 1. Yes</W>{"\n"}  2. Yes, and don&apos;t ask again for go test{"\n"}  3. No, and tell Claude what to do differently
        </span>
        {"\n"}<M>conch notified you: “Fix flaky tests needs you” · ! jumps here</M>
      </>
    ),
  },
  branch: {
    title: <>main · <M>checked out in the repo</M></>,
    body: (
      <>
        <M>Uncommitted</M>{"\n"}  <Y>M</Y> README.md                          <G>+1</G>{"\n\n"}
        <M>Agents</M>{"\n"}  none on this branch{"\n\n"}
        <M>enter diff · y copy path · esc back</M>
      </>
    ),
  },
  login: {
    title: <>feat/login · <M>not checked out</M></>,
    body: (
      <>
        <M>Pull request</M>{"\n"}  #12 Add OAuth login          <G>✓ checks passing</G> · review requested{"\n\n"}
        <M>Changed since main</M>{"\n"}  <G>A</G> auth/oauth.go                    <G>+184</G>{"\n"}  <Y>M</Y> auth/session.go                   <G>+22</G> <R>−7</R>{"\n"}  <Y>M</Y> go.mod                             <G>+2</G>{"\n\n"}
        <M>Commits ahead</M>{"\n"}  a41c9e2 Add OAuth callback handler{"\n"}  7b02f1d Store the provider token in the session{"\n\n"}
        <M>o open PR · c agent in a new worktree</M>
      </>
    ),
  },
  terminal: {
    title: <>dev-server · <M>main</M></>,
    body: (
      <>
        <M>~/src/api</M> $ make dev{"\n"}go run ./cmd/api -addr :8080{"\n"}<G>listening</G> on :8080{"\n"}GET /healthz <G>200</G> 1.2ms{"\n"}GET /v1/users <G>200</G> 8.4ms{"\n"}POST /v1/login <Y>401</Y> 3.1ms{"\n"}
        <M>▌</M>
      </>
    ),
  },
  sessions: {
    title: <>Sessions · api <M>· enter resume · a agent · R reload</M></>,
    body: (
      <>
        <M>4 saved · all agents ·</M> <Y>⚠ 1 interrupted</Y> <M>(I resumes all)</M>{"\n\n"}
        <Y>⚠</Y> Claude Code  Fix the flaky login test   <M>fix-login · 2h</M>{"\n"}
        <G>●</G> Codex        Add rate limiting          <M>rate-limit · 5m</M>{"\n"}
        <M>·</M> OpenCode     Refactor config            <M>main · 1d</M>{"\n"}
        <M>·</M> Gemini CLI   Explain the auth flow      <M>main · 3d</M>
      </>
    ),
  },
  project: {
    title: <>api · <M>~/src/api · base main</M></>,
    body: (
      <>
        <M>Agents</M>{"\n"}  <W>⠋</W> Add health check   <M>“adding /healthz, writing tests”</M>{"\n"}  <Y>!</Y> Fix flaky tests    <M>“needs approval to run go test 50×”</M>{"\n\n"}
        <M>Worktrees</M>{"\n"}  ~/src/api.worktrees/add-health-check{"\n"}  ~/src/api.worktrees/fix-flaky{"\n\n"}
        <M>Usage</M>  ctx 81k · out 19k · $0.77{"\n\n"}
        <M>t task · c agent · n terminal · F local files · i agent setup</M>
      </>
    ),
  },
  gpu: {
    title: <>gpu-box · <M>ssh ubuntu@10.0.4.12 · linux/amd64</M></>,
    body: (
      <>
        <G>●</G> online · conch server up 3d{"\n\n"}
        <M>Agents installed</M>{"\n"}  Claude Code   2.1.4{"\n"}  Codex         0.46.0{"\n"}  Gemini CLI    <M>not installed · enter installs</M>{"\n\n"}
        <M>Plan limits</M>{"\n"}  Claude 5h  ████░░░░░░ 42%{"\n"}  Claude 7d  ██░░░░░░░░ 18%{"\n\n"}
        <M>Your laptop can sleep — agents here keep running.</M>
      </>
    ),
  },
}

export function TuiDemo() {
  const [selected, setSelected] = useState("a-health")
  const row = rows.find((r) => r.id === selected)
  const view = views[row?.view ?? "health"]
  const selectable = rows.filter((r) => r.view)

  function move(delta: number) {
    const i = selectable.findIndex((r) => r.id === selected)
    const next = selectable[Math.min(selectable.length - 1, Math.max(0, i + delta))]
    setSelected(next.id)
  }

  return (
    <div className="overflow-hidden rounded-xl border border-term-border bg-term text-term-foreground shadow-[0_40px_100px_-30px_rgb(0_0_0/0.45)]">
      <div className="flex items-center gap-2 border-b border-term-border px-3.5 py-2.5">
        <span className="flex gap-1.5">
          <i className="size-2.5 rounded-full bg-white/15" />
          <i className="size-2.5 rounded-full bg-white/15" />
          <i className="size-2.5 rounded-full bg-white/15" />
        </span>
        <span className="ml-2 font-mono text-[0.7rem] text-term-muted">conch — click the tree, or use ↑↓</span>
      </div>
      <div className="grid font-mono text-[0.78rem] leading-[1.65] md:grid-cols-[290px_minmax(0,1fr)]">
        <div
          role="listbox"
          tabIndex={0}
          aria-label="conch tree"
          onKeyDown={(e) => {
            const delta = { ArrowDown: 1, j: 1, ArrowUp: -1, k: -1 }[e.key]
            if (delta) {
              e.preventDefault()
              move(delta)
            }
          }}
          className="border-term-border py-2.5 outline-none focus-visible:ring-1 focus-visible:ring-brand/60 max-md:border-b md:border-r"
        >
          {rows.map((r) => (
            <div
              key={r.id}
              role={r.view ? "option" : undefined}
              aria-selected={r.view ? selected === r.id : undefined}
              onClick={() => r.view && setSelected(r.id)}
              className={cn(
                "flex justify-between gap-3 pr-3 whitespace-pre",
                r.view && "cursor-pointer hover:bg-white/5",
                selected === r.id && "bg-[#0f7b8a] text-white hover:bg-[#0f7b8a] [&_span]:text-white",
                r.header && "mb-0.5 font-semibold",
              )}
              style={{ paddingLeft: `${0.75 + r.depth * 0.9}rem` }}
            >
              <span className="truncate">{r.label}</span>
              {r.meta && <span className="shrink-0">{r.meta}</span>}
            </div>
          ))}
        </div>
        <div className="flex min-h-[340px] min-w-0 flex-col">
          <div className="truncate border-b border-term-border px-4 py-2 text-[0.72rem]">{view.title}</div>
          <div key={selected} className="flex-1 overflow-x-auto px-4 py-3 whitespace-pre animate-in fade-in duration-300">
            {view.body}
          </div>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 border-t border-term-border px-3.5 py-1.5 font-mono text-[0.72rem] text-term-muted">
        <span><W>✦</W> Ask</span>
        <button type="button" onClick={() => setSelected("a-flaky")} className="text-waiting hover:underline">
          ⚑ 1 waiting
        </button>
        <span>t task</span>
        <span>c agent</span>
        <span className="max-sm:hidden">Claude 5h 42% · 7d 18%</span>
        <span className="ml-auto">? keys</span>
      </div>
    </div>
  )
}
