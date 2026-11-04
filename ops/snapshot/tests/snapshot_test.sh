#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)
source_script="$repo_root/ops/snapshot/source/bbsit-snapshot-export"
pull_script="$repo_root/ops/snapshot/backup-server/bbsit-snapshot-pull"
test_root=$(mktemp -d)
trap 'rm -rf -- "$test_root"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
assert_file() { [[ -f "$1" ]] || fail "missing file: $1"; }
assert_eq() { [[ "$1" == "$2" ]] || fail "got [$1], want [$2]${3:+ ($3)}"; }
count_dirs() { find "$1" -mindepth 1 -maxdepth 1 -type d -name "$2" | wc -l; }

mkdir -p "$test_root/bin" "$test_root/source" "$test_root/stacks/demo/backups" \
  "$test_root/hooks" "$test_root/remote-ready" "$test_root/backup"
mkdir -p "$test_root/remote-files/documents"
sqlite3 "$test_root/state.db" 'CREATE TABLE projects (id TEXT PRIMARY KEY); INSERT INTO projects VALUES ("demo");'
printf 'db_path: %s\nsecret_key_path: %s\n' "$test_root/state.db" "$test_root/secret.key" > "$test_root/config.yaml"
printf 'test master key\n' > "$test_root/secret.key"
printf 'age1testrecipient\n' > "$test_root/recipient.txt"
printf '%s\n' "$test_root/secret.key" > "$test_root/secret-paths"
printf 'services: {}\n' > "$test_root/stacks/demo/compose.yaml"
printf 'unchanged application backup\n' > "$test_root/stacks/demo/backups/demo.tar"
printf 'immutable document\n' > "$test_root/remote-files/documents/document.txt"

cat > "$test_root/bin/age" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
output=
while (($#)); do
  case "$1" in
    --output) output=$2; shift 2 ;;
    --recipients-file) shift 2 ;;
    --encrypt) shift ;;
    *) echo "unexpected fake age argument: $1" >&2; exit 2 ;;
  esac
done
[[ -n "$output" ]]
printf 'FAKE-AGE-ENCRYPTED\n' > "$output"
cat >> "$output"
EOF
cat > "$test_root/bin/bbsit-ctl" <<EOF
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "\$1" == backup && "\$2" == demo ]]
printf '%s\\n' '$test_root/stacks/demo/backups/demo.tar'
EOF
cat > "$test_root/hooks/10-test.sh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
mkdir -p "$SNAPSHOT_RUN_DIR/hooks"
printf 'hook-ok\n' > "$SNAPSHOT_RUN_DIR/hooks/test.txt"
EOF
chmod +x "$test_root/bin/age" "$test_root/bin/bbsit-ctl" "$test_root/hooks/10-test.sh"

cat > "$test_root/export.conf" <<EOF
SNAPSHOT_ROOT=$test_root/source
BBSIT_DB_PATH=$test_root/state.db
BBSIT_CONFIG_PATH=$test_root/config.yaml
BBSIT_STACK_ROOT=$test_root/stacks
BBSIT_CTL=$test_root/bin/bbsit-ctl
BBSIT_BACKUP_PROJECTS="demo"
AGE_RECIPIENT_FILE=$test_root/recipient.txt
SECRET_PATHS_FILE=$test_root/secret-paths
HOOK_DIR=$test_root/hooks
SOURCE_KEEP_READY=2
SOURCE_KEEP_FAILED=2
SOURCE_MAX_RUN_BYTES=10485760
ALLOW_NONROOT=1
EOF

cat > "$test_root/pull.conf" <<EOF
SNAPSHOT_ROOT=$test_root/backup
SOURCE_LOCAL_DIR=$test_root/remote-ready
FILESETS_LOCAL_DIR=$test_root/remote-files
FILESETS="documents"
KEEP_DAILY=2
KEEP_WEEKLY=1
KEEP_MONTHLY=1
KEEP_FAILED=2
WARN_PERCENT=70
PRUNE_PERCENT=75
FAIL_PERCENT=80
DISK_USE_OVERRIDE=10
ALLOW_NONROOT=1
EOF

