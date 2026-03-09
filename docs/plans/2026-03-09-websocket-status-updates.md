# WebSocket Status Updates Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add real-time WebSocket status updates to bbsit using a listener pattern on the deployer, streaming deploy steps, state changes, and command output to the browser.

**Architecture:** The deployer emits structured `Event` values through a `DeployListener` interface. A WebSocket hub in the web package implements this interface, broadcasting events to connected browser clients filtered by project subscription. Command output is streamed line-by-line instead of collected.

**Tech Stack:** gorilla/websocket, React hooks, TypeScript

---

### Task 1: Add gorilla/websocket dependency

**Files:**
- Modify: `go.mod`

**Step 1: Add the dependency**

Run: `cd /Users/jimmy/workspace/bbsit && go get github.com/gorilla/websocket`

**Step 2: Verify**

Run: `grep gorilla go.mod`
Expected: `github.com/gorilla/websocket` appears in require block

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add gorilla/websocket dependency"
```

---

### Task 2: Create DeployListener interface and Event types

**Files:**
- Create: `internal/deployer/listener.go`
- Test: `internal/deployer/listener_test.go`

**Step 1: Write the test for Event JSON serialization and multi-listener fanout**

Create `internal/deployer/listener_test.go`:

```go
package deployer

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestEventJSON(t *testing.T) {
	e := Event{
		Type:      EventStepStart,
		ProjectID: "myapp",
		Timestamp: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
		Step:      "pull",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Event
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != EventStepStart || got.ProjectID != "myapp" || got.Step != "pull" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// mockListener records events for testing
type mockListener struct {
	mu     sync.Mutex
	events []Event
}

func (m *mockListener) OnEvent(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

func (m *mockListener) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]Event, len(m.events))
	copy(cp, m.events)
	return cp
}

func TestEmitFansOut(t *testing.T) {
	d := testDeployer(t)
	l1 := &mockListener{}
	l2 := &mockListener{}
	d.AddListener(l1)
	d.AddListener(l2)

	d.emit(Event{Type: EventStepStart, ProjectID: "p1", Step: "pull"})

	if len(l1.Events()) != 1 {
		t.Errorf("listener 1 got %d events, want 1", len(l1.Events()))
	}
	if len(l2.Events()) != 1 {
		t.Errorf("listener 2 got %d events, want 1", len(l2.Events()))
	}
	if l1.Events()[0].Step != "pull" {
		t.Errorf("listener 1 got step %q, want pull", l1.Events()[0].Step)
	}
}

