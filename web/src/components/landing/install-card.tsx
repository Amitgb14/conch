import { CommandLine, TerminalWindow } from "@/components/terminal"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { site } from "@/lib/site"

const methods = [
  {
    value: "script",
    label: "Install script",
    commands: [site.installCommand],
    note: "Downloads the latest release, verifies its checksum and installs ~/.local/bin/conch.",
  },
  {
    value: "go",
    label: "Go",
    commands: [site.goInstallCommand],
    note: "Needs Go 1.25 or newer.",
  },
  {
    value: "source",
    label: "From source",
    commands: [`git clone ${site.repo}`, "cd conch && make build"],
    note: "Builds bin/conch. make test runs the test suite with the race detector.",
  },
]

const then = [
  { command: "conch", note: "open the TUI — starts the server on first run" },
  { command: 'conch task -cwd ~/src/api "Add a health check"', note: "a branch, a worktree and an agent" },
  { command: "conch update", note: "move to the newest release later" },
]

export function InstallCard() {
  return (
    <div id="install" className="w-full scroll-mt-20 rounded-2xl border bg-card p-2 shadow-sm">
      <Tabs defaultValue="script" className="gap-0">
        <div className="flex flex-wrap items-center justify-between gap-2 px-2 pt-1 pb-3">
          <TabsList>
            {methods.map((m) => (
              <TabsTrigger key={m.value} value={m.value} className="px-2.5">
                {m.label}
              </TabsTrigger>
            ))}
          </TabsList>
          <Badge variant="outline" className="font-mono">
            macOS · Linux
          </Badge>
        </div>
        {methods.map((m) => (
          <TabsContent key={m.value} value={m.value}>
            <TerminalWindow title={`Terminal — ${m.label}`}>
              {m.commands.map((c) => (
                <CommandLine key={c} command={c} className="not-last:pb-0" />
              ))}
            </TerminalWindow>
            <p className="px-2 pt-3 text-xs text-muted-foreground">{m.note}</p>
          </TabsContent>
        ))}
      </Tabs>
      <div className="mt-3 border-t px-2 pt-3 pb-1">
        <p className="mb-2 font-mono text-xs text-muted-foreground">then</p>
        <ul className="flex flex-col gap-1.5">
          {then.map((t) => (
            <li key={t.command} className="flex flex-col gap-x-3 text-sm sm:flex-row sm:items-baseline">
              <code className="font-mono text-[0.8rem] break-all sm:whitespace-nowrap">{t.command}</code>
              <span className="text-xs text-muted-foreground">{t.note}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
