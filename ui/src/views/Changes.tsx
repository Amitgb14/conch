import { Fragment, useState } from "react"
import { post } from "../bridge"
import { language, percent, splitRows } from "../lib/lib.mjs"
import type { ChangedFile, ChangesState, Comment, DiffLine, Tick } from "../types"
import { Code, Dot, Field } from "./parts"

// A branch's Changes: its files down the left, the selected file's diff
// in the middle with comments on its lines, and the commit, the review
// and the checks on the right. What it does is the native model's — the
// page draws the model and says what was clicked.
const say = (type: string, f?: Record<string, unknown>) => post("changes", type, f)

export function Changes({ s }: { s: ChangesState }) {
  return (
    <div className="changes">
      <Files s={s} />
      <Diff s={s} />
      <Side s={s} />
    </div>
  )
}

function TickBox({ tick, label, onClick }: { tick: Tick; label: string; onClick: (e: React.MouseEvent) => void }) {
  return (
    <button className={`tick ${tick}`} role="checkbox" aria-checked={tick === "on" ? true : tick === "mixed" ? "mixed" : false}
      aria-label={label} title={label} onClick={(e) => { e.stopPropagation(); onClick(e) }}>
      {tick === "on" ? "✓" : tick === "mixed" ? "–" : ""}
    </button>
  )
}

// --- the files

function Files({ s }: { s: ChangesState }) {
  const [menu, setMenu] = useState<string | null>(null)
  const row = (f: ChangedFile, committed: boolean) => {
    const on = s.selected?.path === f.path && s.selected.committed === committed
    const slash = f.path.lastIndexOf("/")
    return (
      <div key={(committed ? "c:" : "u:") + f.path} className={on ? "file on" : "file"}
        onContextMenu={(e) => { e.preventDefault(); setMenu(menu === f.path ? null : f.path) }}>
        {!committed && f.tick && (
          <TickBox tick={f.tick} label={`Tick ${f.path} to stage or commit it`} onClick={(e) => say("tickFile", { path: f.path, shift: e.shiftKey })} />
        )}
        <button className="file-main" title={f.path} onClick={() => say("select", { path: f.path, committed })}>
          <span className={`code c-${f.code}`}>{f.code}</span>
          <span className="name">{slash < 0 ? f.path : f.path.slice(slash + 1)}</span>
          <span className="dir">{slash < 0 ? "" : f.path.slice(0, slash)}</span>
        </button>
        {f.notes > 0 && <span className="badge" title={`${f.notes} comments`}>{f.notes}</span>}
        {f.live && <span className="live" title="Being written right now" />}
        {!committed && f.stage !== "off" && <span className="ok" title={f.stage === "on" ? "Staged" : "Part staged"}>{f.stage === "on" ? "●" : "◐"}</span>}
        {f.binary ? <span className="muted small">binary</span> : <><span className="ok small">+{f.added}</span><span className="err small">−{f.deleted}</span></>}
        {menu === f.path && (
          <div className="menu" onMouseLeave={() => setMenu(null)}>
            {!committed && s.worktree && s.commit.canStage && f.stage !== "on" && <button onClick={() => { setMenu(null); say("stageFile", { path: f.path, unstage: false }) }}>Stage</button>}
            {!committed && s.worktree && s.commit.canStage && f.stage !== "off" && <button onClick={() => { setMenu(null); say("stageFile", { path: f.path, unstage: true }) }}>Unstage</button>}
            <button onClick={() => { setMenu(null); say("copyPath", { path: f.path }) }}>Copy Path</button>
          </div>
        )}
      </div>
    )
  }
  return (
    <aside className="files" aria-label="Files">
      {s.loading && <p className="muted pad">Reading…</p>}
      {!s.loading && s.worktree && (
        <>
          <div className="heading">
            {s.allTick && s.files.length > 0 && <TickBox tick={s.allTick} label="Tick every uncommitted file" onClick={() => say("tickAll")} />}
            <span>Uncommitted · {s.files.length}</span>
            {s.allTick && s.allTick !== "off" && <span className="chip">{s.commitWhat}</span>}
          </div>
          {s.files.length === 0 && <p className="muted pad">Nothing uncommitted</p>}
          {s.files.map((f) => row(f, false))}
          {s.committed.length > 0 && <div className="heading">Committed on {s.branch} · {s.committed.length}</div>}
          {s.committed.map((f) => row(f, true))}
        </>
      )}
      {!s.loading && !s.worktree && (
        <>
          <div className="heading">Changes against {s.base}</div>
          {s.files.length === 0 && <p className="muted pad">Nothing changed against {s.base}</p>}
          {s.files.map((f) => row(f, true))}
        </>
      )}
      {s.commits.length > 0 && <div className="heading">Commits · {s.commits.length}</div>}
      {s.commits.slice(0, 20).map((c) => (
        <div key={c.hash} className="commit"><span className="work mono">{c.hash.slice(0, 7)}</span><span className="subject">{c.subject}</span></div>
      ))}
    </aside>
  )
}

