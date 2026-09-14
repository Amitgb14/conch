import { InfoIcon, TriangleAlertIcon } from "lucide-react"

import { cn } from "@/lib/utils"

export function Callout({
  type = "note",
  title,
  children,
}: {
  type?: "note" | "warning"
  title?: string
  children: React.ReactNode
}) {
  const Icon = type === "warning" ? TriangleAlertIcon : InfoIcon
  return (
    <div
      className={cn(
        "my-6 flex gap-3 rounded-xl border px-4 py-3 text-sm [&_p]:my-0",
        type === "warning" ? "border-waiting/30 bg-waiting/5" : "border-brand/25 bg-brand-soft/60",
      )}
    >
      <Icon className={cn("mt-0.5 size-4 shrink-0", type === "warning" ? "text-waiting" : "text-brand")} />
      <div className="flex flex-col gap-1">
        {title && <p className="font-medium">{title}</p>}
        <div className="text-muted-foreground [&_strong]:text-foreground">{children}</div>
      </div>
    </div>
  )
}
