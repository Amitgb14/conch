import { ArrowRightIcon } from "lucide-react"
import Image, { type StaticImageData } from "next/image"
import Link from "next/link"

import { Button } from "@/components/ui/button"
import agent from "@/images/phone-agent.png"
import agents from "@/images/phone-agents.png"
import watch from "@/images/phone-watch.png"

// Photographed in the app itself, at a phone's size: web/scripts/phone-capture.
const shots: { src: StaticImageData; alt: string; caption: string }[] = [
  {
    src: agents,
    alt: "The phone app's Agents tab: a waiting agent with its question and three answers to tap, a working agent below it with the last line it printed",
    caption:
      "What needs you, first: the question an agent is waiting on, with its answers as buttons. Tap one and it carries on.",
  },
  {
    src: watch,
    alt: "The phone app's Watch tab: every agent's and terminal's screen live, two to a row, the waiting one ringed in coral",
    caption: "Watch: every agent's and terminal's screen at once, live. A tile turns coral when its agent needs you.",
  },
  {
    src: agent,
    alt: "An agent on the phone: its state and cost, the question it asks, its output, and a composer pinned to the bottom",
    caption: "One agent, as a conversation: swipe sideways for the next, scroll back through its output, or type a reply.",
  },
]

export function Phone() {
  return (
    <div className="flex flex-col gap-10">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-sm text-muted-foreground">
          A page the gateway serves itself — nothing to install, no account, and no server of ours in between.
        </p>
        <Button variant="outline" className="h-10 w-fit px-4" nativeButton={false} render={<Link href="/docs/phone" />}>
          Set it up <ArrowRightIcon data-icon="inline-end" />
        </Button>
      </div>
      <div className="grid gap-8 sm:grid-cols-3">
        {shots.map((s) => (
          <figure key={s.caption} className="flex flex-col gap-3">
            <div className="mx-auto w-full max-w-[17rem] overflow-hidden rounded-[1.75rem] border-[6px] border-term-border bg-black shadow-[0_24px_60px_-20px_rgb(0_0_0/0.45)]">
              <Image src={s.src} alt={s.alt} sizes="(min-width: 640px) 17rem, 100vw" className="h-auto w-full" />
            </div>
            <figcaption className="text-sm text-muted-foreground">{s.caption}</figcaption>
          </figure>
        ))}
      </div>
    </div>
  )
}
