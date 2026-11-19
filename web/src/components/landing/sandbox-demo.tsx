"use client";

import { RotateCcwIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { CopyButton } from "@/components/copy-button";
import { TerminalWindow } from "@/components/terminal";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

// What the demo offers comes from sandbox-demo.json, which
// cmd/conch/webdemo_test.go holds to conch itself: each provider's sizes
// and regions to its registry file, the agents to the supported ones, and
// each flag printed below to the CLI's usage. Change it there, not here.
import data from "./sandbox-demo.json";

type Provider = {
  name: string;
  label: string;
  keyEnv: string;
  keyHint: string;
  access: string;
  regions: string[];
  sizes: string[];
  limits: { cpu?: number[]; memory?: number[]; disk?: number[] };
};
type Agent = { name: string; label: string; color: string; token?: string };

const providers = data.providers as Provider[];
const agents = data.agents as Agent[];

const project = "api";
const repo = "https://github.com/you/api";

type Choice = {
  provider: string;
  region: string;
  size: string;
  cpu: number;
  memory: number;
  disk: number;
  label: string;
  passToken: boolean;
  agent: string;
  prompt: string;
};

function defaults(p: Provider, prev?: Choice): Choice {
  return {
    provider: p.name,
    region: p.regions[0] ?? "",
    size: p.sizes.includes("default") ? "default" : (p.sizes[0] ?? ""),
    cpu: p.limits.cpu
      ? Math.min(p.limits.cpu[1], Math.max(p.limits.cpu[0], 2))
      : 0,
    memory: p.limits.memory ? Math.min(p.limits.memory[1], 4) : 0,
    disk: p.limits.disk
      ? Math.min(p.limits.disk[1], Math.max(p.limits.disk[0], 10))
      : 0,
    label: prev?.label ?? "api-box",
    passToken: prev?.passToken ?? true,
    agent: prev?.agent ?? "claude",
    prompt: prev?.prompt ?? "Fix the flaky login test",
  };
}

function providerOf(c: Choice) {
  return providers.find((p) => p.name === c.provider) ?? providers[0];
}

function agentOf(c: Choice) {
  return agents.find((a) => a.name === c.agent) ?? agents[0];
}

function quote(s: string) {
  return `"${s.replace(/(["\\$`])/g, "\\$1")}"`;
}

// The lines a choice makes, one list per way of doing it. A line that
// starts with # is a comment, kept out of what is copied.
export function commands(c: Choice) {
  const p = providerOf(c);
  const a = agentOf(c);
  const env = c.passToken && a.token ? [a.token] : [];
  const box = c.label || "api-box";

  const toml = [`[sandbox.${p.name}]`];
  if (c.region) toml.push(`region = ${quote(c.region)}`);
  if (c.size) toml.push(`size = ${quote(c.size)}`);
  if (p.limits.cpu) toml.push(`cpu = ${c.cpu}`);
  if (p.limits.memory) toml.push(`memory = ${c.memory}`);
  if (p.limits.disk) toml.push(`disk = ${c.disk}`);
  if (env.length) toml.push(`env = [${env.map(quote).join(", ")}]`);

  const once = [
    "# once: the provider, from the registry, and its key",
    `conch sandbox provider install ${p.name}`,
    `export ${p.keyEnv}=${p.keyHint}`,
  ];
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
  ];

  // One flag group a line, joined as the shell joins them, so the line
  // fits beside the demo and still pastes as one command.
  const sizing: string[] = [];
  if (c.region) sizing.push(`-region ${c.region}`);
  if (c.size) sizing.push(`-size ${c.size}`);
  if (p.limits.cpu) sizing.push(`-cpu ${c.cpu}`);
  if (p.limits.memory) sizing.push(`-memory ${c.memory}`);
  if (p.limits.disk) sizing.push(`-disk ${c.disk}`);
  const create = [
    `conch sandbox -provider ${p.name} create -label ${box}`,
    sizing.join(" "),
    env.map((e) => `-env ${e}`).join(" "),
  ]
    .filter(Boolean)
    .map(
      (l, i, all) =>
        (i > 0 ? "    " : "") + l + (i < all.length - 1 ? " \\" : ""),
    );
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
  ];
  return { oneCommand, stepByStep };
}

