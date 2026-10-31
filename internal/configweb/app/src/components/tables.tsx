import { useState } from "react"
import { ArrowDown, ArrowUp, Check, ChevronRight, Pencil, Plus, Trash2 } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FieldRow } from "@/components/fields"
import { freshName, move, renameKey } from "@/lib/model.js"
import { cn } from "@/lib/utils"
import type { Field } from "@/types"

type Item = Record<string, any>

function setIn(item: Item, key: string, v: unknown): Item {
  const next = { ...item }
  if (v === undefined) delete next[key]
  else next[key] = v
  return next
}

/** What an entry is called in its collapsed row: its first field that says. */
function summary(field: Field, item: Item): string {
  const [first, second] = field.fields ?? []
  const a = first ? item[first.key] : undefined
  const b = second ? item[second.key] : undefined
  return [a, b].filter((x) => typeof x === "string" && x).join(" · ")
}

/** An array of tables — [[actions]], [[permissions]] — as cards that open. */
export function TablesEditor({ field, value, onChange }: { field: Field; value: Item[] | undefined; onChange: (v: Item[] | undefined) => void }) {
  const list = value ?? []
  const [open, setOpen] = useState<number | null>(null)
  const set = (next: Item[]) => onChange(next.length ? next : undefined)
  return (
    <div className="space-y-2 pt-2">
      {list.length === 0 && (
        <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">None yet.</div>
      )}
      {list.map((item, i) => {
        const isOpen = open === i
        return (
          <div key={i} className={cn("rounded-lg border bg-background/40 transition-colors", isOpen && "border-ring/40")}>
            <div className="flex items-center gap-2 px-3 py-2">
              <button type="button" className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={() => setOpen(isOpen ? null : i)}>
                <ChevronRight className={cn("size-4 shrink-0 text-muted-foreground transition-transform", isOpen && "rotate-90")} />
                <span className="w-6 shrink-0 font-mono text-xs text-muted-foreground">{i + 1}</span>
                <span className="truncate text-sm">{summary(field, item) || <span className="text-muted-foreground italic">empty</span>}</span>
                {typeof item.action === "string" && (
                  <Badge variant={item.action === "deny" ? "destructive" : "secondary"} className="ml-1">{item.action}</Badge>
                )}
              </button>
              <Button variant="ghost" size="icon" className="size-7" title="Move up" disabled={i === 0} onClick={() => { set(move(list, i, -1)); setOpen(null) }}>
                <ArrowUp />
              </Button>
              <Button variant="ghost" size="icon" className="size-7" title="Move down" disabled={i === list.length - 1} onClick={() => { set(move(list, i, 1)); setOpen(null) }}>
                <ArrowDown />
              </Button>
              <Button variant="ghost" size="icon" className="size-7 text-muted-foreground hover:text-destructive" title="Remove" onClick={() => { set(list.filter((_, j) => j !== i)); setOpen(null) }}>
                <Trash2 />
              </Button>
            </div>
            {isOpen && (
              <div className="divide-y border-t px-4">
                {(field.fields ?? []).map((f) => (
                  <FieldRow key={f.key} field={f} value={item[f.key]} onChange={(v) => set(list.map((x, j) => (j === i ? setIn(x, f.key, v) : x)))} />
                ))}
              </div>
            )}
          </div>
        )
      })}
      <Button variant="outline" size="sm" onClick={() => { set([...list, {}]); setOpen(list.length) }}>
        <Plus /> Add {field.label.toLowerCase().replace(/s$/, "")}
      </Button>
    </div>
  )
}

/** Tables keyed by name — [sandbox.daytona] — each a card of its own. */
export function KeyedEditor({ field, value, onChange }: { field: Field; value: Record<string, Item> | undefined; onChange: (v: Record<string, Item> | undefined) => void }) {
  const obj = value ?? {}
  const names = Object.keys(obj)
  const [open, setOpen] = useState<string | null>(names.length === 1 ? names[0] : null)
  const [renaming, setRenaming] = useState<{ from: string; to: string } | null>(null)
  const set = (next: Record<string, Item>) => onChange(Object.keys(next).length ? next : undefined)
  const add = (name: string) => {
    const n = freshName(obj, name)
    set({ ...obj, [n]: {} })
    setOpen(n)
  }
  const missing = (field.suggest ?? []).filter((s) => !names.includes(s))
  return (
    <div className="space-y-2 pt-2">
      {names.length === 0 && (
        <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">No {field.keyLabel?.toLowerCase() ?? "entry"} set up yet.</div>
      )}
      {names.map((name) => {
        const isOpen = open === name
        const known = (field.suggest ?? []).includes(name)
        return (
          <div key={name} className={cn("rounded-lg border bg-background/40", isOpen && "border-ring/40")}>
            <div className="flex items-center gap-2 px-3 py-2">
              {renaming?.from === name ? (
                <form className="flex flex-1 items-center gap-2" onSubmit={(e) => { e.preventDefault(); const to = renaming.to.trim(); set(renameKey(obj, name, to)); if (open === name) setOpen(to); setRenaming(null) }}>
                  <Input autoFocus className="h-8 font-mono" value={renaming.to} onChange={(e) => setRenaming({ from: name, to: e.target.value })} />
                  <Button size="icon" className="size-8" type="submit" title="Rename" disabled={!renaming.to.trim() || (renaming.to.trim() !== name && Object.hasOwn(obj, renaming.to.trim()))}><Check /></Button>
                </form>
              ) : (
                <button type="button" className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={() => setOpen(isOpen ? null : name)}>
                  <ChevronRight className={cn("size-4 shrink-0 text-muted-foreground transition-transform", isOpen && "rotate-90")} />
                  <span className="font-mono text-sm">{name}</span>
                  {!known && <Badge variant="outline" className="text-muted-foreground">not known to this build</Badge>}
                </button>
              )}
              {renaming?.from !== name && (
                <Button variant="ghost" size="icon" className="size-7" title="Rename" onClick={() => setRenaming({ from: name, to: name })}><Pencil /></Button>
              )}
              <Button variant="ghost" size="icon" className="size-7 text-muted-foreground hover:text-destructive" title="Remove"
                onClick={() => set(Object.fromEntries(Object.entries(obj).filter(([k]) => k !== name)))}>
                <Trash2 />
              </Button>
            </div>
            {isOpen && (
              <div className="divide-y border-t px-4">
                {(field.fields ?? []).map((f) => (
                  <FieldRow key={f.key} field={f} value={obj[name]?.[f.key]} onChange={(v) => set({ ...obj, [name]: setIn(obj[name] ?? {}, f.key, v) })} />
                ))}
              </div>
            )}
          </div>
        )
      })}
      <div className="flex flex-wrap gap-2">
        {missing.map((s) => (
          <Button key={s} variant="outline" size="sm" onClick={() => add(s)}><Plus /> {s}</Button>
        ))}
        {missing.length === 0 && (
          <Button variant="outline" size="sm" onClick={() => add("provider")}><Plus /> Add {field.keyLabel?.toLowerCase() ?? "entry"}</Button>
        )}
      </div>
    </div>
  )
}
