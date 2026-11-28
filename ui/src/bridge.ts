// The one way a view speaks to its host. The Mac app hands the page a
// message handler (WKWebView, in the view's own content world); an
// Electron host would hand it window.conchHost from its preload. Either
// way the host checks each message before acting on it.

type Host = { post: (m: Record<string, unknown>) => void }

declare global {
  interface Window {
    webkit?: { messageHandlers?: { conch?: { postMessage: (m: unknown) => void } } }
    conchHost?: Host
  }
}

export function post(view: string, type: string, fields: Record<string, unknown> = {}) {
  const m = { ...fields, view, type }
  const wk = window.webkit?.messageHandlers?.conch
  if (wk) wk.postMessage(m)
  else window.conchHost?.post(m)
}
