# bbsit daily snapshots

This directory implements the two-phase snapshot design:

1. The bbsit host creates application-consistent exports at 02:00 Asia/Taipei.
2. A separate backup server pulls only atomically published runs at 03:30.
3. The backup server verifies SHA-256, writes `.verified`, updates `latest`
   atomically, and promotes
   hard-linked daily snapshots into weekly and monthly recovery points.

Defaults keep 14 daily, 8 weekly, and 6 monthly recovery points. Capacity warns
at 70%, prunes expired points at 75%, and refuses new snapshots at 80%.

## Security properties

- Database dumps are created with application-native tools or SQLite's online
  `.backup`; live database files are not rsynced as the primary backup.
- bbsit's database and matching AES key are in the same run. The key and other
  recovery secrets are streamed directly through `age`; a plaintext secret
  archive never exists.
- `.env` files are deliberately excluded. Generated `compose.yaml` files and
  public bbsit configuration are retained for inventory and recovery.
- The source stores no backup-server credential. The backup server uses a
  read-only, forced `rrsync` key, so compromise of that key cannot delete old
  snapshots.
- A run is visible under `ready/` only after checksums and `.complete` have been
  flushed and the directory has been renamed atomically.

The backup server should use full-disk encryption too. `age` protects recovery
secrets, but application dumps and attachments can still contain sensitive
business data.

## 1. Prepare the age recovery key

Create this on the backup server or an offline recovery workstation. Keep the
identity off the bbsit source host.

```bash
sudo apt-get update
sudo apt-get install age rsync
sudo install -d -m 0700 /root/bbsit-recovery
sudo age-keygen -o /root/bbsit-recovery/identity.agekey
sudo age-keygen -y /root/bbsit-recovery/identity.agekey
```

The last command prints a public `age1...` recipient. Put only that public line
on the source as `/etc/bbsit/backup-age-recipient.txt`, owned by root, mode
0600. Store a second offline copy of `identity.agekey` before enabling backups.

## 2. Install the source exporter

From this repository on the bbsit host:

```bash
sudo apt-get update
sudo apt-get install age rsync sqlite3 zstd
sudo ops/snapshot/source/install-source.sh
sudoedit /etc/bbsit/backup-age-recipient.txt
sudoedit /etc/bbsit/snapshot-secret-paths
sudoedit /etc/bbsit/snapshot-export.conf
```

The minimum secret path is `/opt/bbsit/secret.key`. For GitLab, also include
`/opt/bbsit/stacks/gitlab/config`, because a GitLab application dump alone is
not sufficient to recover encrypted settings. Add verified cloudflared
credential paths as well. On the current host, InvenTree's `config.yaml` and
`secret_key.txt`, and company-site-scraper's `config.json`, also belong in the
encrypted recovery list rather than an ordinary FILESET.

Application adapters under `source/hooks.example/` are examples, not enabled
automatically. Review each path, then install only the adapters that apply:

```bash
sudo install -o root -g root -m 0755 \
  ops/snapshot/source/hooks.example/10-company-site-scraper-db.sh \
  /etc/bbsit/snapshot.d/
sudo install -o root -g root -m 0755 \
  ops/snapshot/source/hooks.example/20-inventree-db.sh \
  /etc/bbsit/snapshot.d/
sudo install -o root -g root -m 0755 \
  ops/snapshot/source/hooks.example/30-mail-knowledge-db.sh \
  /etc/bbsit/snapshot.d/
```

The 40-Ankipan adapter needs a running `db` service and Docker Compose. It runs
`pg_dump` inside the container, where the existing PostgreSQL environment is
available, so credentials are not copied into the hook or command line.

For GitLab, add and deploy the documented `backup:` block in
`docs/backups.md`, then prove it manually:

```bash
sudo bbsit-ctl backup gitlab
```

Only after that succeeds, add `gitlab` to `BBSIT_BACKUP_PROJECTS` in
`/etc/bbsit/snapshot-export.conf`. Do the same for any other project using a
bbsit `backup:` block.

