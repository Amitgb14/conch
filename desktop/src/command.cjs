// How the app reaches conch's server: `conch bridge`, which speaks the
// protocol on its stdin and stdout and starts the server if none is
// running. On Windows conch runs inside WSL — its panes are Linux
// programs — so the bridge is started there through wsl.exe, in a login
// shell so that conch's own install folder (~/.local/bin) is on PATH.
"use strict"
const path = require("node:path")

/** The conch program on Linux or macOS: CONCH_BIN, else the first of the
 *  usual places that exists, else "conch" for PATH to find. An app started
 *  from a desktop menu often has a PATH without ~/.local/bin. */
function findConch(env, home, exists) {
  if (env.CONCH_BIN) return env.CONCH_BIN
  for (const p of [path.join(home, ".local", "bin", "conch"), path.join(home, "go", "bin", "conch"), "/usr/local/bin/conch", "/opt/homebrew/bin/conch"]) {
    if (exists(p)) return p
  }
  return "conch"
}

/** The command that bridges to the server: { file, args, where }. */
function bridgeCommand({ platform, env, home, exists }) {
  if (platform === "win32") {
    const args = []
    if (env.CONCH_WSL_DISTRO) args.push("-d", env.CONCH_WSL_DISTRO)
    // The program inside WSL is $0, so nothing in it is read as shell.
    args.push("-e", "sh", "-lc", 'exec "$0" bridge', env.CONCH_BIN || "conch")
    return { file: "wsl.exe", args, where: env.CONCH_WSL_DISTRO ? `WSL (${env.CONCH_WSL_DISTRO})` : "WSL" }
  }
  return { file: findConch(env, home, exists), args: ["bridge"], where: "this computer" }
}

module.exports = { bridgeCommand, findConch }
