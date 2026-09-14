"use client"

import { useRef } from "react"

import { CopyButton } from "@/components/copy-button"
import { cn } from "@/lib/utils"

// Replaces <pre> in MDX: a dark terminal-style block with a copy button.
export function CodeBlock({ className, children, ...props }: React.ComponentProps<"pre">) {
  const ref = useRef<HTMLPreElement>(null)
  return (
    <div className="group relative my-5 overflow-hidden rounded-xl border border-term-border bg-term text-term-foreground">
      <pre
        ref={ref}
        className={cn(
          "overflow-x-auto py-3.5 pr-12 pl-4 font-mono text-[0.8rem] leading-relaxed [&>code]:rounded-none [&>code]:border-0 [&>code]:bg-transparent [&>code]:p-0 [&>code]:text-[length:inherit] [&>code]:text-inherit",
          className,
        )}
        {...props}
      >
        {children}
      </pre>
      <CopyButton
        getValue={() => ref.current?.innerText ?? ""}
        className="absolute top-2 right-2 opacity-0 group-hover:opacity-70 focus-visible:opacity-100"
      />
    </div>
  )
}
