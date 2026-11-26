// conch-ui's logic that is not drawing: tested with node, imported by the
// views. Plain JavaScript so node runs it as it is.

/** Removed and added lines side by side, paired in order; context on both. */
export function splitRows(lines) {
  const out = []
  let removed = [], added = []
  const flush = () => {
    for (let i = 0; i < Math.max(removed.length, added.length); i++) out.push({ left: removed[i] ?? null, right: added[i] ?? null })
    removed = []
    added = []
  }
  for (const l of lines ?? []) {
    if (l.kind === "removed") removed.push(l)
    else if (l.kind === "added") added.push(l)
    else {
      flush()
      out.push({ left: l, right: l })
    }
  }
  flush()
  return out
}

const byExt = {
  go: "go", js: "js", mjs: "js", cjs: "js", jsx: "js", ts: "js", tsx: "js", swift: "js", rs: "js", c: "js", h: "js",
  cc: "js", cpp: "js", java: "js", kt: "js", cs: "js", py: "py", rb: "py", sh: "shell", bash: "shell", zsh: "shell",
  toml: "py", yaml: "py", yml: "py",
}

/** The highlighter's language for a file, from its name. */
export function language(path) {
  const name = String(path ?? "").split("/").pop() ?? ""
  if (name === "Makefile" || name === "Dockerfile") return "shell"
  const dot = name.lastIndexOf(".")
  if (dot < 0) return "plain"
  return byExt[name.slice(dot + 1).toLowerCase()] ?? "plain"
}

const keywords = {
  go: "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false",
  js: "async await break case catch class const continue default do else enum export extends false finally for from function guard if import in let new nil null return self static struct switch this throw true try typeof var void while yield fn impl pub use mut match func",
  py: "and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return True try while with yield end",
  shell: "if then else elif fi for while do done case esac in function return export local",
}
const sets = Object.fromEntries(Object.entries(keywords).map(([k, v]) => [k, new Set(v.split(" "))]))
const lineComment = { go: "//", js: "//", py: "#", shell: "#" }

/**
 * A line in coloured pieces: keywords, strings, comments and numbers, the
 * rest plain. The pieces always join back to the line. A small lexer, not
 * a parser: a diff shows lines out of context, so a block comment begun
 * on another line is not known here either way.
 */
export function tokens(text, lang) {
  const s = String(text ?? "")
  if (!sets[lang] || s.length > 2000) return s ? [{ k: "plain", s }] : []
  const out = []
  let plain = ""
  const push = (k, v) => {
    if (plain) out.push({ k: "plain", s: plain })
    plain = ""
    out.push({ k, s: v })
  }
  const lc = lineComment[lang]
  let i = 0
  while (i < s.length) {
    const c = s[i]
    if (s.startsWith(lc, i)) { push("com", s.slice(i)); i = s.length; continue }
    if (lang === "js" && s.startsWith("/*", i)) {
      const end = s.indexOf("*/", i + 2)
      const j = end < 0 ? s.length : end + 2
      push("com", s.slice(i, j)); i = j; continue
    }
    if (c === '"' || c === "'" || c === "`") {
      let j = i + 1
      while (j < s.length && s[j] !== c) j += s[j] === "\\" ? 2 : 1
      j = Math.min(j + 1, s.length)
      push("str", s.slice(i, j)); i = j; continue
    }
    if (/[0-9]/.test(c) && !/[A-Za-z0-9_]/.test(s[i - 1] ?? "")) {
      const m = /^[0-9][0-9a-fA-FxX._]*/.exec(s.slice(i))[0]
      push("num", m); i += m.length; continue
    }
    if (/[A-Za-z_$]/.test(c)) {
      const m = /^[A-Za-z_$][A-Za-z0-9_$]*/.exec(s.slice(i))[0]
      if (sets[lang].has(m)) push("kw", m)
      else plain += m
      i += m.length
      continue
    }
    plain += c
    i++
  }
  if (plain) out.push({ k: "plain", s: plain })
  return out
}

/** passed of total as a whole percent, kept to 0…100. */
export function percent(passed, total) {
  if (!total || !(passed > 0)) return 0
  return Math.max(0, Math.min(100, Math.round((passed / total) * 100)))
}

const isWaiting = (a) => a.running && a.state === "blocked"
const isWorking = (a) => a.running && a.state === "working"
const isDone = (a) => a.running && a.state === "done"

/** How many agents there are, and how many wait, work or are done. */
export function agentCounts(agents) {
  const all = agents ?? []
  return { all: all.length, waiting: all.filter(isWaiting).length, working: all.filter(isWorking).length, done: all.filter(isDone).length }
}

/** The agents a filter keeps: all, waiting, working or done. */
export function filterAgents(agents, filter) {
  const all = agents ?? []
  const f = { waiting: isWaiting, working: isWorking, done: isDone }[filter]
  return f ? all.filter(f) : all
}