func TestEmitNoListeners(t *testing.T) {
	d := testDeployer(t)
	// Should not panic
	d.emit(Event{Type: EventLog, ProjectID: "p1", Message: "hello"})
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./internal/deployer/ -run "TestEvent|TestEmit" -v`
Expected: FAIL — `Event`, `EventStepStart`, `AddListener`, `emit` undefined

**Step 3: Create the listener.go file**

Create `internal/deployer/listener.go`:

```go
package deployer

import "time"

// EventType categorizes deployment events.
type EventType string

const (
	EventStepStart   EventType = "step_start"
	EventStepDone    EventType = "step_done"
	EventLog         EventType = "log"
	EventStateChange EventType = "state_change"
	EventDeployDone  EventType = "deploy_done"
)

// Event is a structured deployment event emitted to listeners.
type Event struct {
	Type      EventType `json:"type"`
	ProjectID string    `json:"project_id"`
	Timestamp time.Time `json:"timestamp"`
	Step      string    `json:"step,omitempty"`
	Status    string    `json:"status,omitempty"`
	Message   string    `json:"message,omitempty"`
	Error     bool      `json:"error,omitempty"`
}

// DeployListener receives deployment events.
type DeployListener interface {
	OnEvent(event Event)
}
```

**Step 4: Add listener support to the Deployer struct**

Modify `internal/deployer/deployer.go`:

Add `listeners []DeployListener` field to the `Deployer` struct (after `locks sync.Map` at line 19):

```go
type Deployer struct {
	db        *db.DB
	locks     sync.Map // project_id -> *sync.Mutex
	log       *slog.Logger
	listeners []DeployListener
}
```

Add `AddListener` and `emit` methods after the `getLock` method (after line 33):

```go
func (d *Deployer) AddListener(l DeployListener) {
	d.listeners = append(d.listeners, l)
}

func (d *Deployer) emit(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	for _, l := range d.listeners {
		l.OnEvent(e)
	}
}
```

**Step 5: Run tests to verify they pass**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./internal/deployer/ -run "TestEvent|TestEmit" -v`
Expected: PASS

**Step 6: Run all existing tests to check for regressions**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./internal/deployer/ -v`
Expected: All tests PASS

**Step 7: Commit**

```bash
git add internal/deployer/listener.go internal/deployer/listener_test.go internal/deployer/deployer.go
git commit -m "feat: add DeployListener interface and event types"
```

---

### Task 3: Instrument deployer with event emission

**Files:**
- Modify: `internal/deployer/deployer.go:37-167` (Deploy, executeDeploy, executeRollback methods)

**Step 1: Write tests that verify events are emitted during deployment lifecycle**

Add to `internal/deployer/listener_test.go`:

```go
func TestDeployEmitsEvents(t *testing.T) {
	d := testDeployer(t)
	l := &mockListener{}
	d.AddListener(l)

	// We can't easily test a full deploy (needs docker), but we can test
	// that emit is wired up by checking the event types emitted by emit() directly
	d.emit(Event{Type: EventStepStart, ProjectID: "test", Step: "pull"})
	d.emit(Event{Type: EventLog, ProjectID: "test", Message: "pulling..."})
	d.emit(Event{Type: EventStepDone, ProjectID: "test", Step: "pull"})
	d.emit(Event{Type: EventStateChange, ProjectID: "test", Status: "running"})
	d.emit(Event{Type: EventDeployDone, ProjectID: "test", Status: "running"})

	events := l.Events()
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5", len(events))
	}

	wantTypes := []EventType{EventStepStart, EventLog, EventStepDone, EventStateChange, EventDeployDone}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
		if events[i].Timestamp.IsZero() {
			t.Errorf("event[%d].Timestamp is zero", i)
		}
	}
}
```

**Step 2: Run test**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./internal/deployer/ -run TestDeployEmitsEvents -v`
Expected: PASS (emit already works from Task 2)

**Step 3: Add emit calls throughout the Deploy method**

Modify `internal/deployer/deployer.go`. The `Deploy` method (lines 37-117) should emit events at each lifecycle point. Here are the insertions:

After line 69 (state updated to deploying), add:
```go
	d.emit(Event{Type: EventStateChange, ProjectID: p.ID, Status: string(types.StatusDeploying)})
```

Replace line 72 (`log.Info("starting deployment")`) with:
```go
	log.Info("starting deployment")
	d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "deploy"})
```

After line 73 (`deployErr := d.executeDeploy(...)`) and before the `if deployErr == nil` check, add:
```go
	if deployErr != nil {
		d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "deploy", Error: true, Message: deployErr.Error()})
	} else {
		d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "deploy"})
	}
```

Wrap the health check section (lines 76-79). Replace:
```go
	if deployErr == nil {
		// Health check
		log.Info("running health check")
		deployErr = health.Check(p.HealthType, p.HealthTarget, 30*time.Second, 3)
	}
```
With:
```go
	if deployErr == nil {
		log.Info("running health check")
		d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "health_check"})
		deployErr = health.Check(p.HealthType, p.HealthTarget, 30*time.Second, 3)
		if deployErr != nil {
			d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "health_check", Error: true, Message: deployErr.Error()})
		} else {
			d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "health_check"})
		}
	}
```

