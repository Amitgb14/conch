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
      { title: "Why conch", href: "/docs/why", description: "What conch does that other agent orchestrators leave to you." },
    ],
  },
  {
    title: "Using conch",
    pages: [
      { title: "The interface", href: "/docs/interface", description: "The tree, splits and tabs, scrollback and settings." },
      { title: "Tasks and worktrees", href: "/docs/tasks", description: "One branch and worktree per task, with your local files." },
      { title: "Agents", href: "/docs/agents", description: "Supported agents, their states, usage and plan limits." },
      { title: "Agents working together", href: "/docs/agents-together", description: "Let one agent hand work to another — a reviewer, a tester — and read what it did." },
      { title: "Sessions", href: "/docs/sessions", description: "Resume saved conversations and interrupted runs." },
      { title: "Remote machines", href: "/docs/remote-machines", description: "Run agents on other machines over ssh." },
      { title: "Sandboxes", href: "/docs/sandboxes", description: "Run agents in a Daytona sandbox that keeps going with your laptop closed." },
      { title: "Mac app", href: "/docs/mac-app", description: "conch in a native macOS window: the tree, tabs and splits, panes drawn by Ghostty where it is built in, a branch's changes, sessions, commits, and answers on a waiting agent." },
      { title: "Mobile preview", href: "/docs/mobile-preview", description: "Your app on a real iOS simulator in Conch.app, beside the agent writing it." },
      { title: "Phone", href: "/docs/phone", description: "See your agents from your phone, reply to one and answer what it asks." },
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
