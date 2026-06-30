# Project Backup & Restore

Application-aware backup/restore for bbsit projects. The daemon execs a
project-defined command inside the target container; the container writes its
dump to a directory that bbsit auto-mounts to `{StackPath}/backups/` on the
host. External tools (restic / rclone / borg / cloud sync) ship the result
off-host — bbsit deliberately does **not** own long-term storage.

## How it works

```
[bbsit-ctl backup <id>]
        │  (HTTP-over-Unix-socket → admin.sock)
        ▼
[bbsit daemon]
        │  podman compose exec -T --user <u> <svc> sh -c "<backup_command>"
        ▼
[<svc> container] ── writes dump ──► /var/opt/<app>/backups   (auto-injected mount)
                                            │
                                            ▼
[host: {StackPath}/backups/<file>]   ← external sync tool picks up
```

Restore is the same path in reverse: bbsit drops the input file into
`{StackPath}/backups/` so it shows up at the same container-side path, then
execs `restore_command` with `BBSIT_BACKUP_FILE` set to the absolute
container path.

`restore --as <new-id>` clones the project under a fresh ID with random
host ports (and tunnel publishes stripped), deploys it, then restores into
the clone — a self-contained backup smoke test.

## Why exec + a host-visible mount?

- **Exec, not API**: GitLab/InvenTree/Postgres/etc. each have their own backup
  tooling. `compose exec` is the universal lowest common denominator.
- **Host-visible mount, not bbsit-owned storage**: every operator already
  has a preferred way to ship bytes off-box. Putting backups in a regular
  directory makes the integration trivial.
- **Reuses compose service identity**: `compose exec` resolves the target
  container through the same compose file the deployer just used (including
  the digest-pinning override), so backups always hit the running image.

## Operator usage

### 1. Add a `backup:` block to the project

```yaml
id: gitlab
display_name: GitLab
services:
  - name: gitlab
    registry_image: gitlab/gitlab-ce
    image_tag: latest
    polled: true
    ports:
      - {host_port: "127.0.0.1:18080", container_port: 80}
    volumes:
      - {host_path: data, container_path: /var/opt/gitlab}
backup:
  service: gitlab
  user: git
  output_path: /var/opt/gitlab/backups
  output_pattern: "*_gitlab_backup.tar"
  backup_command: gitlab-backup create STRATEGY=copy CRON=1
  restore_command: |
    cp "$BBSIT_BACKUP_FILE" /var/opt/gitlab/backups/ &&
    chown git:git /var/opt/gitlab/backups/$(basename "$BBSIT_BACKUP_FILE") &&
    gitlab-backup restore BACKUP=$(basename "$BBSIT_BACKUP_FILE" _gitlab_backup.tar) force=yes
```

bbsit auto-injects a volume `backups → output_path` in the named service on
the next deploy. If you've already mounted `output_path` yourself, bbsit
respects your config and skips the auto-injection.

### 2. Trigger a backup

```sh
bbsit-ctl backup gitlab            # prints absolute host path of the new file
bbsit-ctl backups gitlab           # lists files in {StackPath}/backups/
bbsit-ctl restore gitlab <file>    # in-place restore
bbsit-ctl restore gitlab <file> --as gitlab-verify   # restore into a fresh clone
```

### 3. (Off-bbsit) ship the file somewhere durable

Whatever you already use. e.g. cron + restic against `{StackPath}/backups/*.tar`.

## File layout

| File | What it does |
|---|---|
| `internal/types/types.go` | `BackupSpec`, `BackupRun`, `BackupFile`, `Project.Backup`, `Project.BackupHostDir()` |
| `internal/db/db.go` | v7 migration (column `projects.backup`, table `backup_runs`); CRUD + `ResetStaleBackups` |
| `internal/runtime/exec.go` | `Runner` — `compose exec -T` wrapper + `IsServiceRunning` |
| `internal/backup/backup.go` | `Service.Run` / `List` / `History`; `newestMatch`, `hashFile` |
| `internal/backup/restore.go` | `Service.Restore`, `cloneForRestore`, `freePort` |
| `internal/web/admin_backup.go` | `POST /backup`, `GET /backups`, `GET /backup-runs`, `POST /restore` |
| `internal/web/api.go` | `validateAndInjectBackup` — validates the spec and adds the bind mount |
| `cmd/bbsit/main.go` | Wires `backup.Service` into `web.NewServer` |
| `cmd/bbsit-ctl/main.go` | `backup` / `backups` / `restore [--as]` subcommands |

## Admin API (Unix socket only)

```
POST   /api/projects/{id}/backup            → 200 BackupRun {id,status,file_path,sha256,bytes,…}
GET    /api/projects/{id}/backups           → 200 []BackupFile  (newest first)
GET    /api/projects/{id}/backup-runs       → 200 []BackupRun   (?limit=N)
POST   /api/projects/{id}/restore           → body {file, as?}; 200 {ok, project, message}
```

These are intentionally only on the admin socket — they can run for minutes
and the daemon's HTTP server is meant for the lightweight web UI. If we want
UI buttons we'll need to mount them on `Server.Handler()` too with auth +
async semantics (see "Follow-ups" below).