// --- the diff

function Diff({ s }: { s: ChangesState }) {
  const lang = language(s.selected?.path)
  const blocks = s.hunks.length > 0 ? s.hunks : s.selected ? [{ index: -1, header: "", ticked: false, lines: s.diff.filter((l) => l.kind !== "meta") }] : []
  return (
    <main className="diff" aria-label="Diff">
      {s.selected && (
        <div className="diff-head">
          <span className="mono path-head" title={s.selected.path}>{s.selected.path}</span>
          {s.selected.live && <span className="chip agent">being written · follows live</span>}
          <span className="grow" />
          <div className="seg" role="tablist">
            <button role="tab" aria-selected={!s.split} className={s.split ? "" : "on"} onClick={() => say("split", { on: false })}>Unified</button>
            <button role="tab" aria-selected={s.split} className={s.split ? "on" : ""} onClick={() => say("split", { on: true })}>Split</button>
          </div>
        </div>
      )}
      <div className="diff-body">
        {!s.selected && <p className="muted pad">{s.loading ? "" : "Nothing to show."}</p>}
        {blocks.map((b) => (
          <div key={b.index} className={b.ticked ? "hunk ticked" : "hunk"}>
            {b.index >= 0 && (
              <div className="hunk-head">
                <TickBox tick={b.ticked ? "on" : "off"} label="Commit this hunk" onClick={() => say("tickHunk", { index: b.index })} />
                <span className="mono muted">{b.header}</span>
              </div>
            )}
            {s.split ? <SplitLines s={s} lines={b.lines} lang={lang} /> : <UnifiedLines s={s} lines={b.lines} lang={lang} />}
          </div>
        ))}
      </div>
    </main>
  )
}

function UnifiedLines({ s, lines, lang }: { s: ChangesState; lines: DiffLine[]; lang: string }) {
  const shown = new Set<number>()
  return (
    <>
      {lines.map((l, i) => {
        const first = l.cl !== undefined && !shown.has(l.cl)
        if (first) shown.add(l.cl!)
        const marked = l.cl !== undefined && (s.comments.some((c) => c.line === l.cl) || s.pending.some((p) => p.line === l.cl))
        return (
          <Fragment key={i}>
            <div className={`line ${l.kind}`}>
              <span className="gutter">
                {l.cl !== undefined && <button className="add" aria-label={`Comment on line ${l.cl}`} onClick={() => say("compose", { line: l.cl })}>+</button>}
              </span>
              <span className={marked ? "mark on" : "mark"} />
              <span className="num">{l.old ?? ""}</span>
              <span className="num">{l.new ?? ""}</span>
              <span className="sign">{l.kind === "added" ? "+" : l.kind === "removed" ? "−" : " "}</span>
              <span className="text"><Code text={l.text} lang={lang} /></span>
            </div>
            {first && <Threads s={s} line={l.cl!} />}
          </Fragment>
        )
      })}
    </>
  )
}

