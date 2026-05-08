# Cloudflare Tunnels

bbsit can manage one or more Cloudflare tunnels for you. Each tunnel runs as a
bbsit-owned `cloudflared` system project; each service in your regular projects
can declare one or more public hostnames that route through a tunnel.

## How it works

```
Cloudflare edge
      │  (outbound-only tunnel)
      ▼
┌─────────────────────────────────────┐
│ Host (Linux)                        │
│                                     │
│  cloudflared (network_mode: host)   │
│      │                              │
│      ├─ app.example.com → :18081 ───┼──► your service A (bind 127.0.0.1)
│      └─ api.example.com → :18082 ───┼──► your service B (bind 127.0.0.1)
└─────────────────────────────────────┘
```

- bbsit deploys `cloudflare/cloudflared:latest` as a hidden "system project"
  named `cf-tunnel-<id>`.
- The system project mounts a generated `config.yml` (ingress rules) and a
  `credentials.json` (tunnel auth) into the container.
- When you change a service's public hostnames, bbsit rewrites `config.yml` and
  restarts the cloudflared container.
- cloudflared uses host networking so it can reach services bound to
  `127.0.0.1` — which is the bbsit default. No extra Docker networking needed.

## A note on tunnel modes

Cloudflare has two tunnel modes:

- **Locally-managed** — auth via `credentials.json`, ingress via local
  `config.yml`. **bbsit uses this mode.**
- **Remotely-managed** — auth via a single `--token` JWT, ingress configured in
  the Cloudflare dashboard. The dashboard's "Create a tunnel" wizard now
  defaults to this. **Don't use this flow with bbsit** — bbsit owns the
  ingress config locally, so the dashboard ingress settings would just be
  ignored (or worse, conflict).

The CLI flow below produces a locally-managed tunnel and is the recommended
path. It also handles DNS records for you, which the dashboard wizard won't.

## Setup (CLI flow)

These commands run cloudflared in a one-shot container; you do not need to
install cloudflared on the host. They write to `~/.cloudflared/` so the
artifacts persist between runs.

> If your host uses **podman** with no default registry, use the fully
> qualified image name `docker.io/cloudflare/cloudflared:latest` in every
> command below. Or set `unqualified-search-registries = ["docker.io"]` in
> `/etc/containers/registries.conf` once.

### 1. Login (one-time per Cloudflare account)

```bash
mkdir -p ~/.cloudflared

docker run -it --rm \
  -v ~/.cloudflared:/home/nonroot/.cloudflared \
  cloudflare/cloudflared:latest tunnel login
```

The container has no browser. It will print a URL like
`https://dash.cloudflare.com/argotunnel?...` — copy that URL into the browser
on your laptop, sign in, pick the zone you want this machine to manage. When
you confirm, `cert.pem` is written to `~/.cloudflared/cert.pem`.

`cert.pem` is only used to *create* tunnels and DNS records; it isn't needed
at runtime. You can archive it after setup.

### 2. Create the tunnel

```bash
docker run -it --rm \
  -v ~/.cloudflared:/home/nonroot/.cloudflared \
  cloudflare/cloudflared:latest tunnel create my-tunnel
```

Output:

```
Tunnel credentials written to /home/nonroot/.cloudflared/<UUID>.json.
Created tunnel my-tunnel with id <UUID>
```

The `<UUID>.json` file is the `credentials.json` bbsit needs.

### 3. Route DNS for each public hostname

For every hostname you want to expose, create the CNAME pointing at the
tunnel:

```bash
docker run -it --rm \
  -v ~/.cloudflared:/home/nonroot/.cloudflared \
  cloudflare/cloudflared:latest tunnel route dns my-tunnel app.example.com

docker run -it --rm \
  -v ~/.cloudflared:/home/nonroot/.cloudflared \
  cloudflare/cloudflared:latest tunnel route dns my-tunnel api.example.com
```

This creates `app.example.com CNAME <UUID>.cfargotunnel.com` in the zone you
authorised in step 1. You can also do this manually in the Cloudflare DNS tab
if you prefer; the CLI is just the shortcut.

### 4. Add the tunnel in bbsit

```bash
cat ~/.cloudflared/<UUID>.json
```

That's a small JSON like:

```json
{"AccountTag":"...","TunnelSecret":"...","TunnelID":"..."}
```

In the bbsit web UI:

1. Top nav → **Tunnels** → **+ New tunnel**
2. **Tunnel ID**: a bbsit-side slug, e.g. `prod`
3. **Display name**: e.g. `Production`
4. **credentials.json**: paste the JSON above
5. Save.

bbsit creates the `cf-tunnel-prod` system project, writes `config.yml` +
`credentials.json` into its stack dir, and starts cloudflared. Toggle "Show
system projects" on the dashboard to verify it's running.

## Exposing a service

In a project's edit page, each service has a **Public hostnames** sub-form:

1. Pick a tunnel from the dropdown
2. Enter the hostname (e.g. `app.example.com` — must match what you ran
   `tunnel route dns` for in step 3)
3. Enter the host port your service is bound to (e.g. `18081`)
4. Save the project.

bbsit rewrites cloudflared's `config.yml` and restarts the cloudflared
container. New ingress is live within a few seconds.

Add as many hostnames per service as needed; one service can route through
multiple tunnels.

## Adding hostnames later

Two steps for each new public hostname:

```bash
# 1. DNS
docker run -it --rm \
  -v ~/.cloudflared:/home/nonroot/.cloudflared \
  cloudflare/cloudflared:latest tunnel route dns my-tunnel newthing.example.com
```

2. In bbsit, edit the project, add the hostname under the relevant service,
   save.

## Disabling / deleting a tunnel

- **Disable** (Tunnels → Edit → uncheck Enabled) stops the cloudflared
  container. Public hostnames stop responding. The tunnel record and
  credentials are kept in bbsit.
- **Delete** stops the container, removes the system project and its stack
  directory, and deletes the tunnel record. The tunnel itself in Cloudflare is
  unaffected — clean it up separately:

  ```bash
  docker run -it --rm \
    -v ~/.cloudflared:/home/nonroot/.cloudflared \
    cloudflare/cloudflared:latest tunnel delete my-tunnel
  ```

## Troubleshooting

- **cloudflared keeps restarting** — toggle "Show system projects" on the
  dashboard, click into `cf-tunnel-<id>`, look at deploy logs. Most common
  causes: pasted credentials don't match the tunnel UUID, or another
  cloudflared instance is already running this tunnel elsewhere.
- **"connection refused" from cloudflared** — your service must bind to
  `127.0.0.1` (the bbsit default). If `bind_host` is `0.0.0.0` it still works
  (cloudflared reaches it via localhost), but the port is then exposed on every
  host interface, which is usually not what you want.
- **Hostname returns 404** — bbsit's catch-all rule. Means no ingress entry in
  `config.yml` matched. Check the hostname in bbsit is exactly what's in DNS.
- **Hostname returns 530 / "no DNS record"** — you skipped step 3 (or did it
  for the wrong zone). Re-run `tunnel route dns`.
- **Changes don't take effect** — bbsit only rewrites `config.yml` and
  restarts cloudflared after a project save (or a tunnel save). To force a
  reconcile, edit-save the project (no field changes needed).

## Multi-tunnel layouts

You can run several tunnels on the same machine — useful for separating
production / staging or splitting accounts. Each is independent: own system
project, own `config.yml`, own restart cycle. The `tunnel login` step is per
Cloudflare account, so if both tunnels are in the same account you only do
step 1 once.
