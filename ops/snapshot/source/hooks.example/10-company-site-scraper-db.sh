#!/usr/bin/env bash
set -Eeuo pipefail

source_db="$BBSIT_STACK_ROOT/company-site-scraper/data/history.db"
destination_dir="$SNAPSHOT_RUN_DIR/projects/company-site-scraper"
[[ -r "$source_db" ]] || { echo "company-site-scraper DB not readable: $source_db" >&2; exit 1; }
mkdir -p "$destination_dir"
sqlite3 "$source_db" ".timeout 30000" ".backup '$destination_dir/history.sqlite3'"
[[ $(sqlite3 "$destination_dir/history.sqlite3" 'PRAGMA integrity_check;') == ok ]]
zstd --quiet --threads=0 --rm "$destination_dir/history.sqlite3"