function SplitLines({ s, lines, lang }: { s: ChangesState; lines: DiffLine[]; lang: string }) {
  const shown = new Set<number>()
  return (
    <>
      {splitRows(lines).map((r: { left: DiffLine | null; right: DiffLine | null }, i: number) => {
        const at = (r.right ?? r.left)?.cl
        const first = at !== undefined && !shown.has(at)
        if (first) shown.add(at!)
        const half = (l: DiffLine | null, side: "old" | "new") => (
          <div className={`half ${l ? l.kind : "empty"}`}>
            <span className="num">{l?.[side] ?? ""}</span>
            <span className="text">{l && <Code text={l.text} lang={lang} />}</span>
          </div>
        )
        return (
          <Fragment key={i}>
            <div className="split-row">{half(r.left, "old")}{half(r.right, "new")}</div>
            {first && <Threads s={s} line={at!} />}
          </Fragment>
        )
      })}
    </>
  )
}

function Threads({ s, line }: { s: ChangesState; line: number }) {
  const here = s.comments.filter((c) => c.line === line)
  const pending = s.pending.filter((p) => p.line === line)
  const sent = s.sent.filter((m) => m.line === line)
  if (!here.length && !pending.length && !sent.length && s.composing !== line) return null
  return (
    <div className="threads">
      {here.map((c) => <Thread key={c.id} s={s} c={c} />)}
      {sent.map((m) => (
        <div key={m.id} className="thread sent">
          <div className="entry"><div className="by"><strong>you</strong><span className="chip ok">sent to {m.to}</span><span className="muted">{m.ago}</span></div><p>{m.note}</p></div>
          <div className="actions"><button onClick={() => say("openPane", { pane: m.pane })}>Open {m.to}</button><span className="grow" /><button onClick={() => say("dismissSent", { id: m.id })}>Dismiss</button></div>
        </div>
      ))}
      {pending.map((p) => (
        <div key={p.id} className="thread pending">
          <div className="entry"><div className="by"><strong>you</strong><span className="chip warn">pending · in your review</span></div><p>{p.note}</p></div>
          <div className="actions"><span className="grow" /><button onClick={() => say("dropPending", { id: p.id })}>Remove</button></div>
        </div>
      ))}
      {s.composing === line && <Composer s={s} line={line} />}
    </div>
  )
}

function Thread({ s, c }: { s: ChangesState; c: Comment }) {
  const [replying, setReplying] = useState(false)
  const [answer, setAnswer] = useState("")
  return (
    <div className={c.chip?.tone === "ok" ? "thread replied" : "thread"}>
      <div className="entry">
        <div className="by"><strong>{c.by}</strong>{c.chip && <span className={`chip ${c.chip.tone}`}>{c.chip.text}</span>}{c.ago && <span className="muted">{c.ago}</span>}</div>
        <p>{c.note}</p>
      </div>
      {c.replies.map((r, i) => (
        <div key={i} className={r.agent ? "entry reply agent" : "entry reply"}>
          <div className="by"><strong>{r.by}</strong>{r.ago && <span className="muted">{r.ago}</span>}</div>
          <p>{r.note}</p>
        </div>
      ))}
      {c.noAnswer && <p className="muted pad">The agent finished without answering this one here — its session has what it said.</p>}
      {replying && (
        <div className="compose-inline">
          <Field label="Answer back" value={answer} onChange={setAnswer} rows={2} enterSends
            placeholder="Answer back — it waits to be sent again with the thread"
            onSubmit={(t) => { say("reply", { id: c.id, note: t }); setAnswer(""); setReplying(false) }} />
          <button className="primary small" disabled={!answer.trim()} onClick={() => { say("reply", { id: c.id, note: answer }); setAnswer(""); setReplying(false) }}>Reply</button>
          <button onClick={() => { setReplying(false); setAnswer("") }}>Cancel</button>
        </div>
      )}
      <div className="actions">
        {c.github && c.url && <button onClick={() => say("openURL", { url: c.url })}>Reply on GitHub</button>}
        {c.github && c.outdated && <span className="muted">outdated</span>}
        {c.waiting && <SendMenu s={s} title="Send to an agent" />}
        {c.sentPane && c.sentTo && <button onClick={() => say("openPane", { pane: c.sentPane })}>Open {c.sentTo}</button>}
        {c.canReply && !replying && <button onClick={() => setReplying(true)}>Reply</button>}
        <span className="grow" />
        {c.canResolve && <button onClick={() => say("resolve", { id: c.id })}>Resolve</button>}
      </div>
    </div>
  )
}

