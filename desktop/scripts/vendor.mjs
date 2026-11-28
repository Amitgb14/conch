// Copies what the window loads into renderer/vendor: xterm.js and its fit
// addon, and conch-ui (../ui, built first if it has not been). The page's
// policy loads scripts from the app alone, so nothing comes from a CDN.
import { execSync } from "node:child_process"
import { copyFileSync, existsSync, mkdirSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(dirname(fileURLToPath(import.meta.url)))
const out = join(here, "renderer", "vendor")
mkdirSync(out, { recursive: true })
const ui = join(here, "..", "ui")
if (!existsSync(join(ui, "dist", "conch-ui.js"))) {
  execSync("pnpm install --frozen-lockfile && pnpm exec vite build", { cwd: ui, stdio: "inherit" })
}
for (const [from, to] of [
  ["node_modules/@xterm/xterm/lib/xterm.mjs", "xterm.mjs"],
  ["node_modules/@xterm/xterm/css/xterm.css", "xterm.css"],
  ["node_modules/@xterm/addon-fit/lib/addon-fit.mjs", "addon-fit.mjs"],
  ["../ui/dist/conch-ui.js", "conch-ui.js"],
  ["../ui/dist/conch-ui.css", "conch-ui.css"],
]) copyFileSync(join(here, from), join(out, to))
console.log("renderer/vendor: xterm, conch-ui")
