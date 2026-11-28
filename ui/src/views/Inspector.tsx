import { post } from "../bridge"
import { percent } from "../lib/lib.mjs"
import type { InspectorState } from "../types"
import { Dot } from "./parts"

// The column at the window's right edge: the focused split's branch at
// a glance. Everything opens elsewhere — a file in the branch's Changes
// tab, a check in the browser.
const say = (type: string, f?: Record<string, unknown>) => post("inspector", type, f)

export function Inspector({ s }: { s: InspectorState }) {
  if (s.empty) return <p className="empty">{s.empty}</p>
  const n = s.files.length + s.moreFiles
  return (
    <div className="column">
      <header className="ins-head">
        <div className="branch">{s.branch}</div>
        {s.pr && (
          <button className="pill" disabled={!s.pr.url} onClick={() => say("openURL", { url: s.pr!.url })}>#{s.pr.number}</button>
        )}
        <div className="sub">
          {s.project}
          {s.base && <> · into {s.base}</>}
          {s.ahead ? <> · ↑{s.ahead}</> : null}
        </div>
      </header>
      {s.error && <p className="error">{s.error}</p>}

      <section className="card">
        <div className="card-head">
          <span>Changes</span>
          {!s.loading && !s.error && (
            <span><span className="ok">+{s.added}</span> <span className="err">−{s.deleted}</span> · {n} {n === 1 ? "file" : "files"}</span>
          )}
        </div>
        {s.loading && <p className="muted">Reading the branch…</p>}
        {!s.loading && !s.error && s.files.length === 0 && <p className="muted">Nothing uncommitted.</p>}
        {s.files.map((f) => (
          <button key={f.path} className="row" title={f.path} onClick={() => say("openFile", { path: f.path })}>
            <span className="code">{f.code}</span>
            <span className="path">{f.path}</span>
            <span className="ok">+{f.added}</span>
            {f.deleted > 0 && <span className="err">−{f.deleted}</span>}
          </button>
        ))}
        {s.moreFiles > 0 && <p className="muted">and {s.moreFiles} more</p>}
        <button className="primary" onClick={() => say("openChanges")}>Open changes</button>
      </section>

      {s.notes.length > 0 && (
        <section className="card">
          <div className="card-head"><span>Notes</span><span>{s.notes.length + s.moreNotes}</span></div>
          {s.notes.map((c) => (
            <div key={c.id || c.file + c.line} className="note">
              <div className="who">
                <strong>{c.by || (c.github ? "GitHub" : "you")}</strong> · {c.file}{c.line ? `:${c.line}` : ""}{c.sent ? " · sent" : ""}
              </div>
              <p>{c.note}</p>
              {c.replies > 0 && <div className="muted">{c.replies} {c.replies === 1 ? "reply" : "replies"}</div>}
              {c.canResolve && <button className="link" onClick={() => say("resolve", { id: c.id })}>Resolve</button>}
            </div>
          ))}
          {s.moreNotes > 0 && <p className="muted">and {s.moreNotes} more</p>}
        </section>
      )}

      {s.pr && (
        <section className="card">
          <div className="card-head"><span>Checks</span>{s.pr.total ? <span>{s.pr.passed ?? 0} of {s.pr.total}</span> : null}</div>
          {s.pr.title && <p className="title">{s.pr.title}</p>}
          {s.pr.total ? <div className="bar"><div style={{ width: `${percent(s.pr.passed, s.pr.total)}%` }} /></div> : null}
          {s.pr.runs.length === 0 && <p className="muted">{s.pr.checks || "No checks reported"}</p>}
          {s.pr.runs.map((r, i) => (
            <button key={r.name + i} className="row" disabled={!r.url} onClick={() => say("openURL", { url: r.url })}>
              <Dot state={r.state} />
              <span className="path">{r.name}</span>
              <span className="muted">{r.state}</span>
            </button>
          ))}
          {s.pr.moreRuns > 0 && <p className="muted">and {s.pr.moreRuns} more</p>}
        </section>
      )}
    </div>
  )
}
