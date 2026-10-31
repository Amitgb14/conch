import { Monitor, Moon, Sun } from "lucide-react"

import type { ThemePref } from "@/lib/theme"
import { cn } from "@/lib/utils"

const choices: { value: ThemePref; label: string; Icon: typeof Sun }[] = [
  { value: "light", label: "Light", Icon: Sun },
  { value: "system", label: "System", Icon: Monitor },
  { value: "dark", label: "Dark", Icon: Moon },
]

/** Light, system or dark: three buttons, the chosen one pressed. */
export function ThemeSwitch({ pref, onChange }: { pref: ThemePref; onChange: (p: ThemePref) => void }) {
  return (
    <div role="radiogroup" aria-label="Theme" className="flex items-center rounded-md border p-0.5">
      {choices.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={pref === value}
          title={label}
          onClick={() => onChange(value)}
          className={cn(
            "flex size-7 items-center justify-center rounded-[5px] text-muted-foreground transition-colors hover:text-foreground",
            pref === value && "bg-accent text-foreground shadow-xs",
          )}
        >
          <Icon className="size-3.5" />
          <span className="sr-only">{label}</span>
        </button>
      ))}
    </div>
  )
}