## Schema

`Project.Backup` is `*BackupSpec` — nil means "no backup configured". Stored
as JSON in `projects.backup` (column, not a separate table).

`backup_runs` is the audit trail:

```sql
CREATE TABLE backup_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status          TEXT NOT NULL,             -- in_progress | success | failed
    trigger_type    TEXT NOT NULL DEFAULT 'manual',
    file_path       TEXT DEFAULT '',
    bytes           INTEGER DEFAULT 0,
    sha256          TEXT DEFAULT '',
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    error_message   TEXT DEFAULT ''
);
```

It is **not** a registry of available backup files — `bbsit-ctl backups`
reads the filesystem directly so operators can drop files in manually
(e.g. fetched from cold storage) and still restore them.

## Invariants & gotchas

- **`output_path` must be absolute** (`validateAndInjectBackup` rejects relative).
- **Auto-injected mount uses relative host path `"backups"`** — resolves
  under `StackPath`, so it travels with `pack`/`unpack` bundles.
- **One backup or restore in flight per project** (per-project mutex in
  `backup.Service.locks`). Does NOT block deploy/start/stop; they have their
  own lock in the deployer. A deploy during a backup can race — operators
  should sequence them externally if it matters.
- **`newestMatch` filters by mtime ≥ start - 5s** — guards against picking up
  a stale file when the backup command silently no-ops. Tiny clock skew
  tolerance because some tools `mtime` the file at the start of the dump.
- **Clone restore strips `IsSystem`, `PublicHostnames`, sets a sibling `StackPath`**
  so the clone never fights the original for cloudflared traffic. Ports get random unprivileged ports via
  `net.Listen(":0")`. There's a TOCTOU window between Close and container
  start — retry on bind failure.
- **`compose exec -T`** is intentional (no TTY) so this runs cleanly from
  the daemon + over HTTP. Don't change it.
- **Crash recovery**: on startup `database.ResetStaleBackups()` flips any
  `in_progress` runs to `failed` so the audit log doesn't lie.

## Known limits / follow-ups

1. **No scheduling.** MVP per the original spec — cron / systemd timer
   calling `bbsit-ctl backup` is the supported path. If we add scheduling
   later, the natural spot is `internal/scheduler/` next to the registry
   poll loop, with cadence stored on `BackupSpec`.
2. **No UI affordance.** The dashboard has no backup/restore buttons. To
   add: wire the four admin endpoints onto `Server.Handler()` with auth,
   make them async (return a `run_id`, surface progress over the existing
   WebSocket hub).
3. **`compose exec` only.** If a target image runs the backup tool only as
   a `compose run` one-shot, we'd need a separate code path. Not seen yet
   for GitLab/InvenTree but plausible for Postgres-only stacks.
4. **No e2e tests against live containers.** Unit tests cover schema +
   clone logic + glob/hash; the `Runner.Exec` and the full backup→restore
   loop need a podman or docker daemon to validate. Add an `act`-style or
   integration-test job to CI if we need that guarantee.
5. **Backup file format / encryption is the app's problem.** bbsit takes a
   sha256 for integrity tracking; it does not encrypt or sign. Operators
   pair this with their off-host sync's encryption.
6. **No retention policy.** bbsit doesn't prune `{StackPath}/backups/`.
   That's again deliberate — but a `backup_retention_days` field on
   `BackupSpec` + a janitor in the scheduler would be a small follow-up.

## Recipes

### GitLab

```yaml
backup:
  service: gitlab
  user: git
  output_path: /var/opt/gitlab/backups
  output_pattern: "*_gitlab_backup.tar"
  backup_command: gitlab-backup create STRATEGY=copy CRON=1
  restore_command: |
    cp "$BBSIT_BACKUP_FILE" /var/opt/gitlab/backups/ &&
    chown git:git /var/opt/gitlab/backups/$(basename "$BBSIT_BACKUP_FILE") &&
    gitlab-backup restore BACKUP=$(basename "$BBSIT_BACKUP_FILE" _gitlab_backup.tar) force=yes
```

Notes:
- `CRON=1` suppresses interactive prompts.
- `STRATEGY=copy` avoids tar-while-writing races on hot Postgres data.
- GitLab's `*_gitlab_backup.tar` filename includes a timestamp + version,
  so the `_gitlab_backup.tar` stripping reconstructs the `BACKUP=` token
  it expects.
- `force=yes` lets restore overwrite an existing repo set — fine for the
  `--as` verify flow, careful in production.

### InvenTree

Not yet validated end-to-end on a live container; the rough shape:

```yaml
backup:
  service: inventree-server
  output_path: /opt/backups
  output_pattern: "data*.json*"
  backup_command: invoke export-records -f /opt/backups/data.json
  restore_command: invoke import-records -f "$BBSIT_BACKUP_FILE"
```

Verify the exact `invoke` task names against the version of InvenTree in
use; older versions had `db.dumpdata` / `db.loaddata` task names.