Run and inspect the first export before enabling the timer:

```bash
sudo systemctl start bbsit-snapshot-export.service
sudo systemctl status bbsit-snapshot-export.service --no-pager
sudo journalctl -u bbsit-snapshot-export.service -n 100 --no-pager
sudo find /var/lib/bbsit-backup/ready -maxdepth 3 -type f -printf '%p %s bytes\n'
sudo systemctl enable --now bbsit-snapshot-export.timer
systemctl list-timers bbsit-snapshot-export.timer
```

## 3. Create restricted pull access

On the backup server, create the SSH key:

```bash
sudo install -d -m 0700 /root/.ssh
sudo ssh-keygen -t ed25519 -f /root/.ssh/bbsit-snapshot -N ''
sudo cat /root/.ssh/bbsit-snapshot.pub
```

On the source, put that one public key into
`/var/lib/bbsit-backup/.ssh/authorized_keys`. Its line must have this prefix:

```text
restrict,command="/usr/bin/rrsync -ro /var/lib/bbsit-backup/ready" ssh-ed25519 AAAA... backup-server
```

For dump-only operation, that restriction and `SOURCE_PATH=/` are sufficient.

Large append-only data should not be duplicated into the source's 40 GB
staging area. To snapshot it directly with `--link-dest`, create a separate
read-only export view. For example, on the source:

```bash
sudo install -d -o root -g bbsit-backup -m 0750 \
  /var/lib/bbsit-export/runs \
  /var/lib/bbsit-export/files/mail-attachments \
  /var/lib/bbsit-export/files/mail-raw-emails \
  /var/lib/bbsit-export/files/company-pages \
  /var/lib/bbsit-export/files/company-optimizer-workspace \
  /var/lib/bbsit-export/files/inventree-media \
  /var/lib/bbsit-export/files/ankipan-raw-staging \
  /var/lib/bbsit-export/files/ankipan-raw-documents \
  /var/lib/bbsit-export/files/ankipan-schema

sudo mount --bind /var/lib/bbsit-backup/ready /var/lib/bbsit-export/runs
sudo mount -o remount,bind,ro /var/lib/bbsit-export/runs
sudo mount --bind /opt/bbsit/stacks/mail-knowledge-db/data/attachments /var/lib/bbsit-export/files/mail-attachments
sudo mount -o remount,bind,ro /var/lib/bbsit-export/files/mail-attachments
sudo mount --bind /opt/bbsit/stacks/mail-knowledge-db/data/raw-emails /var/lib/bbsit-export/files/mail-raw-emails
sudo mount -o remount,bind,ro /var/lib/bbsit-export/files/mail-raw-emails
sudo mount --bind /opt/bbsit/stacks/company-site-scraper/data/pages /var/lib/bbsit-export/files/company-pages
sudo mount -o remount,bind,ro /var/lib/bbsit-export/files/company-pages
sudo mount --bind /opt/bbsit/stacks/company-site-scraper/data/optimizer_workspace /var/lib/bbsit-export/files/company-optimizer-workspace
sudo mount -o remount,bind,ro /var/lib/bbsit-export/files/company-optimizer-workspace
sudo mount --bind /opt/bbsit/stacks/inventree/data/media /var/lib/bbsit-export/files/inventree-media
sudo mount -o remount,bind,ro /var/lib/bbsit-export/files/inventree-media
sudo mount --bind /opt/bbsit/stacks/ankipan/data/raw-staging /var/lib/bbsit-export/files/ankipan-raw-staging
sudo mount -o remount,bind,ro /var/lib/bbsit-export/files/ankipan-raw-staging
sudo mount --bind /opt/bbsit/stacks/ankipan/data/raw-documents /var/lib/bbsit-export/files/ankipan-raw-documents
sudo mount -o remount,bind,ro /var/lib/bbsit-export/files/ankipan-raw-documents
sudo mount --bind /opt/bbsit/stacks/ankipan/data/schema /var/lib/bbsit-export/files/ankipan-schema
sudo mount -o remount,bind,ro /var/lib/bbsit-export/files/ankipan-schema
```

