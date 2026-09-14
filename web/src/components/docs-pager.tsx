"use client"

import { ArrowLeftIcon, ArrowRightIcon } from "lucide-react"
import Link from "next/link"
import { usePathname } from "next/navigation"

import { normalizePath } from "@/components/docs-sidebar"
import { docPages, docs } from "@/lib/docs"

export function DocsBreadcrumb() {
  const pathname = normalizePath(usePathname())
  const section = docs.find((s) => s.pages.some((p) => p.href === pathname))
  return <p className="mb-2 font-mono text-xs text-muted-foreground">{section?.title ?? "Docs"}</p>
}

export function DocsPager() {
  const pathname = normalizePath(usePathname())
  const i = docPages.findIndex((p) => p.href === pathname)
  if (i < 0) return null
  const prev = docPages[i - 1]
  const next = docPages[i + 1]
  return (
    <div className="mt-16 grid gap-3 border-t pt-8 sm:grid-cols-2">
      {prev ? (
        <Link href={prev.href} className="group flex flex-col gap-1 rounded-xl border p-4 transition-colors hover:bg-muted/50">
          <span className="flex items-center gap-1 text-xs text-muted-foreground">
            <ArrowLeftIcon className="size-3" /> Previous
          </span>
          <span className="font-medium">{prev.title}</span>
        </Link>
      ) : (
        <span />
      )}
      {next && (
        <Link
          href={next.href}
          className="group flex flex-col items-end gap-1 rounded-xl border p-4 text-right transition-colors hover:bg-muted/50"
        >
          <span className="flex items-center gap-1 text-xs text-muted-foreground">
            Next <ArrowRightIcon className="size-3" />
          </span>
          <span className="font-medium">{next.title}</span>
        </Link>
      )}
    </div>
  )
}