// The window is drawn as Conch.app draws it (macos/Sources/ConchApp): the
// Terminal theme's colours and the surfaces the app derives from them
// (ConchKit/Themes.swift), and its own light pair, Terminal Light, in a
// light page. The
// menus are the machine row's (ConchKit/RowMenu.swift) and the sheet is
// New Sandbox… (NewSandboxSheet.swift), field for field.
const look =
  "[--app-bg:#FBFBFC] [--app-chrome:#F1F1F2] [--app-side:#F6F6F7] [--app-raise:#ECECED] [--app-well:#FFFFFF] " +
  "[--app-fg:#1F2328] [--app-fg2:#616468] [--app-muted:#6E7781] [--app-line:#DADDE2] [--app-accent:#0F7B8A] " +
  "[--app-ok:#1A7F37] [--app-warn:#9A6700] [--app-menu:#F6F6F6] " +
  "dark:[--app-bg:#0C0E11] dark:[--app-chrome:#181A1D] dark:[--app-side:#121417] dark:[--app-raise:#1F2124] " +
  "dark:[--app-well:#090B0D] dark:[--app-fg:#D4D9DF] dark:[--app-fg2:#989CA1] dark:[--app-muted:#8B949E] " +
  "dark:[--app-line:#1E232A] dark:[--app-accent:#2AA5B5] dark:[--app-ok:#9ECE6A] dark:[--app-warn:#E0AF68] " +
  "dark:[--app-menu:#26282C]";

// Lucide's outlines for the few icons the window shows.
const icons: Record<string, React.ReactNode> = {
  laptop: (
    <>
      <path d="M18 5a2 2 0 0 1 2 2v8.526a2 2 0 0 0 .212.897l1.068 2.127a1 1 0 0 1-.9 1.45H3.62a1 1 0 0 1-.9-1.45l1.068-2.127A2 2 0 0 0 4 15.526V7a2 2 0 0 1 2-2z" />
      <path d="M20.054 15.987H3.946" />
    </>
  ),
  cloud: <path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z" />,
  layers: (
    <>
      <path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z" />
      <path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65" />
      <path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65" />
    </>
  ),
  terminal: (
    <>
      <path d="m7 11 2-2-2-2" />
      <path d="M11 13h4" />
      <rect width="18" height="18" x="3" y="3" rx="2" ry="2" />
    </>
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="8" />
      <path d="m21 21-4.3-4.3" />
    </>
  ),
  info: (
    <>
      <circle cx="12" cy="12" r="10" />
      <path d="M12 16v-4" />
      <path d="M12 8h.01" />
    </>
  ),
};

function Icon({
  name,
  size = 14,
  className,
}: {
  name: string;
  size?: number;
  className?: string;
}) {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn("shrink-0", className)}
    >
      {icons[name]}
    </svg>
  );
}

// Hint marks what to click next, as a pulsing dot in its corner.
function Hint({
  on,
  children,
  className,
}: {
  on: boolean;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <span className={cn("relative inline-flex", className)}>
      {children}
      {on && (
        <span className="pointer-events-none absolute -top-1 -right-1 z-10 flex size-2.5">
          <span className="absolute inline-flex size-full animate-ping rounded-full bg-red-500 opacity-75" />
          <span className="relative inline-flex size-2.5 rounded-full bg-red-500" />
        </span>
      )}
    </span>
  );
}

// What making a sandbox says in its pane, a line at a time. The first is
// conch's own words; the rest stand for its steps.
function creating(p: Provider, label: string) {
  return [
    `Creating a ${p.label} sandbox…`,
    `  sandbox sb-7f3a2c started`,
    `  reaching it over ssh: ${p.access}`,
    `  installing conch there…`,
    `  ${label} added as a machine`,
  ];
}

const spinner = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏";

function useTicker(on: boolean, ms: number) {
  const [n, setN] = useState(0);
  useEffect(() => {
    if (!on) return;
    const id = setInterval(() => setN((x) => x + 1), ms);
    return () => clearInterval(id);
  }, [on, ms]);
  return n;
}

