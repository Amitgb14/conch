import { useState } from "react"
import { post } from "../bridge"
import { agentCounts, filterAgents } from "../lib/lib.mjs"
import type { AgentCard, AgentsState } from "../types"

// Every agent on every machine as a card: what it is doing, where, and —
// for one waiting — its question with the answers as buttons. Plan
// limits and what the agents last did on the right.
const say = (type: string, f?: Record<string, unknown>) => post("agents", type, f)

const filters = [
  { id: "all", label: "All" },
  { id: "waiting", label: "Needs you" },
  { id: "working", label: "Working" },
  { id: "done", label: "Done" },
] as const

const word = (a: AgentCard) => !a.running ? "exited" : a.state === "blocked" ? "waiting" : a.state || "idle"

export function Agents({ s }: { s: AgentsState }) {
  const [filter, setFilter] = useState<string>("all")
  const counts = agentCounts(s.agents) as Record<string, number>
  const shown: AgentCard[] = filterAgents(s.agents, filter)
  return (
    <div className="agents">
      <main className="agents-main">
        <div className="agents-bar">
          <strong>Agents</strong>
          <span className="muted">{counts.all} {counts.all === 1 ? "agent" : "agents"}</span>
          <span className="grow" />
          <div className="seg" role="tablist" aria-label="Show">
            {filters.map((f) => (
              <button key={f.id} role="tab" aria-selected={filter === f.id} className={filter === f.id ? "on" : ""} onClick={() => setFilter(f.id)}>
                {f.label}{f.id !== "all" && counts[f.id] ? ` · ${counts[f.id]}` : ""}
              </button>
            ))}
          </div>
          <button className="primary small" onClick={() => say("newTask")}>New Task</button>
        </div>
        {shown.length === 0 && <p className="empty">{s.agents.length === 0 ? "No agents yet. New Task starts one on a branch of its own." : "None here."}</p>}
        <div className="grid">
          {shown.map((a) => (
            <article key={a.key} className={`agent-card ${a.state === "blocked" && a.running ? "waiting" : ""} ${a.focused ? "focused" : ""}`}
              onDoubleClick={() => say("open", { key: a.key })}>
              <div className="agent-top">
                <span className="mark" style={{ background: a.color }} aria-hidden="true" />
                <strong className="agent-title" title={a.title}>{a.title}</strong>
                <span className={`state s-${a.running ? a.state || "idle" : "exited"}`}>{word(a)}</span>
              </div>
              <div className="agent-body">
                {a.question ? (
                  <>
                    <p className="q">{a.question.text || a.question.kind}</p>
                    {a.answering ? <p className="muted small">Sending your answer…</p> : (
                      <div className="choices">
                        {a.question.choices.map((c) => (
                          <button key={c.index} className={c.default ? "primary small" : ""} onClick={() => say("answer", { key: a.key, choice: c.index })}>{c.label}</button>
                        ))}
                      </div>
                    )}
                  </>
                ) : (
                  <p className="muted">{a.agent ? `${a.agent}${a.ago ? ` · ${word(a)} ${a.ago}` : ""}` : "terminal"}</p>
                )}
              </div>
              <div className="agent-foot">
                <span className="muted small place" title={a.place}>{a.place}</span>
                <span className="grow" />
                <button className="link" onClick={() => say("open", { key: a.key })}>Open</button>
              </div>
            </article>
          ))}
        </div>
      </main>
      <aside className="agents-side">
        <section>
          <div className="side-head">Plan limits</div>
          {s.limits.length === 0 && <p className="muted small">No agent has reported its plan yet.</p>}
          {s.limits.map((l) => (
            <div key={l.label} className="limit">
              <div className="limit-text"><span>{l.label}</span><span>{Math.round(l.pct)}%</span></div>
              <div className="bar"><div style={{ width: `${Math.max(0, Math.min(100, l.pct))}%`, background: l.color }} /></div>
            </div>
          ))}
        </section>
        <section>
          <div className="side-head">Activity</div>
          {s.activity.length === 0 && <p className="muted small">Nothing yet.</p>}
          {s.activity.map((e, i) => (
            <button key={e.key + i} className="activity" onClick={() => say("open", { key: e.key })}>
              <span className="muted small when">{e.ago}</span>
              <span className="mark small" style={{ background: e.color }} aria-hidden="true" />
              <span><strong>{e.agent}</strong> {e.text}</span>
            </button>
          ))}
        </section>
      </aside>
    </div>
  )
}
