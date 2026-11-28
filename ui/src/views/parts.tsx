import { useEffect, useRef, useState } from "react"
import { tokens } from "../lib/lib.mjs"

/** A check's or an agent's state as a coloured dot. */
export function Dot({ state }: { state: string }) {
  const s = state.toLowerCase()
  const tone = s === "pass" || s === "success" || s === "done" ? "ok"
    : s === "fail" || s === "failure" || s === "error" ? "err"
    : s === "blocked" ? "warn"
    : s === "pending" || s === "running" || s === "queued" || s === "working" ? "work" : "muted"
  return <span className={`dot ${tone}`} aria-hidden="true">●</span>
}

/** A line of code, coloured. */
export function Code({ text, lang }: { text: string; lang: string }) {
  return <>{tokens(text, lang).map((t: { k: string; s: string }, i: number) => t.k === "plain" ? t.s : <span key={i} className={`tk-${t.k}`}>{t.s}</span>)}</>
}

/**
 * A text box whose words are the page's while it has the keyboard: the
 * host sends the view back after each change, and taking its copy then
 * would move the caret under somebody typing. Changes go to the host as
 * they are typed, a moment after the last key.
 */
export function Field({ value, onChange, placeholder, rows = 3, mono = false, onSubmit, enterSends = false, label }: {
  value: string; onChange: (v: string) => void; placeholder?: string; rows?: number; mono?: boolean
  /**
   * ⌘↩ submits; with enterSends, ↩ does too and ⇧↩ is a new line. It is
   * handed the words as they are now: the owner's copy of them may be a
   * keystroke behind, and a note sent from that copy lost what was typed
   * last.
   */
  onSubmit?: (text: string) => void; enterSends?: boolean; label: string
}) {
  const [text, setText] = useState(value)
  const latest = useRef(value)
  const focused = useRef(false)
  const timer = useRef<number | undefined>(undefined)
  useEffect(() => { if (!focused.current) { setText(value); latest.current = value } }, [value])
  useEffect(() => () => window.clearTimeout(timer.current), [])
  return (
    <textarea
      aria-label={label}
      className={mono ? "field mono" : "field"}
      rows={rows}
      value={text}
      placeholder={placeholder}
      onFocus={() => { focused.current = true }}
      onBlur={() => { focused.current = false; window.clearTimeout(timer.current); onChange(latest.current) }}
      onChange={(e) => {
        const v = e.target.value
        setText(v)
        latest.current = v
        window.clearTimeout(timer.current)
        timer.current = window.setTimeout(() => onChange(v), 150)
      }}
      onKeyDown={(e) => {
        const now = latest.current
        if (onSubmit && e.key === "Enter" && (e.metaKey || (enterSends && !e.shiftKey)) && now.trim()) {
          e.preventDefault()
          window.clearTimeout(timer.current)
          onChange(now)
          onSubmit(now)
        }
      }}
    />
  )
}
