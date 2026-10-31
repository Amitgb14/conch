import { useEffect, useId, useState } from "react"
import { Eye, EyeOff, Plus, RotateCcw, Trash2 } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { parseInts, parseList, parseNumber, validClock } from "@/lib/model.js"
import { cn } from "@/lib/utils"
import type { Field } from "@/types"

// Radix's Select cannot hold "", which is what an unset choice is in
// config.toml, so it stands in for it while the menu is open.
const NONE = "__conch_default__"

type Props = { field: Field; value: any; onChange: (v: any) => void; id?: string }

/** One setting: its label and help to the left, its control to the right or below. */
export function FieldRow({ field, value, onChange, changed }: Props & { changed?: boolean }) {
  const id = useId()
  const wide = ["text", "list", "map", "tables", "keyed"].includes(field.kind) && !(field.kind === "list" && field.options)
  const resettable = field.default !== undefined && value !== undefined
  const label = (
    <div className="min-w-0 flex-1 space-y-1">
      <div className="flex items-center gap-2">
        <Label htmlFor={id} className="text-sm font-medium">{field.label}</Label>
        {changed && <span className="size-1.5 rounded-full bg-brand" title="Changed, not saved yet" />}
        {resettable && (
          <Button variant="ghost" size="icon" className="size-6 text-muted-foreground" title="Back to the default" onClick={() => onChange(undefined)}>
            <RotateCcw className="size-3" />
          </Button>
        )}
      </div>
      {field.help && <p className="text-[13px] leading-snug text-muted-foreground">{field.help}</p>}
      {field.key && <code className="font-mono text-[11px] text-muted-foreground/70">{field.key}</code>}
    </div>
  )
  if (wide) {
    return (
      <div className="space-y-3 py-4">
        {label}
        <Control field={field} value={value} onChange={onChange} id={id} />
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-3 py-4 sm:flex-row sm:items-start sm:justify-between sm:gap-8">
      {label}
      <div className="w-full shrink-0 sm:w-72 sm:pt-0.5">
        <Control field={field} value={value} onChange={onChange} id={id} />
      </div>
    </div>
  )
}

export function Control({ field, value, onChange, id }: Props) {
  switch (field.kind) {
    case "bool":
      return (
        <div className="flex h-9 items-center sm:justify-end">
          <Switch id={id} checked={value ?? (field.default as boolean) ?? false} onCheckedChange={(v) => onChange(v)} />
        </div>
      )
    case "select":
      return <SelectControl field={field} value={value} onChange={onChange} id={id} />
    case "int":
    case "float":
      return <NumberControl field={field} value={value} onChange={onChange} id={id} />
    case "secret":
      return <SecretControl field={field} value={value} onChange={onChange} id={id} />
    case "text":
      return (
        <Textarea id={id} className="min-h-16 font-mono text-[13px]" value={value ?? ""} placeholder={field.placeholder}
          onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)} />
      )
    case "list":
      return field.options ? <ChoiceList field={field} value={value} onChange={onChange} /> : <ListControl field={field} value={value} onChange={onChange} id={id} />
    case "ints":
      return <IntsControl field={field} value={value} onChange={onChange} id={id} />
    case "map":
      return <MapControl field={field} value={value} onChange={onChange} />
    default:
      return <StringControl field={field} value={value} onChange={onChange} id={id} />
  }
}

function StringControl({ field, value, onChange, id }: Props) {
  const clock = field.key === "quiet_start" || field.key === "quiet_end"
  const bad = clock && !validClock(value)
  return (
    <div className="space-y-1">
      <Input id={id} value={value ?? ""} placeholder={field.placeholder} aria-invalid={bad || undefined}
        className={cn(clock && "font-mono")} onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)} />
      {bad && <p className="text-xs text-destructive">A time like 22:00, or empty for never.</p>}
    </div>
  )
}

function SecretControl({ field, value, onChange, id }: Props) {
  const [shown, setShown] = useState(false)
  return (
    <div className="flex gap-2">
      <Input id={id} type={shown ? "text" : "password"} autoComplete="off" spellCheck={false} className="font-mono"
        value={value ?? ""} placeholder={field.placeholder ?? "not set"} onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)} />
      <Button variant="outline" size="icon" type="button" title={shown ? "Hide" : "Show"} onClick={() => setShown(!shown)}>
        {shown ? <EyeOff /> : <Eye />}
      </Button>
    </div>
  )
}

