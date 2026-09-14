import Link from "next/link"

import { GithubIcon } from "@/components/github-icon"
import { Logo } from "@/components/logo"
import { MobileNav } from "@/components/mobile-nav"
import { ThemeToggle } from "@/components/theme-toggle"
import { Button } from "@/components/ui/button"
import { site } from "@/lib/site"

export const mainNav = [
  { title: "Features", href: "/#features" },
  { title: "How it works", href: "/#how-it-works" },
  { title: "Agents", href: "/#agents" },
  { title: "Docs", href: "/docs" },
]

export function SiteHeader() {
  return (
    <header className="sticky top-0 z-50 w-full border-b border-border/60 bg-background/80 backdrop-blur-md">
      <div className="mx-auto flex h-14 w-full max-w-6xl items-center gap-4 px-5 sm:px-6">
        <Logo />
        <span className="hidden rounded-full border px-2 py-0.5 font-mono text-[0.65rem] text-muted-foreground sm:inline">
          open source
        </span>
        <nav className="ml-auto hidden items-center gap-1 md:flex">
          {mainNav.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            >
              {item.title}
            </Link>
          ))}
        </nav>
        <div className="ml-auto flex items-center gap-1 md:ml-2">
          <Button
            variant="ghost"
            size="icon"
            aria-label="GitHub"
            nativeButton={false}
            render={<a href={site.repo} />}
          >
            <GithubIcon className="size-4" />
          </Button>
          <ThemeToggle />
          <Button className="ml-1 hidden sm:inline-flex" nativeButton={false} render={<Link href="/#install" />}>
            Install
          </Button>
          <MobileNav />
        </div>
      </div>
    </header>
  )
}