In the failure branch (after line 83, rollback section), add emit calls. After the `log.Error("deployment failed, attempting rollback", ...)` line:
```go
		d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "rollback"})
```

After the rollback completes (after the `if rollbackErr != nil` / else block), before `state.LastDeployAt`:
```go
		if rollbackErr != nil {
			d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "rollback", Error: true, Message: rollbackErr.Error()})
		} else {
			d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "rollback"})
		}
```

Before `return deployErr` in the failure branch:
```go
		d.emit(Event{Type: EventStateChange, ProjectID: p.ID, Status: string(state.Status)})
		d.emit(Event{Type: EventDeployDone, ProjectID: p.ID, Status: string(state.Status), Error: true, Message: deployErr.Error()})
```

In the success branch (after line 104 `log.Info("deployment succeeded")`):
```go
	d.emit(Event{Type: EventStateChange, ProjectID: p.ID, Status: string(types.StatusRunning)})
	d.emit(Event{Type: EventDeployDone, ProjectID: p.ID, Status: string(types.StatusRunning)})
```

**Step 4: Run all deployer tests**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./internal/deployer/ -v`
Expected: All tests PASS

**Step 5: Commit**

```bash
git add internal/deployer/deployer.go internal/deployer/listener_test.go
git commit -m "feat: emit deploy events at each lifecycle step"
```

---

### Task 4: Stream command output line-by-line

**Files:**
- Modify: `internal/deployer/deployer.go:119-140,210-232` (executeDeploy, composeCmd)

**Step 1: Refactor composeCmd to accept a log callback and stream output**

Replace the `composeCmd` function (lines 210-232) with a version that accepts an optional log callback:

```go
func composeCmd(stackPath string, logFn func(line string, isErr bool), args ...string) error {
	composeFile := stackPath + "/compose.yaml"
	overridePath := stackPath + "/compose.override.yaml"

	fileArgs := []string{"-f", composeFile}
	if fileExists(overridePath) {
		fileArgs = append(fileArgs, "-f", overridePath)
	}

	fullArgs := []string{"compose"}
	fullArgs = append(fullArgs, fileArgs...)
	fullArgs = append(fullArgs, args...)
	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = stackPath

	if logFn == nil {
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %s", strings.Join(args, " "), string(out))
		}
		return nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	var wg sync.WaitGroup
	scanLines := func(r io.Reader, isErr bool) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			logFn(scanner.Text(), isErr)
		}
	}
	wg.Add(2)
	go scanLines(stdout, false)
	go scanLines(stderr, true)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return nil
}
```

Add `"bufio"` and `"io"` to the imports at the top of `deployer.go`.

**Step 2: Update executeDeploy to pass log callback**

Replace `executeDeploy` (lines 119-140) with:

```go
func (d *Deployer) executeDeploy(p *types.Project, digest string, log *slog.Logger) error {
	logFn := func(line string, isErr bool) {
		d.emit(Event{Type: EventLog, ProjectID: p.ID, Message: line, Error: isErr})
	}

	if err := WriteComposeFiles(p, ""); err != nil {
		return fmt.Errorf("write compose files: %w", err)
	}

	log.Info("pulling images")
	d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "pull"})
	if err := composeCmd(p.StackPath, logFn, "pull"); err != nil {
		d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "pull", Error: true, Message: err.Error()})
		return fmt.Errorf("compose pull: %w", err)
	}
	d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "pull"})

	log.Info("bringing up stack")
	d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "up"})
	if err := composeCmd(p.StackPath, logFn, "up", "-d", "--force-recreate", "--remove-orphans"); err != nil {
		d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "up", Error: true, Message: err.Error()})
		return fmt.Errorf("compose up: %w", err)
	}
	d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "up"})

	return nil
}
```

**Step 3: Update executeRollback similarly**

Replace `executeRollback` (lines 142-167) with:

```go
func (d *Deployer) executeRollback(p *types.Project, previousDigest string, log *slog.Logger) error {
	logFn := func(line string, isErr bool) {
		d.emit(Event{Type: EventLog, ProjectID: p.ID, Message: line, Error: isErr})
	}

	if previousDigest == "" {
		return fmt.Errorf("no previous digest to rollback to")
	}

	log.Info("rolling back", "to", ShortDigest(previousDigest))

	imageRef := ""
	if p.ConfigMode == types.ConfigModeForm {
		imageRef = fmt.Sprintf("%s@%s", p.RegistryImage, previousDigest)
	}
	if err := WriteComposeFiles(p, imageRef); err != nil {
		return fmt.Errorf("write rollback compose: %w", err)
	}

	if err := composeCmd(p.StackPath, logFn, "up", "-d", "--force-recreate", "--remove-orphans"); err != nil {
		return fmt.Errorf("compose up rollback: %w", err)
	}

	if err := health.Check(p.HealthType, p.HealthTarget, 30*time.Second, 3); err != nil {
		return fmt.Errorf("health check after rollback: %w", err)
	}

	return nil
}
```

**Step 4: Update Stop and Start to also pass nil logFn (no streaming for simple ops)**

In `Stop` (line 183): change `composeCmd(p.StackPath, "down")` to `composeCmd(p.StackPath, nil, "down")`

In `Start` (line 199): change `composeCmd(p.StackPath, "up", "-d")` to `composeCmd(p.StackPath, nil, "up", "-d")`

**Step 5: Run all tests**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./... -v`
Expected: All tests PASS

