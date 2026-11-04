#!/usr/bin/env bash
set -Eeuo pipefail

[[ $EUID -eq 0 ]] || { echo 'install-source.sh must run as root' >&2; exit 1; }
src_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

getent group bbsit-backup >/dev/null || groupadd --system bbsit-backup
if ! id bbsit-backup >/dev/null 2>&1; then
  useradd --system --gid bbsit-backup --home-dir /var/lib/bbsit-backup \
    --shell /bin/sh bbsit-backup
fi
install -d -o root -g bbsit-backup -m 0750 \
  /var/lib/bbsit-backup /var/lib/bbsit-backup/staging \
  /var/lib/bbsit-backup/ready /var/lib/bbsit-backup/failed
install -d -o root -g root -m 0755 /etc/bbsit /etc/bbsit/snapshot.d
install -o root -g root -m 0755 "$src_dir/bbsit-snapshot-export" /usr/local/sbin/
install -o root -g root -m 0644 "$src_dir/bbsit-snapshot-export.service" /etc/systemd/system/
install -o root -g root -m 0644 "$src_dir/bbsit-snapshot-export.timer" /etc/systemd/system/
if [[ ! -e /etc/bbsit/snapshot-export.conf ]]; then
  install -o root -g root -m 0600 "$src_dir/snapshot-export.conf.example" /etc/bbsit/snapshot-export.conf
fi
if [[ ! -e /etc/bbsit/snapshot-secret-paths ]]; then
  install -o root -g root -m 0600 "$src_dir/snapshot-secret-paths.example" /etc/bbsit/snapshot-secret-paths
fi
systemctl daemon-reload
echo 'Source snapshot files installed. Configure age/key access, run a manual export, then enable the timer.'

