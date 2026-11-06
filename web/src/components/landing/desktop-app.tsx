import { ArrowRightIcon, DownloadIcon } from "lucide-react"
import Image, { type StaticImageData } from "next/image"
import Link from "next/link"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import agents from "@/images/desktop-agents.png"
import browser from "@/images/desktop-browser.png"
import files from "@/images/desktop-files.png"
import git from "@/images/desktop-git.png"
import limits from "@/images/desktop-limits.png"
import settings from "@/images/desktop-settings.png"
import skills from "@/images/desktop-skills.png"
import { site } from "@/lib/site"
import { conchVersion } from "@/lib/version"

const more: { src: StaticImageData; alt: string; caption: string }[] = [
  {
    src: git,
    alt: "Conch.app's git tab: a branch's uncommitted changes, a diff with a note the agent answered inline, running CI checks and the commit panel",
    caption: "A branch's git tab: its diff by hunk, notes the agent answers inline, the pull request's checks, and commit.",
  },
  {
    src: files,
    alt: "Conch.app's Files tab: a project's files marked by their languages' logos, and a TypeScript file open in the editor",
    caption: "Files: edit beside the agent, each file marked by its language, pictures, video and pages shown as they are.",
  },
  {
    src: browser,
    alt: "Conch.app with a browser tab showing a local dev server's page beside the agents",
    caption: "A browser tab: the dev server's port opens next to the agents writing it.",
  },
  {
    src: settings,
    alt: "Conch.app's Settings: Checks & autopilot drawn as the line a finished branch takes, Fix CI and Merge just turned on and marked edited, and a bar to save them",
    caption: "Settings: each setting with the control that suits it, and the autopilot as the line a finished branch takes. Two steps just turned on, waiting for Save.",
  },
  {
    src: skills,
    alt: "Conch.app's Skills & MCP servers: conch's five skills on every agent, a skill of your own, and two MCP servers with a box per agent, waiting for Apply",
    caption: "Skills & MCP servers: conch's skills on every agent, so they run a team, a review loop or a ticket to a merged PR. Your own skills and servers, ticked per agent.",
  },
  {
    src: limits,
    alt: "Conch.app's status bar saying Claude's 5-hour limit is 91% used, beside the usage chip, while an agent works",
    caption: "A plan nearly used up says so: a notification and the status bar, before the limit stops your agents mid-task.",
  },
]

// Where the beta's zip is: on the release the site was built from, else
// the releases page (a site built before any tag, or from a dev version).
export function downloadURL(): string {
  const v = conchVersion()
  return /^\d+\.\d+\.\d+$/.test(v) ? `${site.releases}/download/v${v}/Conch_${v}_macos_arm64.zip` : site.releases
}

function Shot({ src, alt, priority }: { src: StaticImageData; alt: string; priority?: boolean }) {
  return (
    <div className="overflow-hidden rounded-xl border border-term-border shadow-[0_24px_60px_-20px_rgb(0_0_0/0.35)]">
      <Image src={src} alt={alt} priority={priority} sizes="(min-width: 1024px) 72rem, 100vw" className="h-auto w-full" />
    </div>
  )
}

export function DesktopApp() {
  return (
    <div className="flex flex-col gap-10">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          <Badge variant="outline" className="font-mono text-[0.7rem]">beta</Badge>
          macOS 14 or later · Apple Silicon · the server comes inside it
        </div>
        <div className="flex flex-wrap gap-3">
          <Button className="h-10 px-4" nativeButton={false} render={<a href={downloadURL()} />}>
            <DownloadIcon data-icon="inline-start" /> Download Conch.app
          </Button>
          <Button variant="outline" className="h-10 px-4" nativeButton={false} render={<Link href="/docs/mac-app#installing-the-beta" />}>
            How to install <ArrowRightIcon data-icon="inline-end" />
          </Button>
        </div>
      </div>
      <figure className="flex flex-col gap-3">
        <Shot
          src={agents}
          priority
          alt="Conch.app: the tree of machines, projects, agents and branches, a waiting agent's question answered from the sidebar, and a working agent in its tab"
        />
        <figcaption className="text-sm text-muted-foreground">
          The same agents the TUI shows, in a native window: answer a waiting agent from the sidebar while another works.
        </figcaption>
      </figure>
      <div className="grid gap-8 md:grid-cols-3">
        {more.map((s) => (
          <figure key={s.caption} className="flex flex-col gap-3">
            <Shot src={s.src} alt={s.alt} />
            <figcaption className="text-sm text-muted-foreground">{s.caption}</figcaption>
          </figure>
        ))}
      </div>
    </div>
  )
}