function SelectControl({ field, value, onChange, id }: Props) {
  const options = field.options ?? []
  const current = value ?? ""
  // A value the file has that this build doesn't offer is still shown, so
  // that opening the page doesn't seem to have changed it.
  const all = options.some((o) => o.value === current) ? options : [...options, { value: current, label: `${current || "(empty)"} (from the file)` }]
  return (
    <Select value={current === "" ? NONE : current} onValueChange={(v) => onChange(v === NONE ? "" : v)}>
      <SelectTrigger id={id} className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {all.map((o) => (
          <SelectItem key={o.value || NONE} value={o.value === "" ? NONE : o.value}>{o.label}</SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

/** Text that turns into a value only when it is one, saying why when it isn't. */
function useParsed<T>(value: T, show: (v: T) => string, parse: (t: string) => { value?: any; error?: string }, onChange: (v: any) => void) {
  const [text, setText] = useState(() => show(value))
  const [error, setError] = useState<string>()
  useEffect(() => {
    // Follow a value changed from elsewhere (a discard, a reload), but not
    // the one this field has just sent, which would rewrite "1." as "1".
    const r = parse(text)
    if (r.error || JSON.stringify(r.value) !== JSON.stringify(value)) {
      setText(show(value))
      setError(undefined)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(value)])
  const change = (t: string) => {
    setText(t)
    const r = parse(t)
    setError(r.error)
    if (!r.error) onChange(r.value)
  }
  return { text, error, change }
}

function NumberControl({ field, value, onChange, id }: Props) {
  const p = useParsed(value, (v) => (v == null ? "" : String(v)), (t) => parseNumber(t, { integer: field.kind === "int", min: field.min, max: field.max }), onChange)
  return (
    <div className="space-y-1">
      <Input id={id} inputMode={field.kind === "int" ? "numeric" : "decimal"} className="font-mono" value={p.text}
        placeholder={field.placeholder ?? (field.default != null ? String(field.default) : undefined)} aria-invalid={!!p.error || undefined}
        onChange={(e) => p.change(e.target.value)} />
      {p.error && <p className="text-xs text-destructive">{p.error}</p>}
    </div>
  )
}

function IntsControl({ field, value, onChange, id }: Props) {
  const p = useParsed(value, (v: number[] | undefined) => (v ?? []).join(", "), parseInts, onChange)
  return (
    <div className="space-y-1">
      <Input id={id} className="font-mono" value={p.text} placeholder={field.placeholder} aria-invalid={!!p.error || undefined} onChange={(e) => p.change(e.target.value)} />
      {p.error && <p className="text-xs text-destructive">{p.error}</p>}
    </div>
  )
}

function ListControl({ field, value, onChange, id }: Props) {
  const list: string[] = value ?? []
  const [draft, setDraft] = useState("")
  const add = () => {
    const more = parseList(draft).filter((v) => !list.includes(v))
    if (more.length) onChange([...list, ...more])
    setDraft("")
  }
  return (
    <div className="space-y-2">
      {list.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {list.map((v) => (
            <Badge key={v} variant="secondary" className="gap-1 pr-1 font-mono">
              {v}
              <button type="button" className="rounded-sm p-0.5 hover:bg-background/60" title={`Remove ${v}`} onClick={() => onChange(list.filter((x) => x !== v).length ? list.filter((x) => x !== v) : undefined)}>
                <Trash2 className="size-3" />
              </button>
            </Badge>
          ))}
        </div>
      )}
      <div className="flex gap-2">
        <Input id={id} className="font-mono" value={draft} placeholder={field.placeholder ?? "add…"}
          onChange={(e) => setDraft(e.target.value)} onKeyDown={(e) => e.key === "Enter" && (e.preventDefault(), add())} />
        <Button variant="outline" type="button" onClick={add} disabled={!draft.trim()}>
          <Plus /> Add
        </Button>
      </div>
    </div>
  )
}

/** A list whose items come from a fixed set: a toggle each. */
function ChoiceList({ field, value, onChange }: Props) {
  const list: string[] = value ?? []
  return (
    <div className="flex flex-wrap gap-1.5 sm:justify-end">
      {(field.options ?? []).map((o) => {
        const on = list.includes(o.value)
        return (
          <Button key={o.value} type="button" size="sm" variant={on ? "default" : "outline"} className="h-7 rounded-full px-3"
            onClick={() => {
              const next = on ? list.filter((v) => v !== o.value) : [...list, o.value]
              onChange(next.length ? next : undefined)
            }}>
            {o.label}
          </Button>
        )
      })}
    </div>
  )
}

function MapControl({ field, value, onChange }: Props) {
  const entries = Object.entries((value ?? {}) as Record<string, string>)
  const [k, setK] = useState("")
  const [v, setV] = useState("")
  const set = (next: [string, string][]) => onChange(next.length ? Object.fromEntries(next) : undefined)
  return (
    <div className="space-y-2">
      {entries.length > 0 && (
        <div className="divide-y rounded-md border">
          {entries.map(([key, val], i) => (
            <div key={key} className="flex items-center gap-2 p-2">
              <code className="w-40 shrink-0 truncate font-mono text-xs text-muted-foreground" title={key}>{key}</code>
              <Input className="font-mono" value={val} onChange={(e) => set(entries.map((x, j) => (j === i ? [key, e.target.value] : x)))} />
              <Button variant="ghost" size="icon" title={`Remove ${key}`} onClick={() => set(entries.filter((_, j) => j !== i))}>
                <Trash2 />
              </Button>
            </div>
          ))}
        </div>
      )}
      <div className="flex flex-col gap-2 sm:flex-row">
        <Input className="font-mono sm:w-48" value={k} placeholder={field.keyLabel ?? "key"} onChange={(e) => setK(e.target.value)} />
        <Input className="font-mono" value={v} placeholder={field.placeholder ?? "value"} onChange={(e) => setV(e.target.value)} />
        <Button variant="outline" type="button" disabled={!k.trim() || !v.trim() || entries.some(([x]) => x === k.trim())}
          onClick={() => { set([...entries, [k.trim(), v]]); setK(""); setV("") }}>
          <Plus /> Add
        </Button>
      </div>
    </div>
  )
}
