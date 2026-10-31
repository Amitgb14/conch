import { useEffect, useState } from "react"

import { resolveTheme, themePref } from "@/lib/model.js"

export type ThemePref = "light" | "dark" | "system"

const KEY = "conch-theme"
const media = () => window.matchMedia("(prefers-color-scheme: dark)")

// The choice is this browser's convenience, not a setting of conch's, so
// it lives in localStorage — which may be missing or refuse (a private
// window), in which case the page simply follows the system.
function read(): ThemePref {
  try {
    return themePref(localStorage.getItem(KEY))
  } catch {
    return "system"
  }
}

function write(pref: ThemePref) {
  try {
    if (pref === "system") localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, pref)
  } catch {
    /* kept for this visit only */
  }
}

/** The theme chosen and the one showing, kept on <html> as the dark class. */
export function useTheme() {
  const [pref, setPref] = useState<ThemePref>(read)
  const [systemDark, setSystemDark] = useState(() => media().matches)
  useEffect(() => {
    const mq = media()
    const on = (e: MediaQueryListEvent) => setSystemDark(e.matches)
    mq.addEventListener("change", on)
    return () => mq.removeEventListener("change", on)
  }, [])
  const resolved = resolveTheme(pref, systemDark)
  useEffect(() => {
    document.documentElement.classList.toggle("dark", resolved === "dark")
  }, [resolved])
  const choose = (p: ThemePref) => {
    write(p)
    setPref(p)
  }
  return { pref, resolved, choose }
}
