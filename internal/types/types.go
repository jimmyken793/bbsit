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
	StatusUnknown    ProjectStatus = "unknown"
	StatusRunning    ProjectStatus = "running"
	StatusStopped    ProjectStatus = "stopped"
	StatusDeploying  ProjectStatus = "deploying"
	StatusFailed     ProjectStatus = "failed"
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

// ServiceConfig defines a single service within a project stack
type ServiceConfig struct {
	Name          string        `json:"name" yaml:"name"`
	RegistryImage string        `json:"registry_image" yaml:"registry_image"`
	ImageTag      string        `json:"image_tag" yaml:"image_tag"`
	Polled        bool          `json:"polled" yaml:"polled"`
	Ports         []PortMapping `json:"ports,omitempty" yaml:"ports,omitempty"`
	Volumes       []VolumeMount `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	ExtraOptions  string        `json:"extra_options,omitempty" yaml:"extra_options,omitempty"`
	HealthType    HealthType    `json:"health_type,omitempty" yaml:"health_type,omitempty"`
	HealthTarget  string        `json:"health_target,omitempty" yaml:"health_target,omitempty"`
}

// Project is the full project definition stored in SQLite
type Project struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"display_name"`
	ConfigMode  ConfigMode `json:"config_mode"`

	// Multi-service form mode
	Services []ServiceConfig `json:"services,omitempty"`
	BindHost string          `json:"bind_host,omitempty"` // host IP for port bindings; default "127.0.0.1", use "0.0.0.0" to expose

	// Legacy single-service form mode fields (kept for DB/JSON backward compat)
	RegistryImage string        `json:"registry_image,omitempty"`
	ImageTag      string        `json:"image_tag,omitempty"`
	Ports         []PortMapping `json:"ports,omitempty"`
	Volumes       []VolumeMount `json:"volumes,omitempty"`
	ExtraOptions  string        `json:"extra_options,omitempty"`

	// Custom mode fields
	CustomCompose string `json:"custom_compose,omitempty"` // stack config: full compose YAML provided by user

	// Common fields
	StackPath    string            `json:"stack_path"`    // e.g. /opt/stacks/webui
	HealthType   HealthType        `json:"health_type"`   // stack-level default
	HealthTarget string            `json:"health_target"` // e.g. http://127.0.0.1:18081/healthz
	PollInterval int               `json:"poll_interval"` // seconds
	Enabled      bool              `json:"enabled"`
	EnvVars      map[string]string `json:"env_vars,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PolledServices returns services that have polling enabled
func (p *Project) PolledServices() []ServiceConfig {
	var result []ServiceConfig
	for _, s := range p.Services {
		if s.Polled {
			result = append(result, s)
		}
	}
	return result
}

// PrimaryService returns the first service, or nil if none
func (p *Project) PrimaryService() *ServiceConfig {
	if len(p.Services) == 0 {
		return nil
	}
	return &p.Services[0]
}

// ProjectState is the runtime state tracked by the agent
type ProjectState struct {
	ProjectID       string            `json:"project_id"`
	CurrentDigests  map[string]string `json:"current_digests"`
	PreviousDigests map[string]string `json:"previous_digests"`
	DesiredDigests  map[string]string `json:"desired_digests"`
	Status          ProjectStatus     `json:"status"`
	LastCheckAt     *time.Time        `json:"last_check_at"`
	LastDeployAt    *time.Time        `json:"last_deploy_at"`
	LastSuccessAt   *time.Time        `json:"last_success_at"`
	LastError       string            `json:"last_error"`

	// Legacy scalar fields (kept for DB backward compat during migration)
	CurrentDigest  string `json:"current_digest,omitempty"`
	PreviousDigest string `json:"previous_digest,omitempty"`
	DesiredDigest  string `json:"desired_digest,omitempty"`
}

// Deployment records a single deployment transaction
type Deployment struct {
	ID           int64             `json:"id"`
	ProjectID    string            `json:"project_id"`
	FromDigests  map[string]string `json:"from_digests"`
	ToDigests    map[string]string `json:"to_digests"`
	Status       DeployStatus      `json:"status"`
	Trigger      DeployTrigger     `json:"trigger"`
	StartedAt    time.Time         `json:"started_at"`
	EndedAt      *time.Time        `json:"ended_at"`
	ErrorMessage string            `json:"error_message"`

	// Legacy scalar fields (kept for DB backward compat)
	FromDigest string `json:"from_digest,omitempty"`
	ToDigest   string `json:"to_digest,omitempty"`
}

// ProjectWithState combines project definition with its current state
type ProjectWithState struct {
	Project
	State ProjectState `json:"state"`
}
