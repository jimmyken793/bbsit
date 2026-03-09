import { useEffect, useRef, useCallback, useState } from 'react'

export type EventType = 'step_start' | 'step_done' | 'log' | 'state_change' | 'deploy_done'

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

export function useWebSocket(projectIds: string[], onEvent: EventHandler) {
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const onEventRef = useRef(onEvent)
  const [connected, setConnected] = useState(false)

  // Keep callback ref current without reconnecting
  onEventRef.current = onEvent

  const connect = useCallback(() => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${proto}//${window.location.host}/ws`)
    wsRef.current = ws

    ws.onopen = () => {
      setConnected(true)
      if (projectIds.length > 0) {
        ws.send(JSON.stringify({ action: 'subscribe', project_ids: projectIds }))
      }
    }

    ws.onmessage = (e) => {
      try {
        const event: DeployEvent = JSON.parse(e.data)
        onEventRef.current(event)
      } catch { /* ignore malformed messages */ }
    }

    ws.onclose = () => {
      setConnected(false)
      wsRef.current = null
      // Reconnect with backoff
      reconnectTimer.current = setTimeout(connect, 3000)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [projectIds])

  // Connect on mount, reconnect when projectIds change
  useEffect(() => {
    connect()
    return () => {
      clearTimeout(reconnectTimer.current)
      wsRef.current?.close()
    }
  }, [connect])

  // Update subscriptions when projectIds change while connected
  useEffect(() => {
    const ws = wsRef.current
    if (ws && ws.readyState === WebSocket.OPEN && projectIds.length > 0) {
      ws.send(JSON.stringify({ action: 'subscribe', project_ids: projectIds }))
    }
  }, [projectIds])

  return { connected }
}
