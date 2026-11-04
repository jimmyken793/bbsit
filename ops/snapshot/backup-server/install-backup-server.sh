#!/usr/bin/env bash
set -Eeuo pipefail

[[ $EUID -eq 0 ]] || { echo 'install-backup-server.sh must run as root' >&2; exit 1; }
src_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

install -d -o root -g root -m 0700 /srv/backups/bbsit-host
install -d -o root -g root -m 0755 /etc/bbsit
install -o root -g root -m 0755 "$src_dir/bbsit-snapshot-pull" /usr/local/sbin/
install -o root -g root -m 0644 "$src_dir/bbsit-snapshot-pull.service" /etc/systemd/system/
install -o root -g root -m 0644 "$src_dir/bbsit-snapshot-pull.timer" /etc/systemd/system/
if [[ ! -e /etc/bbsit/snapshot-pull.conf ]]; then
  install -o root -g root -m 0600 "$src_dir/snapshot-pull.conf.example" /etc/bbsit/snapshot-pull.conf
fi
systemctl daemon-reload
echo 'Backup-server snapshot files installed. Configure SSH, run a manual pull, then enable the timer.'