// A machine's row: the fold arrow, its icon with a dot for its state, its
// name — as NodeView draws a .machine node.
function MachineRow({
  icon,
  name,
  state,
  open,
  depth = 0,
  hint,
  onClick,
}: {
  icon: string;
  name: string;
  state: "ok" | "warn";
  open: boolean;
  depth?: number;
  hint?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={!onClick}
      className={cn(
        "flex h-[26px] w-full items-center gap-[7px] rounded-md pr-2 text-left text-[13px] text-(--app-fg) enabled:hover:bg-(--app-raise)",
        hint && "ring-1 ring-red-500/70",
      )}
      style={{ paddingLeft: depth * 14 + 8 }}
    >
      <span className="w-2.5 text-[10px] text-(--app-muted)">
        {open ? "▾" : "▸"}
      </span>
      <Hint on={!!hint}>
        <span className="relative text-(--app-fg2)">
          <Icon name={icon} />
          <span
            className={cn(
              "absolute -right-0.5 -bottom-px size-1.5 rounded-full ring-[1.5px] ring-(--app-side)",
              state === "ok" ? "bg-(--app-ok)" : "bg-(--app-warn)",
            )}
          />
        </span>
      </Hint>
      <span className="truncate">{name}</span>
    </button>
  );
}

function GroupRow({
  icon,
  name,
  depth,
  open = false,
}: {
  icon: string;
  name: string;
  depth: number;
  open?: boolean;
}) {
  return (
    <div
      className="flex h-[26px] items-center gap-[7px] pr-2 text-[13px] text-(--app-fg)"
      style={{ paddingLeft: depth * 14 + 8 }}
    >
      <span className="w-2.5 text-[10px] text-(--app-muted)">
        {open ? "▾" : "▸"}
      </span>
      <Icon name={icon} size={13} className="text-(--app-fg2)" />
      <span className="truncate">{name}</span>
    </div>
  );
}

type MenuItem =
  { title: string; hint?: boolean; sub?: boolean; onClick?: () => void } | "-";

// A context menu as macOS draws one: the item under the pointer in the
// system's blue, a submenu's arrow at the right.
function ContextMenu({
  items,
  className,
}: {
  items: MenuItem[];
  className?: string;
}) {
  return (
    <div
      role="menu"
      className={cn(
        "absolute z-20 w-[210px] rounded-[7px] border border-black/10 bg-(--app-menu)/95 p-[5px] text-[13px] text-(--app-fg) shadow-xl backdrop-blur dark:border-white/10",
        className,
      )}
    >
      {items.map((it, i) =>
        it === "-" ? (
          <div key={i} className="mx-2 my-[5px] h-px bg-(--app-fg)/15" />
        ) : (
          <button
            key={i}
            type="button"
            role="menuitem"
            disabled={!it.onClick}
            onClick={it.onClick}
            className={cn(
              "flex h-[22px] w-full items-center justify-between rounded-[4px] px-2 text-left",
              it.onClick
                ? "hover:bg-[#0A64D9] hover:text-white"
                : "cursor-default",
              it.hint && "bg-[#0A64D9] text-white",
            )}
          >
            <Hint on={!!it.hint}>
              <span className="pr-2">{it.title}</span>
            </Hint>
            {it.sub && <span className="text-[10px] opacity-70">▶</span>}
          </button>
        ),
      )}
    </div>
  );
}

// One row of the sheet's Sandbox card: its label at the left, a line
// above it but for the first.
function SheetRow({
  label,
  first,
  children,
}: {
  label: string;
  first?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex min-h-10 items-center gap-2.5 px-3 py-1.5",
        !first && "border-t border-(--app-line)",
      )}
    >
      <span className="w-20 shrink-0 text-[12.5px] text-(--app-fg2) sm:w-24">
        {label}
      </span>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2.5 gap-y-1">
        {children}
      </div>
    </div>
  );
}

