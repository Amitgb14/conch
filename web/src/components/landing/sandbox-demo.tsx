"use client"

import { RotateCcwIcon } from "lucide-react"
import { useEffect, useState } from "react"

import { CopyButton } from "@/components/copy-button"
import { TerminalWindow } from "@/components/terminal"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { cn } from "@/lib/utils"

// What the demo offers comes from sandbox-demo.json, which
// cmd/conch/webdemo_test.go holds to conch itself: each provider's sizes
// and regions to its registry file, the agents to the supported ones, and
// each flag printed below to the CLI's usage. Change it there, not here.
import data from "./sandbox-demo.json"

type Provider = {
  name: string
  label: string
  keyEnv: string
  keyHint: string
  access: string
  regions: string[]
  sizes: string[]
  limits: { cpu?: number[]; memory?: number[]; disk?: number[] }
}
type Agent = { name: string; label: string; color: string; token?: string }

const providers = data.providers as Provider[]
const agents = data.agents as Agent[]

const project = "api"
const repo = "https://github.com/you/api"

type Step = "idle" | "add" | "providers" | "dialog" | "creating" | "ready" | "task" | "running"

type Choice = {
  provider: string
  region: string
  size: string
  cpu: number
  memory: number
  disk: number
  label: string
  passToken: boolean
  agent: string
  prompt: string
}

function defaults(p: Provider, prev?: Choice): Choice {
  return {
    provider: p.name,
    region: p.regions[0] ?? "",
    size: p.sizes.includes("default") ? "default" : (p.sizes[0] ?? ""),
    cpu: p.limits.cpu ? Math.min(p.limits.cpu[1], Math.max(p.limits.cpu[0], 2)) : 0,
    memory: p.limits.memory ? Math.min(p.limits.memory[1], 4) : 0,
    disk: p.limits.disk ? Math.min(p.limits.disk[1], Math.max(p.limits.disk[0], 10)) : 0,
    label: prev?.label ?? "api-box",
    passToken: prev?.passToken ?? true,
    agent: prev?.agent ?? "claude",
    prompt: prev?.prompt ?? "Fix the flaky login test",
  }
}

function providerOf(c: Choice) {
  return providers.find((p) => p.name === c.provider) ?? providers[0]
}

function agentOf(c: Choice) {
  return agents.find((a) => a.name === c.agent) ?? agents[0]
}

function quote(s: string) {
  return `"${s.replace(/(["\\$`])/g, "\\$1")}"`
}

// branchName is what conch names a task's branch from its prompt, near
// enough for a picture: lower case, words joined by dashes.
function branchName(prompt: string) {
  const slug = prompt
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .split("-")
    .slice(0, 5)
    .join("-")
  return "conch/" + (slug || "task")
}

// The lines a choice makes, one list per way of doing it. A line that
// starts with # is a comment, kept out of what is copied.
export function commands(c: Choice) {
  const p = providerOf(c)
  const a = agentOf(c)
  const env = c.passToken && a.token ? [a.token] : []
  const box = c.label || "api-box"

  const toml = [`[sandbox.${p.name}]`]
  if (c.region) toml.push(`region = ${quote(c.region)}`)
  if (c.size) toml.push(`size = ${quote(c.size)}`)
  if (p.limits.cpu) toml.push(`cpu = ${c.cpu}`)
  if (p.limits.memory) toml.push(`memory = ${c.memory}`)
  if (p.limits.disk) toml.push(`disk = ${c.disk}`)
  if (env.length) toml.push(`env = [${env.map(quote).join(", ")}]`)

  const once = [
    "# once: the provider, from the registry, and its key",
    `conch sandbox provider install ${p.name}`,
    `export ${p.keyEnv}=${p.keyHint}`,
  ]
  const oneCommand = [
    ...once,
    "",
    "# its size: Settings → Sandboxes, or ~/.config/conch/config.toml",
    ...toml.map((l) => "#   " + l),
    "",
    `# from the project's checkout: a sandbox, ${project} cloned from origin,`,
    "# and the agent on a branch of its own",
    `cd ~/src/${project}`,
    `conch task -sandbox ${p.name} -agent ${a.name} ${quote(c.prompt)}`,
  ]

  // One flag group a line, joined as the shell joins them, so the line
  // fits beside the demo and still pastes as one command.
  const sizing: string[] = []
  if (c.region) sizing.push(`-region ${c.region}`)
  if (c.size) sizing.push(`-size ${c.size}`)
  if (p.limits.cpu) sizing.push(`-cpu ${c.cpu}`)
  if (p.limits.memory) sizing.push(`-memory ${c.memory}`)
  if (p.limits.disk) sizing.push(`-disk ${c.disk}`)
  const create = [`conch sandbox -provider ${p.name} create -label ${box}`, sizing.join(" "), env.map((e) => `-env ${e}`).join(" ")]
    .filter(Boolean)
    .map((l, i, all) => (i > 0 ? "    " : "") + l + (i < all.length - 1 ? " \\" : ""))
  const stepByStep = [
    ...once,
    "",
    "# a sandbox, sized now, with conch installed and added as a machine",
    ...create,
    "",
    "# the agent and the project, on that machine",
    `conch -m ${box} agent install ${a.name}`,
    `conch -m ${box} project clone ${repo}`,
    `# then t on ${project}, under ${box} in the tree`,
  ]
  return { oneCommand, stepByStep }
}

