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

export type MessageHandler = (data: unknown) => void
export type ConnectionStatus = 'connected' | 'connecting' | 'reconnecting' | 'offline'

const WS_BASE_URL = import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws'

// ── Channel name helpers ─────────────────────────────────────────────────────
export const WsChannels = {
  orderbook: (marketId: string) => `market:orderbook:${marketId}`,
  ticker:    (marketId: string) => `market:ticker:${marketId}`,
  trades:    (marketId: string) => `market:trades:${marketId}`,
  userNotifications: ()          => 'user:notifications',
} as const

// ── Dev-mode API logger (never logs tokens or sensitive data) ────────────────
const isDev = import.meta.env.DEV
function wsLog(msg: string) {
  if (isDev) console.debug(`[WS] ${msg}`)
}

class WebSocketService {
  private socket: WebSocket | null = null
  private subscribers: Map<string, Set<MessageHandler>> = new Map()
  private reconnectAttempt = 0
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null
  private pingInterval: ReturnType<typeof setInterval> | null = null
  private isExplicitlyClosed = false
  private onReconnectHooks: Set<() => void> = new Set()
  private onStatusHooks: Set<(connected: boolean, status: ConnectionStatus) => void> = new Set()
  private onLatencyHooks: Set<(ms: number) => void> = new Set()
  private lastPingSentAt = 0
  private currentLatency = 0

  constructor() {
    this.connect()
  }

  // ── Public lifecycle hooks ───────────────────────────────────────────────

  public onStatus(hook: (connected: boolean, status: ConnectionStatus) => void) {
    this.onStatusHooks.add(hook)
    hook(this.isConnected(), this.getStatus())
    return () => this.onStatusHooks.delete(hook)
  }

  public onLatency(hook: (ms: number) => void) {
    this.onLatencyHooks.add(hook)
    hook(this.currentLatency)
    return () => this.onLatencyHooks.delete(hook)
  }

  public onReconnect(hook: () => void) {
    this.onReconnectHooks.add(hook)
    return () => this.onReconnectHooks.delete(hook)
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
    
    this.notifyStatus(false, this.reconnectAttempt > 0 ? 'reconnecting' : 'connecting')

    // Pass JWT as query param for initial connection (per backend docs)
    const token = localStorage.getItem('access_token')
    const url = token ? `${WS_BASE_URL}?token=${encodeURIComponent(token)}` : WS_BASE_URL

    wsLog(`Connecting to ${WS_BASE_URL}`)

    try {
      this.socket = new WebSocket(url)

      this.socket.onopen = () => {
        this.reconnectAttempt = 0
        this.notifyStatus(true, 'connected')
        wsLog('Connected')

        // Send auth frame for private channels (per backend protocol)
        this.sendAuthFrame()

        this.measureLatency()
        this.startHeartbeat()
        this.resubscribeAll()

        this.onReconnectHooks.forEach((hook) => {
          try { hook() } catch { /* ignore */ }
        })
      }

      this.socket.onmessage = (event) => {
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

      this.socket.onclose = () => {
        
        this.notifyStatus(false, 'offline')
        this.cleanup()
        if (!this.isExplicitlyClosed) {
          this.scheduleReconnect()
        }
      }

      this.socket.onerror = () => {
        wsLog('Error — closing socket')
        this.notifyStatus(false, 'offline')
        if (this.socket) this.socket.close()
      }
    } catch {
      this.notifyStatus(false, 'offline')
      this.scheduleReconnect()
    }
  }

  public disconnect() {
    this.isExplicitlyClosed = true
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout)
      this.reconnectTimeout = null
    }
    if (this.socket) {
      this.socket.close()
    }
    this.cleanup()
    this.notifyStatus(false, 'offline')
  }

  // ── Subscribe / Unsubscribe ──────────────────────────────────────────────

  public subscribe(channel: string, handler: MessageHandler) {
    let set = this.subscribers.get(channel)
    const isFirst = !set || set.size === 0
    if (!set) {
      set = new Set()
      this.subscribers.set(channel, set)
    }
    set.add(handler)

    if (isFirst && this.socket && this.socket.readyState === WebSocket.OPEN) {
      this.sendSubscribe(channel)
    }

    return () => this.unsubscribe(channel, handler)
  }

  public unsubscribe(channel: string, handler: MessageHandler) {
    const set = this.subscribers.get(channel)
    if (set) {
      set.delete(handler)
      if (set.size === 0) {
        this.subscribers.delete(channel)
        if (this.socket && this.socket.readyState === WebSocket.OPEN) {
          this.sendUnsubscribe(channel)
        }
      }
    }
  }

  // ── Private helpers ──────────────────────────────────────────────────────

  private sendAuthFrame() {
    const token = localStorage.getItem('access_token')
    if (token && this.socket && this.socket.readyState === WebSocket.OPEN) {
      // Send auth frame — NEVER log the token value
      this.socket.send(JSON.stringify({ action: 'auth', token }))
      
      wsLog('Auth frame sent')
    }
  }

  private sendSubscribe(channel: string) {
    if (this.socket && this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ action: 'subscribe', channel }))
      wsLog(`subscribe ${channel}`)
    }
  }

  private sendUnsubscribe(channel: string) {
    if (this.socket && this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ action: 'unsubscribe', channel }))
      wsLog(`unsubscribe ${channel}`)
    }
  }

  private resubscribeAll() {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) return
    const activeChannels = Array.from(this.subscribers.keys()).filter(
      (ch) => (this.subscribers.get(ch)?.size ?? 0) > 0
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
