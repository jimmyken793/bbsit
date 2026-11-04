package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"

	"github.com/kingyoung/bbsit/internal/types"
)

type DB struct {
	conn    *sql.DB
	secrets *secretCipher
}

func Open(path string) (*DB, error) {
	return OpenWithKey(path, path+".key")
}

// OpenWithKey opens the database and encrypts sensitive fields with the
// master key at keyPath. The key is generated with mode 0600 on first use.
func OpenWithKey(path, keyPath string) (*DB, error) {
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		for _, dbFile := range []string{path, path + "-wal"} {
			hasEncryptedSecrets, readErr := fileContainsEncryptedSecrets(dbFile)
			if readErr != nil {
				return nil, fmt.Errorf("check database for encrypted secrets: %w", readErr)
			}
			if hasEncryptedSecrets {
				return nil, fmt.Errorf("secret key %q is missing but %q contains encrypted values; restore the matching key", keyPath, dbFile)
			}
		}
	} else if err != nil {
		return nil, fmt.Errorf("stat secret key: %w", err)
	}
	secrets, err := loadSecretCipher(keyPath)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("database path %q must not be a symlink", path)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("chmod database: %w", err)
		}
	} else if os.IsNotExist(err) {
		f, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if createErr != nil {
			return nil, fmt.Errorf("create database: %w", createErr)
		}
		if closeErr := f.Close(); closeErr != nil {
			return nil, fmt.Errorf("close new database: %w", closeErr)
		}
	} else {
		return nil, fmt.Errorf("stat database: %w", err)
	}
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db := &DB{conn: conn, secrets: secrets}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	for _, privatePath := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(privatePath, 0600); err != nil && !os.IsNotExist(err) {
			conn.Close()
			return nil, fmt.Errorf("chmod database file %q: %w", privatePath, err)
		}
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	if _, err := db.conn.Exec(schemaV1); err != nil {
		return err
	}
	// v2: add bind_host column
	db.conn.Exec(`ALTER TABLE projects ADD COLUMN bind_host TEXT DEFAULT '127.0.0.1'`)
	// v3: add multi-service columns
	db.conn.Exec(`ALTER TABLE projects ADD COLUMN services TEXT DEFAULT '[]'`)
	db.conn.Exec(`ALTER TABLE project_state ADD COLUMN current_digests TEXT DEFAULT '{}'`)
	db.conn.Exec(`ALTER TABLE project_state ADD COLUMN previous_digests TEXT DEFAULT '{}'`)
	db.conn.Exec(`ALTER TABLE project_state ADD COLUMN desired_digests TEXT DEFAULT '{}'`)
	db.conn.Exec(`ALTER TABLE deployments ADD COLUMN from_digests TEXT DEFAULT '{}'`)
	db.conn.Exec(`ALTER TABLE deployments ADD COLUMN to_digests TEXT DEFAULT '{}'`)
	// Migrate existing data to new columns
	db.migrateV3Data()
	// v4: convert custom-mode projects to form mode
	db.migrateV4CustomToForm()
	// v5: add cloudflared tunnels and is_system flag
	db.conn.Exec(`ALTER TABLE projects ADD COLUMN is_system INTEGER NOT NULL DEFAULT 0`)
	if _, err := db.conn.Exec(schemaV5Tunnels); err != nil {
		return err
	}
	// v6: add cf_api_token to tunnels for DNS auto-routing
	db.conn.Exec(`ALTER TABLE tunnels ADD COLUMN cf_api_token TEXT NOT NULL DEFAULT ''`)
	// v7: per-project backup spec + backup_runs history
	db.conn.Exec(`ALTER TABLE projects ADD COLUMN backup TEXT DEFAULT ''`)
	if _, err := db.conn.Exec(schemaV7BackupRuns); err != nil {
		return err
	}
	// v8: encrypt project environment variables and Cloudflare credentials.
	// Start pending even on databases migrated by earlier versions, which may
	// have stopped after committing ciphertext but before scrubbing free pages.
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS secret_migration_cleanup (id INTEGER PRIMARY KEY CHECK (id = 1), pending INTEGER NOT NULL); INSERT OR IGNORE INTO secret_migration_cleanup VALUES (1, 1)`); err != nil {
		return err
	}
	_, err := db.migrateV8Secrets()
	if err != nil {
		return err
	}
	var pending int
	if err := db.conn.QueryRow(`SELECT pending FROM secret_migration_cleanup WHERE id=1`).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		// Remove plaintext remnants from SQLite free pages and the WAL after the
		// one-time migration. This cannot erase historic copies outside this DB.
		if err := db.checkpointSecrets(); err != nil {
			return fmt.Errorf("checkpoint encrypted secret migration: %w", err)
		}
		if _, err := db.conn.Exec(`VACUUM`); err != nil {
			return fmt.Errorf("vacuum encrypted secret migration: %w", err)
		}
		if err := db.checkpointSecrets(); err != nil {
			return fmt.Errorf("final checkpoint encrypted secret migration: %w", err)
		}
		if _, err := db.conn.Exec(`UPDATE secret_migration_cleanup SET pending=0 WHERE id=1`); err != nil {
			return err
		}
	}
	// v9: classify every legacy environment variable as secret. NULL means the
	// project predates per-key classification; [] is an explicit no-secret set.
	db.conn.Exec(`ALTER TABLE projects ADD COLUMN secret_env_keys TEXT`)
	if err := db.migrateV9SecretEnvKeys(); err != nil {
		return err
	}
	return nil
}

func (db *DB) checkpointSecrets() error {
	var busy, logFrames, checkpointed int
	if err := db.conn.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("database busy during checkpoint (%d of %d frames checkpointed)", checkpointed, logFrames)
	}
	return nil
}

func (db *DB) migrateV9SecretEnvKeys() error {
	rows, err := db.conn.Query(`SELECT id, env_vars FROM projects WHERE secret_env_keys IS NULL`)
	if err != nil {
		return err
	}
	type legacyProject struct {
		id   string
		envs map[string]string
	}
	var projects []legacyProject
	for rows.Next() {
		var id, stored string
		if err := rows.Scan(&id, &stored); err != nil {
			rows.Close()
			return err
		}
		envs, err := db.decodeProjectEnv(id, stored)
		if err != nil {
			rows.Close()
			return fmt.Errorf("decode legacy project %s environment classification: %w", id, err)
		}
		projects = append(projects, legacyProject{id: id, envs: envs})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(projects) == 0 {
		return nil
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, project := range projects {
		keys := make([]string, 0, len(project.envs))
		for key := range project.envs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		data, err := json.Marshal(keys)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE projects SET secret_env_keys=? WHERE id=?`, string(data), project.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) migrateV8Secrets() (bool, error) {
	type projectSecret struct{ id, env string }
	var projects []projectSecret
	rows, err := db.conn.Query(`SELECT id, env_vars FROM projects`)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var item projectSecret
		if err := rows.Scan(&item.id, &item.env); err != nil {
			rows.Close()
			return false, err
		}
		if !isEncryptedSecret(item.env) {
			if item.env == "" || item.env == "null" {
				item.env = "{}"
			}
			projects = append(projects, item)
		} else {
			plain, err := db.secrets.decrypt(projectEnvAAD(item.id), item.env)
			if err != nil || !json.Valid([]byte(plain)) {
				rows.Close()
				if err == nil {
					err = fmt.Errorf("decrypted value is not valid JSON")
				}
				return false, fmt.Errorf("validate encrypted environment for project %s: %w", item.id, err)
			}
		}
	}
	if err := rows.Close(); err != nil {
		return false, err
	}

	type tunnelSecrets struct{ id, secret, token string }
	var tunnels []tunnelSecrets
	rows, err = db.conn.Query(`SELECT id, tunnel_secret, cf_api_token FROM tunnels`)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var item tunnelSecrets
		if err := rows.Scan(&item.id, &item.secret, &item.token); err != nil {
			rows.Close()
			return false, err
		}
		needsMigration := false
		if item.secret != "" {
			if isEncryptedSecret(item.secret) {
				if _, err := db.secrets.decrypt(tunnelSecretAAD(item.id, "tunnel_secret"), item.secret); err != nil {
					rows.Close()
					return false, fmt.Errorf("validate encrypted tunnel secret for %s: %w", item.id, err)
				}
			} else {
				needsMigration = true
			}
		}
		if item.token != "" {
			if isEncryptedSecret(item.token) {
				if _, err := db.secrets.decrypt(tunnelSecretAAD(item.id, "cf_api_token"), item.token); err != nil {
					rows.Close()
					return false, fmt.Errorf("validate encrypted tunnel API token for %s: %w", item.id, err)
				}
			} else {
				needsMigration = true
			}
		}
		if needsMigration {
			tunnels = append(tunnels, item)
		}
	}
	if err := rows.Close(); err != nil {
		return false, err
	}

	if len(projects) == 0 && len(tunnels) == 0 {
		return false, nil
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE secret_migration_cleanup SET pending=1 WHERE id=1`); err != nil {
		return false, err
	}
	for _, item := range projects {
		value, err := db.secrets.encrypt(projectEnvAAD(item.id), item.env)
		if err != nil {
			return false, err
		}
		if _, err := tx.Exec(`UPDATE projects SET env_vars=? WHERE id=?`, value, item.id); err != nil {
			return false, err
		}
	}
	for _, item := range tunnels {
		secret, token := item.secret, item.token
		if secret != "" && !isEncryptedSecret(secret) {
			secret, err = db.secrets.encrypt(tunnelSecretAAD(item.id, "tunnel_secret"), secret)
			if err != nil {
				return false, err
			}
		}
		if token != "" && !isEncryptedSecret(token) {
			token, err = db.secrets.encrypt(tunnelSecretAAD(item.id, "cf_api_token"), token)
			if err != nil {
				return false, err
			}
		}
		if _, err := tx.Exec(`UPDATE tunnels SET tunnel_secret=?, cf_api_token=? WHERE id=?`, secret, token, item.id); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// migrateV3Data converts legacy single-service projects to the new services array format.
func (db *DB) migrateV3Data() {
	// Migrate projects: convert scalar fields to single-service array
	rows, err := db.conn.Query(`SELECT id, registry_image, image_tag, ports, volumes, extra_options FROM projects WHERE services='[]' AND registry_image != ''`)
	if err != nil {
		return
	}
	defer rows.Close()

	type migRow struct {
		id, image, tag, ports, volumes, extra string
	}
	var toMigrate []migRow
	for rows.Next() {
		var r migRow
		if err := rows.Scan(&r.id, &r.image, &r.tag, &r.ports, &r.volumes, &r.extra); err != nil {
			continue
		}
		toMigrate = append(toMigrate, r)
	}
	rows.Close()

	for _, r := range toMigrate {
		var ports []types.PortMapping
		var volumes []types.VolumeMount
		json.Unmarshal([]byte(r.ports), &ports)
		json.Unmarshal([]byte(r.volumes), &volumes)
		svc := types.ServiceConfig{
			Name:          r.id,
			RegistryImage: r.image,
			ImageTag:      r.tag,
			Polled:        true,
			Ports:         ports,
			Volumes:       volumes,
			ExtraOptions:  r.extra,
		}
		svcJSON, _ := json.Marshal([]types.ServiceConfig{svc})
		db.conn.Exec(`UPDATE projects SET services=? WHERE id=?`, string(svcJSON), r.id)
	}

	// Migrate project_state: convert scalar digests to maps
	stateRows, err := db.conn.Query(`SELECT project_id, current_digest, previous_digest, desired_digest FROM project_state WHERE current_digests='{}' AND current_digest != ''`)
	if err != nil {
		return
	}
	defer stateRows.Close()

	type stateRow struct {
		id, current, previous, desired string
	}
	var statesToMigrate []stateRow
	for stateRows.Next() {
		var r stateRow
		if err := stateRows.Scan(&r.id, &r.current, &r.previous, &r.desired); err != nil {
			continue
		}
		statesToMigrate = append(statesToMigrate, r)
	}
	stateRows.Close()

	for _, r := range statesToMigrate {
		cur := marshalDigestMap(r.id, r.current)
		prev := marshalDigestMap(r.id, r.previous)
		des := marshalDigestMap(r.id, r.desired)
		db.conn.Exec(`UPDATE project_state SET current_digests=?, previous_digests=?, desired_digests=? WHERE project_id=?`,
			cur, prev, des, r.id)
	}

	// Migrate deployments: convert scalar digests to maps
	deployRows, err := db.conn.Query(`SELECT id, project_id, from_digest, to_digest FROM deployments WHERE from_digests='{}' AND (from_digest != '' OR to_digest != '')`)
	if err != nil {
		return
	}
	defer deployRows.Close()

	type deployRow struct {
		id        int64
		projectID string
		from, to  string
	}
	var deploysTomigrate []deployRow
	for deployRows.Next() {
		var r deployRow
		if err := deployRows.Scan(&r.id, &r.projectID, &r.from, &r.to); err != nil {
			continue
		}
		deploysTomigrate = append(deploysTomigrate, r)
	}
	deployRows.Close()

	for _, r := range deploysTomigrate {
		from := marshalDigestMap(r.projectID, r.from)
		to := marshalDigestMap(r.projectID, r.to)
		db.conn.Exec(`UPDATE deployments SET from_digests=?, to_digests=? WHERE id=?`, from, to, r.id)
	}
}

// migrateV4CustomToForm converts custom-mode projects to form mode by parsing
// their custom_compose YAML into structured services.
func (db *DB) migrateV4CustomToForm() {
	rows, err := db.conn.Query(`SELECT id, custom_compose, env_vars FROM projects WHERE config_mode='custom' AND custom_compose != ''`)
	if err != nil {
		return
	}
	defer rows.Close()

	type row struct{ id, compose, envJSON string }
	var toMigrate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.compose, &r.envJSON); err != nil {
			continue
		}
		toMigrate = append(toMigrate, r)
	}
	rows.Close()

	for _, r := range toMigrate {
		var sc struct {
			RegistryImage string                `yaml:"registry_image"`
			ImageTag      string                `yaml:"image_tag"`
			PullPolicy    string                `yaml:"pull_policy"`
			Ports         []types.PortMapping   `yaml:"ports"`
			Volumes       []types.VolumeMount   `yaml:"volumes"`
			ExtraOptions  string                `yaml:"extra_options"`
			Services      []types.ServiceConfig `yaml:"services"`
			EnvVars       map[string]string     `yaml:"env_vars"`
		}
		if err := yaml.Unmarshal([]byte(r.compose), &sc); err != nil {
			continue
		}

		var services []types.ServiceConfig
		if len(sc.Services) > 0 {
			services = sc.Services
		} else if sc.RegistryImage != "" {
			tag := sc.ImageTag
			if tag == "" {
				tag = "latest"
			}
			services = []types.ServiceConfig{{
				Name:          r.id,
				RegistryImage: sc.RegistryImage,
				ImageTag:      tag,
				Polled:        true,
				PullPolicy:    sc.PullPolicy,
				Ports:         sc.Ports,
				Volumes:       sc.Volumes,
				ExtraOptions:  sc.ExtraOptions,
			}}
		}

		svcJSON, _ := json.Marshal(services)

		// Merge env vars from custom_compose into existing env_vars
		var existingEnv map[string]string
		json.Unmarshal([]byte(r.envJSON), &existingEnv)
		if existingEnv == nil {
			existingEnv = map[string]string{}
		}
		for k, v := range sc.EnvVars {
			existingEnv[k] = v
		}
		envJSON, _ := json.Marshal(existingEnv)

		db.conn.Exec(
			`UPDATE projects SET config_mode='form', services=?, env_vars=?, custom_compose='' WHERE id=?`,
			string(svcJSON), string(envJSON), r.id)
	}
}

func marshalDigestMap(serviceName, digest string) string {
	if digest == "" {
		return "{}"
	}
	m := map[string]string{serviceName: digest}
	b, _ := json.Marshal(m)
	return string(b)
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS projects (
    id              TEXT PRIMARY KEY,
    display_name    TEXT NOT NULL,
    config_mode     TEXT NOT NULL DEFAULT 'form',

    -- form mode (legacy single-service)
    registry_image  TEXT,
    image_tag       TEXT DEFAULT 'latest',
    ports           TEXT DEFAULT '[]',
    volumes         TEXT DEFAULT '[]',
    extra_options   TEXT DEFAULT '',
    bind_host       TEXT DEFAULT '127.0.0.1',

    -- custom mode
    custom_compose  TEXT DEFAULT '',

    -- common
    stack_path      TEXT NOT NULL,
    health_type     TEXT NOT NULL DEFAULT 'http',
    health_target   TEXT DEFAULT '',
    poll_interval   INTEGER NOT NULL DEFAULT 300,
    enabled         INTEGER NOT NULL DEFAULT 1,
    env_vars        TEXT DEFAULT '{}',
	secret_env_keys TEXT,

    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project_state (
    project_id      TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    current_digest  TEXT DEFAULT '',
    previous_digest TEXT DEFAULT '',
    desired_digest  TEXT DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'unknown',
    last_check_at   TEXT,
    last_deploy_at  TEXT,
    last_success_at TEXT,
    last_error      TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS deployments (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    from_digest     TEXT DEFAULT '',
    to_digest       TEXT DEFAULT '',
    status          TEXT NOT NULL,
    trigger_type    TEXT NOT NULL DEFAULT 'poll',
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    error_message   TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_deployments_project ON deployments(project_id, started_at DESC);

CREATE TABLE IF NOT EXISTS auth (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash   TEXT NOT NULL,
    created_at      TEXT NOT NULL
);
`

const schemaV7BackupRuns = `
CREATE TABLE IF NOT EXISTS backup_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status          TEXT NOT NULL,
    trigger_type    TEXT NOT NULL DEFAULT 'manual',
    file_path       TEXT DEFAULT '',
    bytes           INTEGER DEFAULT 0,
    sha256          TEXT DEFAULT '',
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    error_message   TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_backup_runs_project ON backup_runs(project_id, started_at DESC);
`

const schemaV5Tunnels = `
CREATE TABLE IF NOT EXISTS tunnels (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    cf_tunnel_id    TEXT NOT NULL,
    account_tag     TEXT NOT NULL,
    tunnel_secret   TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
`

// --- Project CRUD ---

func (db *DB) CreateProject(p *types.Project) error {
	portsJSON, _ := json.Marshal(p.Ports)
	volsJSON, _ := json.Marshal(p.Volumes)
	envJSON, _ := json.Marshal(p.EnvVars)
	if p.EnvVars == nil {
		envJSON = []byte("{}")
	}
	encryptedEnv, err := db.secrets.encrypt(projectEnvAAD(p.ID), string(envJSON))
	if err != nil {
		return fmt.Errorf("encrypt project environment: %w", err)
	}
	secretEnvJSON := marshalSecretEnvKeys(p.EnvVars, p.SecretEnvKeys)
	svcJSON, _ := json.Marshal(p.Services)
	if p.Services == nil {
		svcJSON = []byte("[]")
	}
	backupJSON := marshalBackupSpec(p.Backup)
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO projects (id, display_name, config_mode,
			registry_image, image_tag, ports, volumes, extra_options, bind_host,
			custom_compose, stack_path, health_type, health_target,
			poll_interval, enabled, env_vars, secret_env_keys, services, is_system, backup, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.DisplayName, p.ConfigMode,
		p.RegistryImage, p.ImageTag, string(portsJSON), string(volsJSON), p.ExtraOptions, p.BindHost,
		p.CustomCompose, p.StackPath, p.HealthType, p.HealthTarget,
		p.PollInterval, p.Enabled, encryptedEnv, secretEnvJSON, string(svcJSON), boolToInt(p.IsSystem), backupJSON, now, now,
	)
	if err != nil {
		return fmt.Errorf("insert project: %w", err)
	}

	_, err = tx.Exec(`INSERT INTO project_state (project_id) VALUES (?)`, p.ID)
	if err != nil {
		return fmt.Errorf("insert state: %w", err)
	}

	return tx.Commit()
}

func (db *DB) UpdateProject(p *types.Project) error {
	portsJSON, _ := json.Marshal(p.Ports)
	volsJSON, _ := json.Marshal(p.Volumes)
	envJSON, _ := json.Marshal(p.EnvVars)
	if p.EnvVars == nil {
		envJSON = []byte("{}")
	}
	encryptedEnv, err := db.secrets.encrypt(projectEnvAAD(p.ID), string(envJSON))
	if err != nil {
		return fmt.Errorf("encrypt project environment: %w", err)
	}
	secretEnvJSON := marshalSecretEnvKeys(p.EnvVars, p.SecretEnvKeys)
	svcJSON, _ := json.Marshal(p.Services)
	if p.Services == nil {
		svcJSON = []byte("[]")
	}
	backupJSON := marshalBackupSpec(p.Backup)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = db.conn.Exec(`
		UPDATE projects SET
			display_name=?, config_mode=?,
			registry_image=?, image_tag=?, ports=?, volumes=?, extra_options=?, bind_host=?,
			custom_compose=?, stack_path=?, health_type=?, health_target=?,
			poll_interval=?, enabled=?, env_vars=?, secret_env_keys=?, services=?, is_system=?, backup=?, updated_at=?
		WHERE id=?`,
		p.DisplayName, p.ConfigMode,
		p.RegistryImage, p.ImageTag, string(portsJSON), string(volsJSON), p.ExtraOptions, p.BindHost,
		p.CustomCompose, p.StackPath, p.HealthType, p.HealthTarget,
		p.PollInterval, p.Enabled, encryptedEnv, secretEnvJSON, string(svcJSON), boolToInt(p.IsSystem), backupJSON, now,
		p.ID,
	)
	return err
}

func (db *DB) DeleteProject(id string) error {
	_, err := db.conn.Exec(`DELETE FROM projects WHERE id=?`, id)
	return err
}

const projectColumns = `id, display_name, config_mode,
	registry_image, image_tag, ports, volumes, extra_options, bind_host,
	custom_compose, stack_path, health_type, health_target,
	poll_interval, enabled, env_vars, secret_env_keys, services, is_system, backup, created_at, updated_at`

func (db *DB) GetProject(id string) (*types.Project, error) {
	row := db.conn.QueryRow(`SELECT `+projectColumns+` FROM projects WHERE id=?`, id)
	return db.scanProject(row.Scan)
}

func (db *DB) ListProjects() ([]types.Project, error) {
	rows, err := db.conn.Query(`SELECT ` + projectColumns + ` FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []types.Project
	for rows.Next() {
		p, err := db.scanProject(rows.Scan)
		if err != nil {
			return nil, err
		}
		projects = append(projects, *p)
	}
	return projects, nil
}

func (db *DB) ListProjectsWithState() ([]types.ProjectWithState, error) {
	rows, err := db.conn.Query(`
		SELECT ` + projectColumns + `,
		       s.current_digests, s.previous_digests, s.desired_digests,
		       s.status, s.last_check_at, s.last_deploy_at, s.last_success_at, s.last_error
		FROM projects p
		LEFT JOIN project_state s ON p.id = s.project_id
		ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []types.ProjectWithState
	for rows.Next() {
		var ps types.ProjectWithState
		var portsJSON, volsJSON, envJSON, secretEnvJSON, svcJSON, backupJSON string
		var enabled, isSystem int
		var createdAt, updatedAt string
		var lastCheck, lastDeploy, lastSuccess sql.NullString
		var curDigests, prevDigests, desDigests, lastErr sql.NullString
		var status sql.NullString

		err := rows.Scan(
			&ps.ID, &ps.DisplayName, (*string)(&ps.ConfigMode),
			&ps.RegistryImage, &ps.ImageTag, &portsJSON, &volsJSON, &ps.ExtraOptions, &ps.BindHost,
			&ps.CustomCompose, &ps.StackPath, (*string)(&ps.HealthType), &ps.HealthTarget,
			&ps.PollInterval, &enabled, &envJSON, &secretEnvJSON, &svcJSON, &isSystem, &backupJSON, &createdAt, &updatedAt,
			&curDigests, &prevDigests, &desDigests,
			&status, &lastCheck, &lastDeploy, &lastSuccess, &lastErr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan project with state: %w", err)
		}

		json.Unmarshal([]byte(portsJSON), &ps.Ports)
		json.Unmarshal([]byte(volsJSON), &ps.Volumes)
		ps.EnvVars, err = db.decodeProjectEnv(ps.ID, envJSON)
		if err != nil {
			return nil, fmt.Errorf("decode project %s environment: %w", ps.ID, err)
		}
		json.Unmarshal([]byte(secretEnvJSON), &ps.SecretEnvKeys)
		json.Unmarshal([]byte(svcJSON), &ps.Services)
		ps.Backup = unmarshalBackupSpec(backupJSON)
		ps.Enabled = enabled == 1
		ps.IsSystem = isSystem == 1
		ps.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		ps.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

		ps.State.ProjectID = ps.ID
		ps.State.CurrentDigests = unmarshalDigestMap(nullStr(curDigests))
		ps.State.PreviousDigests = unmarshalDigestMap(nullStr(prevDigests))
		ps.State.DesiredDigests = unmarshalDigestMap(nullStr(desDigests))
		ps.State.Status = types.ProjectStatus(nullStr(status))
		ps.State.LastError = nullStr(lastErr)
		ps.State.LastCheckAt = parseNullTime(lastCheck)
		ps.State.LastDeployAt = parseNullTime(lastDeploy)
		ps.State.LastSuccessAt = parseNullTime(lastSuccess)

		result = append(result, ps)
	}
	return result, nil
}

// --- State ---

func (db *DB) UpdateState(s *types.ProjectState) error {
	curJSON, _ := json.Marshal(s.CurrentDigests)
	prevJSON, _ := json.Marshal(s.PreviousDigests)
	desJSON, _ := json.Marshal(s.DesiredDigests)
	if s.CurrentDigests == nil {
		curJSON = []byte("{}")
	}
	if s.PreviousDigests == nil {
		prevJSON = []byte("{}")
	}
	if s.DesiredDigests == nil {
		desJSON = []byte("{}")
	}

	_, err := db.conn.Exec(`
		UPDATE project_state SET
			current_digests=?, previous_digests=?, desired_digests=?,
			status=?, last_check_at=?, last_deploy_at=?, last_success_at=?, last_error=?
		WHERE project_id=?`,
		string(curJSON), string(prevJSON), string(desJSON),
		s.Status, fmtTime(s.LastCheckAt), fmtTime(s.LastDeployAt), fmtTime(s.LastSuccessAt), s.LastError,
		s.ProjectID,
	)
	return err
}

func (db *DB) GetState(projectID string) (*types.ProjectState, error) {
	row := db.conn.QueryRow(`SELECT project_id, current_digests, previous_digests, desired_digests,
		status, last_check_at, last_deploy_at, last_success_at, last_error
		FROM project_state WHERE project_id=?`, projectID)
	var s types.ProjectState
	var lastCheck, lastDeploy, lastSuccess sql.NullString
	var curJSON, prevJSON, desJSON string
	err := row.Scan(&s.ProjectID, &curJSON, &prevJSON, &desJSON,
		(*string)(&s.Status), &lastCheck, &lastDeploy, &lastSuccess, &s.LastError)
	if err != nil {
		return nil, err
	}
	s.CurrentDigests = unmarshalDigestMap(curJSON)
	s.PreviousDigests = unmarshalDigestMap(prevJSON)
	s.DesiredDigests = unmarshalDigestMap(desJSON)
	s.LastCheckAt = parseNullTime(lastCheck)
	s.LastDeployAt = parseNullTime(lastDeploy)
	s.LastSuccessAt = parseNullTime(lastSuccess)
	return &s, nil
}

// ResetStaleStates clears any "deploying" project states and incomplete deployments
// left over from a previous crash or restart.
func (db *DB) ResetStaleStates() error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.conn.Exec(`
		UPDATE project_state SET status=?, last_error='interrupted by restart'
		WHERE status=?`,
		types.StatusFailed, types.StatusDeploying)
	if err != nil {
		return fmt.Errorf("reset stale states: %w", err)
	}
	_, err = db.conn.Exec(`
		UPDATE deployments SET status=?, ended_at=?, error_message='interrupted by restart'
		WHERE status=?`,
		types.DeployFailed, now, types.DeployInProgress)
	if err != nil {
		return fmt.Errorf("reset stale deployments: %w", err)
	}
	return nil
}

// --- Deployments ---

func (db *DB) InsertDeployment(d *types.Deployment) (int64, error) {
	fromJSON, _ := json.Marshal(d.FromDigests)
	toJSON, _ := json.Marshal(d.ToDigests)
	if d.FromDigests == nil {
		fromJSON = []byte("{}")
	}
	if d.ToDigests == nil {
		toJSON = []byte("{}")
	}

	res, err := db.conn.Exec(`
		INSERT INTO deployments (project_id, from_digest, to_digest, from_digests, to_digests, status, trigger_type, started_at, ended_at, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ProjectID, d.FromDigest, d.ToDigest, string(fromJSON), string(toJSON), d.Status, d.Trigger,
		d.StartedAt.UTC().Format(time.RFC3339), fmtTimePtr(d.EndedAt), d.ErrorMessage,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) FinishDeployment(id int64, status types.DeployStatus, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.conn.Exec(`UPDATE deployments SET status=?, ended_at=?, error_message=? WHERE id=?`,
		status, now, errMsg, id)
	return err
}

func (db *DB) ListDeployments(projectID string, limit int) ([]types.Deployment, error) {
	rows, err := db.conn.Query(`
		SELECT id, project_id, from_digest, to_digest, from_digests, to_digests, status, trigger_type, started_at, ended_at, error_message
		FROM deployments WHERE project_id=? ORDER BY started_at DESC LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []types.Deployment
	for rows.Next() {
		var d types.Deployment
		var startedAt string
		var endedAt sql.NullString
		var fromJSON, toJSON string
		err := rows.Scan(&d.ID, &d.ProjectID, &d.FromDigest, &d.ToDigest,
			&fromJSON, &toJSON,
			(*string)(&d.Status), (*string)(&d.Trigger), &startedAt, &endedAt, &d.ErrorMessage)
		if err != nil {
			return nil, err
		}
		d.FromDigests = unmarshalDigestMap(fromJSON)
		d.ToDigests = unmarshalDigestMap(toJSON)
		d.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
		d.EndedAt = parseNullTime(endedAt)
		result = append(result, d)
	}
	return result, nil
}

// --- Tunnels ---

func (db *DB) CreateTunnel(t *types.Tunnel) error {
	secret, err := db.encryptOptionalTunnelSecret(t.ID, "tunnel_secret", t.TunnelSecret)
	if err != nil {
		return err
	}
	token, err := db.encryptOptionalTunnelSecret(t.ID, "cf_api_token", t.CFAPIToken)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.conn.Exec(`
		INSERT INTO tunnels (id, name, cf_tunnel_id, account_tag, tunnel_secret, cf_api_token, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.CFTunnelID, t.AccountTag, secret, token, boolToInt(t.Enabled), now, now,
	)
	return err
}

func (db *DB) UpdateTunnel(t *types.Tunnel) error {
	secret, err := db.encryptOptionalTunnelSecret(t.ID, "tunnel_secret", t.TunnelSecret)
	if err != nil {
		return err
	}
	token, err := db.encryptOptionalTunnelSecret(t.ID, "cf_api_token", t.CFAPIToken)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.conn.Exec(`
		UPDATE tunnels SET name=?, cf_tunnel_id=?, account_tag=?, tunnel_secret=?, cf_api_token=?, enabled=?, updated_at=?
		WHERE id=?`,
		t.Name, t.CFTunnelID, t.AccountTag, secret, token, boolToInt(t.Enabled), now, t.ID,
	)
	return err
}

func (db *DB) DeleteTunnel(id string) error {
	_, err := db.conn.Exec(`DELETE FROM tunnels WHERE id=?`, id)
	return err
}

func (db *DB) GetTunnel(id string) (*types.Tunnel, error) {
	row := db.conn.QueryRow(`
		SELECT id, name, cf_tunnel_id, account_tag, tunnel_secret, cf_api_token, enabled, created_at, updated_at
		FROM tunnels WHERE id=?`, id)
	return db.scanTunnel(row.Scan)
}

func (db *DB) ListTunnels() ([]types.Tunnel, error) {
	rows, err := db.conn.Query(`
		SELECT id, name, cf_tunnel_id, account_tag, tunnel_secret, cf_api_token, enabled, created_at, updated_at
		FROM tunnels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tunnels []types.Tunnel
	for rows.Next() {
		t, err := db.scanTunnel(rows.Scan)
		if err != nil {
			return nil, err
		}
		tunnels = append(tunnels, *t)
	}
	return tunnels, nil
}

func (db *DB) scanTunnel(scan func(...any) error) (*types.Tunnel, error) {
	var t types.Tunnel
	var enabled int
	var createdAt, updatedAt string
	if err := scan(&t.ID, &t.Name, &t.CFTunnelID, &t.AccountTag, &t.TunnelSecret, &t.CFAPIToken, &enabled, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	var err error
	if t.TunnelSecret != "" {
		t.TunnelSecret, err = db.secrets.decrypt(tunnelSecretAAD(t.ID, "tunnel_secret"), t.TunnelSecret)
		if err != nil {
			return nil, fmt.Errorf("decrypt tunnel %s secret: %w", t.ID, err)
		}
	}
	if t.CFAPIToken != "" {
		t.CFAPIToken, err = db.secrets.decrypt(tunnelSecretAAD(t.ID, "cf_api_token"), t.CFAPIToken)
		if err != nil {
			return nil, fmt.Errorf("decrypt tunnel %s API token: %w", t.ID, err)
		}
	}
	t.Enabled = enabled == 1
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &t, nil
}

// --- Auth ---

func (db *DB) SetPassword(hash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.conn.Exec(`
		INSERT INTO auth (id, password_hash, created_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET password_hash=?, created_at=?`,
		hash, now, hash, now)
	return err
}

func (db *DB) GetPasswordHash() (string, error) {
	var hash string
	err := db.conn.QueryRow(`SELECT password_hash FROM auth WHERE id=1`).Scan(&hash)
	return hash, err
}

// --- helpers ---

func (db *DB) scanProject(scan func(...any) error) (*types.Project, error) {
	var p types.Project
	var portsJSON, volsJSON, envJSON, secretEnvJSON, svcJSON, backupJSON string
	var enabled, isSystem int
	var createdAt, updatedAt string
	err := scan(
		&p.ID, &p.DisplayName, (*string)(&p.ConfigMode),
		&p.RegistryImage, &p.ImageTag, &portsJSON, &volsJSON, &p.ExtraOptions, &p.BindHost,
		&p.CustomCompose, &p.StackPath, (*string)(&p.HealthType), &p.HealthTarget,
		&p.PollInterval, &enabled, &envJSON, &secretEnvJSON, &svcJSON, &isSystem, &backupJSON, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(portsJSON), &p.Ports)
	json.Unmarshal([]byte(volsJSON), &p.Volumes)
	p.EnvVars, err = db.decodeProjectEnv(p.ID, envJSON)
	if err != nil {
		return nil, fmt.Errorf("decode project %s environment: %w", p.ID, err)
	}
	json.Unmarshal([]byte(secretEnvJSON), &p.SecretEnvKeys)
	json.Unmarshal([]byte(svcJSON), &p.Services)
	p.Backup = unmarshalBackupSpec(backupJSON)
	p.Enabled = enabled == 1
	p.IsSystem = isSystem == 1
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &p, nil
}

func marshalSecretEnvKeys(env map[string]string, configured []string) string {
	keys := configured
	if keys == nil {
		keys = make([]string, 0, len(env))
		for key := range env {
			keys = append(keys, key)
		}
	}
	keys = append([]string(nil), keys...)
	sort.Strings(keys)
	data, _ := json.Marshal(keys)
	return string(data)
}

func (db *DB) decodeProjectEnv(id, stored string) (map[string]string, error) {
	plain, err := db.secrets.decrypt(projectEnvAAD(id), stored)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	if err := json.Unmarshal([]byte(plain), &result); err != nil {
		return nil, fmt.Errorf("parse decrypted environment JSON: %w", err)
	}
	return result, nil
}

func (db *DB) encryptOptionalTunnelSecret(id, field, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	encrypted, err := db.secrets.encrypt(tunnelSecretAAD(id, field), value)
	if err != nil {
		return "", fmt.Errorf("encrypt tunnel %s: %w", field, err)
	}
	return encrypted, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func unmarshalDigestMap(s string) map[string]string {
	if s == "" || s == "{}" {
		return map[string]string{}
	}
	m := map[string]string{}
	json.Unmarshal([]byte(s), &m)
	return m
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func parseNullTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func fmtTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func fmtTimePtr(t *time.Time) interface{} {
	return fmtTime(t)
}

func marshalBackupSpec(b *types.BackupSpec) string {
	if b == nil {
		return ""
	}
	out, err := json.Marshal(b)
	if err != nil {
		return ""
	}
	return string(out)
}

func unmarshalBackupSpec(s string) *types.BackupSpec {
	if s == "" || s == "{}" {
		return nil
	}
	b := &types.BackupSpec{}
	if err := json.Unmarshal([]byte(s), b); err != nil {
		return nil
	}
	return b
}

// --- Backup runs ---

func (db *DB) InsertBackupRun(r *types.BackupRun) (int64, error) {
	res, err := db.conn.Exec(`
		INSERT INTO backup_runs (project_id, status, trigger_type, file_path, bytes, sha256, started_at, ended_at, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ProjectID, r.Status, r.Trigger, r.FilePath, r.Bytes, r.SHA256,
		r.StartedAt.UTC().Format(time.RFC3339), fmtTime(r.EndedAt), r.Error,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) FinishBackupRun(id int64, status types.BackupStatus, filePath, sha256 string, bytes int64, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.conn.Exec(`
		UPDATE backup_runs SET status=?, file_path=?, sha256=?, bytes=?, ended_at=?, error_message=?
		WHERE id=?`,
		status, filePath, sha256, bytes, now, errMsg, id)
	return err
}

func (db *DB) ListBackupRuns(projectID string, limit int) ([]types.BackupRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.conn.Query(`
		SELECT id, project_id, status, trigger_type, file_path, bytes, sha256, started_at, ended_at, error_message
		FROM backup_runs WHERE project_id=? ORDER BY started_at DESC LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []types.BackupRun
	for rows.Next() {
		var r types.BackupRun
		var startedAt string
		var endedAt sql.NullString
		err := rows.Scan(&r.ID, &r.ProjectID, (*string)(&r.Status), &r.Trigger,
			&r.FilePath, &r.Bytes, &r.SHA256, &startedAt, &endedAt, &r.Error)
		if err != nil {
			return nil, err
		}
		r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
		r.EndedAt = parseNullTime(endedAt)
		result = append(result, r)
	}
	return result, nil
}

// ResetStaleBackups marks any in_progress backup runs as failed. Called on
// startup to recover from a crash mid-backup.
func (db *DB) ResetStaleBackups() error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.conn.Exec(`
		UPDATE backup_runs SET status=?, ended_at=?, error_message='interrupted by restart'
		WHERE status=?`,
		types.BackupFailed, now, types.BackupInProgress)
	return err
}
