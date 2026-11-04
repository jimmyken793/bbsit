# Daily snapshot and Mautic migration plan

Date: 2026-08-09 (Asia/Taipei)

## Goals

- RPO: at most 24 hours for all bbsit-managed application data.
- Keep application-consistent exports plus off-host, point-in-time snapshots.
- Use the assumed 1 TB SSD rsync server without filling it past 80%.
- Move the existing Mautic site into a bbsit-managed multi-service stack with a
  rehearsed rollback.

## Current state

The bbsit daemon is enabled and running. Six projects are registered, but none
has a `backup` block and `backup_runs` has no history. There is no active bbsit,
rsync, restic, borg, or application-backup systemd timer. The old
`/etc/cron.d/gitlab-backup` recipe is entirely commented out.

| Project | Data observed | Runtime state | Current backup status |
|---|---:|---|---|
| GitLab | about 33 GB | running | no current dump; one old 3.8 GB dump from 2025-09 |
| company-site-scraper | about 10 GB | running | migration copies exist; no off-host schedule |
| InvenTree | about 219 MB in container | running | internal DB/media files are produced, but remain on-host |
| Ankipan | about 9.8 GB | registered/enabled, no current containers | no dump or off-host copy |
| mail-knowledge-db | about 40 GB, including a 15 GB SQLite DB | registered/enabled, no current container | no dump or off-host copy |
| Cloudflared system project | small | running | credentials only on this host |

`/opt/bbsit` is about 92 GB in total. The source filesystem is 909 GB, 84%
used, with about 146 GB free, so local backup staging must be bounded.

Mautic currently runs outside bbsit from `/opt/mautic` using host nginx,
PHP 8.1-FPM, and MariaDB 10.6.23. Its application tree is about 948 MB and the
`mauticdb` database is about 141.5 MiB. The enabled site is
`mkt.kingyoung.com.tw`. Its cron file runs import, segment update, and scheduled
contact export tasks, but no backup. The older `/home/jimmy/mautic` tree is not
the active document root.

## Snapshot design

Use an application-consistent export phase on the source, followed by a pull
from the backup server. Do not rsync live SQLite, PostgreSQL, MariaDB, or GitLab
data directories as the primary database backup.

1. At 02:00, `bbsit-snapshot-export.service` takes a lock and creates a new
   staging directory under `/var/lib/bbsit-backup/staging/<run-id>`.
2. It invokes each application's native dump or SQLite online-backup operation.
3. It copies file data, configuration needed for recovery, a project inventory,
   image/version metadata, and SHA-256 checksums into the run directory.
4. It fsyncs the manifest, writes `.complete` last, and atomically publishes the
   run under `/var/lib/bbsit-backup/ready/<run-id>`.
5. At 03:30, the backup server pulls only completed runs through a restricted,
   read-only rsync-over-SSH account. The source has no credentials that can
   delete backup-server snapshots.
6. The backup server creates `snapshots/YYYY-MM-DD` with rsync `--link-dest`
   against the previous successful snapshot, verifies the manifest, and only
   then moves the `latest` symlink atomically.
7. Source staging keeps two successful runs and has a hard 40 GB limit. A failed
   off-host pull never deletes the newest complete local run.

Large immutable/append-only payloads (mail attachments/raw messages, scraped
pages, and raw Ankipan documents) may be exposed through explicit read-only
bind mounts and pulled directly into the same backup-server recovery point.
This avoids duplicating more than 40 GB on the nearly-full source filesystem.
Live database directories and mutable secret/config files are never eligible
for this path; they must use the consistent export and encrypted bundle.

The backup server filesystem must support hard links. Monitor both bytes and
inodes. Use SSH host-key pinning and a forced read-only rsync command. Because
the snapshots include authentication material, use full-disk encryption on the
backup server or encrypt the recovery-secret bundle to an offline `age`
recipient before it enters staging.

## What each export contains

| Component | Consistent export | File snapshot |
|---|---|---|
| bbsit | SQLite `.backup` of `state.db`; encrypted recovery bundle containing the matching `secret_key_path` | config, project manifests, generated compose files excluding runtime `.env` |
| GitLab | `gitlab-backup create STRATEGY=copy CRON=1` through a bbsit `backup` block | `/etc/gitlab` bind data, especially `gitlab-secrets.json`; registry storage if not covered by the configured GitLab backup mode |
| InvenTree | validate and use its supported DB backup command; retain current DB/media bundle output | media, plugins, config, secret key file |
| company-site-scraper | SQLite online backup of `history.db` through an application command or backup socket | optimizer workspace and other non-database data; prune obsolete migration copies separately |
| Ankipan | `pg_dump --format=custom` from the `db` service | raw documents, staging, and schema directories; never use live PGDATA as the only backup |
| mail-knowledge-db | SQLite `.backup`, then zstd compression and `PRAGMA integrity_check` | attachments and raw emails; do not make a live file copy of the 15 GB DB |
| Mautic | `mariadb-dump --single-transaction --routines --triggers --events` | persistent config and media; custom plugins/themes/translations or the exact custom image digest |
| Cloudflared | covered by encrypted bbsit DB/key recovery bundle | generated tunnel config and credential files as defense in depth |

