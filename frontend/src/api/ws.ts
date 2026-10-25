/**
 * src/api/ws.ts
 *
 * Singleton WebSocket service for TradeDrift real-time streams.
 *
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Protocol (verified against backend docs):
 *   Auth frame:    { action: "auth",        token:   "<JWT>" }
 *   Subscribe:     { action: "subscribe",   channel: "<channel>" }
 *   Unsubscribe:   { action: "unsubscribe", channel: "<channel>" }
 *
 * Channel naming (verified against backend):
 *   market:orderbook:<market_id>   e.g. "market:orderbook:BTC-USDT"
 *   market:ticker:<market_id>      e.g. "market:ticker:BTC-USDT"
 *   market:trades:<market_id>      e.g. "market:trades:BTC-USDT"
 *   user:notifications             (requires auth frame first)
 *
 * Incoming frame from server:
 *   { stream: "<channel>", data: {...} }
 *
 * One singleton connection — do NOT create additional WebSocket instances.
 */

import axios from 'axios'

export type MessageHandler = (data: unknown) => void
export type ConnectionStatus = 'connected' | 'connecting' | 'reconnecting' | 'offline'

const WS_BASE_URL = import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws'

// ── Channel name helpers ─────────────────────────────────────────────────────
/** Validates user identifier to prevent malformed frames or path traversal attempts */
export function isValidUserId(userId: unknown): userId is string {
  if (typeof userId !== 'string') return false
  const trimmed = userId.trim()
  if (!trimmed) return false
  if (trimmed.includes('/') || trimmed.includes('\\') || trimmed.includes(':') || /\s/.test(trimmed)) {
    return false
  }
  return true
}

export const WsChannels = {
  orderbook: (marketId: string) => `market:orderbook:${marketId}`,
  ticker:    (marketId: string) => `market:ticker:${marketId}`,
  trades:    (marketId: string) => `market:trades:${marketId}`,
  userNotifications: (userId?: string | null): string =>
    isValidUserId(userId) ? `user:notifications:${userId.trim()}` : '',
  userPortfolio:     (userId?: string | null): string =>
    isValidUserId(userId) ? `user:portfolio:${userId.trim()}` : '',
} as const

// ── Dev-mode API logger (never logs tokens or sensitive data) ────────────────
const isDev = import.meta.env.DEV
function wsLog(msg: string) {
  if (isDev) console.debug(`[WS] ${msg}`)
}

