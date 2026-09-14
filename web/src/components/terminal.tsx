import { CopyButton } from "@/components/copy-button"
import { cn } from "@/lib/utils"

export function TerminalWindow({
  title,
  children,
  className,
  bodyClassName,
}: {
  title?: string
  children: React.ReactNode
  className?: string
  bodyClassName?: string
}) {
  return (
    <div
      className={cn(
        "overflow-hidden rounded-xl border border-term-border bg-term text-term-foreground shadow-[0_24px_60px_-20px_rgb(0_0_0/0.35)]",
        className,
      )}
    >
      <div className="flex items-center gap-2 border-b border-term-border px-3.5 py-2.5">
        <span className="flex gap-1.5">
          <i className="size-2.5 rounded-full bg-white/15" />
          <i className="size-2.5 rounded-full bg-white/15" />
          <i className="size-2.5 rounded-full bg-white/15" />
        </span>
        {title && <span className="ml-2 truncate font-mono text-[0.7rem] text-term-muted">{title}</span>}
      </div>
      <div className={cn("font-mono text-[0.8rem] leading-relaxed", bodyClassName)}>{children}</div>
    </div>
  )
}

// A single shell command with a prompt and a copy button.
export function CommandLine({ command, className }: { command: string; className?: string }) {
  return (
    <div className={cn("flex items-center gap-2 px-4 py-3", className)}>
      <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap">
        <span className="text-term-muted select-none">$ </span>
        {command}
      </code>
      <CopyButton value={command} className="text-term-foreground" />
    </div>
  )
}
