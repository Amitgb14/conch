import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  AlertTriangle, Bell, Bot, Box, FileCode2, GitMerge, Keyboard, Loader2, Lock, Palette, RefreshCw, Save, Search, Server,
  ShieldCheck, Smartphone, Sparkles, SquareTerminal, Terminal, Undo2, WifiOff, Zap, type LucideIcon,
} from "lucide-react"
import { toast } from "sonner"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { Toaster } from "@/components/ui/sonner"
import { FieldRow } from "@/components/fields"
import { KeyedEditor, TablesEditor } from "@/components/tables"
import { ApiError, load, saveConfig, saveRaw } from "@/api"
import { changedSections, getValue, same, searchSections, setValue } from "@/lib/model.js"
import { useTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"
import { ThemeSwitch } from "@/components/theme-switch"
import type { Cfg, Payload, Section } from "@/types"

const icons: Record<string, LucideIcon> = {
  palette: Palette, keyboard: Keyboard, bell: Bell, bot: Bot, "shield-check": ShieldCheck, "git-merge": GitMerge,
  sparkles: Sparkles, terminal: Terminal, "square-terminal": SquareTerminal, server: Server, "refresh-cw": RefreshCw,
  smartphone: Smartphone, box: Box, zap: Zap, lock: Lock,
}

export default function App() {
  const theme = useTheme()
  const [data, setData] = useState<Payload>()
  const [draft, setDraft] = useState<Cfg>()
  const [raw, setRaw] = useState("")
  const [tab, setTab] = useState("form")
  const [query, setQuery] = useState("")
  const [busy, setBusy] = useState(false)
  const [fatal, setFatal] = useState<string>()
  const [active, setActive] = useState<string>()

  const take = useCallback((p: Payload) => {
    setData(p)
    setDraft(p.config)
    setRaw(p.raw)
  }, [])

  useEffect(() => {
    load().then(take).catch((e: ApiError) => setFatal(e.message))
  }, [take])

  const changed = useMemo(() => (data && draft ? changedSections(data.schema, data.config, draft) : []), [data, draft])
  const rawChanged = !!data && raw !== data.raw
  const dirty = tab === "raw" ? rawChanged : changed.length > 0
  const visible = useMemo(() => new Set(searchSections(data?.schema, query)), [data, query])

  const save = useCallback(async () => {
    if (!data || !draft || busy || !dirty) return
    setBusy(true)
    try {
      const p = tab === "raw" ? await saveRaw(data.version, raw) : await saveConfig(data.version, draft)
      take(p)
      toast.success("Saved to config.toml", { description: "conch has it: the TUI uses it now." })
    } catch (e) {
      const err = e as ApiError
      if (err.status === 409 && err.current) {
        toast.error("config.toml changed underneath", {
          description: "Something else wrote it since this page read it. Reload to see that version: your edits here are dropped.",
          action: { label: "Reload", onClick: () => take(err.current!) },
          duration: 15000,
        })
      } else {
        toast.error("Not saved", { description: err.message })
      }
    } finally {
      setBusy(false)
    }
  }, [data, draft, raw, tab, busy, dirty, take])

  const discard = () => data && take(data)

  // ⌘S / ctrl+S saves, as an editor would.
  const saveRef = useRef(save)
  saveRef.current = save
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault()
        void saveRef.current()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  // Leaving with unsaved changes asks first.
  useEffect(() => {
    const onLeave = (e: BeforeUnloadEvent) => {
      if (dirty) e.preventDefault()
    }
    window.addEventListener("beforeunload", onLeave)
    return () => window.removeEventListener("beforeunload", onLeave)
  }, [dirty])

  // Which section is on screen, for the sidebar.
  useEffect(() => {
    if (tab !== "form" || !data) return
    const obs = new IntersectionObserver(
      (entries) => {
        const top = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0]
        if (top) setActive(top.target.id.replace(/^s-/, ""))
      },
      { rootMargin: "-80px 0px -60% 0px" },
    )
    data.schema.forEach((s) => {
      const el = document.getElementById("s-" + s.id)
      if (el) obs.observe(el)
    })
    return () => obs.disconnect()
  }, [data, tab, query])

  if (fatal) {
    return (
      <div className="flex min-h-svh items-center justify-center p-6">
        <Card className="max-w-md">
          <CardHeader>
            <CardTitle className="flex items-center gap-2"><WifiOff className="size-5" /> Can't reach conch</CardTitle>
            <CardDescription>{fatal}</CardDescription>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              In conch, open Settings (<kbd className="rounded border px-1 font-mono text-xs">,</kbd>) and choose <b>Edit in browser</b>, or run{" "}
              <code className="font-mono text-xs">conch config web</code>.
            </p>
          </CardContent>
        </Card>
      </div>
    )
  }
  if (!data || !draft) {
    return (
      <div className="flex min-h-svh items-center justify-center text-muted-foreground">
        <Loader2 className="mr-2 size-4 animate-spin" /> Reading config.toml…
      </div>
    )
  }

  const shown = data.schema.filter((s) => visible.has(s.id))

  return (
    <div className="min-h-svh">
      <Toaster position="bottom-right" richColors closeButton theme={theme.resolved} />
      <header className="sticky top-0 z-20 border-b bg-background/80 backdrop-blur supports-[backdrop-filter]:bg-background/60">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4">
          <img src="./icon.svg" alt="" className="size-7 shrink-0" />
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline gap-1.5 text-[0.95rem] leading-tight">
              <span className="font-mono font-semibold tracking-tight">conch</span>
              <span className="hidden text-muted-foreground sm:inline">settings</span>
            </div>
            <div className="hidden truncate font-mono text-[11px] text-muted-foreground sm:block" title={data.path}>
              {data.path}
              {!data.exists && " · not written yet: the defaults"}
            </div>
          </div>
          <div className="ml-auto flex shrink-0 items-center gap-1.5 sm:gap-2">
            <ThemeSwitch pref={theme.pref} onChange={theme.choose} />
            {dirty && (
              <Badge variant="outline" className="hidden border-brand/50 text-brand sm:inline-flex">
                {tab === "raw" ? "Unsaved edits" : `${changed.length} section${changed.length === 1 ? "" : "s"} changed`}
              </Badge>
            )}
            <Button variant="ghost" size="sm" onClick={discard} disabled={!dirty || busy}>
              <Undo2 /> <span className="hidden sm:inline">Discard</span>
            </Button>
            <Button size="sm" onClick={save} disabled={!dirty || busy} title="Save (⌘S)">
              {busy ? <Loader2 className="animate-spin" /> : <Save />} Save
            </Button>
          </div>
        </div>
      </header>

      <Tabs value={tab} onValueChange={(t) => {
        if (dirty && !confirm("Switch views and drop the changes not saved here?")) return
        discard()
        setTab(t)
      }} className="mx-auto max-w-6xl gap-0 px-4">
        <div className="flex flex-col gap-3 py-4 sm:flex-row sm:items-center">
          <TabsList>
            <TabsTrigger value="form">Settings</TabsTrigger>
            <TabsTrigger value="raw"><FileCode2 className="size-3.5" /> config.toml</TabsTrigger>
          </TabsList>
          {tab === "form" && (
            <div className="relative sm:ml-auto sm:w-72">
              <Search className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground" />
              <Input className="pl-8" placeholder="Search settings…" value={query} onChange={(e) => setQuery(e.target.value)} />
            </div>
          )}
        </div>

        {data.problems && data.problems.length > 0 && (
          <Alert variant="destructive" className="mb-4">
            <AlertTriangle />
            <AlertTitle>conch passes over some of this</AlertTitle>
            <AlertDescription>
              <ul className="list-disc space-y-0.5 pl-4">
                {data.problems.map((p) => <li key={p}>{p}</li>)}
              </ul>
            </AlertDescription>
          </Alert>
        )}

        <TabsContent value="form" className="pb-24">
          <div className="grid gap-8 md:grid-cols-[200px_1fr]">
            <nav className="sticky top-20 hidden max-h-[calc(100svh-6rem)] self-start overflow-y-auto md:block">
              <ul className="space-y-0.5">
                {shown.map((s) => {
                  const Icon = icons[s.icon] ?? Box
                  return (
                    <li key={s.id}>
                      <a href={"#s-" + s.id}
                        onClick={(e) => { e.preventDefault(); document.getElementById("s-" + s.id)?.scrollIntoView({ behavior: "smooth", block: "start" }) }}
                        className={cn("flex items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                          active === s.id && "bg-accent text-foreground")}>
                        <Icon className="size-4" />
                        <span className="truncate">{s.title}</span>
                        {changed.includes(s.id) && <span className="ml-auto size-1.5 rounded-full bg-brand" />}
                      </a>
                    </li>
                  )
                })}
              </ul>
            </nav>
            <div className="min-w-0 space-y-6">
              {shown.length === 0 && <p className="py-12 text-center text-sm text-muted-foreground">No setting matches “{query}”.</p>}
              {shown.map((s) => (
                <SectionCard key={s.id} section={s} draft={draft} saved={data.config} changed={changed.includes(s.id)} onChange={setDraft} />
              ))}
            </div>
          </div>
        </TabsContent>

        <TabsContent value="raw" className="space-y-3 pb-24">
          <p className="text-sm text-muted-foreground">
            The file as it is. Saved as typed, comments and all, once conch has read it without an error. Saving from the Settings view rewrites it without comments.
          </p>
          <Textarea spellCheck={false} className="min-h-[65svh] font-mono text-[13px] leading-relaxed" value={raw} onChange={(e) => setRaw(e.target.value)} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function SectionCard({ section, draft, saved, changed, onChange }: {
  section: Section
  draft: Cfg
  saved: Cfg
  changed: boolean
  onChange: (fn: (c: Cfg) => Cfg) => void
}) {
  const Icon = icons[section.icon] ?? Box
  return (
    <Card id={"s-" + section.id} className="scroll-mt-20 gap-0 py-0">
      <CardHeader className="border-b py-4 [.border-b]:pb-4">
        <CardTitle className="flex items-center gap-2 text-base">
          <span className="flex size-7 items-center justify-center rounded-md bg-brand/15 text-brand"><Icon className="size-4" /></span>
          {section.title}
          {section.key && <code className="font-mono text-xs font-normal text-muted-foreground">[{section.key}]</code>}
          {changed && <Badge variant="outline" className="ml-auto border-brand/50 text-brand">changed</Badge>}
        </CardTitle>
        <CardDescription>{section.description}</CardDescription>
      </CardHeader>
      <CardContent className="divide-y">
        {section.fields.map((f) => {
          const v = f.key === "" ? draft[section.key] : getValue(draft, section.key, f.key)
          const set = (nv: unknown) => onChange((c) => setValue(c, section.key, f.key, nv))
          if (f.kind === "tables") {
            return (
              <div key={f.key} className="py-4">
                <TablesEditor field={f} value={v} onChange={set} />
              </div>
            )
          }
          if (f.kind === "keyed") {
            return (
              <div key={f.key || "keyed"} className="py-4">
                <KeyedEditor field={f} value={v} onChange={set} />
              </div>
            )
          }
          const was = f.key === "" ? saved[section.key] : getValue(saved, section.key, f.key)
          return <FieldRow key={f.key} field={f} value={v} onChange={set} changed={!same(was, v)} />
        })}
      </CardContent>
    </Card>
  )
}