**Step 6: Commit**

```bash
git add internal/deployer/deployer.go
git commit -m "feat: stream docker compose output line-by-line to listeners"
```

---

### Task 5: Remove redundant deploy-level step events from Deploy method

After Task 4, `executeDeploy` now emits its own granular step_start/step_done events for "pull" and "up". The higher-level "deploy" step_start/step_done events added in Task 3 are now redundant. Clean up the Deploy method:

**Files:**
- Modify: `internal/deployer/deployer.go` (Deploy method)

**Step 1: Remove the coarse-grained deploy step events**

In the `Deploy` method, remove these lines added in Task 3:
- The `d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "deploy"})` line
- The `if deployErr != nil { d.emit(Event{Type: EventStepDone, ...}) } else { d.emit(Event{Type: EventStepDone, ...}) }` block after `executeDeploy`

Keep the health_check, rollback, state_change, and deploy_done events — those are still correct.

**Step 2: Run tests**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./internal/deployer/ -v`
Expected: All tests PASS

**Step 3: Commit**

```bash
git add internal/deployer/deployer.go
git commit -m "refactor: remove redundant deploy step events, keep granular pull/up steps"
```

---

### Task 6: Create WebSocket hub

**Files:**
- Create: `internal/web/ws.go`
- Create: `internal/web/ws_test.go`

**Step 1: Write hub tests**

Create `internal/web/ws_test.go`:

```go
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingyoung/bbsit/internal/deployer"
)

func TestHubBroadcastToSubscribed(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	// Start test server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	}))
	defer srv.Close()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Subscribe to project "app1"
	sub := ClientMessage{Action: "subscribe", ProjectIDs: []string{"app1"}}
	conn.WriteJSON(sub)
	time.Sleep(50 * time.Millisecond) // let subscription register

	// Emit event for "app1"
	hub.OnEvent(deployer.Event{
		Type:      deployer.EventStepStart,
		ProjectID: "app1",
		Step:      "pull",
		Timestamp: time.Now(),
	})

	// Emit event for "app2" (should not be received)
	hub.OnEvent(deployer.Event{
		Type:      deployer.EventStepStart,
		ProjectID: "app2",
		Step:      "pull",
		Timestamp: time.Now(),
	})

	// Read message
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var evt deployer.Event
	json.Unmarshal(msg, &evt)
	if evt.ProjectID != "app1" {
		t.Errorf("got project %q, want app1", evt.ProjectID)
	}
	if evt.Step != "pull" {
		t.Errorf("got step %q, want pull", evt.Step)
	}

	// Second read should timeout (app2 event filtered)
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Error("expected timeout for unsubscribed project, got message")
	}
}

