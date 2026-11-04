#!/usr/bin/env bash
set -Eeuo pipefail

compose="$BBSIT_STACK_ROOT/ankipan/compose.yaml"
destination_dir="$SNAPSHOT_RUN_DIR/projects/ankipan"
[[ -r "$compose" ]] || { echo "Ankipan compose file not readable: $compose" >&2; exit 1; }
mkdir -p "$destination_dir"
docker compose -f "$compose" exec -T db sh -ceu \
  'exec pg_dump --format=custom --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' \
  > "$destination_dir/ankipan.pgdump"
[[ -s "$destination_dir/ankipan.pgdump" ]]

