package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
	"gopkg.in/yaml.v3"

	"github.com/kingyoung/bbsit/internal/types"
)

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
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
	return nil
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
			RegistryImage string              `yaml:"registry_image"`
			ImageTag      string              `yaml:"image_tag"`
			Ports         []types.PortMapping  `yaml:"ports"`
			Volumes       []types.VolumeMount  `yaml:"volumes"`
			ExtraOptions  string               `yaml:"extra_options"`
			Services      []types.ServiceConfig `yaml:"services"`
			EnvVars       map[string]string    `yaml:"env_vars"`
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

// --- Project CRUD ---

func (db *DB) CreateProject(p *types.Project) error {
	portsJSON, _ := json.Marshal(p.Ports)
	volsJSON, _ := json.Marshal(p.Volumes)
	envJSON, _ := json.Marshal(p.EnvVars)
	svcJSON, _ := json.Marshal(p.Services)
	if p.Services == nil {
		svcJSON = []byte("[]")
	}
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
			poll_interval, enabled, env_vars, services, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.DisplayName, p.ConfigMode,
		p.RegistryImage, p.ImageTag, string(portsJSON), string(volsJSON), p.ExtraOptions, p.BindHost,
		p.CustomCompose, p.StackPath, p.HealthType, p.HealthTarget,
		p.PollInterval, p.Enabled, string(envJSON), string(svcJSON), now, now,
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
	svcJSON, _ := json.Marshal(p.Services)
	if p.Services == nil {
		svcJSON = []byte("[]")
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := db.conn.Exec(`
		UPDATE projects SET
			display_name=?, config_mode=?,
			registry_image=?, image_tag=?, ports=?, volumes=?, extra_options=?, bind_host=?,
			custom_compose=?, stack_path=?, health_type=?, health_target=?,
			poll_interval=?, enabled=?, env_vars=?, services=?, updated_at=?
		WHERE id=?`,
		p.DisplayName, p.ConfigMode,
		p.RegistryImage, p.ImageTag, string(portsJSON), string(volsJSON), p.ExtraOptions, p.BindHost,
		p.CustomCompose, p.StackPath, p.HealthType, p.HealthTarget,
		p.PollInterval, p.Enabled, string(envJSON), string(svcJSON), now,
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
	poll_interval, enabled, env_vars, services, created_at, updated_at`

func (db *DB) GetProject(id string) (*types.Project, error) {
	row := db.conn.QueryRow(`SELECT `+projectColumns+` FROM projects WHERE id=?`, id)
	return scanProject(row)
}

func (db *DB) ListProjects() ([]types.Project, error) {
	rows, err := db.conn.Query(`SELECT ` + projectColumns + ` FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []types.Project
	for rows.Next() {
		p, err := scanProjectRows(rows)
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
		var portsJSON, volsJSON, envJSON, svcJSON string
		var enabled int
		var createdAt, updatedAt string
		var lastCheck, lastDeploy, lastSuccess sql.NullString
		var curDigests, prevDigests, desDigests, lastErr sql.NullString
		var status sql.NullString

		err := rows.Scan(
			&ps.ID, &ps.DisplayName, (*string)(&ps.ConfigMode),
			&ps.RegistryImage, &ps.ImageTag, &portsJSON, &volsJSON, &ps.ExtraOptions, &ps.BindHost,
			&ps.CustomCompose, &ps.StackPath, (*string)(&ps.HealthType), &ps.HealthTarget,
			&ps.PollInterval, &enabled, &envJSON, &svcJSON, &createdAt, &updatedAt,
			&curDigests, &prevDigests, &desDigests,
			&status, &lastCheck, &lastDeploy, &lastSuccess, &lastErr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan project with state: %w", err)
		}

		json.Unmarshal([]byte(portsJSON), &ps.Ports)
		json.Unmarshal([]byte(volsJSON), &ps.Volumes)
		json.Unmarshal([]byte(envJSON), &ps.EnvVars)
		json.Unmarshal([]byte(svcJSON), &ps.Services)
		ps.Enabled = enabled == 1
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

func scanProject(row *sql.Row) (*types.Project, error) {
	var p types.Project
	var portsJSON, volsJSON, envJSON, svcJSON string
	var enabled int
	var createdAt, updatedAt string
	err := row.Scan(
		&p.ID, &p.DisplayName, (*string)(&p.ConfigMode),
		&p.RegistryImage, &p.ImageTag, &portsJSON, &volsJSON, &p.ExtraOptions, &p.BindHost,
		&p.CustomCompose, &p.StackPath, (*string)(&p.HealthType), &p.HealthTarget,
		&p.PollInterval, &enabled, &envJSON, &svcJSON, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(portsJSON), &p.Ports)
	json.Unmarshal([]byte(volsJSON), &p.Volumes)
	json.Unmarshal([]byte(envJSON), &p.EnvVars)
	json.Unmarshal([]byte(svcJSON), &p.Services)
	p.Enabled = enabled == 1
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &p, nil
}

func scanProjectRows(rows *sql.Rows) (*types.Project, error) {
	var p types.Project
	var portsJSON, volsJSON, envJSON, svcJSON string
	var enabled int
	var createdAt, updatedAt string
	err := rows.Scan(
		&p.ID, &p.DisplayName, (*string)(&p.ConfigMode),
		&p.RegistryImage, &p.ImageTag, &portsJSON, &volsJSON, &p.ExtraOptions, &p.BindHost,
		&p.CustomCompose, &p.StackPath, (*string)(&p.HealthType), &p.HealthTarget,
		&p.PollInterval, &enabled, &envJSON, &svcJSON, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(portsJSON), &p.Ports)
	json.Unmarshal([]byte(volsJSON), &p.Volumes)
	json.Unmarshal([]byte(envJSON), &p.EnvVars)
	json.Unmarshal([]byte(svcJSON), &p.Services)
	p.Enabled = enabled == 1
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &p, nil
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
