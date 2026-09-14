"use client"

import { CheckIcon, SparklesIcon, TriangleAlertIcon } from "lucide-react"
import { useState } from "react"

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { cn } from "@/lib/utils"

type Action = { kind: string; text: string; detail: string; invalid?: string }

const examples: { value: string; label: string; prompt: string; answer?: string; actions: Action[] }[] = [
  {
    value: "tasks",
    label: "Start tasks",
    prompt: "start 3 agents on api: fix the flaky login test, add rate limiting, update the README",
    actions: [
      { kind: "task", text: "Fix the flaky login test", detail: "api · conch/fix-flaky-login-test · claude" },
      { kind: "task", text: "Add rate limiting to the public API", detail: "api · conch/add-rate-limiting · claude" },
      { kind: "task", text: "Update the README for the new endpoints", detail: "api · conch/update-readme · codex" },
    ],
  },
  {
    value: "message",
    label: "Message an agent",
    prompt: "tell the agent on fix-login to also cover the logout path",
    actions: [
      { kind: "send", text: "Also cover the logout path in the test you're fixing.", detail: "→ Fix flaky tests · local · fix-login" },
    ],
  },
  {
    value: "question",
    label: "Ask a question",
    prompt: "what is waiting for me?",
    answer:
      "One agent needs you: “Fix flaky tests” on api (fix/flaky) wants approval to run the login test 50 times. “Add health check” is still working; gpu-box has two agents working.",
    actions: [],
  },
  {
    value: "invalid",
    label: "Invalid actions",
    prompt: "start gemini on the docs project to fix broken links",
    actions: [
      { kind: "task", text: "Fix broken links", detail: "docs · gemini", invalid: "Gemini CLI isn't installed on local — skipped" },
    ],
  },
]

function Plan({ actions }: { actions: Action[] }) {
  const [checked, setChecked] = useState(() => actions.map((a) => !a.invalid))
  return (
    <ul className="flex flex-col gap-2">
      {actions.map((a, i) => (
        <li key={a.text}>
          <button
            type="button"
            disabled={!!a.invalid}
            onClick={() => setChecked((c) => c.map((v, j) => (j === i ? !v : v)))}
            className={cn(
              "flex w-full items-start gap-3 rounded-lg border border-term-border px-3 py-2 text-left transition-colors",
              a.invalid ? "opacity-70" : "hover:bg-white/5",
            )}
          >
            <span
              className={cn(
                "mt-0.5 grid size-4 shrink-0 place-items-center rounded border border-term-border",
                checked[i] && "border-[#0f7b8a] bg-[#0f7b8a] text-white",
              )}
            >
              {checked[i] && <CheckIcon className="size-3" />}
            </span>
            <span className="flex min-w-0 flex-col gap-0.5">
              <span className={cn(a.invalid && "line-through")}>
                <span className="text-term-muted">{a.kind} </span>
                {a.text}
              </span>
              <span className="text-[0.7rem] text-term-muted">{a.detail}</span>
              {a.invalid && (
                <span className="flex items-center gap-1 text-[0.7rem] text-waiting">
                  <TriangleAlertIcon className="size-3" /> {a.invalid}
                </span>
              )}
            </span>
          </button>
        </li>
      ))}
    </ul>
  )
}

export function BrainDemo() {
  return (
    <Tabs defaultValue="tasks" className="gap-4">
      <TabsList className="h-auto! flex-wrap">
        {examples.map((e) => (
          <TabsTrigger key={e.value} value={e.value} className="px-3">
            {e.label}
          </TabsTrigger>
        ))}
      </TabsList>
      {examples.map((e) => (
        <TabsContent key={e.value} value={e.value}>
          <div className="overflow-hidden rounded-xl border border-term-border bg-term font-mono text-[0.8rem] text-term-foreground">
            <div className="flex items-center gap-2 border-b border-term-border px-4 py-3">
              <SparklesIcon className="size-3.5 shrink-0 text-working" />
              <span className="text-term-muted">Ask</span>
              <span className="min-w-0 truncate">{e.prompt}</span>
            </div>
            <div className="flex flex-col gap-3 px-4 py-4">
              {e.answer && <p className="leading-relaxed whitespace-normal">{e.answer}</p>}
              {e.actions.length > 0 && <Plan actions={e.actions} />}
            </div>
            <div className="flex justify-between border-t border-term-border px-4 py-2 text-[0.7rem] text-term-muted">
              <span>
                {e.actions.length
                  ? "space toggle · enter run selected · e edit request · esc cancel"
                  : "e ask something else · esc close"}
              </span>
            </div>
          </div>
        </TabsContent>
      ))}
    </Tabs>
  )
}
