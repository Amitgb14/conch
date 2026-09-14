"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"

import { docs } from "@/lib/docs"
import { cn } from "@/lib/utils"

export function normalizePath(path: string) {
  return path.length > 1 ? path.replace(/\/$/, "") : path
}

export function DocsSidebar() {
  const pathname = normalizePath(usePathname())
  return (
    <nav className="flex flex-col gap-6 text-sm">
      {docs.map((section) => (
        <div key={section.title} className="flex flex-col gap-0.5">
          <p className="mb-1.5 px-2.5 font-mono text-xs text-muted-foreground">{section.title}</p>
          {section.pages.map((page) => (
            <Link
              key={page.href}
              href={page.href}
              className={cn(
                "rounded-md px-2.5 py-1.5 transition-colors hover:bg-muted",
                pathname === page.href ? "bg-muted font-medium text-foreground" : "text-muted-foreground",
              )}
            >
              {page.title}
            </Link>
          ))}
        </div>
      ))}
    </nav>
  )
}
