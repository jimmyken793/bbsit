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
