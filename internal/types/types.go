package types

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"
)

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
	StatusStopping   ProjectStatus = "stopping"
	StatusStarting   ProjectStatus = "starting"
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

// PortMapping represents a single host:container port mapping.
// HostPort accepts "8080" or "127.0.0.1:8080" (with optional bind address).
type PortMapping struct {
	HostPort      string `json:"host_port" yaml:"host_port"`             // e.g. "8080", "127.0.0.1:8080", "localhost:8080"
	ContainerPort int    `json:"container_port" yaml:"container_port"`
	Protocol      string `json:"protocol,omitempty" yaml:"protocol,omitempty"` // tcp (default) | udp
}

// ParseHostPort splits HostPort into bind address and port number.
// "8080" → ("", 8080, nil), "127.0.0.1:8080" → ("127.0.0.1", 8080, nil)
func (pm PortMapping) ParseHostPort() (bindAddr string, port int, err error) {
	s := pm.HostPort
	if s == "" {
		return "", 0, fmt.Errorf("empty host_port")
	}
	// Try plain number first
	if p, err := strconv.Atoi(s); err == nil {
		return "", p, nil
	}
	// host:port format
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, fmt.Errorf("invalid host_port %q: %w", s, err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port in host_port %q: %w", s, err)
	}
	return host, p, nil
}

// UnmarshalJSON handles both integer (legacy) and string formats for HostPort.
func (pm *PortMapping) UnmarshalJSON(data []byte) error {
	// Try the struct form with a string host_port
	type Alias PortMapping
	var raw struct {
		Alias
		HostPortRaw json.RawMessage `json:"host_port"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*pm = PortMapping(raw.Alias)

	// HostPortRaw could be a JSON number (legacy) or a JSON string
	var s string
	if err := json.Unmarshal(raw.HostPortRaw, &s); err == nil {
		pm.HostPort = s
		return nil
	}
	var n int
	if err := json.Unmarshal(raw.HostPortRaw, &n); err == nil {
		pm.HostPort = strconv.Itoa(n)
		return nil
	}
	return fmt.Errorf("host_port must be a string or number, got %s", string(raw.HostPortRaw))
}

// UnmarshalYAML handles both integer (legacy) and string formats for HostPort.
func (pm *PortMapping) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		HostPort      interface{} `yaml:"host_port"`
		ContainerPort int         `yaml:"container_port"`
		Protocol      string      `yaml:"protocol,omitempty"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	pm.ContainerPort = raw.ContainerPort
	pm.Protocol = raw.Protocol
	switch v := raw.HostPort.(type) {
	case string:
		pm.HostPort = v
	case int:
		pm.HostPort = strconv.Itoa(v)
	case float64:
		pm.HostPort = strconv.Itoa(int(v))
	default:
		return fmt.Errorf("host_port must be a string or number, got %T", v)
	}
	return nil
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
	Platform      string        `json:"platform,omitempty" yaml:"platform,omitempty"` // e.g. linux/amd64, linux/arm64
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
