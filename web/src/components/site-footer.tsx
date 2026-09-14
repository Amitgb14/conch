import Link from "next/link"

import { Logo } from "@/components/logo"
import { site } from "@/lib/site"

const columns = [
  {
    title: "Docs",
    links: [
      { title: "Quick start", href: "/docs/quick-start" },
      { title: "Tasks and worktrees", href: "/docs/tasks" },
      { title: "Remote machines", href: "/docs/remote-machines" },
      { title: "Keys and mouse", href: "/docs/keys" },
      { title: "Configuration", href: "/docs/configuration" },
    ],
  },
  {
    title: "Project",
    links: [
      { title: "Source", href: site.repo },
      { title: "Releases", href: site.releases },
      { title: "Issues", href: site.issues },
      { title: "License", href: `${site.repo}/blob/master/LICENSE` },
    ],
  },
]

export function SiteFooter() {
  return (
    <footer className="border-t bg-surface">
      <div className="mx-auto grid w-full max-w-6xl gap-10 px-5 py-12 sm:px-6 md:grid-cols-[1.5fr_1fr_1fr]">
        <div className="flex flex-col gap-3">
          <Logo />
          <p className="max-w-sm text-sm text-muted-foreground">
            A terminal orchestrator for AI coding agents. Your agents keep working when you close
            the UI.
          </p>
        </div>
        {columns.map((col) => (
          <div key={col.title} className="flex flex-col gap-2 text-sm">
            <p className="font-mono text-xs text-muted-foreground">{col.title}</p>
            {col.links.map((link) =>
              link.href.startsWith("/") ? (
                <Link key={link.href} href={link.href} className="hover:underline">
                  {link.title}
                </Link>
              ) : (
                <a key={link.href} href={link.href} className="hover:underline">
                  {link.title}
                </a>
              ),
            )}
          </div>
        ))}
      </div>
      <div className="border-t">
        <div className="mx-auto flex w-full max-w-6xl flex-wrap justify-between gap-2 px-5 py-4 font-mono text-xs text-muted-foreground sm:px-6">
          <span>Apache License 2.0 · written in Go</span>
        </div>
      </div>
    </footer>
  )
}