function NumberField({
  value,
  unit,
  limit,
  onChange,
}: {
  value: number;
  unit: string;
  limit: number[];
  onChange: (n: number) => void;
}) {
  return (
    <label className="flex items-center gap-1">
      <input
        type="number"
        min={limit[0]}
        max={limit[1]}
        value={value}
        placeholder={`${limit[0]}–${limit[1]}`}
        aria-label={unit}
        onChange={(e) => {
          const n = Number(e.target.value);
          if (Number.isFinite(n))
            onChange(Math.min(limit[1], Math.max(limit[0], Math.round(n))));
        }}
        className="h-[26px] w-[52px] rounded-md border border-(--app-line) bg-(--app-well) px-1.5 text-right text-[12.5px] text-(--app-fg) outline-none"
      />
      <span className="text-[11.5px] text-(--app-muted)">{unit}</span>
    </label>
  );
}

type Step =
  "idle" | "menu" | "sheet" | "creating" | "ready" | "agentMenu" | "running";

// stacked keeps the commands under the demo rather than beside it, for a
// column as narrow as the docs'.
export function SandboxDemo({ stacked = false }: { stacked?: boolean }) {
  const [stage, setStep] = useState<Step>("idle");
  const [choice, setChoice] = useState<Choice>(() => defaults(providers[0]));
  const [progress, setProgress] = useState(0);
  const p = providerOf(choice);
  const a = agentOf(choice);
  const box = choice.label || "api-box";
  const createLines = creating(p, box);
  const step: Step =
    stage === "creating" && progress >= createLines.length ? "ready" : stage;
  const set = (patch: Partial<Choice>) =>
    setChoice((c) => ({ ...c, ...patch }));
  const tick = useTicker(step === "creating" || step === "running", 90);

  // Each tick shows the next line of the create pane, until the last; the
  // sandbox is then online, and the demo ready for an agent.
  useEffect(() => {
    if (step !== "creating") return;
    const id = setInterval(
      () => setProgress((n) => Math.min(n + 1, createLines.length)),
      650,
    );
    return () => clearInterval(id);
  }, [step, createLines.length]);

  const go = (next: Step) => {
    setProgress(0);
    setStep(next);
  };
  const reset = () => go("idle");
  const pickProvider = (name: string) => {
    const next = providers.find((x) => x.name === name);
    if (next) setChoice((c) => defaults(next, c));
  };

  const { oneCommand, stepByStep } = commands(choice);
  const made =
    step === "creating" ||
    step === "ready" ||
    step === "agentMenu" ||
    step === "running";
  const online = step !== "creating";
  const sizing = [
    choice.region,
    choice.size,
    p.limits.cpu && `${choice.cpu} vCPU`,
    p.limits.memory && `${choice.memory} GiB`,
    p.limits.disk && `${choice.disk} GiB disk`,
  ]
    .filter(Boolean)
    .join(" · ");
  const createArgs = [
    `conch sandbox -provider ${p.name} create -label ${box}`,
    choice.region && `-region ${choice.region}`,
    choice.size && `-size ${choice.size}`,
    p.limits.cpu && `-cpu ${choice.cpu}`,
    p.limits.memory && `-memory ${choice.memory}`,
    p.limits.disk && `-disk ${choice.disk}`,
  ]
    .filter(Boolean)
    .join(" ");

  const tabs = [
    { name: "zsh", on: !made },
    ...(made
      ? [{ name: `sandbox create · ${p.label}`, on: step !== "running" }]
      : []),
    ...(step === "running" ? [{ name: `${a.label} · ${box}`, on: true }] : []),
  ];

  const localMenu: MenuItem[] = [
    { title: "Start an Agent", sub: true },
    { title: "New Terminal" },
    { title: "SSH to a Host…" },
    "-",
    { title: "Add Project…" },
    { title: "Add Machine…" },
    { title: "New Sandbox…", hint: true, onClick: () => go("sheet") },
    { title: "Skills & MCP Servers…" },
    "-",
    { title: "Reconnect" },
    { title: "Reload Server" },
  ];
  const boxMenu: MenuItem[] = [
    { title: "Start an Agent", sub: true, hint: true },
    { title: "New Terminal" },
    "-",
    { title: "Add Project…" },
    { title: "Add Machine…" },
    { title: "New Sandbox…" },
    { title: "Skills & MCP Servers…" },
    "-",
    { title: "Reconnect" },
    { title: "Reload Server" },
    { title: "Upgrade conch There" },
    { title: "Remove Machine…" },
  ];

  return (
    <div
      className={cn(
        "grid min-w-0 gap-6",
        !stacked && "lg:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)]",
      )}
    >
      <div className={cn("@container min-w-0", look)}>
        <div
          className="relative overflow-hidden rounded-[10px] border border-(--app-line) bg-(--app-bg) font-[system-ui,-apple-system,BlinkMacSystemFont,'Segoe_UI',sans-serif] text-(--app-fg) shadow-lg"
          onClick={(e) => {
            // A click outside an open menu closes it, as on the Mac.
            if (
              e.target === e.currentTarget &&
              (step === "menu" || step === "agentMenu")
            )
              go(step === "menu" ? "idle" : "ready");
          }}
        >
          {/* The toolbar: the window's buttons, the workspaces, search and the counts. */}
          <div className="flex h-11 items-center gap-3 border-b border-(--app-line) bg-(--app-chrome) px-3">
            <span className="flex gap-2" aria-hidden="true">
              <span className="size-3 rounded-full bg-[#FF5F57]" />
              <span className="size-3 rounded-full bg-[#FEBC2E]" />
              <span className="size-3 rounded-full bg-[#28C840]" />
            </span>
            <span className="ml-2 rounded-md bg-(--app-raise) px-2 py-0.5 text-[12px] font-medium">
              Default
            </span>
            <span className="mx-auto hidden h-7 w-full max-w-[300px] items-center gap-2 rounded-md border border-(--app-line) bg-(--app-well) px-2 text-[12px] text-(--app-muted) @[560px]:flex">
              <Icon name="search" size={12} />
              <span className="truncate">
                Search agents, branches, files, actions
              </span>
              <span className="ml-auto text-[11px]">⌘K</span>
            </span>
            <span className="ml-auto text-[11.5px] text-(--app-muted) @[560px]:ml-0">
              {step === "running" ? "1 working" : "0 agents"}
            </span>
          </div>

          <div className="grid h-[540px] grid-cols-[150px_minmax(0,1fr)] @[560px]:grid-cols-[200px_minmax(0,1fr)]">
            {/* The sidebar's tree. */}
            <div className="relative min-w-0 border-r border-(--app-line) bg-(--app-side) p-2">
              <p className="px-2 pt-2.5 pb-1 text-[11px] font-semibold text-(--app-muted)">
                MACHINES
              </p>
              <MachineRow
                icon="laptop"
                name="This Mac"
                state="ok"
                open
                hint={step === "idle"}
                onClick={step === "idle" ? () => go("menu") : undefined}
              />
              <GroupRow icon="layers" name="Workspace" depth={1} />
              <GroupRow icon="terminal" name="CLI" depth={1} />
              {made && (
                <>
                  <MachineRow
                    icon="cloud"
                    name={box}
                    state={online ? "ok" : "warn"}
                    open={step === "running"}
                    hint={step === "ready"}
                    onClick={
                      step === "ready" ? () => go("agentMenu") : undefined
                    }
                  />
                  {step === "running" && (
                    <>
                      <GroupRow icon="terminal" name="CLI" depth={1} open />
                      <div
                        className="flex h-[26px] items-center gap-[7px] pr-2 text-[13px]"
                        style={{ paddingLeft: 2 * 14 + 8 }}
                      >
                        <span className="w-2.5" />
                        <span
                          className="size-2.5 shrink-0 rounded-full"
                          style={{ background: a.color }}
                        />
                        <span className="truncate">{a.label}</span>
                        <span className="ml-auto shrink-0 font-mono text-[11px] text-(--app-accent)">
                          {spinner[tick % spinner.length]}
                        </span>
                      </div>
                    </>
                  )}
                </>
              )}
              {step === "menu" && (
                <ContextMenu items={localMenu} className="top-[72px] left-6" />
              )}
              {step === "agentMenu" && (
                <div className="absolute top-[150px] left-6 z-20">
                  <ContextMenu items={boxMenu} className="relative" />
                  <div className="absolute top-0 left-[130px] z-30 @[560px]:left-[205px]">
                    <ContextMenu
                      className="relative w-[170px]"
                      items={agents.map((x, i) => ({
                        title: x.label,
                        hint: i === 0 && choice.agent === x.name,
                        onClick: () => {
                          set({ agent: x.name });
                          go("running");
                        },
                      }))}
                    />
                  </div>
                </div>
              )}
            </div>

            {/* The tabs, underlined, and the pane in front. */}
            <div className="flex min-w-0 flex-col">
              <div className="flex h-[34px] items-end gap-1 overflow-hidden border-b border-(--app-line) bg-(--app-chrome) px-2">
                {tabs.map((t) => (
                  <span
                    key={t.name}
                    className={cn(
                      "truncate border-b-2 px-2.5 pb-1.5 text-[12px]",
                      t.on
                        ? "border-(--app-accent) text-(--app-fg)"
                        : "border-transparent text-(--app-muted)",
                    )}
                  >
                    {t.name}
                  </span>
                ))}
                <span className="pb-1 text-[15px] text-(--app-muted)">+</span>
              </div>
              <pre className="min-h-0 flex-1 overflow-hidden px-3 py-2.5 font-mono text-[11.5px] leading-[1.6] whitespace-pre-wrap text-(--app-fg)">
                {!made && (
                  <>
                    <span className="text-(--app-accent)">~</span> ${" "}
                    <span className="animate-pulse">▍</span>
                  </>
                )}
                {(step === "creating" ||
                  step === "ready" ||
                  step === "agentMenu") && (
                  <>
                    <span className="text-(--app-accent)">~</span> ${" "}
                    {createArgs}
                    {"\n"}
                    {createLines.slice(0, Math.max(1, progress)).map((l, i) => (
                      <span key={i} className={cn(i > 0 && "text-(--app-fg2)")}>
                        {l}
                        {"\n"}
                      </span>
                    ))}
                    {step === "creating" ? (
                      <span className="text-(--app-accent)">
                        {spinner[tick % spinner.length]}
                      </span>
                    ) : (
                      <span className="text-(--app-ok)">
                        ✓ {box} is in the sidebar
                      </span>
                    )}
                  </>
                )}
                {step === "running" && (
                  <>
                    <span className="text-(--app-accent)">{box}:~</span> ${" "}
                    {a.name}
                    {"\n"}
                    <span className="text-(--app-fg2)">
                      {a.label} is running on {box}, in the cloud.{"\n"}
                      It signs in here the first time, as on any new machine.
                      {"\n"}
                      Close the laptop: it keeps working.
                    </span>
                  </>
                )}
              </pre>
            </div>
          </div>

          {/* The status bar: where the keyboard is, and its keys. */}
          <div className="flex h-7 items-center gap-3 border-t border-(--app-line) bg-(--app-chrome) px-3 text-[11px] text-(--app-muted)">
            <span
              className={cn(
                "rounded px-1.5 py-px text-[10.5px] font-bold tracking-wider",
                made
                  ? "bg-(--app-ok)/15 text-(--app-ok)"
                  : "bg-(--app-accent)/15 text-(--app-accent)",
              )}
            >
              {made ? "PANE" : "TREE"}
            </span>
            <span className="truncate">
              {made
                ? "⌘D split · ⌘F find · ⌘K go to"
                : "⏎ open · t task · c agent · n terminal"}
            </span>
            {step !== "idle" && (
              <button
                type="button"
                onClick={reset}
                className="ml-auto flex shrink-0 items-center gap-1 hover:text-(--app-fg)"
              >
                <RotateCcwIcon className="size-3" /> again
              </button>
            )}
          </div>

          {/* New Sandbox…, as a sheet from the top of the window. */}
          {step === "sheet" && (
            <div className="absolute inset-0 z-30 flex justify-center bg-black/25">
              <div className="flex max-h-full w-[min(520px,calc(100%-16px))] flex-col overflow-y-auto rounded-b-[10px] border border-t-0 border-(--app-line) bg-(--app-bg) shadow-2xl">
                <div className="flex items-center gap-3 px-5 py-4">
                  <span className="flex size-[34px] shrink-0 items-center justify-center rounded-[9px] bg-(--app-accent) text-white">
                    <Icon name="cloud" size={17} />
                  </span>
                  <div>
                    <p className="text-[15px] font-semibold">New sandbox</p>
                    <p className="text-[12px] text-(--app-muted)">
                      A machine in the cloud with conch installed, added to the
                      sidebar. It runs, and costs, until you stop it.
                    </p>
                  </div>
                </div>
                <div className="flex flex-col gap-4 border-y border-(--app-line) p-5">
                  <p className="text-[10.5px] font-semibold tracking-[0.06em] text-(--app-muted)">
                    PROVIDER
                  </p>
                  <div className="-mt-2.5 flex flex-col gap-1.5">
                    {providers.map((x) => {
                      const on = x.name === choice.provider;
                      return (
                        <button
                          key={x.name}
                          type="button"
                          onClick={() => pickProvider(x.name)}
                          className={cn(
                            "flex items-center gap-[11px] rounded-[10px] px-3 py-2.5 text-left",
                            on
                              ? "bg-(--app-accent)/10 ring-[1.5px] ring-(--app-accent)/70"
                              : "bg-(--app-raise) ring-1 ring-(--app-line)",
                          )}
                        >
                          <span className="flex size-[30px] shrink-0 items-center justify-center rounded-[7px] bg-(--app-raise) text-[13.5px] font-bold text-(--app-fg2) ring-1 ring-(--app-line)">
                            {x.label[0].toUpperCase()}
                          </span>
                          <span className="min-w-0 flex-1">
                            <span className="block text-[13px] font-semibold">
                              {x.label}
                            </span>
                            <span className="flex items-center gap-[5px] text-[11.5px] text-(--app-muted)">
                              <span className="size-1.5 rounded-full bg-(--app-ok)" />{" "}
                              Key set
                            </span>
                          </span>
                          <span
                            className={cn(
                              "flex size-4 items-center justify-center rounded-full",
                              on
                                ? "bg-(--app-accent) text-white"
                                : "ring-1 ring-(--app-line)",
                            )}
                          >
                            {on && (
                              <svg
                                aria-hidden="true"
                                viewBox="0 0 24 24"
                                className="size-3"
                                fill="none"
                                stroke="currentColor"
                                strokeWidth={3}
                              >
                                <path d="m6 12 4 4 8-8" />
                              </svg>
                            )}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                  <p className="text-[10.5px] font-semibold tracking-[0.06em] text-(--app-muted)">
                    SANDBOX
                  </p>
                  <div className="-mt-2.5 rounded-[10px] bg-(--app-raise) ring-1 ring-(--app-line)">
                    <SheetRow label="Name" first>
                      <input
                        value={choice.label}
                        onChange={(e) =>
                          set({
                            label: e.target.value
                              .replace(/[^a-z0-9-]/gi, "")
                              .slice(0, 24),
                          })
                        }
                        placeholder="optional: how the sidebar names it"
                        aria-label="Name"
                        className="h-[26px] w-full rounded-md border border-(--app-line) bg-(--app-well) px-2 text-[12.5px] text-(--app-fg) outline-none placeholder:text-(--app-muted)"
                      />
                    </SheetRow>
                    {p.regions.length > 0 && (
                      <SheetRow label="Region">
                        <select
                          value={choice.region}
                          onChange={(e) => set({ region: e.target.value })}
                          aria-label="Region"
                          className="h-[24px] rounded-md border border-(--app-line) bg-(--app-well) px-1.5 text-[12.5px] text-(--app-fg)"
                        >
                          {p.regions.map((r) => (
                            <option key={r} value={r}>
                              {r}
                            </option>
                          ))}
                        </select>
                      </SheetRow>
                    )}
                    {p.sizes.length > 0 && (
                      <SheetRow label="Size">
                        <span className="inline-flex rounded-md bg-(--app-well) p-0.5 ring-1 ring-(--app-line)">
                          {p.sizes.map((z) => (
                            <button
                              key={z}
                              type="button"
                              onClick={() => set({ size: z })}
                              className={cn(
                                "rounded-[5px] px-2.5 py-0.5 text-[12px] capitalize",
                                choice.size === z
                                  ? "bg-(--app-raise) font-medium shadow-sm ring-1 ring-(--app-line)"
                                  : "text-(--app-fg2)",
                              )}
                            >
                              {z}
                            </button>
                          ))}
                        </span>
                      </SheetRow>
                    )}
                    {(p.limits.cpu || p.limits.memory || p.limits.disk) && (
                      <SheetRow label="Size">
                        {p.limits.cpu && (
                          <NumberField
                            value={choice.cpu}
                            unit="vCPU"
                            limit={p.limits.cpu}
                            onChange={(n) => set({ cpu: n })}
                          />
                        )}
                        {p.limits.memory && (
                          <NumberField
                            value={choice.memory}
                            unit="GiB"
                            limit={p.limits.memory}
                            onChange={(n) => set({ memory: n })}
                          />
                        )}
                        {p.limits.disk && (
                          <NumberField
                            value={choice.disk}
                            unit="GiB disk"
                            limit={p.limits.disk}
                            onChange={(n) => set({ disk: n })}
                          />
                        )}
                      </SheetRow>
                    )}
                  </div>
                  <p className="flex items-start gap-2 rounded-lg bg-(--app-well) p-2.5 text-[11.5px] text-(--app-fg2)">
                    <Icon
                      name="info"
                      size={12}
                      className="mt-0.5 text-(--app-muted)"
                    />
                    <span>
                      ssh reaches it with {p.access}. The key comes from{" "}
                      {p.keyEnv}, set in Settings → Sandboxes.
                    </span>
                  </p>
                </div>
                <div className="flex items-center gap-2.5 px-5 py-3 text-[13px]">
                  <span className="text-[12px] text-(--app-fg2)">
                    Providers ▾
                  </span>
                  <button
                    type="button"
                    onClick={reset}
                    className="ml-auto rounded-md bg-(--app-raise) px-3 py-1 ring-1 ring-(--app-line)"
                  >
                    Cancel
                  </button>
                  <Hint on>
                    <button
                      type="button"
                      onClick={() => go("creating")}
                      className="rounded-md bg-(--app-accent) px-3 py-1 text-white ring-1 ring-red-500/70 hover:opacity-90"
                    >
                      Create on {p.label}
                    </button>
                  </Hint>
                </div>
              </div>
            </div>
          )}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          {step === "idle" &&
            "Click where the red dot is: right-click This Mac, as in Conch.app."}
          {step === "menu" &&
            "New Sandbox… is on every machine's menu, and in File."}
          {step === "sheet" &&
            `${p.label} · ${sizing || "its own size"}. Pick another provider or size, then Create.`}
          {step === "creating" &&
            "conch runs the create in a pane of its own, so the install is watched."}
          {(step === "ready" || step === "agentMenu") &&
            `${box} is a machine now: right-click it to start an agent there.`}
          {step === "running" &&
            `${a.label} runs on ${box}. Again starts over.`}
        </p>
      </div>

      <div className="flex min-w-0 flex-col gap-3">
        <p className="font-mono text-xs text-muted-foreground">
          under the hood · follows what you pick
        </p>
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
          The agent signs in inside the sandbox, as on any new machine — conch
          never copies credentials. To run unattended, pass a token in instead
          {a.token ? <> ({a.token})</> : null}.
        </p>
      </div>
    </div>
  );
}

function Script({ lines }: { lines: string[] }) {
  const text = lines.filter((l) => l && !l.startsWith("#")).join("\n");
  return (
    <TerminalWindow title="sh" className="shadow-none">
      <div className="relative">
        <CopyButton
          value={text}
          className="absolute top-2 right-2 text-term-foreground"
        />
        <pre className="overflow-x-auto px-4 py-3 pr-12 text-[0.75rem] leading-relaxed">
          {lines.map((l, i) => (
            <div key={i} className={cn(l.startsWith("#") && "text-term-muted")}>
              {l || " "}
            </div>
          ))}
        </pre>
      </div>
    </TerminalWindow>
  );
}
