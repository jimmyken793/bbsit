# WebSocket Status Updates via Listener Pattern

## Problem

bbsit's frontend has no real-time visibility into deployment progress. The project detail page polls at 3-second intervals only while status is "deploying", and the dashboard loads state once on navigation. Users cannot see step-by-step progress, command output, or state transitions as they happen.

## Solution

Add a `DeployListener` interface to the deployer that emits structured events at each deployment lifecycle point. A WebSocket hub implements this interface, broadcasting events to connected browser clients. Clients subscribe to specific projects via messages on the WebSocket.

## Architecture

```
Deployer
  |
  |-- emit(Event) --> DeployListener interface
                           |
                      WebSocket Hub (implements DeployListener)
                           |
                      Per-client goroutines
                           |
                      Browser WebSocket connections
```

## Components

### 1. Event Types (`internal/deployer/listener.go`)

```go
type EventType string

const (
    EventStepStart   EventType = "step_start"
    EventStepDone    EventType = "step_done"
    EventLog         EventType = "log"
    EventStateChange EventType = "state_change"
    EventDeployDone  EventType = "deploy_done"
)

type Event struct {
    Type      EventType `json:"type"`
    ProjectID string    `json:"project_id"`
    Timestamp time.Time `json:"timestamp"`
    Step      string    `json:"step,omitempty"`
    Status    string    `json:"status,omitempty"`
    Message   string    `json:"message,omitempty"`
    Error     bool      `json:"error,omitempty"`
}

type DeployListener interface {
    OnEvent(event Event)
}
```

### 2. Deployer Changes (`internal/deployer/deployer.go`)

- Add `listeners []DeployListener` field and `AddListener(DeployListener)` method
- Add `emit(Event)` helper that fans out to all listeners
- Emit `EventStepStart`/`EventStepDone` at each phase (pull, up, health_check, rollback)
- Emit `EventStateChange` when project status changes
- Emit `EventDeployDone` at the end with success/failure summary

### 3. Command Output Streaming

Change `composeCmd()` to stream stdout/stderr line-by-line instead of collecting with `CombinedOutput()`. Pass an `emitLog func(string, bool)` callback that emits `EventLog` events. This enables real-time command output in the browser.

### 4. WebSocket Hub (`internal/web/ws.go`)

- Manages set of connected clients with their project subscriptions
- Implements `DeployListener` interface
- On `OnEvent`, broadcasts to clients subscribed to that event's project_id
- Each client has a buffered send channel; slow clients are disconnected
- Ping/pong keepalive at 30-second intervals

### 5. WebSocket Protocol

**Endpoint:** `GET /ws` (upgrades to WebSocket)

**Authentication:** Session cookie validated during HTTP upgrade handshake.

**Client -> Server messages:**
```json
{"action": "subscribe", "project_ids": ["project-1", "project-2"]}
{"action": "unsubscribe", "project_ids": ["project-1"]}
```

**Server -> Client messages:**
```json
{"type": "step_start", "project_id": "abc", "timestamp": "...", "step": "pull"}
{"type": "log", "project_id": "abc", "timestamp": "...", "message": "Pulling image..."}
{"type": "step_done", "project_id": "abc", "timestamp": "...", "step": "pull"}
{"type": "state_change", "project_id": "abc", "timestamp": "...", "status": "running"}
{"type": "deploy_done", "project_id": "abc", "timestamp": "...", "status": "running"}
```

### 6. Frontend Changes

- New `useWebSocket` hook: connects to `/ws`, auto-reconnects with backoff, parses events
- Dashboard: subscribes to all projects, updates status badges in real-time
- Project detail: subscribes to that project, renders live deploy log with step progress indicators
- Remove 3-second polling from ProjectDetailPage

## Dependencies

- `github.com/gorilla/websocket` — WebSocket upgrade and connection management

## Files Changed

| File | Change |
|------|--------|
| `internal/deployer/listener.go` | New — Event types, DeployListener interface |
| `internal/deployer/deployer.go` | Add listeners, emit events at each step, stream command output |
| `internal/web/ws.go` | New — WebSocket hub, client management, DeployListener impl |
| `internal/web/server.go` | Register `/ws` route, wire hub as listener on deployer |
| `frontend/src/hooks/useWebSocket.ts` | New — WebSocket connection hook with reconnection |
| `frontend/src/pages/DashboardPage.tsx` | Subscribe to all projects, update badges in real-time |
| `frontend/src/pages/ProjectDetailPage.tsx` | Subscribe to project, show live log, remove polling |
| `go.mod` | Add gorilla/websocket dependency |
