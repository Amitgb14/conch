import { tokenFrom } from "@/lib/model.js"
import type { Cfg, Payload } from "@/types"

// The token comes in the address's fragment; it is kept for this tab and
// taken out of the address bar, where it would be copied with the link.
function token(): string {
  const fromHash = tokenFrom(location.hash)
  if (fromHash) {
    try { sessionStorage.setItem("conch-token", fromHash) } catch { /* private window */ }
    history.replaceState(null, "", location.pathname + location.search)
    return fromHash
  }
  try { return sessionStorage.getItem("conch-token") ?? "" } catch { return "" }
}

const key = token()

export class ApiError extends Error {
  status: number
  current?: Payload
  constructor(message: string, status: number, current?: Payload) {
    super(message)
    this.status = status
    this.current = current
  }
}

async function call(method: string, body?: unknown): Promise<Payload> {
  let res: Response
  try {
    res = await fetch("./api/config", {
      method,
      headers: { "X-Conch-Token": key, ...(body ? { "Content-Type": "application/json" } : {}) },
      body: body ? JSON.stringify(body) : undefined,
    })
  } catch {
    throw new ApiError("conch isn't answering: it may have closed this editor. Open Edit in browser from Settings again.", 0)
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(data.error ?? res.statusText, res.status, data.current)
  return data as Payload
}

export const load = () => call("GET")
export const saveConfig = (version: string, config: Cfg) => call("PUT", { version, config })
export const saveRaw = (version: string, raw: string) => call("PUT", { version, raw })