export_one() {
  local run_id=$1
  PATH="$test_root/bin:$PATH" BBSIT_SNAPSHOT_CONFIG="$test_root/export.conf" \
    SNAPSHOT_RUN_ID="$run_id" "$source_script"
  cp -al "$test_root/source/ready/$run_id" "$test_root/remote-ready/$run_id"
}

pull_one() {
  BBSIT_SNAPSHOT_CONFIG="$test_root/pull.conf" "$pull_script"
}

export_one 20260630T180000Z
assert_file "$test_root/source/ready/20260630T180000Z/.complete"
assert_file "$test_root/source/ready/20260630T180000Z/control-plane/recovery-secrets.tar.age"
assert_file "$test_root/source/ready/20260630T180000Z/projects/demo/demo.tar"
assert_file "$test_root/source/ready/20260630T180000Z/hooks/test.txt"
[[ ! -e "$test_root/source/ready/20260630T180000Z/.secret-paths" ]] || fail 'plaintext secret path list leaked'
(
  cd "$test_root/source/ready/20260630T180000Z"
  sha256sum --check --strict SHA256SUMS >/dev/null
)
assert_eq "$(sqlite3 "$test_root/source/ready/20260630T180000Z/control-plane/state.db" 'SELECT id FROM projects;')" demo
pull_one
assert_eq "$(readlink "$test_root/backup/latest")" daily/20260630T180000Z
assert_file "$test_root/backup/daily/20260630T180000Z/.verified"
assert_file "$test_root/backup/daily/20260630T180000Z/files/documents/document.txt"
assert_file "$test_root/backup/daily/20260630T180000Z/FILESETS_SHA256SUMS"
assert_file "$test_root/backup/monthly/2026-07/.verified"

export_one 20260708T020000Z
pull_one
inode_1=$(stat -c '%i' "$test_root/backup/daily/20260630T180000Z/control-plane/config.yaml")
inode_2=$(stat -c '%i' "$test_root/backup/daily/20260708T020000Z/control-plane/config.yaml")
assert_eq "$inode_1" "$inode_2" 'unchanged file should use --link-dest hard link'
files_inode_1=$(stat -c '%i' "$test_root/backup/daily/20260630T180000Z/files/documents/document.txt")
files_inode_2=$(stat -c '%i' "$test_root/backup/daily/20260708T020000Z/files/documents/document.txt")
assert_eq "$files_inode_1" "$files_inode_2" 'unchanged fileset should use --link-dest hard link'

export_one 20260801T020000Z
assert_eq "$(count_dirs "$test_root/source/ready" '20*T*Z')" 2 'source retention'
pull_one
assert_eq "$(readlink "$test_root/backup/latest")" daily/20260801T020000Z
assert_eq "$(count_dirs "$test_root/backup/daily" '20*T*Z')" 2 'daily retention'
assert_eq "$(count_dirs "$test_root/backup/weekly" '20*-W*')" 1 'weekly retention'
assert_eq "$(count_dirs "$test_root/backup/monthly" '20*-*')" 1 'monthly retention'

# A source directory without .complete is never considered publishable.
mkdir "$test_root/remote-ready/20990101T020000Z"
pull_one
assert_eq "$(readlink "$test_root/backup/latest")" daily/20260801T020000Z

# A failed remote listing must fail the job instead of reporting no new run.
cat > "$test_root/bin/rsync" <<'EOF'
#!/usr/bin/env bash
echo 'simulated remote listing failure' >&2
exit 12
EOF
chmod +x "$test_root/bin/rsync"
cat > "$test_root/remote-pull.conf" <<EOF
SNAPSHOT_ROOT=$test_root/remote-backup
SSH_KEY=$test_root/secret.key
SSH_KNOWN_HOSTS=$test_root/recipient.txt
DISK_USE_OVERRIDE=10
ALLOW_NONROOT=1
EOF
if PATH="$test_root/bin:$PATH" BBSIT_SNAPSHOT_CONFIG="$test_root/remote-pull.conf" "$pull_script"; then
  fail 'remote listing failure was reported as success'
fi

printf 'snapshot tests: PASS\n'