// Hint marks what to click next, as a pulsing dot in its corner.
function Hint({ on, children, className }: { on: boolean; children: React.ReactNode; className?: string }) {
  return (
    <span className={cn("relative inline-flex", className)}>
      {children}
      {on && (
        <span className="pointer-events-none absolute -top-1 -right-1 flex size-2.5">
          <span className="absolute inline-flex size-full animate-ping rounded-full bg-red-500 opacity-75" />
          <span className="relative inline-flex size-2.5 rounded-full bg-red-500" />
        </span>
      )}
    </span>
  )
}

function MenuRow({
  k,
  label,
  onClick,
  hint,
  disabled,
}: {
  k: string
  label: string
  onClick?: () => void
  hint?: boolean
  disabled?: boolean
}) {
  return (
    <Hint on={!!hint} className="w-full">
      <button
        type="button"
        disabled={disabled}
        onClick={onClick}
        className={cn(
          "flex w-full items-center gap-3 rounded-md px-2 py-1 text-left",
          disabled ? "text-term-muted" : "hover:bg-white/10",
          hint && "ring-1 ring-red-500/70",
        )}
      >
        <span className="w-3 text-term-muted">{k}</span>
        <span className="truncate">{label}</span>
      </button>
    </Hint>
  )
}

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={on}
      className={cn(
        "rounded-md border px-2 py-0.5",
        on ? "border-[#0f7b8a] bg-[#0f7b8a] text-white" : "border-term-border hover:bg-white/10",
      )}
    >
      {children}
    </button>
  )
}

function Range({
  label,
  unit,
  range,
  value,
  onChange,
}: {
  label: string
  unit: string
  range: number[]
  value: number
  onChange: (n: number) => void
}) {
  return (
    <label className="flex items-center gap-3">
      <span className="w-20 shrink-0 text-term-muted">{label}</span>
      <input
        type="range"
        min={range[0]}
        max={range[1]}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className="min-w-0 flex-1 accent-[#0f7b8a]"
      />
      <span className="w-16 shrink-0 text-right">
        {value} {unit}
      </span>
    </label>
  )
}

// What making a sandbox says, a line at a time. The first two are conch's
// own words; the rest stand for its steps.
function creating(p: Provider, label: string) {
  return [
    `Creating a ${p.label} sandbox…`,
    `  sandbox sb-7f3a2c started`,
    `  reaching it over ssh: ${p.access}`,
    `  installing conch there…`,
    `  ${label} added as a machine`,
  ]
}

const spinner = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

function useTicker(on: boolean, ms: number) {
  const [n, setN] = useState(0)
  useEffect(() => {
    if (!on) return
    const id = setInterval(() => setN((x) => x + 1), ms)
    return () => clearInterval(id)
  }, [on, ms])
  return n
}

