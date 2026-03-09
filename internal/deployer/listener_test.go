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