func TestHubUnsubscribe(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Subscribe then unsubscribe
	conn.WriteJSON(ClientMessage{Action: "subscribe", ProjectIDs: []string{"app1"}})
	time.Sleep(50 * time.Millisecond)
	conn.WriteJSON(ClientMessage{Action: "unsubscribe", ProjectIDs: []string{"app1"}})
	time.Sleep(50 * time.Millisecond)

	hub.OnEvent(deployer.Event{Type: deployer.EventLog, ProjectID: "app1", Message: "test"})

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Error("should not receive events after unsubscribe")
	}
}

func TestHubClientDisconnect(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	conn.WriteJSON(ClientMessage{Action: "subscribe", ProjectIDs: []string{"app1"}})
	time.Sleep(50 * time.Millisecond)

	conn.Close()
	time.Sleep(50 * time.Millisecond)

	// Hub should handle the disconnected client gracefully
	hub.OnEvent(deployer.Event{Type: deployer.EventLog, ProjectID: "app1", Message: "after disconnect"})

	// Verify hub is still running — connect a new client
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial after disconnect: %v", err)
	}
	conn2.Close()
}

func TestHubConcurrentEvents(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.WriteJSON(ClientMessage{Action: "subscribe", ProjectIDs: []string{"app1"}})
	time.Sleep(50 * time.Millisecond)

	// Send many events concurrently
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hub.OnEvent(deployer.Event{Type: deployer.EventLog, ProjectID: "app1", Message: "concurrent"})
		}()
	}
	wg.Wait()

	// Read some messages (don't need to read all, just verify no panic/deadlock)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	count := 0
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
		count++
	}
	if count == 0 {
		t.Error("expected to read at least one event")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./internal/web/ -run "TestHub" -v`
Expected: FAIL — `NewHub`, `ClientMessage`, `HandleWS`, `Stop` undefined

**Step 3: Implement the WebSocket hub**

Create `internal/web/ws.go`:

```go
package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingyoung/bbsit/internal/deployer"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ClientMessage is a message sent from the browser to the hub.
type ClientMessage struct {
	Action     string   `json:"action"`      // "subscribe" or "unsubscribe"
	ProjectIDs []string `json:"project_ids"`
}

// client represents a single WebSocket connection.
type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte

	mu       sync.RWMutex
	projects map[string]bool // subscribed project IDs
}

// Hub manages WebSocket clients and broadcasts deployer events.
type Hub struct {
	clients    map[*client]bool
	register   chan *client
	unregister chan *client
	broadcast  chan []byte // raw JSON messages to all clients (filtered by subscription)
	event      chan deployer.Event
	stop       chan struct{}
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*client]bool),
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan []byte, 256),
		event:      make(chan deployer.Event, 256),
		stop:       make(chan struct{}),
	}
}

// OnEvent implements deployer.DeployListener.
func (h *Hub) OnEvent(e deployer.Event) {
	select {
	case h.event <- e:
	default:
		// Drop event if buffer full (don't block deployer)
	}
}