// stacked keeps the commands under the demo rather than beside it, for a
// column as narrow as the docs'.
export function SandboxDemo({ stacked = false }: { stacked?: boolean }) {
  const [stage, setStep] = useState<Step>("idle")
  const [choice, setChoice] = useState<Choice>(() => defaults(providers[0]))
  const [progress, setProgress] = useState(0)
  const p = providerOf(choice)
  const a = agentOf(choice)
  const box = choice.label || "api-box"
  const step: Step = stage === "creating" && progress >= creating(p, box).length ? "ready" : stage
  const set = (patch: Partial<Choice>) => setChoice((c) => ({ ...c, ...patch }))
  const tick = useTicker(step === "creating" || step === "running", 90)

  // Creating and starting are a few lines each, shown one after another.
  const creatingLines = creating(p, box)
  const runningLines = [`  cloning ${repo} into it…`, `  ${branchName(choice.prompt)} checked out in its own worktree`, `  ${a.label} started`]
  const lines = step === "creating" ? creatingLines : runningLines
  // Each tick shows the next line, until the last; creating is then done,
  // and the demo is ready for a task.
  const max = (step === "creating" ? creatingLines : runningLines).length
  useEffect(() => {
    if (step !== "creating" && step !== "running") return
    const id = setInterval(() => setProgress((n) => Math.min(n + 1, max)), 650)
    return () => clearInterval(id)
  }, [step, max])

  const go = (next: Step) => {
    setProgress(0)
    setStep(next)
  }
  const reset = () => go("idle")
  const pickProvider = (name: string) => {
    const next = providers.find((x) => x.name === name)
    if (!next) return
    setChoice((c) => defaults(next, c))
    setStep("dialog")
  }

  const made = step === "ready" || step === "task" || step === "running"
  const working = step === "running" && progress >= runningLines.length
  const { oneCommand, stepByStep } = commands(choice)

  return (
    <div className={cn("grid grid-cols-1 gap-6", !stacked && "lg:grid-cols-[1.15fr_1fr]")}>
      <div className="flex min-w-0 flex-col gap-3">
        <p className="font-mono text-xs text-muted-foreground">
          interactive demo · click where the <span className="text-red-500">red dot</span> pulses
        </p>
        <TerminalWindow title="conch" bodyClassName="text-[0.75rem]">
          <div className="grid min-h-[22rem] grid-cols-1 sm:grid-cols-[13rem_1fr]">
            <div className="flex flex-col gap-0.5 border-b border-term-border px-3 py-3 whitespace-nowrap sm:border-r sm:border-b-0">
              <span>
                <span className="text-term-muted">▾ </span>
                <span className="text-done">● </span>local
              </span>
              <span className="pl-4 text-term-muted">▸ {project}</span>
              {step !== "idle" && step !== "add" && step !== "providers" && step !== "dialog" && (
                <>
                  <span className="mt-1">
                    <span className="text-term-muted">▾ </span>
                    <span className="text-working">▤ </span>Sandboxes <span className="text-term-muted">1</span>
                  </span>
                  <span className="pl-4">
                    <span className="text-term-muted">▾ </span>
                    {p.label}
                  </span>
                  <span className="truncate pl-8">
                    <span className="text-term-muted">{made ? "▾ " : "▸ "}</span>
                    {made ? <span className="text-done">● </span> : <span className="text-working">{spinner[tick % spinner.length]} </span>}
                    {box}
                    {!made && <span className="text-term-muted"> creating</span>}
                  </span>
                  {step === "running" && (
                    <>
                      <span className="truncate pl-12">
                        <span className="text-term-muted">▾ </span>
                        {project}
                      </span>
                      <span className="truncate pl-16 text-term-muted">⎇ {branchName(choice.prompt).slice(6)}</span>
                      <span className="truncate pl-16">
                        {working ? (
                          <span className="text-working">{spinner[tick % spinner.length]} </span>
                        ) : (
                          <span className="text-term-muted">○ </span>
                        )}
                        <span style={{ color: a.color }}>● </span>
                        {a.label}
                      </span>
                    </>
                  )}
                </>
              )}
            </div>

            <div className="flex min-w-0 flex-col gap-3 px-4 py-3">
              {step === "idle" && (
                <div className="flex flex-1 flex-col items-start justify-center gap-3 text-term-muted">
                  <p className="whitespace-normal">
                    Your agents run on this computer. Give one a machine of its own in the cloud, so it keeps going with the laptop
                    closed.
                  </p>
                  <Hint on>
                    <button
                      type="button"
                      onClick={() => setStep("add")}
                      className="rounded-md border border-term-border px-2 py-1 text-term-foreground ring-1 ring-red-500/70 hover:bg-white/10"
                    >
                      <span className="mr-2 rounded border border-term-border px-1">M</span>Add a machine
                    </button>
                  </Hint>
                </div>
              )}

              {step === "add" && (
                <div className="flex flex-col gap-1">
                  <p className="mb-1 font-semibold">Add a machine</p>
                  <MenuRow k="s" label="Over ssh…" disabled />
                  <MenuRow k="b" label="New sandbox…" hint onClick={() => setStep("providers")} />
                </div>
              )}

              {step === "providers" && (
                <div className="flex flex-col gap-1">
                  <p className="mb-1 font-semibold">New sandbox</p>
                  {providers.map((x, i) => (
                    <MenuRow key={x.name} k={x.name[0]} label={x.label + "…"} hint={i === 0} onClick={() => pickProvider(x.name)} />
                  ))}
                  <MenuRow k="*" label="Install a provider from the registry…" disabled />
                  <MenuRow k="+" label="Add a provider conch doesn't know…" disabled />
                </div>
              )}

              {step === "dialog" && (
                <div className="flex flex-col gap-2.5">
                  <p className="font-semibold">New {p.label} sandbox</p>
                  <p className="whitespace-normal text-term-muted">
                    Creates a sandbox with {p.label}, installs conch there and adds it as a machine. It runs, and costs, until you stop
                    it.
                  </p>
                  <label className="flex items-center gap-3">
                    <span className="w-20 shrink-0 text-term-muted">Label</span>
                    <input
                      value={choice.label}
                      placeholder="api-box"
                      onChange={(e) => set({ label: e.target.value.replace(/[^a-zA-Z0-9-]/g, "").slice(0, 24) })}
                      className="min-w-0 flex-1 rounded-md border border-term-border bg-transparent px-2 py-0.5 outline-none focus:border-[#0f7b8a]"
                    />
                  </label>
                  {p.regions.length > 0 && (
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="w-20 shrink-0 text-term-muted">Region</span>
                      {p.regions.map((r) => (
                        <Chip key={r} on={choice.region === r} onClick={() => set({ region: r })}>
                          {r}
                        </Chip>
                      ))}
                    </div>
                  )}
                  {p.sizes.length > 0 && (
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="w-20 shrink-0 text-term-muted">Size</span>
                      {p.sizes.map((s) => (
                        <Chip key={s} on={choice.size === s} onClick={() => set({ size: s })}>
                          {s}
                        </Chip>
                      ))}
                    </div>
                  )}
                  {p.limits.cpu && <Range label="vCPUs" unit="" range={p.limits.cpu} value={choice.cpu} onChange={(n) => set({ cpu: n })} />}
                  {p.limits.memory && (
                    <Range label="Memory" unit="GiB" range={p.limits.memory} value={choice.memory} onChange={(n) => set({ memory: n })} />
                  )}
                  {p.limits.disk && <Range label="Disk" unit="GiB" range={p.limits.disk} value={choice.disk} onChange={(n) => set({ disk: n })} />}
                  {a.token && (
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="w-20 shrink-0 text-term-muted">Pass in</span>
                      <Chip on={choice.passToken} onClick={() => set({ passToken: !choice.passToken })}>
                        {a.token}
                      </Chip>
                    </div>
                  )}
                  <div className="mt-1 flex gap-2">
                    <Hint on>
                      <button
                        type="button"
                        onClick={() => go("creating")}
                        className="rounded-md bg-[#0f7b8a] px-3 py-1 text-white ring-1 ring-red-500/70 hover:opacity-90"
                      >
                        Create
                      </button>
                    </Hint>
                    <button type="button" onClick={() => setStep("providers")} className="rounded-md px-3 py-1 text-term-muted hover:bg-white/10">
                      Back
                    </button>
                  </div>
                </div>
              )}

              {(step === "creating" || step === "running") && (
                <div className="flex flex-col gap-0.5">
                  {lines.slice(0, progress + 1).map((l, i) => (
                    <span key={i} className={cn("truncate", i > 0 && "text-term-muted")}>
                      {l}
                    </span>
                  ))}
                  {working && (
                    <div className="mt-3 flex flex-col gap-1 rounded-md border border-term-border p-3">
                      <span>
                        <span style={{ color: a.color }}>● </span>
                        {a.label} <span className="text-term-muted">on {box}</span>
                      </span>
                      <span className="whitespace-normal">&gt; {choice.prompt}</span>
                      <span className="text-working">{spinner[tick % spinner.length]} working…</span>
                      <span className="mt-2 whitespace-normal text-term-muted">
                        Close the laptop: it keeps going in the sandbox. conch stops the sandbox after 30 minutes with nothing working.
                      </span>
                    </div>
                  )}
                </div>
              )}

              {step === "ready" && (
                <div className="flex flex-col items-start gap-3">
                  <p className="whitespace-normal">
                    <span className="text-done">●</span> {box} is a machine now, like any other in the tree.
                  </p>
                  <Hint on>
                    <button
                      type="button"
                      onClick={() => setStep("task")}
                      className="rounded-md border border-term-border px-2 py-1 ring-1 ring-red-500/70 hover:bg-white/10"
                    >
                      <span className="mr-2 rounded border border-term-border px-1">t</span>New task on {project}
                    </button>
                  </Hint>
                </div>
              )}

              {step === "task" && (
                <div className="flex flex-col gap-2.5">
                  <p className="font-semibold">
                    New task · {project} on {box}
                  </p>
                  <div className="flex flex-wrap gap-1.5">
                    {agents.map((x) => (
                      <Chip key={x.name} on={choice.agent === x.name} onClick={() => set({ agent: x.name })}>
                        <span style={{ color: choice.agent === x.name ? undefined : x.color }}>● </span>
                        {x.label}
                      </Chip>
                    ))}
                  </div>
                  <input
                    value={choice.prompt}
                    onChange={(e) => set({ prompt: e.target.value.slice(0, 80) })}
                    aria-label="Prompt"
                    className="rounded-md border border-term-border bg-transparent px-2 py-1 outline-none focus:border-[#0f7b8a]"
                  />
                  <div>
                    <Hint on>
                      <button
                        type="button"
                        onClick={() => go("running")}
                        className="rounded-md bg-[#0f7b8a] px-3 py-1 text-white ring-1 ring-red-500/70 hover:opacity-90"
                      >
                        Start {a.label}
                      </button>
                    </Hint>
                  </div>
                </div>
              )}
            </div>
          </div>
          <div className="flex items-center justify-between border-t border-term-border px-3 py-1.5 text-[0.7rem] text-term-muted">
            <span className="truncate">
              {p.label} · {box}
              {choice.region && ` · ${choice.region}`}
              {choice.size && ` · ${choice.size}`}
              {p.limits.cpu && ` · ${choice.cpu} vCPU`}
              {p.limits.memory && ` · ${choice.memory} GiB`}
              {p.limits.disk && ` · ${choice.disk} GiB disk`}
            </span>
            {step !== "idle" && (
              <button type="button" onClick={reset} className="flex items-center gap-1 hover:text-term-foreground">
                <RotateCcwIcon className="size-3" /> again
              </button>
            )}
          </div>
        </TerminalWindow>
      </div>

      <div className="flex min-w-0 flex-col gap-3">
        <p className="font-mono text-xs text-muted-foreground">under the hood · follows what you pick</p>
        <Tabs defaultValue="one" className="gap-3">
          <TabsList>
            <TabsTrigger value="one" className="px-3">
              In one command
            </TabsTrigger>
            <TabsTrigger value="steps" className="px-3">
              Step by step
            </TabsTrigger>
          </TabsList>
          <TabsContent value="one">
            <Script lines={oneCommand} />
          </TabsContent>
          <TabsContent value="steps">
            <Script lines={stepByStep} />
          </TabsContent>
        </Tabs>
        <p className="text-sm leading-6 text-muted-foreground">
          The agent signs in inside the sandbox, as on any new machine — conch never copies credentials. To run unattended, pass a
          token in instead{a.token ? <> ({a.token})</> : null}.
        </p>
      </div>
    </div>
  )
}

function Script({ lines }: { lines: string[] }) {
  const text = lines.filter((l) => l && !l.startsWith("#")).join("\n")
  return (
    <TerminalWindow title="sh" className="shadow-none">
      <div className="relative">
        <CopyButton value={text} className="absolute top-2 right-2 text-term-foreground" />
        <pre className="overflow-x-auto px-4 py-3 pr-12 text-[0.75rem] leading-relaxed">
          {lines.map((l, i) => (
            <div key={i} className={cn(l.startsWith("#") && "text-term-muted")}>
              {l || " "}
            </div>
          ))}
        </pre>
      </div>
    </TerminalWindow>
  )
}
