// Numbered steps for guides. Each <Step title="…"> gets a counter bubble.
export function Steps({ children }: { children: React.ReactNode }) {
  return <div className="my-6 ml-3 border-l pl-7 [counter-reset:step]">{children}</div>
}

export function Step({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="relative pb-6 last:pb-0 [counter-increment:step]">
      <span className="absolute top-0 -left-[2.65rem] grid size-7 place-items-center rounded-full border bg-background font-mono text-xs text-muted-foreground before:content-[counter(step,decimal-leading-zero)]" />
      <h3 className="mt-0 mb-2 text-base font-semibold">{title}</h3>
      <div className="[&>*:first-child]:mt-0">{children}</div>
    </div>
  )
}
