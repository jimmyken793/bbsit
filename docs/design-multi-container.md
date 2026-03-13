# Multi-Container Deployment Support

## Overview

Extend bbsit's form mode to support multi-service stacks (e.g., app + database + redis)
where multiple services can be independently polled for new image digests, with atomic
stack-level rollback and flexible health checks.

## Requirements

- **Multi-service stacks**: One project = multiple services in a single compose file
- **Poll multiple images**: Each service can opt into registry polling; any new digest triggers full stack redeploy
- **Flexible health checks**: Stack-level default health check, with optional per-service overrides
- **Atomic rollback**: All services revert together to their previous digest snapshot

## Architecture: Extend Form Mode with Services Array

### Type Changes

```go
// NEW: Per-service configuration
type ServiceConfig struct {
    Name          string        `json:"name"`           // compose service name
    RegistryImage string        `json:"registry_image"`
    ImageTag      string        `json:"image_tag"`
    Polled        bool          `json:"polled"`         // whether scheduler polls this image
    Ports         []PortMapping `json:"ports,omitempty"`
    Volumes       []VolumeMount `json:"volumes,omitempty"`
    ExtraOptions  string        `json:"extra_options,omitempty"`
    HealthType    HealthType    `json:"health_type,omitempty"`   // per-service override
    HealthTarget  string        `json:"health_target,omitempty"` // per-service override
}

// CHANGED: Project struct
type Project struct {
    ID          string     `json:"id"`
    DisplayName string     `json:"display_name"`
    ConfigMode  ConfigMode `json:"config_mode"`

    // Form mode — replaces scalar RegistryImage/ImageTag/Ports/Volumes
    Services     []ServiceConfig `json:"services,omitempty"`
    BindHost     string          `json:"bind_host,omitempty"`

    // DEPRECATED but kept for JSON backward compat during migration
    RegistryImage string        `json:"registry_image,omitempty"`
    ImageTag      string        `json:"image_tag,omitempty"`
    Ports         []PortMapping `json:"ports,omitempty"`
    Volumes       []VolumeMount `json:"volumes,omitempty"`
    ExtraOptions  string        `json:"extra_options,omitempty"`

    // Custom mode (unchanged)
    CustomCompose string `json:"custom_compose,omitempty"`

    // Common (unchanged)
    StackPath    string            `json:"stack_path"`
    HealthType   HealthType        `json:"health_type"`         // stack-level default
    HealthTarget string            `json:"health_target"`
    PollInterval int               `json:"poll_interval"`
    Enabled      bool              `json:"enabled"`
    EnvVars      map[string]string `json:"env_vars,omitempty"`
    CreatedAt    time.Time         `json:"created_at"`
    UpdatedAt    time.Time         `json:"updated_at"`
}

// CHANGED: ProjectState digest tracking
type ProjectState struct {
    ProjectID       string            `json:"project_id"`
    CurrentDigests  map[string]string `json:"current_digests"`  // service -> digest
    PreviousDigests map[string]string `json:"previous_digests"` // atomic rollback snapshot
    DesiredDigests  map[string]string `json:"desired_digests"`  // latest from registry
    Status          ProjectStatus     `json:"status"`
    LastCheckAt     *time.Time        `json:"last_check_at"`
    LastDeployAt    *time.Time        `json:"last_deploy_at"`
    LastSuccessAt   *time.Time        `json:"last_success_at"`
    LastError       string            `json:"last_error"`

    // DEPRECATED — kept for backward compat during migration
    CurrentDigest  string `json:"current_digest,omitempty"`
    PreviousDigest string `json:"previous_digest,omitempty"`
    DesiredDigest  string `json:"desired_digest,omitempty"`
}
```

### Database Migration

Add new JSON columns alongside existing scalar columns. Migrate data on startup.

```sql
-- Migration: add services column to projects
ALTER TABLE projects ADD COLUMN services TEXT DEFAULT '[]';

-- Migration: add digest map columns to project_state
ALTER TABLE project_state ADD COLUMN current_digests TEXT DEFAULT '{}';
ALTER TABLE project_state ADD COLUMN previous_digests TEXT DEFAULT '{}';
ALTER TABLE project_state ADD COLUMN desired_digests TEXT DEFAULT '{}';
```

**Startup migration logic** (in `db.Open` or a `Migrate()` call):
1. For each project with `registry_image != ''` and `services == '[]'`:
   - Convert scalar fields into a single-element `Services` array
   - Clear deprecated scalar fields
2. For each project_state with `current_digest != ''` and `current_digests == '{}'`:
   - Convert scalar digests into single-entry maps keyed by project ID (service name)

### Compose Generation Changes

`generateFormCompose()` loops over `Services[]` instead of using scalar fields:

