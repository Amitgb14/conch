export type DocPage = { title: string; href: string; description: string }
export type DocSection = { title: string; pages: DocPage[] }

// The docs sidebar, in reading order. Each href has a page.mdx under
// src/app/docs; the pager at the bottom of a page follows this order too.
export const docs: DocSection[] = [
  {
    title: "Getting started",
    pages: [
      { title: "Introduction", href: "/docs", description: "What conch is and how it is put together." },
      { title: "Installation", href: "/docs/installation", description: "Install, update and reload conch." },
      { title: "Quick start", href: "/docs/quick-start", description: "From an empty terminal to agents working in parallel." },
    ],
  },
  {
    title: "Using conch",
    pages: [
      { title: "The interface", href: "/docs/interface", description: "The tree, splits and tabs, scrollback and settings." },
      { title: "Tasks and worktrees", href: "/docs/tasks", description: "One branch and worktree per task, with your local files." },
      { title: "Agents", href: "/docs/agents", description: "Supported agents, their states, usage and plan limits." },
      { title: "Sessions", href: "/docs/sessions", description: "Resume saved conversations and interrupted runs." },
      { title: "Remote machines", href: "/docs/remote-machines", description: "Run agents on other machines over ssh." },
      { title: "Brain", href: "/docs/brain", description: "Ask for work in plain words and approve the plan." },
    ],
  },
  {
    title: "Reference",
    pages: [
      { title: "Keys and mouse", href: "/docs/keys", description: "Every key binding in the TUI." },
      { title: "Command line", href: "/docs/cli", description: "Scripting conch from a shell." },
      { title: "Configuration", href: "/docs/configuration", description: "Files conch keeps and config.toml." },
      { title: "Protocol", href: "/docs/protocol", description: "The NDJSON protocol and the source layout." },
    ],
  },
]

export const docPages = docs.flatMap((s) => s.pages)
