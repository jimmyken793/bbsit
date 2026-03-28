import { createContext, useContext, useEffect, useRef, useCallback, useState, useMemo } from 'react'

export type EventType = 'step_start' | 'step_done' | 'log' | 'state_change' | 'deploy_done' | 'poll_done' | 'project_updated' | 'project_deleted'

export interface DeployEvent {
  type: EventType
  project_id: string
  timestamp: string
  step?: string
  status?: string
  message?: string
  error?: boolean
}

type EventHandler = (event: DeployEvent) => void
type Unsubscribe = () => void

interface WebSocketContextValue {
  subscribe: (projectIds: string[], handler: EventHandler) => Unsubscribe
  connected: boolean
}

export const WebSocketContext = createContext<WebSocketContextValue | null>(null)

/**
 * Hook that manages a single shared WebSocket connection.
 * Used by WebSocketProvider in App — not called directly by pages.
 */
export function useWebSocketConnection() {
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const [connected, setConnected] = useState(false)

  // Each subscriber: { projectIds, handler }
  const subscribersRef = useRef<Map<number, { projectIds: string[]; handler: EventHandler }>>(new Map())
  const nextIdRef = useRef(0)
  // Track what we last sent to avoid duplicate subscribe messages
  const lastSentRef = useRef<string>('')

  const syncSubscriptions = useCallback(() => {
    const ws = wsRef.current
    if (!ws || ws.readyState !== WebSocket.OPEN) return

    // Collect all unique project IDs across subscribers
    const allIds = new Set<string>()
    for (const sub of subscribersRef.current.values()) {
      for (const id of sub.projectIds) allIds.add(id)
    }

    const sorted = [...allIds].sort()
    const key = sorted.join(',')

    // Skip if nothing changed since last send
    if (key === lastSentRef.current) return
    lastSentRef.current = key

    if (sorted.length > 0) {
      ws.send(JSON.stringify({ action: 'subscribe', project_ids: sorted }))
    } else {
      // No subscribers left — unsubscribe from everything on backend
      ws.send(JSON.stringify({ action: 'unsubscribe_all' }))
    }
  }, [])

  const connect = useCallback(() => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${proto}//${window.location.host}/ws`)
    wsRef.current = ws

    ws.onopen = () => {
      setConnected(true)
      // Force re-send on reconnect
      lastSentRef.current = ''
      syncSubscriptions()
    }

    ws.onmessage = (e) => {
      try {
        const event: DeployEvent = JSON.parse(e.data)
        // Fan out to all subscribers interested in this project
        for (const sub of subscribersRef.current.values()) {
          if (sub.projectIds.includes(event.project_id)) {
            sub.handler(event)
          }
        }
      } catch { /* ignore malformed messages */ }
    }

    ws.onclose = () => {
      setConnected(false)
      wsRef.current = null
      reconnectTimer.current = setTimeout(connect, 3000)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [syncSubscriptions])

  // Connect once on mount
  useEffect(() => {
    connect()
    return () => {
      clearTimeout(reconnectTimer.current)
      wsRef.current?.close()
    }
  }, [connect])

  const subscribe = useCallback((projectIds: string[], handler: EventHandler): Unsubscribe => {
    const id = nextIdRef.current++
    subscribersRef.current.set(id, { projectIds, handler })
    syncSubscriptions()

    return () => {
      subscribersRef.current.delete(id)
      syncSubscriptions()
    }
  }, [syncSubscriptions])

  return { subscribe, connected }
}

/**
 * Hook for pages to subscribe to WebSocket events for specific projects.
 * Requires WebSocketProvider to be mounted above in the component tree.
 */
export function useWebSocket(projectIds: string[], onEvent: EventHandler) {
  const ctx = useContext(WebSocketContext)
  const onEventRef = useRef(onEvent)
  onEventRef.current = onEvent

  // Stabilize projectIds — only change when the actual IDs change, not the array reference
  const stableIds = useMemo(() => projectIds, [projectIds.join(',')])

  useEffect(() => {
    if (!ctx || stableIds.length === 0) return
    return ctx.subscribe(stableIds, (event) => onEventRef.current(event))
  }, [ctx, stableIds])

  return { connected: ctx?.connected ?? false }
}