function Composer({ s, line }: { s: ChangesState; line: number }) {
  const [text, setText] = useState("")
  const empty = !text.trim()
  const add = (pr: boolean, note = text) => { if (note.trim()) say("addComment", { line, note, pr }) }
  return (
    <div className="thread composer">
      <Field label={`Comment on line ${line}`} value={text} onChange={setText} rows={3} enterSends placeholder={`Comment on line ${line}`} onSubmit={(t) => add(false, t)} />
      <div className="actions">
        <span className="muted small">↩ note for the agent · ⇧↩ new line</span>
        <span className="grow" />
        <button onClick={() => say("cancelCompose")}>Cancel</button>
        <button disabled={empty || !s.review.prOpen} title={s.review.prOpen ? "Waits in your review until you submit it" : "No pull request is open for this branch"} onClick={() => add(true)}>Add to PR Review</button>
        <button className="primary small" disabled={empty} onClick={() => add(false)}>Add Note for the Agent</button>
      </div>
    </div>
  )
}

function SendMenu({ s, title }: { s: ChangesState; title: string }) {
  const [open, setOpen] = useState(false)
  if (!s.send.canPick) return <button disabled={!s.send.enabled} onClick={() => say("sendNotes", {})}>{title}</button>
  return (
    <span className="menu-wrap">
      <button disabled={!s.send.enabled} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>{title} ▾</button>
      {open && (
        <div className="menu" role="menu" onMouseLeave={() => setOpen(false)}>
          {s.send.agents.length > 0 && <div className="menu-head">On {s.send.branch}</div>}
          {s.send.agents.map((a) => <button key={a.id} role="menuitem" disabled={a.disabled} onClick={() => { setOpen(false); say("sendNotes", { to: a.id }) }}>{a.label}</button>)}
          <div className="menu-head">Start one with the notes</div>
          {s.send.startable.map((a) => <button key={a.name} role="menuitem" onClick={() => { setOpen(false); say("sendNotes", { start: a.name }) }}>New {a.title}</button>)}
        </div>
      )}
    </span>
  )
}

// --- the side: commit, checks, review