function isTokenValid(token: string | null): boolean {
  if (!token) return false
  try {
    const parts = token.split('.')
    if (parts.length !== 3) return false
    const base64Url = parts[1]
    const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/')
    const jsonPayload = decodeURIComponent(
      atob(base64)
        .split('')
        .map((c) => '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2))
        .join('')
    )
    const decoded = JSON.parse(jsonPayload)
    if (!decoded.exp) return true
    const now = Math.floor(Date.now() / 1000)
    return decoded.exp > now + 10 // 10-second grace window
  } catch {
    return false
  }
}

class WebSocketService {
  private socket: WebSocket | null = null
  private subscribers: Map<string, Set<MessageHandler>> = new Map()
  private reconnectAttempt = 0
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null
  private pingInterval: ReturnType<typeof setInterval> | null = null
  private isExplicitlyClosed = false
  private connectionEpoch = 0
  private onReconnectHooks: Set<() => void> = new Set()
  private onStatusHooks: Set<(connected: boolean, status: ConnectionStatus) => void> = new Set()
  private onLatencyHooks: Set<(ms: number) => void> = new Set()
  private lastPingSentAt = 0
  private currentLatency = 0
  private isRefreshingToken = false

  constructor() {
    this.connect()
  }

  // ── Public lifecycle hooks ───────────────────────────────────────────────

  public onStatus(hook: (connected: boolean, status: ConnectionStatus) => void): () => void {
    this.onStatusHooks.add(hook)
    hook(this.isConnected(), this.getStatus())
    return () => { this.onStatusHooks.delete(hook) }
  }

  public onLatency(hook: (ms: number) => void): () => void {
    this.onLatencyHooks.add(hook)
    hook(this.currentLatency)
    return () => { this.onLatencyHooks.delete(hook) }
  }

  public onReconnect(hook: () => void): () => void {
    this.onReconnectHooks.add(hook)
    return () => { this.onReconnectHooks.delete(hook) }
  }

  public getStatus(): ConnectionStatus {
    if (!this.socket) return 'offline'
    if (this.socket.readyState === WebSocket.OPEN) return 'connected'
    if (this.socket.readyState === WebSocket.CONNECTING)
      return this.reconnectAttempt > 0 ? 'reconnecting' : 'connecting'
    return 'offline'
  }

  public isConnected(): boolean {
    return this.socket !== null && this.socket.readyState === WebSocket.OPEN
  }

  // ── Connection ───────────────────────────────────────────────────────────

  public connect() {
    if (
      this.socket &&
      (this.socket.readyState === WebSocket.OPEN ||
        this.socket.readyState === WebSocket.CONNECTING)
    ) {
      return
    }

    this.isExplicitlyClosed = false
    const epoch = ++this.connectionEpoch

    this.notifyStatus(false, this.reconnectAttempt > 0 ? 'reconnecting' : 'connecting')

    // Verify token validity before passing as query param.
    // An expired token causes the Gateway to reject with 401, breaking even public market data.
    const token = typeof localStorage !== 'undefined' ? localStorage.getItem('access_token') : null
    const hasValidToken = isTokenValid(token)

    if (token && !hasValidToken) {
      const refreshToken = typeof localStorage !== 'undefined' ? localStorage.getItem('refresh_token') : null
      if (refreshToken && !this.isRefreshingToken) {
        this.isRefreshingToken = true
        wsLog('Stored access_token has expired. Attempting token refresh before connecting...')
        const apiBase = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080'
        axios
          .post(`${apiBase}/api/v1/auth/refresh`, { refreshToken })
          .then(({ data }) => {
            this.isRefreshingToken = false
            if (epoch !== this.connectionEpoch) return
            if (data?.accessToken && typeof localStorage !== 'undefined') {
              localStorage.setItem('access_token', data.accessToken)
              if (data.refreshToken) localStorage.setItem('refresh_token', data.refreshToken)
              wsLog('Token refreshed. Connecting with authenticated session...')
            }
            this.connect()
          })
          .catch(() => {
            this.isRefreshingToken = false
            if (epoch !== this.connectionEpoch) return
            wsLog('Token refresh failed. Continuing in unauthenticated public mode.')
            this.connect()
          })
        return
      }
      if (this.isRefreshingToken) {
        return
      }
    }

    const url = hasValidToken ? `${WS_BASE_URL}?token=${encodeURIComponent(token!)}` : WS_BASE_URL

    wsLog(`Connecting to ${WS_BASE_URL} (authenticated: ${hasValidToken})`)

    try {
      if (typeof WebSocket === 'undefined') {
        return
      }
      const socket = new WebSocket(url)
      this.socket = socket

      socket.onopen = () => {
        if (epoch !== this.connectionEpoch) {
          try { socket.close() } catch { /* ignore */ }
          return
        }
        this.reconnectAttempt = 0
        this.notifyStatus(true, 'connected')
        this.measureLatency()
        this.startHeartbeat()
        this.resubscribeAll()

        this.onReconnectHooks.forEach((hook) => {
          try { hook() } catch { /* ignore */ }
        })
      }

      socket.onmessage = (event) => {
        if (epoch !== this.connectionEpoch) return
        try {
          const lines = (event.data as string).split('\n')
          for (const line of lines) {
            if (!line.trim()) continue
            const parsed = JSON.parse(line) as Record<string, unknown>

            // Pong / latency measurement
            if (parsed.event === 'pong' || parsed.type === 'pong') {
              if (this.lastPingSentAt > 0) {
                const rtt = Math.max(1, Date.now() - this.lastPingSentAt)
                this.currentLatency = rtt
                this.onLatencyHooks.forEach((h) => h(rtt))
              }
              continue
            }

            // Route on `stream` field — server sends { stream: "<channel>", data: {...} }
            const channel = parsed.stream as string | undefined
            if (channel) {
              wsLog(`Message on ${channel}`)
              const handlers = this.subscribers.get(channel)
              if (handlers) {
                handlers.forEach((h) => h(parsed.data))
              }
            }
          }
        } catch {
          // Ignore malformed frames
        }
      }

      socket.onclose = () => {
        if (epoch !== this.connectionEpoch) return
        this.notifyStatus(false, 'offline')
        this.cleanup()
        if (!this.isExplicitlyClosed) {
          this.scheduleReconnect()
        }
      }

      socket.onerror = () => {
        if (epoch !== this.connectionEpoch) return
        wsLog('Error — closing socket')
        this.notifyStatus(false, 'offline')
        try { socket.close() } catch { /* ignore */ }
      }
    } catch {
      this.notifyStatus(false, 'offline')
      this.scheduleReconnect()
    }
  }

  /**
   * Reconnect the WebSocket immediately with the current authentication status.
   * Cleans up the previous socket connection so it cannot deliver events.
   */
  public reconnect() {
    this.isExplicitlyClosed = false
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout)
      this.reconnectTimeout = null
    }
    if (this.socket) {
      const oldSocket = this.socket
      this.socket = null
      oldSocket.onmessage = null
      oldSocket.onerror = null
      oldSocket.onclose = null
      if (oldSocket.readyState === WebSocket.CONNECTING) {
        oldSocket.onopen = () => {
          try { oldSocket.close() } catch { /* ignore */ }
        }
      } else {
        oldSocket.onopen = null
        try {
          oldSocket.close()
        } catch { /* ignore */ }
      }
    }
    this.cleanup()
    this.reconnectAttempt = 0
    this.connect()
  }

  public disconnect() {
    this.connectionEpoch++
    this.isExplicitlyClosed = true
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout)
      this.reconnectTimeout = null
    }
    if (this.socket) {
      const oldSocket = this.socket
      this.socket = null
      oldSocket.onmessage = null
      oldSocket.onerror = null
      oldSocket.onclose = null
      if (oldSocket.readyState === WebSocket.CONNECTING) {
        oldSocket.onopen = () => {
          try { oldSocket.close() } catch { /* ignore */ }
        }
      } else {
        oldSocket.onopen = null
        try {
          oldSocket.close()
        } catch { /* ignore */ }
      }
    }
    this.cleanup()
    this.notifyStatus(false, 'offline')
  }

  /**
   * Purges all private user-scoped channel subscriptions (e.g. user:notifications:*, user:portfolio:*).
   * Call on logout so subscriptions from previous users are never resubscribed.
   */
  public clearPrivateSubscriptions() {
    for (const channel of Array.from(this.subscribers.keys())) {
      if (channel.startsWith('user:')) {
        this.subscribers.delete(channel)
      }
    }
  }

  // ── Subscribe / Unsubscribe ──────────────────────────────────────────────

  public subscribe(channel: string | null | undefined, handler: MessageHandler): () => void {
    if (!channel || !channel.trim()) {
      return () => {}
    }
    const safeChannel = channel.trim()
    let set = this.subscribers.get(safeChannel)
    const isFirst = !set || set.size === 0
    if (!set) {
      set = new Set()
      this.subscribers.set(safeChannel, set)
    }
    set.add(handler)

    if (!this.socket || this.socket.readyState === WebSocket.CLOSED) {
      this.connect()
    } else if (isFirst && this.socket.readyState === WebSocket.OPEN) {
      this.sendSubscribe(safeChannel)
    }

    return () => this.unsubscribe(safeChannel, handler)
  }

  public unsubscribe(channel: string | null | undefined, handler: MessageHandler) {
    if (!channel || !channel.trim()) return
    const safeChannel = channel.trim()
    const set = this.subscribers.get(safeChannel)
    if (set) {
      set.delete(handler)
      if (set.size === 0) {
        this.subscribers.delete(safeChannel)
        if (this.socket && this.socket.readyState === WebSocket.OPEN) {
          this.sendUnsubscribe(safeChannel)
        }
      }
    }
  }

  private sendSubscribe(channel: string) {
    if (!channel || !channel.trim()) return
    const safeChannel = channel.trim()
    if (this.socket && this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ event: 'subscribe', streams: [safeChannel] }))
      wsLog(`subscribe ${safeChannel}`)
    }
  }

  private sendUnsubscribe(channel: string) {
    if (!channel || !channel.trim()) return
    const safeChannel = channel.trim()
    if (this.socket && this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ event: 'unsubscribe', streams: [safeChannel] }))
      wsLog(`unsubscribe ${safeChannel}`)
    }
  }

  private resubscribeAll() {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) return
    const activeChannels = Array.from(this.subscribers.keys()).filter(
      (ch) => Boolean(ch && ch.trim()) && (this.subscribers.get(ch)?.size ?? 0) > 0
    )
    // Send individual subscribe frames per channel (protocol requires singular channel)
    for (const channel of activeChannels) {
      this.sendSubscribe(channel)
    }
  }

  private notifyStatus(connected: boolean, status: ConnectionStatus) {
    this.onStatusHooks.forEach((hook) => {
      try { hook(connected, status) } catch { /* ignore */ }
    })
  }

  private scheduleReconnect() {
    if (this.reconnectTimeout) return
    this.reconnectAttempt++
    const baseDelay = Math.min(30000, 1000 * Math.pow(2, Math.min(this.reconnectAttempt, 5)))
    const jitter = Math.floor(Math.random() * 1000)
    const delay = baseDelay + jitter
    wsLog(`Reconnecting in ${delay}ms (attempt ${this.reconnectAttempt})`)
    this.reconnectTimeout = setTimeout(() => {
      this.reconnectTimeout = null
      this.connect()
    }, delay)
  }

  private measureLatency() {
    if (this.socket && this.socket.readyState === WebSocket.OPEN) {
      this.lastPingSentAt = Date.now()
      this.socket.send(JSON.stringify({ event: 'ping', ts: this.lastPingSentAt }))
    }
  }

  private startHeartbeat() {
    this.stopHeartbeat()
    this.pingInterval = setInterval(() => {
      this.measureLatency()
    }, 15000)
  }

  private stopHeartbeat() {
    if (this.pingInterval) {
      clearInterval(this.pingInterval)
      this.pingInterval = null
    }
  }

  private cleanup() {
    this.stopHeartbeat()
    this.socket = null
    this.currentLatency = 0
  }
}

export const wsService = new WebSocketService()
