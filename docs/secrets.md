# Secret storage

bbsit stores project environment variables and Cloudflare tunnel credentials
encrypted at rest with AES-256-GCM. The master key is a separate file configured
by `secret_key_path` and created with mode `0600` on first startup.

## Operator behavior

- Every environment-variable value remains encrypted at rest. Per-key
  `secret_env_keys` controls disclosure, not SQLite encryption.
- The UI, Web API, and admin Unix socket keep every key visible, return
  `********` for Secret values, and return non-secret values normally.
  Submitting the mask for an existing Secret preserves its value.
- Ordinary project YAML and `bbsit-ctl export` / `pack` include non-secret ENV
  values and use `********` placeholders for Secret values. Importing this over
  an existing project preserves its secrets; a new target requires the
  operator to replace the placeholders.
- For an explicit recovery export, `export` and `pack` accept
  `--export-secrets --output <file>`. This is available only through the admin
  Unix socket, never the Web API. It refuses stdout and existing output paths,
  creates mode `0600`, and writes a warning/audit event without logging values.
- Generated stack `.env` files and the SQLite database/key files are forced to
  mode `0600`.
- Cloudflared's `credentials.json` must be readable by its non-root container
  user, so it remains `0644` inside a stack directory forced to `0700`.

Put runtime environment values in `env_vars` and classify sensitive keys as
Secret. Raw `extra_options`, backup commands, and bind-mounted application
configuration are regular project data and are not field-encrypted by bbsit.

Example project classification:

```yaml
env_vars:
  APP_MODE: production
  API_TOKEN: "********"
secret_env_keys:
  - API_TOKEN
```

Existing projects are migrated with every existing ENV classified as Secret.
Unchecking Secret and saving preserves the existing value, makes it visible in
subsequent UI/API responses, and writes an audit warning containing the project
and key name but never the value.

Recovery exports contain plaintext secrets and should be encrypted before they
leave the host. Pack bundles can also contain sensitive application data from
persistent volumes even when `--export-secrets` is not used.

## Upgrade and recovery

On the first startup after this feature is installed, bbsit encrypts existing
plaintext `env_vars`, tunnel secrets, and Cloudflare API tokens. It then
checkpoints/truncates the WAL and runs `VACUUM` to remove plaintext SQLite pages.
Historic database copies, filesystem snapshots, terminal output, and logs made
before migration are not rewritten and must be secured or retired separately.

Back up `state.db` and the configured key together, but restrict the key more
tightly than ordinary snapshot data. A restored database is unusable without
the matching key. If the key is lost, bbsit intentionally cannot recover the
encrypted values.

Example:

```yaml
db_path: /opt/bbsit/state.db
secret_key_path: /opt/bbsit/secret.key
```

After an upgrade, verify:

```sh
sudo stat -c '%a %U:%G %n' \
  /opt/bbsit/state.db /opt/bbsit/secret.key /opt/bbsit/stacks/*/.env
sudo sqlite3 /opt/bbsit/state.db \
  "select id, substr(env_vars,1,16) from projects;"
```

The database query should show the `bbsit:secret:v1:` prefix, never JSON values.
