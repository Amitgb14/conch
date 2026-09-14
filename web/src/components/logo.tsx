import Link from "next/link"

import { cn } from "@/lib/utils"

export function Logo({ className }: { className?: string }) {
  return (
    <Link href="/" className={cn("flex items-center gap-2 text-[0.95rem]", className)}>
      <span
        aria-hidden
        className="grid size-6 place-items-center rounded-md bg-primary font-mono text-xs font-bold text-primary-foreground"
      >
        ▸
      </span>
      <span className="font-mono font-semibold tracking-tight">conch</span>
    </Link>
  )
}
