import Image, { type StaticImageData } from "next/image"

import desktopAgents from "@/images/desktop-agents.png"
import desktopBrowser from "@/images/desktop-browser.png"
import desktopFiles from "@/images/desktop-files.png"
import desktopGit from "@/images/desktop-git.png"
import desktopLimits from "@/images/desktop-limits.png"
import desktopSettings from "@/images/desktop-settings.png"
import desktopSkills from "@/images/desktop-skills.png"
import phoneAgent from "@/images/phone-agent.png"
import phoneAgents from "@/images/phone-agents.png"
import phoneWatch from "@/images/phone-watch.png"
import { cn } from "@/lib/utils"

// The pictures a docs page may show, by name: MDX cannot import them, and
// next/image wants the import rather than a path, so they are named here.
// The desktop ones are taken in Conch.app; the phone ones are photographed
// in the app itself by web/scripts/phone-capture.
const images: Record<string, StaticImageData> = {
  "desktop-agents": desktopAgents,
  "desktop-browser": desktopBrowser,
  "desktop-files": desktopFiles,
  "desktop-git": desktopGit,
  "desktop-limits": desktopLimits,
  "desktop-settings": desktopSettings,
  "desktop-skills": desktopSkills,
  "phone-agent": phoneAgent,
  "phone-agents": phoneAgents,
  "phone-watch": phoneWatch,
}

/** A screenshot in a docs page: `<Shot name="phone-agents" alt="…" caption="…" />` */
export function Shot({
  name,
  alt,
  caption,
  phone,
}: {
  name: string
  alt: string
  caption?: string
  phone?: boolean
}) {
  const src = images[name]
  if (!src) throw new Error(`no such screenshot: ${name} (add it to doc-shot.tsx)`)
  return (
    <figure className="my-6 flex flex-col gap-3">
      <div
        className={cn(
          "overflow-hidden border border-term-border bg-black shadow-[0_24px_60px_-20px_rgb(0_0_0/0.35)]",
          phone ? "mx-auto w-full max-w-[17rem] rounded-[1.75rem] border-[6px]" : "rounded-xl",
        )}
      >
        <Image src={src} alt={alt} sizes={phone ? "17rem" : "(min-width: 1024px) 48rem, 100vw"} className="h-auto w-full" />
      </div>
      {caption && <figcaption className="text-sm text-muted-foreground">{caption}</figcaption>}
    </figure>
  )
}
