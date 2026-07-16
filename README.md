# bbsit

Pull-based deployment daemon for Linux. Manages multiple Docker Compose stacks with automatic updates, health checks, and rollback.

Your containers are the kids. BBSit watches them, picks them up when they fall, and recovers them when they're sick.

## Architecture

```
CI (build + push image)
        │
        ▼  (bbsit polls registry)
┌─────────────────────────┐
│  bbsit                  │
│  ├── Web UI (:9090)     │
│  ├── Poll scheduler     │
│  ├── Compose deployer   │
│  ├── Health checker     │
│  └── SQLite state       │
│                         │
│  Docker Compose stacks  │
│  ├── ollama             │
│  ├── webui              │
│  └── api-a              │
│                         │
│  Caddy reverse proxy    │
└─────────────────────────┘
```

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/jimmyken793/bbsit/main/install.sh | sudo bash
```

Requires Debian/Ubuntu with Docker or Podman installed. Detects amd64/arm64 automatically. With Podman, also install `podman-compose` (or `docker-compose`) so `podman compose` has a backend.

## Quick Start (build from source)

```bash
# Build and install locally (Linux)
make install

# Or build a .deb package
make deb
sudo dpkg -i dist/bbsit_0.1.0_amd64.deb

# Deploy .deb to a remote host
TARGET_HOST=user@192.168.1.100 make deploy-deb

# Start
sudo systemctl enable --now bbsit

# Open Web UI → http://<host-ip>:9090
# First visit → set password → add projects
```

## Local Development (macOS)

```bash
# 1. Build the frontend (one-time, rebuild after frontend changes)
cd frontend && npm install && npm run build && cd ..

# 2. Create a local config
mkdir -p /tmp/bbsit/stacks
cat > /tmp/bbsit/config.yaml <<'EOF'
listen: "127.0.0.1:9090"
db_path: "/tmp/bbsit/state.db"
stack_root: "/tmp/bbsit/stacks"
log_level: "debug"
EOF

# 3. Run
go run ./cmd/bbsit -config /tmp/bbsit/config.yaml
```

Open http://localhost:9090 — first visit will prompt you to set a password.

> **Note:** Deploy and health-check features require Docker or Podman (auto-detected; configurable via `runtime:` in `config.yaml`). The web UI, project management, and API work without either.

## Project Configuration

Projects are configured via a structured form in the Web UI. Each project can have one or more services, each with its own image, ports, volumes, and polling settings. BBSit generates `compose.yaml` automatically.

The editor has two views — **Form** for structured fields and **YAML** for quick bulk editing. Switching between them converts the data, so you can use whichever is faster for the task at hand.

### Multi-Service Stacks

A single project can contain multiple services (e.g., app + database + redis). Add services via the "Add service" button. Each service has:

- **Name** — Used as the Docker Compose service name
- **Registry image + tag** — The container image to deploy
- **Polled** — Whether bbsit polls the registry for new digests (enable for app images, disable for stable base images like `postgres`)
- **Platform** — Docker image platform (e.g. `linux/amd64`, `linux/arm64`). Leave empty to use the host's native architecture
- **Ports, volumes, extra options** — Per-service configuration

Environment variables are shared across all services via a `.env` file.

When any polled service has a new digest, bbsit redeploys the entire stack. Rollbacks are atomic — all services revert together to their previous digest snapshot.

### YAML Format

The YAML view uses the same fields as the form. You can define a single service using top-level fields, or multiple services using the `services` array.

**Single service:**

```yaml
registry_image: registry.example.com/my-app
image_tag: latest
platform: linux/amd64

ports:
  - host_port: "127.0.0.1:8080"
    container_port: 80

volumes:
  - host_path: ./data
    container_path: /app/data

env_vars:
  DATABASE_URL: postgres://localhost/mydb

extra_options: |
  deploy:
    restart_policy:
      condition: on-failure
```

**Multi-service:**

```yaml
services:
  - name: app
    registry_image: registry.example.com/my-app
    image_tag: latest
    polled: true
    platform: linux/amd64
    ports:
      - host_port: 8080
        container_port: 80
  - name: redis
    registry_image: redis
    image_tag: 7
    polled: false

env_vars:
  DATABASE_URL: postgres://localhost/mydb
