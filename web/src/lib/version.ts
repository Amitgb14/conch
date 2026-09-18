import { execFileSync } from "node:child_process"
import { readFileSync } from "node:fs"
import path from "node:path"

// The conch version shown on the site, resolved at build time: the newest
// v* release tag, or else the version in internal/proto (e.g. 0.1.2-dev).
export function conchVersion(): string {
  if (process.env.CONCH_VERSION) return process.env.CONCH_VERSION.replace(/^v/, "")
  try {
    const tag = execFileSync("git", ["describe", "--tags", "--abbrev=0", "--match", "v*"], {
      stdio: ["ignore", "pipe", "ignore"],
    })
      .toString()
      .trim()
    if (tag) return tag.replace(/^v/, "")
  } catch {
    // No tags yet, or not a git checkout.
  }
  try {
    const src = readFileSync(path.join(process.cwd(), "..", "internal", "proto", "proto.go"), "utf8")
    const m = src.match(/var Version = "([^"]+)"/)
    if (m) return m[1]
  } catch {
    // Built outside the conch repository.
  }
  return "dev"
}
