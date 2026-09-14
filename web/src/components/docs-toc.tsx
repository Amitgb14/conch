"use client"

import { usePathname } from "next/navigation"
import { useEffect, useState } from "react"

import { cn } from "@/lib/utils"

type Heading = { id: string; text: string; level: number }

// "On this page": read the h2/h3 headings rehype-slug gave ids to, and
// highlight the one nearest the top of the viewport.
export function DocsToc() {
  const pathname = usePathname()
  const [headings, setHeadings] = useState<Heading[]>([])
  const [active, setActive] = useState("")

  useEffect(() => {
    const els = Array.from(document.querySelectorAll<HTMLHeadingElement>("[data-docs] h2[id], [data-docs] h3[id]"))
    // Reading the DOM after navigation is the point of this effect.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setHeadings(els.map((el) => ({ id: el.id, text: el.textContent ?? "", level: el.tagName === "H2" ? 2 : 3 })))
    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((e) => e.isIntersecting)
        if (visible.length) setActive(visible[0].target.id)
      },
      { rootMargin: "-80px 0px -70% 0px" },
    )
    els.forEach((el) => observer.observe(el))
    return () => observer.disconnect()
  }, [pathname])

  if (!headings.length) return null
  return (
    <div className="flex flex-col gap-2 text-sm">
      <p className="font-mono text-xs text-muted-foreground">On this page</p>
      <ul className="flex flex-col gap-1.5 border-l">
        {headings.map((h) => (
          <li key={h.id}>
            <a
              href={`#${h.id}`}
              className={cn(
                "-ml-px block border-l py-0.5 transition-colors hover:text-foreground",
                h.level === 3 ? "pl-6" : "pl-3",
                active === h.id ? "border-foreground text-foreground" : "border-transparent text-muted-foreground",
              )}
            >
              {h.text}
            </a>
          </li>
        ))}
      </ul>
    </div>
  )
}