```go
func generateFormCompose(p *types.Project) ([]byte, error) {
    services := make(map[string]any)
    for _, svc := range p.Services {
        service := map[string]any{
            "image": svc.RegistryImage + ":" + svc.ImageTag,
        }
        if len(svc.Ports) > 0 {
            // ... port mappings with p.BindHost
        }
        if len(svc.Volumes) > 0 {
            // ... volume mounts
        }
        if svc.ExtraOptions != "" {
            // ... merge extra options
        }
        services[svc.Name] = service
    }
    return yaml.Marshal(map[string]any{"services": services})
}
```

**Digest override** becomes per-service:

```go
func generateDigestOverride(services []types.ServiceConfig, digests map[string]string) ([]byte, error) {
    overrides := make(map[string]any)
    for _, svc := range services {
        if d, ok := digests[svc.Name]; ok && d != "" {
            overrides[svc.Name] = map[string]any{"image": d}
        }
    }
    return yaml.Marshal(map[string]any{"services": overrides})
}
```

### Scheduler Changes

`reconcileOne()` polls all services with `Polled: true`:

```go
func (s *Scheduler) reconcileOne(ctx context.Context, p *types.Project, state *types.ProjectState, trigger types.DeployTrigger) {
    changed := false
    for _, svc := range p.Services {
        if !svc.Polled {
            continue
        }
        remoteDigest, err := registry.GetRemoteDigest(svc.RegistryImage, svc.ImageTag)
        if err != nil { ... }
        state.DesiredDigests[svc.Name] = remoteDigest
        if remoteDigest != state.CurrentDigests[svc.Name] {
            changed = true
        }
    }
    s.db.UpdateState(state)
    if !changed { return }

    // Any service changed → full stack redeploy with all desired digests
    if err := s.deployer.Deploy(p, state.DesiredDigests, trigger); err != nil {
        log.Error("deployment failed", "error", err)
    }
}
```

### Deployer Changes

`Deploy()` accepts `map[string]string` instead of a single digest string:

```go
func (d *Deployer) Deploy(p *types.Project, targetDigests map[string]string, trigger types.DeployTrigger) error {
    // ... lock, get state ...

    deploy := &types.Deployment{
        ProjectID:   p.ID,
        FromDigests: state.CurrentDigests,  // snapshot for rollback
        ToDigests:   targetDigests,
        // ...
    }

    // ... execute deploy ...

    // On success:
    state.PreviousDigests = state.CurrentDigests  // atomic snapshot
    state.CurrentDigests = targetDigests
}
```

### Health Check Changes

After deployment, run health checks:

```go
func (d *Deployer) runHealthChecks(p *types.Project) error {
    // 1. Stack-level health check (if configured)
    if p.HealthType != types.HealthNone {
        if err := health.Check(p.HealthType, p.HealthTarget, 30*time.Second, 3); err != nil {
            return err
        }
    }
    // 2. Per-service health checks (if any service has overrides)
    for _, svc := range p.Services {
        if svc.HealthType != "" && svc.HealthType != types.HealthNone {
            if err := health.Check(svc.HealthType, svc.HealthTarget, 30*time.Second, 3); err != nil {
                return fmt.Errorf("service %s health check: %w", svc.Name, err)
            }
        }
    }
    return nil
}
```

### Deployment History Changes

```sql
-- Deployment table changes
ALTER TABLE deployments ADD COLUMN from_digests TEXT DEFAULT '{}';
ALTER TABLE deployments ADD COLUMN to_digests TEXT DEFAULT '{}';
```

### Frontend Changes

**Types (types.ts)**:
- Add `ServiceConfig` interface
- Add `services: ServiceConfig[]` to `Project`
- Change `ProjectState` digest fields to `Record<string, string>`

**ProjectFormPage.tsx**:
- Add "Services" section with a dynamic list of service configurations
- Each service card has: name, image, tag, polled checkbox, ports, volumes, extra options
- Optional per-service health check fields
- Keep the current single-service UI as default; "Add service" button reveals multi-service mode

**ProjectDetailPage.tsx**:
- Show per-service digest info in the state card
- Display services list in the config card

**DashboardPage.tsx**:
- Show primary service image (first polled service) on dashboard card

## Migration Strategy

1. **Database**: Add new columns with defaults. Run data migration on startup.
2. **API**: Accept both old (scalar) and new (services array) formats during transition.
   - If `services` is empty but `registry_image` is set, auto-convert to single-service array.
3. **Frontend**: New form UI. Old projects auto-migrate on first edit.

## Implementation Order

1. Types + database migration
2. Compose generation (loop over services)
3. Scheduler (poll multiple images)
4. Deployer (accept digest map, atomic rollback)
5. API updates (accept/return services array)
6. Frontend form + detail page
7. Tests for all layers