```

| Field | Required | Description |
|-------|----------|-------------|
| `registry_image` | yes (single-service) | Container image (e.g. `registry.example.com/my-app`) |
| `image_tag` | no | Image tag (default: `latest`) |
| `platform` | no | Docker image platform (e.g. `linux/amd64`, `linux/arm64`). Default: host architecture |
| `ports` | no | Port mappings. `host_port` accepts `8080` or `"127.0.0.1:8080"` (with bind address). `container_port`, optional `protocol` |
| `volumes` | no | Bind mounts with `host_path`, `container_path`, optional `readonly` |
| `env_vars` | no | Environment variables as key-value pairs (shared across services) |
| `extra_options` | no | Raw YAML fragment merged into the compose service block |
| `services` | no | Array of services (replaces top-level `registry_image`/`ports`/etc.) |
| `backup` | no | App-aware backup/restore spec for one service |

Each entry in `services` supports: `name`, `registry_image`, `image_tag`, `polled`, `platform`, `ports`, `volumes`, `extra_options`.

BBSit generates `compose.yaml` from these fields. Health check, poll interval, and enabled/disabled are configured separately below the editor.

### Backups

Projects can define an app-aware `backup:` block. bbsit runs the configured
command inside the target service with `compose exec -T`, auto-mounts
`{StackPath}/backups/` into the service at `output_path`, and records each run
in the audit log. Long-term storage is intentionally left to external tools
such as restic, rclone, borg, or your existing cloud sync.

```yaml
backup:
  service: app
  user: app
  output_path: /var/opt/app/backups
  output_pattern: "*.tar.gz"
  backup_command: app-backup --output /var/opt/app/backups
  restore_command: app-restore "$BBSIT_BACKUP_FILE"
```

`output_path` must be absolute. If the target service does not already mount
that path, bbsit injects a relative `backups` volume on the next deploy.

Use the CLI to run and inspect backups:

```bash
bbsit-ctl backup <id>                         # run backup_command
bbsit-ctl backups <id>                        # list files in {StackPath}/backups/
bbsit-ctl restore <id> <file>                 # restore in place
bbsit-ctl restore <id> <file> --as <new-id>   # restore into a fresh clone
```

See [docs/backups.md](docs/backups.md) for API details, GitLab/InvenTree
recipes, restore-smoke-test behavior, and current limits.

## Cloudflare Tunnels

bbsit can manage Cloudflare tunnels and route public hostnames to your services
without opening ports on the host. See [docs/cloudflared.md](docs/cloudflared.md)
for setup.

## Deploy Flow

1. Polls container registry for new image digests (per polled service)
2. Compares remote vs local running digest for each service
3. If any service has a new digest, writes `compose.override.yaml` with per-service pinned digests
4. `docker compose pull && docker compose up -d`
5. Health checks — stack-level first, then per-service overrides (HTTP / TCP)
6. Success → update state · Failure → atomic rollback (all services revert together)

## CLI

`bbsit-ctl` talks to the daemon over a Unix socket at `/run/bbsit/admin.sock`.
The package install creates a `bbsit` system group that owns the socket — add
yourself once and you can manage bbsit without sudo:

```bash
sudo usermod -aG bbsit $USER
newgrp bbsit   # or log out and back in
```

```bash
bbsit-ctl status                       # All projects
bbsit-ctl history <id>                 # Deployment log
bbsit-ctl start <id>                   # Start a stopped project
bbsit-ctl stop <id>                    # Stop a project
bbsit-ctl deploy <id>                  # Force a manual deploy
bbsit-ctl rollback <id>                # Roll back to previous digest
bbsit-ctl delete <id>                  # Stop and remove a project

# Move a project to another bbsit host
bbsit-ctl export <id>                  # Print project YAML to stdout
bbsit-ctl pack <id> -o backup.tar.gz   # Project YAML + persistent data dirs
bbsit-ctl unpack backup.tar.gz         # Restore on the target host

# App-aware backups, when the project has a backup: block
bbsit-ctl backup <id>                  # Run backup_command
bbsit-ctl backups <id>                 # List backup files
bbsit-ctl restore <id> <file>          # Run restore_command
```

`pack` includes any volume host paths that live under the project's stack
directory. Volumes pointing at absolute paths outside the stack must be
migrated separately. `unpack` accepts both YAML (config only) and tar.gz
(config + data) — the format is auto-detected.

The socket path and group are configurable in `config.yaml`:

```yaml
admin_socket: "/run/bbsit/admin.sock"
admin_group: "bbsit"    # empty = root only; "docker" also works on docker hosts
```

Override at runtime with `BBSIT_SOCKET=/path/to/sock bbsit-ctl ...`.

## Server Config

BBSit itself is configured via a YAML file (default: `/opt/bbsit/config.yaml`). Pass a custom path with `-config`:

```bash
bbsit -config /path/to/config.yaml
```

### Format

```yaml
# Web UI listen address
listen: "0.0.0.0:9090"

# SQLite database path
db_path: "/opt/bbsit/state.db"

# Root directory for compose stacks
# Each project gets a subdirectory: {stack_root}/{project_id}/
stack_root: "/opt/stacks"

# Log level: debug | info | warn | error
log_level: "info"
```

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `listen` | yes | `0.0.0.0:9090` | Address and port for the web UI |
| `db_path` | yes | `/opt/bbsit/state.db` | Path to SQLite database (parent directory must exist) |
| `stack_root` | yes | `/opt/stacks` | Root directory for compose stacks (must exist) |
| `log_level` | no | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

All paths are validated at startup — bbsit will exit with a clear error if directories are missing.

## Files

```
/opt/bbsit/
  bbsit               Binary
  config.yaml         BBSit config
  state.db            SQLite

/opt/stacks/
  <project>/
    compose.yaml          Generated or custom
    compose.override.yaml Image digest pin (auto)
    .env                  Environment variables
    data/                 Bind mount target (persistent)
```
