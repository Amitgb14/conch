"use client"

import { CheckIcon, CopyIcon } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

export function CopyButton({
  value,
  getValue,
  className,
}: {
  value?: string
  getValue?: () => string
  className?: string
}) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={copied ? "Copied" : "Copy"}
      className={cn("text-current opacity-70 hover:opacity-100", className)}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value ?? getValue?.() ?? "")
          setCopied(true)
          setTimeout(() => setCopied(false), 1500)
        } catch {
          // Clipboard access denied; the text is still selectable.
        }
      }}
    >
      {copied ? <CheckIcon /> : <CopyIcon />}
    </Button>
  )
}
