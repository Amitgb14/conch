import Link from "next/link"

import { ConchIcon } from "@/components/conch-icon"
import { cn } from "@/lib/utils"

export function Logo({ className }: { className?: string }) {
  return (
    <Link href="/" className={cn("flex items-center gap-2 text-[0.95rem]", className)}>
      <ConchIcon className="size-6 text-brand" />
      <span className="font-mono font-semibold tracking-tight">conch</span>
    </Link>
  )
}
