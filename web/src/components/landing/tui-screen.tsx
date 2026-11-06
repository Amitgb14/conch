import { cn } from "@/lib/utils"

import data from "./tui-frames.json"

// Real conch TUI screens, captured from a staged demo project and converted
// from the server's ANSI frames into styled runs.
type Style = { fg?: string; bg?: string; b?: number; d?: number; i?: number; u?: number }
type Run = [string, number]

const styles = data.styles as unknown as Style[]
const frames = data.frames as unknown as Record<string, Run[][]>

const VERTICAL = new Set("│┃║╭╮╰╯├┤┌┐└┘")

// Fonts disagree on the width of symbols such as ⏺ ⎿ ⠏ and box drawing, so
// every non-ASCII character gets exactly one cell; vertical box lines are
// stretched to meet the rows above and below.
function Cells({ text }: { text: string }) {
  const parts: React.ReactNode[] = []
  let ascii = ""
  let i = 0
  for (const ch of text) {
    if (ch.charCodeAt(0) < 0x7f) {
      ascii += ch
      continue
    }
    if (ascii) parts.push(ascii)
    ascii = ""
    parts.push(
      <span
        key={i++}
        className="inline-block w-[1ch] text-center"
        style={VERTICAL.has(ch) ? { transform: "scaleY(1.35)" } : undefined}
      >
        {ch}
      </span>,
    )
  }
  if (ascii) parts.push(ascii)
  return <>{parts}</>
}

export function Line({ runs }: { runs: Run[] }) {
  return (
    <div className="h-[1.22em] whitespace-pre">
      {runs.map(([text, s], i) => {
        const st = styles[s]
        return (
          <span
            key={i}
            style={{
              color: st.fg,
              backgroundColor: st.bg,
              fontWeight: st.b ? 700 : undefined,
              opacity: st.d ? 0.6 : undefined,
              fontStyle: st.i ? "italic" : undefined,
              textDecoration: st.u ? "underline" : undefined,
            }}
          >
            <Cells text={text} />
          </span>
        )
      })}
      {runs.length === 0 && " "}
    </div>
  )
}

/** A captured TUI screen, drawn as text: sharp at any size, unlike a picture. */
export function TuiScreen({ id, className }: { id: string; className?: string }) {
  return (
    <div
      className={cn("w-max font-mono text-[#e6edf3]", className)}
      style={{ fontSize: "clamp(8px, 0.93vw, 12.5px)", lineHeight: 1.22 }}
    >
      {frames[id].map((runs, i) => (
        <Line key={i} runs={runs} />
      ))}
    </div>
  )
}

/** The window a screen is shown in: the TUI's own colours, macOS's buttons. */
export function TuiWindow({ title, children }: { title?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-xl border border-term-border bg-[#0d1117] shadow-[0_40px_100px_-30px_rgb(0_0_0/0.45)]">
      <div className="flex items-center gap-2 border-b border-white/10 bg-[#161b22] px-3.5 py-2.5">
        <span className="flex gap-1.5">
          <i className="size-2.5 rounded-full bg-[#ff5f57]" />
          <i className="size-2.5 rounded-full bg-[#febc2e]" />
          <i className="size-2.5 rounded-full bg-[#28c840]" />
        </span>
        {title}
      </div>
      <div className="overflow-x-auto px-3 py-3">{children}</div>
    </div>
  )
}
