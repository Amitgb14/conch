// conch's desktop app for Linux and Windows: a window on conch's server,
// as the TUI and Conch.app are. The server is reached through `conch
// bridge` (command.cjs) — on Windows inside WSL — and spoken to here
// (connection.cjs); the window asks for what it draws through the preload,
// only the methods `allowed` names, and draws terminals with xterm.js and
// the richer views with conch-ui (../ui), the same views the Mac app shows.
"use strict"
const { app, BrowserWindow, ipcMain, shell, Menu } = require("electron")
const { spawn } = require("node:child_process")
const fs = require("node:fs")
const os = require("node:os")
const path = require("node:path")
const { bridgeCommand } = require("./command.cjs")
const { Connection, allowed } = require("./connection.cjs")

let win = null
let conn = null
let child = null
let retry = null
let status = { state: "connecting", where: "", server: "", error: "" }

function setStatus(s) {
  status = { ...status, ...s }
  win?.webContents.send("conch:status", status)
}

/** The environment the bridge runs in: this one, less what would tie it
 *  to the pane the app happened to be started from. */
function bridgeEnv() {
  const env = { ...process.env }
  if (!env.CONCH_DESKTOP_KEEP_SOCKET) {
    delete env.CONCH_PANE_ID
  }
  return env
}

async function connect() {
  clearTimeout(retry)
  const cmd = bridgeCommand({ platform: process.platform, env: process.env, home: os.homedir(), exists: (p) => fs.existsSync(p) })
  setStatus({ state: "connecting", where: cmd.where, error: "" })
  let stderr = ""
  try {
    child = spawn(cmd.file, cmd.args, { stdio: ["pipe", "pipe", "pipe"], env: bridgeEnv(), windowsHide: true })
  } catch (e) {
    return failed(`could not start ${cmd.file}: ${e.message}`)
  }
  child.stderr.on("data", (b) => { stderr = (stderr + b).slice(-2000) })
  child.on("error", (e) => failed(e.code === "ENOENT"
    ? (process.platform === "win32" ? "WSL was not found: install it (wsl --install), then conch inside it" : `conch was not found (${cmd.file}): install it, or set CONCH_BIN`)
    : String(e)))
  const c = new Connection(child.stdin, child.stdout)
  conn = c
  c.on("event", (e) => win?.webContents.send("conch:event", e))
  c.on("close", (why) => { if (conn === c) failed(stderr.trim() || why) })
  try {
    const hello = await c.call("hello", { client: "conch-desktop", version: app.getVersion(), protocol: 1, capabilities: [] })
    setStatus({ state: "connected", server: hello?.version ?? "", error: "" })
  } catch (e) {
    failed(stderr.trim() || e.message)
  }
}

function failed(why) {
  if (status.state === "closed" && status.error === why) return
  conn = null
  try { child?.kill() } catch {}
  setStatus({ state: "closed", error: why })
  retry = setTimeout(connect, 3000)
}

ipcMain.handle("conch:call", (e, method, params) => {
  if (!fromOurPage(e)) throw new Error("not from conch's window")
  if (!allowed.has(method)) throw new Error(`${method} is not the window's to ask`)
  if (!conn) throw new Error("not connected")
  return conn.call(method, params)
})
ipcMain.handle("conch:status", () => status)
ipcMain.handle("conch:reconnect", () => { try { child?.kill() } catch {}; connect() })
ipcMain.handle("conch:open", (e, url) => {
  if (fromOurPage(e) && /^https?:\/\//i.test(String(url))) shell.openExternal(String(url))
})

const page = path.join(__dirname, "..", "renderer", "index.html")
function fromOurPage(e) {
  try { return new URL(e.senderFrame.url).pathname.endsWith("/renderer/index.html") && e.senderFrame.url.startsWith("file://") } catch { return false }
}

function createWindow() {
  win = new BrowserWindow({
    width: 1320, height: 820, minWidth: 640, minHeight: 400,
    backgroundColor: "#15191E",
    title: "Conch",
    autoHideMenuBar: true,
    webPreferences: { preload: path.join(__dirname, "preload.cjs"), contextIsolation: true, sandbox: true, nodeIntegration: false },
  })
  // The window is the app's page and nothing else: links go to the browser.
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:\/\//i.test(url)) shell.openExternal(url)
    return { action: "deny" }
  })
  win.webContents.on("will-navigate", (e) => e.preventDefault())
  win.loadFile(page)
  win.on("closed", () => { win = null })
  if (process.env.CONCH_DESKTOP_DRIVE) drive(process.env.CONCH_DESKTOP_DRIVE)
}

/** For testing by hand or by an agent with no screen to look at: commands
 *  in DIR/cmd — `js CODE`, `snap NAME`, `quit` — run one at a time, each
 *  answered in DIR/done. Only when CONCH_DESKTOP_DRIVE names a folder. */
function drive(dir) {
  const cmdFile = path.join(dir, "cmd"), done = path.join(dir, "done")
  setInterval(async () => {
    if (!fs.existsSync(cmdFile) || !win) return
    const line = fs.readFileSync(cmdFile, "utf8").trim()
    fs.rmSync(cmdFile)
    let out = ""
    try {
      if (line.startsWith("js ")) out = JSON.stringify(await win.webContents.executeJavaScript(line.slice(3)))
      else if (line.startsWith("snap ")) {
        const img = await win.webContents.capturePage()
        const f = path.join(dir, line.slice(5).trim() + ".png")
        fs.writeFileSync(f, img.toPNG())
        out = f
      } else if (line.startsWith("type ")) {
        // Typed as keys, through the same path a keyboard takes.
        for (const ch of line.slice(5)) win.webContents.sendInputEvent({ type: "char", keyCode: ch })
        out = "typed"
      } else if (line.startsWith("key ")) {
        const k = line.slice(4).trim()
        win.webContents.sendInputEvent({ type: "keyDown", keyCode: k })
        if (k === "Enter" || k === "Return") win.webContents.sendInputEvent({ type: "char", keyCode: "\r" })
        win.webContents.sendInputEvent({ type: "keyUp", keyCode: k })
        out = "pressed"
      } else if (line === "quit") { app.quit(); return }
      else out = "unknown: " + line
    } catch (e) { out = "error: " + e.message }
    fs.writeFileSync(done, out + "\n")
  }, 300)
}

app.whenReady().then(() => {
  if (process.platform !== "darwin") Menu.setApplicationMenu(null)
  createWindow()
  connect()
})
app.on("window-all-closed", () => { try { child?.kill() } catch {}; app.quit() })
