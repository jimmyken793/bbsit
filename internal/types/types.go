package types

import "time"

// ConfigMode determines how compose.yaml is generated
type ConfigMode string

const (
	ConfigModeForm   ConfigMode = "form"   // bbsit generates compose.yaml from structured fields
	ConfigModeCustom ConfigMode = "custom" // user provides compose.yaml via stack config
)

type ProjectStatus string

const (
	StatusUnknown   ProjectStatus = "unknown"
	StatusRunning   ProjectStatus = "running"
	StatusStopped   ProjectStatus = "stopped"
	StatusDeploying ProjectStatus = "deploying"
	StatusFailed    ProjectStatus = "failed"
	StatusRolledBack ProjectStatus = "rolled_back"
)

type DeployTrigger string

const (
	TriggerPoll    DeployTrigger = "poll"
	TriggerManual  DeployTrigger = "manual"
	TriggerStartup DeployTrigger = "startup"
)

type DeployStatus string

const (
	DeploySuccess    DeployStatus = "success"
	DeployFailed     DeployStatus = "failed"
	DeployRolledBack DeployStatus = "rolled_back"
	DeployInProgress DeployStatus = "in_progress"
)

type HealthType string

const (
	HealthHTTP HealthType = "http"
	HealthTCP  HealthType = "tcp"
	HealthNone HealthType = "none"
)

// PortMapping represents a single host:container port mapping
type PortMapping struct {
	HostPort      int    `json:"host_port" yaml:"host_port"`
	ContainerPort int    `json:"container_port" yaml:"container_port"`
	Protocol      string `json:"protocol,omitempty" yaml:"protocol,omitempty"` // tcp (default) | udp
}

// VolumeMount represents a bind mount
type VolumeMount struct {
	HostPath      string `json:"host_path" yaml:"host_path"`
	ContainerPath string `json:"container_path" yaml:"container_path"`
	ReadOnly      bool   `json:"readonly,omitempty" yaml:"readonly,omitempty"`
}

// Project is the full project definition stored in SQLite
type Project struct {
	ID            string     `json:"id"`
	DisplayName   string     `json:"display_name"`
	ConfigMode    ConfigMode `json:"config_mode"`

	// Form mode fields
	RegistryImage string        `json:"registry_image,omitempty"` // e.g. registry.example.com/webui
	ImageTag      string        `json:"image_tag,omitempty"`      // e.g. latest
	Ports         []PortMapping `json:"ports,omitempty"`
	Volumes       []VolumeMount `json:"volumes,omitempty"`
	ExtraOptions  string        `json:"extra_options,omitempty"` // raw YAML fragment merged into service
	BindHost      string        `json:"bind_host,omitempty"`     // host IP for port bindings; default "127.0.0.1", use "0.0.0.0" to expose

	// Custom mode fields
	CustomCompose string `json:"custom_compose,omitempty"` // stack config: full compose YAML provided by user

	// Common fields
	StackPath    string     `json:"stack_path"`    // e.g. /opt/stacks/webui
	HealthType   HealthType `json:"health_type"`
	HealthTarget string     `json:"health_target"` // e.g. http://127.0.0.1:18081/healthz
	PollInterval int        `json:"poll_interval"` // seconds
	Enabled      bool       `json:"enabled"`
	EnvVars      map[string]string `json:"env_vars,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProjectState is the runtime state tracked by the agent
type ProjectState struct {
	ProjectID      string        `json:"project_id"`
	CurrentDigest  string        `json:"current_digest"`
	PreviousDigest string        `json:"previous_digest"`
	DesiredDigest  string        `json:"desired_digest"`
	Status         ProjectStatus `json:"status"`
	LastCheckAt    *time.Time    `json:"last_check_at"`
	LastDeployAt   *time.Time    `json:"last_deploy_at"`
	LastSuccessAt  *time.Time    `json:"last_success_at"`
	LastError      string        `json:"last_error"`
}

// Deployment records a single deployment transaction
type Deployment struct {
	ID           int64         `json:"id"`
	ProjectID    string        `json:"project_id"`
	FromDigest   string        `json:"from_digest"`
	ToDigest     string        `json:"to_digest"`
	Status       DeployStatus  `json:"status"`
	Trigger      DeployTrigger `json:"trigger"`
	StartedAt    time.Time     `json:"started_at"`
	EndedAt      *time.Time    `json:"ended_at"`
	ErrorMessage string        `json:"error_message"`
}

// ProjectWithState combines project definition with its current state
type ProjectWithState struct {
	Project
	State ProjectState `json:"state"`
}