Make these bind mounts persistent with reviewed `/etc/fstab` entries before
relying on the timer. Change the forced command to:

```text
restrict,command="/usr/bin/rrsync -ro /var/lib/bbsit-export" ssh-ed25519 AAAA... backup-server
```

Then set `SOURCE_PATH=/runs` and list those names in `FILESETS` on the backup
server. Only use FILESETS for immutable/append-only payloads: each file is
transferred consistently, but a changing directory is not an application-level
transaction. The puller hashes every fileset after transfer.

Then enforce ownership and permissions:

```bash
sudo install -d -o bbsit-backup -g bbsit-backup -m 0700 /var/lib/bbsit-backup/.ssh
sudo chown bbsit-backup:bbsit-backup /var/lib/bbsit-backup/.ssh/authorized_keys
sudo chmod 0600 /var/lib/bbsit-backup/.ssh/authorized_keys
```

On the backup server, verify the source host-key fingerprint through an
independent channel before pinning it. After verification:

```bash
sudo ssh-keyscan -H bbsit-source.example.com | sudo tee -a /root/.ssh/known_hosts
sudo chmod 0600 /root/.ssh/known_hosts
```

## 4. Install the backup-server puller

From a checkout of this repository on the 1 TB backup server:

```bash
sudo ops/snapshot/backup-server/install-backup-server.sh
sudoedit /etc/bbsit/snapshot-pull.conf
sudo systemctl start bbsit-snapshot-pull.service
sudo systemctl status bbsit-snapshot-pull.service --no-pager
sudo journalctl -u bbsit-snapshot-pull.service -n 100 --no-pager
sudo readlink /srv/backups/bbsit-host/latest
sudo du -sh /srv/backups/bbsit-host
sudo systemctl enable --now bbsit-snapshot-pull.timer
systemctl list-timers bbsit-snapshot-pull.timer
```

The target filesystem must support hard links. Weekly/monthly directories are
hard-linked promotions, so do not estimate their cost by summing ordinary
`du -sh` output for every directory.

## 5. Verification and restore

Verify a selected recovery point on the backup server:

```bash
cd /srv/backups/bbsit-host/latest
sudo sha256sum --check --strict SHA256SUMS
if test -f FILESETS_SHA256SUMS; then
  sudo sha256sum --check --strict FILESETS_SHA256SUMS
fi
```

Decrypt recovery secrets only on an isolated recovery host, into a root-only
temporary directory:

```bash
sudo install -d -m 0700 /root/bbsit-restore-secrets
sudo age --decrypt -i /root/bbsit-recovery/identity.agekey \
  /srv/backups/bbsit-host/latest/control-plane/recovery-secrets.tar.age \
  | sudo tar -x -C /root/bbsit-restore-secrets
```

Restore `control-plane/state.db` together with the matching decrypted
`opt/bbsit/secret.key`; mixing keys from different snapshots makes encrypted
environment values unrecoverable.

## Coverage gate

A green framework run does not prove every application's data is covered.
Before calling the system production-ready, the latest run must contain and
successfully restore:

- bbsit `state.db` plus the matching encrypted recovery bundle;
- GitLab native dump plus GitLab configuration/secrets;
- InvenTree database and required media/plugins;
- company-site-scraper SQLite database and required workspace data;
- Ankipan PostgreSQL dump plus raw-document/staging/schema data;
- mail-knowledge-db SQLite database plus attachments/raw emails;
- later, Mautic MariaDB dump plus config/media/customizations.

The included hooks cover the databases listed above. Large append-only file
datasets can use the curated FILESETS path, while smaller mutable datasets need
reviewed application-specific hooks. Never include live PostgreSQL data
directories or plaintext secret files in FILESETS.

Run the local end-to-end regression test with:

```bash
make snapshot-test
```
