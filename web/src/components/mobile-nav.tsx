"use client"

import { MenuIcon } from "lucide-react"
import Link from "next/link"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { docs } from "@/lib/docs"

export function MobileNav() {
  const [open, setOpen] = useState(false)
  const close = () => setOpen(false)
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger render={<Button variant="ghost" size="icon" className="md:hidden" aria-label="Menu" />}>
        <MenuIcon />
      </SheetTrigger>
      <SheetContent side="right" className="overflow-y-auto">
        <SheetHeader>
          <SheetTitle className="font-mono">conch</SheetTitle>
        </SheetHeader>
        <nav className="flex flex-col gap-6 px-4 pb-8">
          <div className="flex flex-col gap-1">
            <Link href="/" onClick={close} className="py-1 font-medium">Home</Link>
            <Link href="/#features" onClick={close} className="py-1 text-muted-foreground">Features</Link>
            <Link href="/#install" onClick={close} className="py-1 text-muted-foreground">Install</Link>
          </div>
          {docs.map((section) => (
            <div key={section.title} className="flex flex-col gap-1">
              <p className="font-mono text-xs text-muted-foreground">{section.title}</p>
              {section.pages.map((page) => (
                <Link key={page.href} href={page.href} onClick={close} className="py-1">
                  {page.title}
                </Link>
              ))}
            </div>
          ))}
        </nav>
      </SheetContent>
    </Sheet>
  )
}