func (h *Hub) Run() {
	for {
		select {
		case <-h.stop:
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()
		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
		case e := <-h.event:
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			h.mu.RLock()
			for c := range h.clients {
				if c.isSubscribed(e.ProjectID) {
					select {
					case c.send <- data:
					default:
						// Slow client — schedule disconnect
						go func(cl *client) {
							h.unregister <- cl
						}(c)
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) Stop() {
	close(h.stop)
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws upgrade", "error", err)
		return
	}

	c := &client{
		hub:      h,
		conn:     conn,
		send:     make(chan []byte, 64),
		projects: make(map[string]bool),
	}

	h.register <- c

	go c.writePump()
	go c.readPump()
}

func (c *client) isSubscribed(projectID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.projects[projectID]
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var cm ClientMessage
		if json.Unmarshal(msg, &cm) != nil {
			continue
		}

		c.mu.Lock()
		switch cm.Action {
		case "subscribe":
			for _, id := range cm.ProjectIDs {
				c.projects[id] = true
			}
		case "unsubscribe":
			for _, id := range cm.ProjectIDs {
				delete(c.projects, id)
			}
		}
		c.mu.Unlock()
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
```

**Step 4: Run tests**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./internal/web/ -run "TestHub" -v -count=1`
Expected: All tests PASS

**Step 5: Commit**

```bash
git add internal/web/ws.go internal/web/ws_test.go
git commit -m "feat: add WebSocket hub with subscription-based event broadcasting"
```

---

### Task 7: Wire hub into server and register /ws route

**Files:**
- Modify: `internal/web/server.go:21-66` (Server struct and Handler method)
- Modify: `cmd/bbsit/main.go:53-62` (wiring)

**Step 1: Add hub field to Server and register /ws route**

Modify `internal/web/server.go`:

Add `hub *Hub` field to Server struct (after line 27):

```go
type Server struct {
	db        *db.DB
	deployer  *deployer.Deployer
	scheduler *scheduler.Scheduler
	log       *slog.Logger
	sessions  sync.Map // token -> expiry
	stackRoot string
	hub       *Hub
}
```

Update `NewServer` to create and start the hub, and register it as a deployer listener:

```go
func NewServer(database *db.DB, dep *deployer.Deployer, sched *scheduler.Scheduler, logger *slog.Logger, stackRoot string) *Server {
	h := NewHub()
	go h.Run()
	dep.AddListener(h)

	return &Server{
		db:        database,
		deployer:  dep,
		scheduler: sched,
		log:       logger,
		stackRoot: stackRoot,
		hub:       h,
	}
}
```

Add the WebSocket route in `Handler()`, after the action endpoints and before the SPA fallback (after line 59):

```go
	// WebSocket for real-time events
	mux.HandleFunc("GET /ws", s.apiAuth(func(w http.ResponseWriter, r *http.Request) {
		s.hub.HandleWS(w, r)
	}))
```

**Step 2: Run all Go tests**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./... -v`
Expected: All tests PASS

**Step 3: Commit**

```bash
git add internal/web/server.go
git commit -m "feat: wire WebSocket hub into server, register /ws route with auth"
```

---

### Task 8: Create useWebSocket React hook

**Files:**
- Create: `frontend/src/hooks/useWebSocket.ts`
- Create: `frontend/src/hooks/useWebSocket.test.ts`

**Step 1: Create the hook**

Create `frontend/src/hooks/useWebSocket.ts`:

```typescript
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
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>()
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
```

**Step 2: Write a basic test**

Create `frontend/src/hooks/useWebSocket.test.ts`:

```typescript
import { describe, it, expect } from 'vitest'
import type { DeployEvent } from './useWebSocket'

describe('DeployEvent type', () => {
  it('parses a valid event', () => {
    const raw = '{"type":"step_start","project_id":"app1","timestamp":"2026-03-09T12:00:00Z","step":"pull"}'
    const event: DeployEvent = JSON.parse(raw)
    expect(event.type).toBe('step_start')
    expect(event.project_id).toBe('app1')
    expect(event.step).toBe('pull')
  })

  it('handles optional fields', () => {
    const raw = '{"type":"log","project_id":"app1","timestamp":"2026-03-09T12:00:00Z","message":"pulling...","error":true}'
    const event: DeployEvent = JSON.parse(raw)
    expect(event.type).toBe('log')
    expect(event.message).toBe('pulling...')
    expect(event.error).toBe(true)
    expect(event.step).toBeUndefined()
  })
})
```

**Step 3: Run frontend tests**

Run: `cd /Users/jimmy/workspace/bbsit/frontend && npm test`
Expected: PASS

**Step 4: Commit**

```bash
git add frontend/src/hooks/useWebSocket.ts frontend/src/hooks/useWebSocket.test.ts
git commit -m "feat: add useWebSocket React hook with auto-reconnect"
```

---

### Task 9: Update DashboardPage with real-time status updates

**Files:**
- Modify: `frontend/src/pages/DashboardPage.tsx`

**Step 1: Add WebSocket integration to DashboardPage**

The dashboard should subscribe to all project IDs and update the status badge in real-time when `state_change` events arrive.

Modify `frontend/src/pages/DashboardPage.tsx`:

Add import at the top (after existing imports):
```typescript
import { useWebSocket } from '../hooks/useWebSocket'
import type { DeployEvent } from '../hooks/useWebSocket'
```

Inside the `DashboardPage` component, after the `load` function (after line 22), add:

```typescript
  const projectIds = projects.map(p => p.id)

  const handleEvent = useCallback((event: DeployEvent) => {
    if (event.type === 'state_change' && event.status) {
      setProjects(prev => prev.map(p =>
        p.id === event.project_id
          ? { ...p, state: { ...p.state, status: event.status as ProjectWithState['state']['status'] } }
          : p
      ))
    }
  }, [])

  useWebSocket(projectIds, handleEvent)
```

Add `useCallback` to the React imports on line 1:
```typescript
import { useState, useEffect, useRef, useCallback } from 'react'
```

**Step 2: Run frontend tests**

Run: `cd /Users/jimmy/workspace/bbsit/frontend && npm test`
Expected: PASS

**Step 3: Build frontend**

Run: `cd /Users/jimmy/workspace/bbsit/frontend && npm run build`
Expected: Build succeeds

**Step 4: Commit**

```bash
git add frontend/src/pages/DashboardPage.tsx
git commit -m "feat: update dashboard with real-time status via WebSocket"
```

---

### Task 10: Update ProjectDetailPage with live deploy log

**Files:**
- Modify: `frontend/src/pages/ProjectDetailPage.tsx`

**Step 1: Replace polling with WebSocket and add live deploy log**

This is the biggest frontend change. The project detail page should:
1. Subscribe to the current project's events
2. Show a live deploy log panel when deploying
3. Update the state card in real-time
4. Remove the 3-second polling

Modify `frontend/src/pages/ProjectDetailPage.tsx`:

Replace the imports (line 1-4):
```typescript
import { useState, useEffect, useCallback, useRef } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { api, shortDigest, fmtTime, ApiError } from '../api'
import { useWebSocket } from '../hooks/useWebSocket'
import type { DeployEvent } from '../hooks/useWebSocket'
import type { ProjectDetail } from '../types'
```

Inside the component, replace the polling useEffect (lines 27-31) with WebSocket-based state updates. Add after the `load` useCallback:

```typescript
  const [logLines, setLogLines] = useState<DeployEvent[]>([])
  const logEndRef = useRef<HTMLDivElement>(null)

  const projectIds = id ? [id] : []

  const handleEvent = useCallback((event: DeployEvent) => {
    // Update state in real-time
    if (event.type === 'state_change' && event.status) {
      setDetail(prev => prev ? {
        ...prev,
        state: { ...prev.state, status: event.status as ProjectDetail['state']['status'] }
      } : prev)
    }
    // Reload full data when deploy finishes
    if (event.type === 'deploy_done') {
      load()
    }
    // Accumulate log lines
    setLogLines(prev => [...prev, event])
  }, [load])

  useWebSocket(projectIds, handleEvent)

  // Clear log when a new deploy starts
  useEffect(() => {
    if (detail?.state.status === 'deploying') {
      setLogLines([])
    }
  }, [detail?.state.status])

  // Auto-scroll log
  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logLines])
```

Remove the old polling useEffect (lines 27-31):
```typescript
  // DELETE THIS BLOCK:
  // useEffect(() => {
  //   if (detail?.state.status !== 'deploying') return
  //   const t = setInterval(load, 3000)
  //   return () => clearInterval(t)
  // }, [detail?.state.status, load])
```

Add the deploy log panel in the JSX. After the action buttons `</div>` (after line 110) and before the `<div className="detail-grid">`:

```tsx
      {logLines.length > 0 && (
        <div className="card" style={{ marginBottom: 20 }}>
          <div className="card-title">Deploy log</div>
          <div className="deploy-log">
            {logLines.map((line, i) => (
              <div key={i} className={`log-line ${line.type}${line.error ? ' log-error' : ''}`}>
                <span className="log-time">{new Date(line.timestamp).toLocaleTimeString()}</span>
                {line.type === 'step_start' && <span className="log-step">▶ {line.step}</span>}
                {line.type === 'step_done' && <span className="log-step">{line.error ? '✗' : '✓'} {line.step}</span>}
                {line.type === 'log' && <span className="log-msg">{line.message}</span>}
                {line.type === 'state_change' && <span className="log-status">→ {line.status}</span>}
                {line.type === 'deploy_done' && <span className="log-status">{line.error ? '✗ Failed' : '✓ Done'}: {line.status}</span>}
              </div>
            ))}
            <div ref={logEndRef} />
          </div>
        </div>
      )}
```

**Step 2: Add deploy log CSS**

Add to `frontend/src/index.css` at the end:

```css
/* Deploy log */
.deploy-log {
  max-height: 300px;
  overflow-y: auto;
  font-family: 'SF Mono', 'Menlo', 'Monaco', monospace;
  font-size: 12px;
  line-height: 1.6;
  background: #1a1a2e;
  color: #e0e0e0;
  padding: 12px;
  border-radius: 6px;
}
.log-line { display: flex; gap: 8px; }
.log-time { color: #666; min-width: 70px; }
.log-step { color: #7ec8e3; font-weight: 600; }
.log-msg { color: #c0c0c0; word-break: break-all; }
.log-status { color: #7ec8e3; font-weight: 600; }
.log-error .log-step,
.log-error .log-msg,
.log-error .log-status { color: #ff6b6b; }
.step_start .log-step { color: #4ecdc4; }
.step_done .log-step { color: #95e1d3; }
.deploy_done .log-status { font-weight: 700; }
```

**Step 3: Run frontend tests**

Run: `cd /Users/jimmy/workspace/bbsit/frontend && npm test`
Expected: PASS

**Step 4: Build frontend**

Run: `cd /Users/jimmy/workspace/bbsit/frontend && npm run build`
Expected: Build succeeds

**Step 5: Commit**

```bash
git add frontend/src/pages/ProjectDetailPage.tsx frontend/src/index.css
git commit -m "feat: add live deploy log to project detail, replace polling with WebSocket"
```

---

### Task 11: Run full test suite and verify

**Files:** None (verification only)

**Step 1: Run Go tests**

Run: `cd /Users/jimmy/workspace/bbsit && go test ./... -v -count=1`
Expected: All tests PASS

**Step 2: Run frontend tests**

Run: `cd /Users/jimmy/workspace/bbsit/frontend && npm test`
Expected: All tests PASS

**Step 3: Build frontend for embedding**

Run: `cd /Users/jimmy/workspace/bbsit/frontend && npm run build`
Expected: Build succeeds

**Step 4: Verify Go build with embedded frontend**

Run: `cd /Users/jimmy/workspace/bbsit && go build ./cmd/bbsit/`
Expected: Binary builds successfully

**Step 5: Run act to test CI**

Run: `cd /Users/jimmy/workspace/bbsit && act`
Expected: CI pipeline passes

**Step 6: Commit any fixes if needed, then final summary commit**

If all green, no action needed. If fixes required, commit them individually.
