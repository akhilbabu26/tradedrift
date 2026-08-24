export type MessageHandler = (data: any) => void

const WS_BASE_URL = import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws'

class WebSocketService {
  private socket: WebSocket | null = null
  private subscribers: Map<string, Set<MessageHandler>> = new Map()
  private reconnectAttempt = 0
  private reconnectTimeout: any = null
  private pingInterval: any = null
  private isExplicitlyClosed = false
  private onReconnectHooks: Set<() => void> = new Set()
  private onStatusHooks: Set<(connected: boolean) => void> = new Set()

  constructor() {
    // Initial connection
    this.connect()
  }

  // Register a hook to track connection state (true = connected, false = connecting/closed)
  public onStatus(hook: (connected: boolean) => void) {
    this.onStatusHooks.add(hook)
    hook(this.isConnected())
    return () => this.onStatusHooks.delete(hook)
  }

  // Register a hook to be called whenever WebSocket reconnects (for REST state resync)
  public onReconnect(hook: () => void) {
    this.onReconnectHooks.add(hook)
    return () => this.onReconnectHooks.delete(hook)
  }

  public connect() {
    if (this.socket && (this.socket.readyState === WebSocket.OPEN || this.socket.readyState === WebSocket.CONNECTING)) {
      return
    }

    this.isExplicitlyClosed = false
    this.notifyStatus(false)
    const token = localStorage.getItem('access_token')
    const url = token ? `${WS_BASE_URL}?token=${encodeURIComponent(token)}` : WS_BASE_URL

    try {
      this.socket = new WebSocket(url)

      this.socket.onopen = () => {
        this.reconnectAttempt = 0
        this.notifyStatus(true)
        this.startHeartbeat()
        this.resubscribeAll()

        // Trigger REST state resynchronization
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
        this.notifyStatus(false)
        this.cleanup()
        if (!this.isExplicitlyClosed) {
          this.scheduleReconnect()
        }
      }

      this.socket.onerror = () => {
        this.notifyStatus(false)
        if (this.socket) this.socket.close()
      }
    } catch {
      this.notifyStatus(false)
      this.scheduleReconnect()
    }
  }

  private notifyStatus(connected: boolean) {
    this.onStatusHooks.forEach((hook) => {
      try { hook(connected) } catch { /* ignore */ }
    })
  }

  private scheduleReconnect() {
    if (this.reconnectTimeout) return
    this.reconnectAttempt++

    // Exponential Backoff with Random Jitter: min(30000, 1000 * 2^attempt) + rand(0, 1000)
    const baseDelay = Math.min(30000, 1000 * Math.pow(2, Math.min(this.reconnectAttempt, 5)))
    const jitter = Math.floor(Math.random() * 1000)
    const delay = baseDelay + jitter

    this.reconnectTimeout = setTimeout(() => {
      this.reconnectTimeout = null
      this.connect()
    }, delay)
  }

  private startHeartbeat() {
    this.stopHeartbeat()
    this.pingInterval = setInterval(() => {
      if (this.socket && this.socket.readyState === WebSocket.OPEN) {
        this.socket.send(JSON.stringify({ event: 'ping' }))
      }
    }, 30000)
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
