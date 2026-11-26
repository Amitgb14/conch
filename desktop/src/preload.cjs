// What the window may do, and nothing more: call the methods the main
// process allows (connection.cjs), hear the server's events and the
// connection's state, and open a web link in the browser.
"use strict"
const { contextBridge, ipcRenderer } = require("electron")

contextBridge.exposeInMainWorld("conchDesktop", {
  platform: process.platform,
  call: (method, params) => ipcRenderer.invoke("conch:call", method, params),
  status: () => ipcRenderer.invoke("conch:status"),
  reconnect: () => ipcRenderer.invoke("conch:reconnect"),
  openExternal: (url) => ipcRenderer.invoke("conch:open", url),
  onEvent: (f) => { ipcRenderer.on("conch:event", (_e, ev) => f(ev)) },
  onStatus: (f) => { ipcRenderer.on("conch:status", (_e, s) => f(s)) },
})
