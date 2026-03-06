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
# Build and install locally
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

## Two Modes

**Form Mode** — Define projects via structured fields in the Web UI (image, ports, volumes, env vars, health check). BBSit generates `compose.yaml` automatically.

**Custom Mode** — Upload a complete `compose.yaml` directly.

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

## Files

```
/opt/bbsit/
  bbsit               Binary
  config.yaml         Config
  state.db            SQLite
  templates/          HTML
  static/             CSS

/opt/stacks/
  <project>/
    compose.yaml          Generated or custom
    compose.override.yaml Image digest pin (auto)
    .env                  Environment variables
    data/                 Bind mount target (persistent)
```
