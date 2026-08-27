export type MessageHandler = (data: any) => void
export type ConnectionStatus = 'connected' | 'connecting' | 'reconnecting' | 'offline'

const WS_BASE_URL = import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws'

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
    if (this.socket.readyState === WebSocket.CONNECTING) return this.reconnectAttempt > 0 ? 'reconnecting' : 'connecting'
    return 'offline'
  }

  public connect() {
    if (this.socket && (this.socket.readyState === WebSocket.OPEN || this.socket.readyState === WebSocket.CONNECTING)) {
      return
    }

    this.isExplicitlyClosed = false
    this.notifyStatus(false, this.reconnectAttempt > 0 ? 'reconnecting' : 'connecting')
    const token = localStorage.getItem('access_token')
    const url = token ? `${WS_BASE_URL}?token=${encodeURIComponent(token)}` : WS_BASE_URL

    try {
      this.socket = new WebSocket(url)

      this.socket.onopen = () => {
        this.reconnectAttempt = 0
        this.notifyStatus(true, 'connected')
        this.measureLatency()
        this.startHeartbeat()
        this.resubscribeAll()

        this.onReconnectHooks.forEach((hook) => {
          try { hook() } catch { /* ignore */ }
        })
      }

      this.socket.onmessage = (event) => {
        try {
          const lines = event.data.split('\n')
          for (const line of lines) {
            if (!line.trim()) continue
            const parsed = JSON.parse(line)

            if (parsed.event === 'pong' || parsed.type === 'pong') {
              if (this.lastPingSentAt > 0) {
                const rtt = Math.max(1, Date.now() - this.lastPingSentAt)
                this.currentLatency = rtt
                this.onLatencyHooks.forEach((h) => h(rtt))
              }
              continue
            }

            if (parsed.stream) {
              const handlers = this.subscribers.get(parsed.stream)
              if (handlers) {
                handlers.forEach((h) => h(parsed.data))
              }
            }
          }
        } catch {
          // ignore malformed frame
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
        this.notifyStatus(false, 'offline')
        if (this.socket) this.socket.close()
      }
    } catch {
      this.notifyStatus(false, 'offline')
      this.scheduleReconnect()
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

  private resubscribeAll() {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) return
    const activeStreams = Array.from(this.subscribers.keys()).filter(
      (s) => (this.subscribers.get(s)?.size ?? 0) > 0
    )
    if (activeStreams.length > 0) {
      this.socket.send(JSON.stringify({ event: 'subscribe', streams: activeStreams }))
    }
  }

  public subscribe(stream: string, handler: MessageHandler) {
    let set = this.subscribers.get(stream)
    const isFirst = !set || set.size === 0
    if (!set) {
      set = new Set()
      this.subscribers.set(stream, set)
    }
    set.add(handler)

    if (isFirst && this.socket && this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ event: 'subscribe', streams: [stream] }))
    }

    return () => this.unsubscribe(stream, handler)
  }

  public unsubscribe(stream: string, handler: MessageHandler) {
    const set = this.subscribers.get(stream)
    if (set) {
      set.delete(handler)
      if (set.size === 0) {
        this.subscribers.delete(stream)
        if (this.socket && this.socket.readyState === WebSocket.OPEN) {
          this.socket.send(JSON.stringify({ event: 'unsubscribe', streams: [stream] }))
        }
      }
    }
  }

  public isConnected(): boolean {
    return this.socket !== null && this.socket.readyState === WebSocket.OPEN
  }
}

export const wsService = new WebSocketService()
