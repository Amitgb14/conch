// The page's logic, kept apart from React so node can test it on its own
// (model.test.mjs, run by the Go test TestUILogic). Everything here works
// on config.toml as TOML reads it: tables by their keys.

/** The token handed out in the address's fragment, which a browser never sends. */
export function tokenFrom(hash) {
  const m = /(?:^#|&)t=([0-9a-f]+)/.exec(hash || "")
  return m ? m[1] : ""
}

/** The value at a section's table and a key in it; "" is the top level. */
export function getValue(cfg, table, key) {
  const t = table ? cfg?.[table] : cfg
  return t == null ? undefined : t[key]
}

/** A copy of cfg with one value set; undefined removes the key. */
export function setValue(cfg, table, key, value) {
  const next = { ...cfg }
  if (!table) {
    if (value === undefined) delete next[key]
    else next[key] = value
    return next
  }
  const t = { ...(next[table] ?? {}) }
  if (key === "") {
    // A keyed section is the whole table: [sandbox.daytona] and the rest.
    next[table] = value ?? {}
    return next
  }
  if (value === undefined) delete t[key]
  else t[key] = value
  next[table] = t
  return next
}

/** Whether two values are the same, as JSON says it. Key order does not count. */
export function same(a, b) {
  return canonical(a) === canonical(b)
}

function canonical(v) {
  if (Array.isArray(v)) return "[" + v.map(canonical).join(",") + "]"
  if (v && typeof v === "object") {
    return "{" + Object.keys(v).filter((k) => v[k] !== undefined).sort().map((k) => JSON.stringify(k) + ":" + canonical(v[k])).join(",") + "}"
  }
  return JSON.stringify(v ?? null)
}

/** The sections whose values differ between what was read and what is shown. */
export function changedSections(schema, saved, draft) {
  const out = []
  for (const s of schema ?? []) {
    const keys = s.key ? [s.key] : s.fields.map((f) => f.key)
    if (keys.some((k) => !same(saved?.[k], draft?.[k]))) out.push(s.id)
  }
  return out
}

/** A list typed as text, split on commas and new lines, blanks dropped. */
export function parseList(text) {
  return String(text ?? "")
    .split(/[,\n]/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/** Whole numbers from text: what isn't one is reported, not dropped. */
export function parseInts(text) {
  const parts = parseList(text)
  const bad = parts.filter((p) => !/^-?\d+$/.test(p))
  if (bad.length) return { error: `${bad.join(", ")} isn't a whole number` }
  return { value: parts.map(Number) }
}

/**
 * A number field's text as a value: "" is undefined (the key left out),
 * and what isn't a number, or is outside min and max, is an error.
 */
export function parseNumber(text, { integer = false, min, max } = {}) {
  const t = String(text ?? "").trim()
  if (t === "") return { value: undefined }
  const n = Number(t)
  if (!Number.isFinite(n)) return { error: `${t} isn't a number` }
  if (integer && !Number.isInteger(n)) return { error: `${t} isn't a whole number` }
  if (min != null && n < min) return { error: `at least ${min}` }
  if (max != null && n > max) return { error: `at most ${max}` }
  return { value: n }
}

/** Whether a "HH:MM" clock is one; empty is allowed, meaning never. */
export function validClock(text) {
  if (!text) return true
  const m = /^(\d{1,2}):(\d{2})$/.exec(String(text).trim())
  return !!m && Number(m[1]) <= 23 && Number(m[2]) <= 59
}

/** A new name for a keyed table that doesn't take one already there. */
export function freshName(existing, base) {
  const have = new Set(Object.keys(existing ?? {}))
  if (!have.has(base)) return base
  for (let i = 2; ; i++) if (!have.has(`${base}-${i}`)) return `${base}-${i}`
}

/** A copy of a keyed table with one entry renamed, keeping the order. */
export function renameKey(obj, from, to) {
  if (from === to || !to || Object.hasOwn(obj ?? {}, to)) return obj
  const out = {}
  for (const [k, v] of Object.entries(obj ?? {})) out[k === from ? to : k] = v
  return out
}

/** A copy of a list with item i moved by delta, clamped to the ends. */
export function move(list, i, delta) {
  const j = Math.max(0, Math.min(list.length - 1, i + delta))
  if (i === j || i < 0 || i >= list.length) return list
  const out = list.slice()
  const [it] = out.splice(i, 1)
  out.splice(j, 0, it)
  return out
}

/** Which sections match a search, by their titles and their fields' labels and keys. */
export function searchSections(schema, query) {
  const q = String(query ?? "").trim().toLowerCase()
  if (!q) return (schema ?? []).map((s) => s.id)
  const hit = (f) =>
    [f.label, f.key, f.help].some((t) => t && t.toLowerCase().includes(q)) || (f.fields ?? []).some(hit)
  return (schema ?? []).filter((s) => [s.title, s.description, s.key].some((t) => t && t.toLowerCase().includes(q)) || s.fields.some(hit)).map((s) => s.id)
}
