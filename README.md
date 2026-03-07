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

## Quick Start

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

> **Note:** Deploy and health-check features require Docker. The web UI, project management, and API work without it.

## Two Modes

**Form Mode** — Define projects via structured fields in the Web UI (image, ports, volumes, env vars, health check). BBSit generates `compose.yaml` automatically.

**Stack Config Mode** — Provide a Docker Compose definition directly in the Web UI. Use this when you need full control over the stack (multiple services, networks, build args, etc.). BBSit writes it to the stack directory and manages the lifecycle from there.

### Stack Config Format

The stack config uses the same fields as the form, written as YAML:

```yaml
registry_image: registry.example.com/my-app
image_tag: latest

ports:
  - host_port: 8080
    container_port: 80
  - host_port: 9090
    container_port: 9090
    protocol: udp

volumes:
  - host_path: ./data
    container_path: /app/data
  - host_path: ./config
    container_path: /app/config
    readonly: true

env_vars:
  DATABASE_URL: postgres://localhost/mydb
  API_KEY: secret123

extra_options: |
  deploy:
    restart_policy:
      condition: on-failure
```

| Field | Required | Description |
|-------|----------|-------------|
| `registry_image` | yes | Container image (e.g. `registry.example.com/my-app`) |
| `image_tag` | no | Image tag (default: `latest`) |
| `ports` | no | Port mappings with `host_port`, `container_port`, optional `protocol` |
| `volumes` | no | Bind mounts with `host_path`, `container_path`, optional `readonly` |
| `env_vars` | no | Environment variables as key-value pairs |
| `extra_options` | no | Raw YAML fragment merged into the compose service block |

BBSit generates `compose.yaml` from the stack config — same as form mode. Health check, poll interval, and enabled/disabled are configured separately below the editor.

## Deploy Flow

1. Polls container registry for new image digests
2. Compares remote vs local running digest
3. Writes `compose.override.yaml` with pinned digest
4. `docker compose pull && docker compose up -d`
5. Health check (HTTP / TCP)
6. Success → update state · Failure → rollback to previous digest

## CLI

```bash
bbsit-ctl status              # All projects
bbsit-ctl history <project>   # Deployment log
```

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