GitLab's backup task does not replace a configuration/secrets backup, and a
restore must use the same GitLab version and edition. Follow the current
[GitLab backup](https://docs.gitlab.com/administration/backup_restore/backup_gitlab/)
and [restore prerequisites](https://docs.gitlab.com/administration/backup_restore/restore_gitlab/).

## Retention and 1 TB budget

Reserve 20% of the SSD, so managed snapshots may consume at most 800 GB.
Start with:

- 14 daily recovery points.
- 8 weekly recovery points.
- 6 monthly recovery points.
- Weekly/monthly points are hard-linked promotions of successful daily
  snapshots, not fresh full copies.

The current baseline is roughly 103 GB including Mautic. The 800 GB cap leaves
about 697 GB for unique changed files and full database archives. Across 28
promoted recovery points, the average unique payload must stay below about
25 GB per point. The largest risk is the 15 GB mail SQLite backup because each
compressed daily copy is a new file; record actual compressed size and daily
growth for 14 days, then adjust monthly retention if projected use exceeds
700 GB. Warn at 70%, prune only expired points at 75%, and page at 80%.

## Restore validation

- Every run: verify SHA-256 manifests and run SQLite `integrity_check` on copied
  SQLite databases.
- Weekly: restore one rotating database into a disposable container and run
  application-level smoke checks.
- Monthly: use `bbsit-ctl restore <id> <file> --as <id>-verify` where supported,
  then destroy the isolated verification stack after recording results.
- Quarterly: rehearse a full GitLab same-version restore and a bbsit host
  recovery using both `state.db` and its matching secret key.
- Alert on failed export, stale `.complete`, failed pull/checksum, low capacity,
  and absence of a successful snapshot for more than 30 hours.

## Mautic migration

Do not combine containerization, a Mautic major upgrade, DNS changes, and backup
rollout in one cutover.

### Phase 1: inventory and image

1. Confirm the exact live Mautic patch version and inventory non-stock plugins,
   themes, translations, PHP extensions, mail transport, queue mode, and upload
   paths.
2. Build a pinned custom image from the exact Mautic 6 Apache image and bake in
   required custom plugins/themes. The official project currently recommends
   Apache over its FPM variant, exposes `mautic_web`, `mautic_cron`, and
   `mautic_worker` roles, and identifies `config`, `var/logs`, and
   `docroot/media` as persistent paths. See the
   [official Mautic Docker image](https://github.com/mautic/docker-mautic).
3. Pin the MariaDB 10.6 image/version compatible with the current 10.6.23 data.
   Use a logical dump/import, not a raw host datadir move.

### Phase 2: bbsit staging project

Create a disabled `mautic` project with four services:

- `mautic-web`: pinned custom Apache image, port bound only to localhost.
- `mautic-cron`: same image with `DOCKER_MAUTIC_ROLE=mautic_cron`.
- `mautic-worker`: same image with `DOCKER_MAUTIC_ROLE=mautic_worker` if the
  configured messenger transports require it.
- `mautic-db`: pinned MariaDB 10.6 image with a relative `data/db` bind mount.

Use relative bbsit volumes for database data, config, and media so `pack` and
snapshot tooling can find them. Put credentials only in bbsit's encrypted
`env_vars`, not in `extra_options` or project YAML. Before production, add
service-scoped secret injection so the MariaDB root credential is not shared
with every service in a multi-service stack.

Import a fresh MariaDB dump and copy persistent media/config into staging. Run
schema checks, login, contact import, email rendering, plugin, campaign,
webhook/API, and outbound mail tests. Compare the current cron list with the
[Mautic 6 cron requirements](https://docs.mautic.org/en/6.0/configuration/cron_jobs.html);
the present host cron lacks the documented campaign update/trigger jobs.

### Phase 3: cutover

1. Take and verify a pre-cutover Mautic DB/files snapshot.
2. Disable the host Mautic cron and prevent writes; take the final logical dump.
3. Import the final dump, sync media/config, and start the bbsit stack on a new
   localhost port.
4. Keep the existing host nginx/TLS endpoint and change only its upstream to the
   container. This avoids a simultaneous DNS or certificate migration.
5. Run login, tracking, import, campaign, worker, mail, and health checks, then
   enable bbsit polling and the new cron/worker services.
6. Keep `/opt/mautic`, the old nginx config, and host MariaDB intact but stopped
   until two daily snapshots and one restore drill have succeeded.

Rollback is: stop the bbsit Mautic stack, restore the old nginx upstream, start
host PHP-FPM/MariaDB/cron, and reconcile any writes accepted after cutover from
the post-cutover dump. Target a 30-minute rollback window.

## Execution order

1. Ship and deploy bbsit encrypted secret storage; rotate credentials exposed
   before encryption and secure historic SQLite copies.
2. Add application-aware backup blocks and prove one manual export per running
   service.
3. Install source export and backup-server pull timers; run 14 days in observe
   mode and measure unique daily storage.
4. Enable retention and alerts; complete the first restore drill.
5. Build and validate the Mautic staging stack.
6. Cut over Mautic only after the snapshot system is green.

## Implementation status (2026-08-10)

The generic snapshot framework in step 3 is implemented under
`ops/snapshot/`, including source and backup-server systemd timers, atomic
publication, encrypted recovery secrets, SHA-256 verification, rsync
`--link-dest`, 14/8/6 retention, capacity gates, example database hooks, and an
end-to-end local regression test.

It is not yet production-green: install it on both hosts, enable and manually
validate the service-specific hooks/backup blocks, include each large file
dataset within the 40 GB source staging budget, and complete a restore drill.
Mautic remains a later phase and is not included until its migration stack and
native MariaDB dump hook exist.