function Side({ s }: { s: ChangesState }) {
  const c = s.commit
  const r = s.review
  return (
    <aside className="side" aria-label="Commit and review">
      {s.busy && <p className="muted small">{s.busy}…</p>}
      {s.worktree && (
        <section className="card">
          <div className="card-head"><span>Commit</span></div>
          <p className="muted small">{c.files === 0 ? "Nothing to commit." : `Commits ${s.commitWhat}.`}</p>
          {/* ⌘↩ commits: the message goes first, so the app commits what was typed. */}
          <Field label="Commit message" value={c.message} mono rows={5} onChange={(v) => say("message", { text: v })}
            onSubmit={() => { if (c.files > 0) say("commit") }} />
          <div className="buttons">
            <button disabled={c.files === 0 || c.writing} onClick={() => say("writeMessage")} title="Draft the message from what is ticked, or everything">{c.writing ? "Writing…" : "✦ Write"}</button>
            <span className="grow" />
            {c.canStage && <button disabled={!c.canStageNow} onClick={() => say("stage")} title="git add what is ticked">Stage</button>}
            <button className="primary small" disabled={!c.canCommit} onClick={() => say("commit")}>Commit</button>
          </div>
          {c.agent && (
            <label className="check"><input type="checkbox" checked={c.credit} onChange={(e) => say("credit", { on: e.target.checked })} /> Credit {c.agent} (Co-Authored-By)</label>
          )}
        </section>
      )}

      {s.checks && (
        <section className="card">
          <div className="card-head"><span>Checks</span></div>
          {s.checks.card && (
            <div className="ci-card">
              <p>{s.checks.card.text}</p>
              <div className="buttons">
                {s.checks.card.install && <button onClick={() => say("installCI")}>{s.checks.card.install}</button>}
                {s.checks.card.open && <button onClick={() => say("openCIService")}>{s.checks.card.open}</button>}
              </div>
            </div>
          )}
          <div className="status"><Dot state={s.checks.status} /><span>{s.checks.text}</span></div>
          {s.checks.progress !== undefined && <div className="bar"><div className={s.checks.status === "fail" ? "fail" : ""} style={{ width: `${percent(s.checks.progress * 100, 100)}%` }} /></div>}
          {s.checks.runs.map((run, i) => (
            <button key={run.name + i} className="row" disabled={!run.url} onClick={() => say("openURL", { url: run.url })}>
              <Dot state={run.state} /><span className="path">{run.name}</span><span className="muted small">{run.where}</span>
            </button>
          ))}
          {s.checks.failing && <button onClick={() => say("fixCI")}>{s.checks.fixLabel}</button>}
          {s.checks.canRerun && <button onClick={() => say("rerunCI")}>Re-run Failed Jobs</button>}
          {s.checks.url && <button className="link" onClick={() => say("openURL", { url: s.checks!.url })}>Open #{s.checks.number} on GitHub</button>}
        </section>
      )}

      <section className="card">
        <div className="card-head"><span>{r.prNumber ? `Review · #${r.prNumber}` : "Review"}</span></div>
        <div className="buttons">
          <span>{r.notes} {r.notes === 1 ? "note" : "notes"} for the agent</span>
          <span className="grow" />
          <SendMenu s={s} title="Send" />
        </div>
        {r.prOpen ? (
          <>
            <p className="muted small">{r.pending} {r.pending === 1 ? "comment" : "comments"} waiting for the pull request</p>
            {r.reviewed ? (
              <>
                <p className={r.reviewed === "requestChanges" ? "err" : "ok"}>
                  {r.reviewed === "approve" ? `You approved #${r.prNumber}` : r.reviewed === "requestChanges" ? "You requested changes" : "You commented"}
                </p>
                <button onClick={() => say("reviewAgain")}>Review Again</button>
              </>
            ) : (
              <>
                <div role="radiogroup" aria-label="Review" className="radios">
                  {r.events.map((e) => (
                    <label key={e.id} className="radio">
                      <input type="radio" name="review" checked={r.event === e.id} onChange={() => say("event", { id: e.id })} />
                      <span><span>{e.title}</span><span className="muted small block">{e.help}</span></span>
                    </label>
                  ))}
                </div>
                <Field label="Review summary" value={r.body} rows={3} placeholder="Summary" onChange={(v) => say("reviewBody", { text: v })} />
                <button className="primary small" disabled={!r.canSubmit} onClick={() => say("submitReview")}>
                  Submit Review{r.pending ? ` with ${r.pending} ${r.pending === 1 ? "comment" : "comments"}` : ""}
                </button>
              </>
            )}
          </>
        ) : (
          <>
            <p className="muted small">No pull request open: comments for one wait until there is.</p>
            <button disabled={!!s.busy} onClick={() => say("openPR")}>Open Pull Request</button>
          </>
        )}
      </section>
    </aside>
  )
}
