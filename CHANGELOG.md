# Changelog

## 0.4.2

### Multi-Container Deployment

- **Multi-service stacks**: A single project can now contain multiple services (e.g., app + database + redis), each with its own image, ports, volumes, and extra options
- **Independent polling**: Each service can be individually marked as polled; any new digest triggers a full stack redeploy
- **Atomic rollback**: All services revert together to their previous digest snapshot
- **Per-service health checks**: Stack-level default health check with optional per-service overrides

### Unified YAML/Form Editor

- The Form and YAML tabs are now two views of the same data with bidirectional conversion
- Switching to YAML serializes the current form state; switching back parses the YAML into form fields
- Projects always save as structured form data — no more separate "custom" mode
- Existing custom-mode projects are auto-migrated to form mode on startup

### Database Migration

- v3: Added `services`, `current_digests`, `previous_digests`, `desired_digests`, `from_digests`, `to_digests` columns
- v4: Converts existing custom-mode projects to form mode by parsing their YAML into structured services

## 0.4.1

- Fix deploy log autoscroll jumping the page
- Reduce Docker output noise in deploy logs
- Fix stale state after deploy completion
- Fix deploy script for remote hosts

## 0.4.0

- Real-time WebSocket updates for dashboard status and deploy logs
- Live deploy log streaming on project detail page
- `useWebSocket` hook for React frontend

## 0.3.0

- Per-project bind host option for port bindings (`127.0.0.1` or `0.0.0.0`)
- Resolve relative volume paths to absolute using stack path
- Force-recreate containers on deploy to ensure new image is used
- Pull images by tag instead of digest during deploy

## 0.2.3

- Fix standalone docker-compose fallback
- Frontend test infrastructure and improved backend coverage
- Curl-pipe-bash install script for easy installation
