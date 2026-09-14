import { cn } from "@/lib/utils"

export function Section({
  id,
  label,
  title,
  lede,
  children,
  className,
}: {
  id?: string
  label: string
  title: React.ReactNode
  lede?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <section id={id} className={cn("scroll-mt-14 border-t", className)}>
      <div className="mx-auto w-full max-w-6xl px-5 py-20 sm:px-6 lg:py-24">
        <p className="font-mono text-xs text-muted-foreground">{label}</p>
        <h2 className="mt-3 max-w-3xl text-3xl font-semibold tracking-[-0.025em] text-balance sm:text-4xl">{title}</h2>
        {lede && <p className="mt-4 max-w-2xl text-pretty text-muted-foreground sm:text-lg">{lede}</p>}
        <div className="mt-12">{children}</div>
      </div>
    </section>
  )
}
