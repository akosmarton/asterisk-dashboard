import type { DashboardData } from './types'

export interface SessionUser {
  name?: string
  email?: string
  monogram: string
}

export interface SessionResponse {
  authenticated: boolean
  password_login: boolean
  oidc_login: boolean
  user: SessionUser | null
}

/**
 * GET /api/session -> 200 {"authenticated", "password_login", "oidc_login",
 * "user"} (never 401). The two *_login flags say which sign-in methods the
 * server offers; "user" is the signed-in identity behind the account button.
 */
export async function getSession(): Promise<SessionResponse> {
  const res = await fetch('/api/session')
  return (await res.json()) as SessionResponse
}

export type LoginResult = { ok: true } | { ok: false; error: string }

/**
 * POST /api/login -> 200 {"ok": true} | 401 {"error"} | 400 {"error"}.
 * The session cookie is HttpOnly and set by the server.
 */
export async function login(username: string, password: string): Promise<LoginResult> {
  const res = await fetch('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })

  if (res.ok) return { ok: true }

  let error = 'Sign in failed'
  try {
    const body = (await res.json()) as { error?: string }
    if (body.error) error = body.error
  } catch {
    // Non-JSON body: keep the generic message.
  }
  return { ok: false, error }
}

/**
 * POST /api/logout -> 204.
 */
export async function logout(): Promise<void> {
  await fetch('/api/logout', { method: 'POST' })
}

export type DataResult =
  | { ok: true; data: DashboardData }
  | { ok: false; unauthorized: boolean }

/**
 * GET /api/data -> DashboardData, or 401 {"error": "unauthorized"}.
 */
export async function getData(): Promise<DataResult> {
  const res = await fetch('/api/data')
  if (res.status === 401) return { ok: false, unauthorized: true }
  if (!res.ok) return { ok: false, unauthorized: false }
  return { ok: true, data: (await res.json()) as DashboardData }
}

export interface DashboardSocketHandlers {
  onOpen: () => void
  onData: (data: DashboardData) => void
  onClose: () => void
}

/**
 * Opens /ws (same origin) and reconnects after 2 seconds whenever the socket
 * closes. Returns a cleanup function that stops the reconnect loop and closes
 * the socket.
 */
export function connectDashboardSocket(handlers: DashboardSocketHandlers): () => void {
  let socket: WebSocket | null = null
  let timer: number | undefined
  let closed = false

  function connect(): void {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${protocol}//${window.location.host}/ws`)
    socket = ws

    ws.onopen = () => {
      handlers.onOpen()
    }

    ws.onmessage = (event) => {
      try {
        handlers.onData(JSON.parse(event.data as string) as DashboardData)
      } catch (err) {
        console.error('[WebSocket] Parse error:', err)
      }
    }

    ws.onclose = () => {
      if (closed) return
      handlers.onClose()
      timer = window.setTimeout(connect, 2000)
    }

    ws.onerror = (err) => {
      console.error('[WebSocket] Error:', err)
      ws.close()
    }
  }

  connect()

  return () => {
    closed = true
    clearTimeout(timer)
    socket?.close()
  }
}
