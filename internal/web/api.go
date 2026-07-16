package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/types"
)

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// --- Auth API ---

func (s *Server) apiAuthStatus(w http.ResponseWriter, r *http.Request) {
	_, passErr := s.db.GetPasswordHash()
	setupRequired := passErr != nil

	loggedIn := false
	if cookie, err := r.Cookie("session"); err == nil {
		if expiry, ok := s.sessions.Load(cookie.Value); ok {
			loggedIn = time.Now().Before(expiry.(time.Time))
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{
		"setup_required": setupRequired,
		"logged_in":      loggedIn,
	})
}

func (s *Server) apiSetup(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.GetPasswordHash(); err == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already set up"})
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err := s.db.SetPassword(string(hash)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.createSession(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	hash, err := s.db.GetPasswordHash()
	if err != nil {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "not set up"})
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid password"})
		return
	}

	s.createSession(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session"); err == nil {
		s.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", MaxAge: -1, Path: "/"})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Projects API ---

func (s *Server) apiListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjectsWithState()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	includeSystem := r.URL.Query().Get("include_system") == "true"
	if !includeSystem {
		filtered := projects[:0]
		for _, p := range projects {
			if !p.IsSystem {
				filtered = append(filtered, p)
			}
		}
		projects = filtered
	}
	if projects == nil {
		projects = []types.ProjectWithState{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) apiGetProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	state, _ := s.db.GetState(id)
	deployments, _ := s.db.ListDeployments(id, 20)
	if deployments == nil {
		deployments = []types.Deployment{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project":     p,
		"state":       state,
		"deployments": deployments,
	})
}

func (s *Server) apiCreateProject(w http.ResponseWriter, r *http.Request) {
	p, err := decodeProject(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.validateAndDefaultProject(p, true); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := s.db.CreateProject(p); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "project ID already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.reconcileTunnels()
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) apiUpdateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := decodeProject(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	p.ID = id // ensure ID matches URL
	if err := s.validateAndDefaultProject(p, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := s.db.UpdateProject(p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.deployer.Emit(deployer.Event{Type: deployer.EventProjectUpdated, ProjectID: p.ID})
	s.reconcileTunnels()
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) apiDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	s.deployer.Stop(p)
	s.deployer.Emit(deployer.Event{Type: deployer.EventProjectDeleted, ProjectID: id})
	s.db.DeleteProject(id)
	s.reconcileTunnels()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Action handlers ---

func (s *Server) apiDeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	go s.scheduler.TriggerManualReconcile(id)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) apiRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	go s.deployer.ManualRollback(p)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) apiStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	go s.deployer.Stop(p)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) apiStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	go s.deployer.Start(p)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// --- YAML / tar.gz import ---

func (s *Server) apiImportProject(w http.ResponseWriter, r *http.Request) {
	body, err := importBodyReader(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if c, ok := body.(io.Closer); ok {
		defer c.Close()
	}

	// Sniff first 2 bytes to detect gzip vs YAML without buffering the whole body.
	br := bufio.NewReader(body)
	magic, _ := br.Peek(2)
	if len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		s.importTarball(w, br)
		return
	}
	s.importYAML(w, br)
}

// importBodyReader returns a reader for the import payload — either the raw
// request body or the first multipart "file" part. The caller is responsible
// for closing the returned reader if it implements io.Closer.
func importBodyReader(r *http.Request) (io.Reader, error) {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/form-data") {
		return r.Body, nil
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("multipart: %w", err)
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, fmt.Errorf("missing file field")
		}
		if err != nil {
			return nil, fmt.Errorf("multipart: %w", err)
		}
		if part.FormName() == "file" {
			return part, nil
		}
		part.Close()
	}
}

func (s *Server) importYAML(w http.ResponseWriter, r io.Reader) {
	data, err := io.ReadAll(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}
	p := &types.Project{}
	if err := yaml.Unmarshal(data, p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid YAML: " + err.Error()})
		return
	}
	s.upsertProject(w, p)
}

func (s *Server) upsertProject(w http.ResponseWriter, p *types.Project) {
	if err := s.validateAndDefaultProject(p, true); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Upsert: try update first, fall back to create
	if _, dbErr := s.db.GetProject(p.ID); dbErr == nil {
		if err := s.db.UpdateProject(p); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	} else {
		if err := s.db.CreateProject(p); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	s.deployer.Emit(deployer.Event{Type: deployer.EventProjectUpdated, ProjectID: p.ID})
	s.reconcileTunnels()

	writeJSON(w, http.StatusOK, p)
}

// reconcileTunnels rebuilds cloudflared ingress for every tunnel after a project change.
// Runs in background so HTTP responses aren't blocked on cloudflared restarts.
func (s *Server) reconcileTunnels() {
	if s.tunnels == nil {
		return
	}
	go func() {
		if err := s.tunnels.ReconcileAll(); err != nil {
			s.log.Error("reconcile tunnels", "error", err)
		}
	}()
}

// --- Helpers ---

func decodeProject(body io.Reader) (*types.Project, error) {
	p := &types.Project{}
	if err := json.NewDecoder(body).Decode(p); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return p, nil
}

func (s *Server) validateAndDefaultProject(p *types.Project, isNew bool) error {
	if isNew && p.ID == "" {
		return fmt.Errorf("project ID is required")
	}
	if isNew && !isValidID(p.ID) {
		return fmt.Errorf("project ID must be lowercase alphanumeric with hyphens only")
	}
	if p.ImageTag == "" {
		p.ImageTag = "latest"
	}
	if p.PollInterval <= 0 {
		p.PollInterval = 300
	}
	if p.StackPath == "" {
		p.StackPath = fmt.Sprintf("%s/%s", s.stackRoot, p.ID)
	}

	// Backward compat: if Services is empty but legacy RegistryImage is set,
	// auto-convert to a single-service array
	if len(p.Services) == 0 && p.RegistryImage != "" {
		p.Services = []types.ServiceConfig{{
			Name:          p.ID,
			RegistryImage: p.RegistryImage,
			ImageTag:      p.ImageTag,
			Polled:        true,
			Ports:         p.Ports,
			Volumes:       p.Volumes,
			ExtraOptions:  p.ExtraOptions,
		}}
	}

	// Backward compat: convert custom mode to form mode
	if p.ConfigMode == types.ConfigModeCustom && p.CustomCompose != "" && len(p.Services) == 0 {
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
		if err := yaml.Unmarshal([]byte(p.CustomCompose), &sc); err == nil {
			if len(sc.Services) > 0 {
				p.Services = sc.Services
			} else if sc.RegistryImage != "" {
				tag := sc.ImageTag
				if tag == "" {
					tag = "latest"
				}
				p.Services = []types.ServiceConfig{{
					Name:          p.ID,
					RegistryImage: sc.RegistryImage,
					ImageTag:      tag,
					Polled:        true,
					PullPolicy:    sc.PullPolicy,
					Ports:         sc.Ports,
					Volumes:       sc.Volumes,
					ExtraOptions:  sc.ExtraOptions,
				}}
			}
			if len(sc.EnvVars) > 0 {
				if p.EnvVars == nil {
					p.EnvVars = map[string]string{}
				}
				for k, v := range sc.EnvVars {
					p.EnvVars[k] = v
				}
			}
		}
		p.ConfigMode = types.ConfigModeForm
		p.CustomCompose = ""
	}

	// Validate services
	for i := range p.Services {
		if p.Services[i].Name == "" {
			return fmt.Errorf("service at index %d has no name", i)
		}
		if p.Services[i].ImageTag == "" {
			p.Services[i].ImageTag = "latest"
		}
		if p.Services[i].PullPolicy == "" {
			p.Services[i].PullPolicy = "always"
		}
		// Normalise volume host paths so absolute paths under the stack dir become
		// relative — relative paths are portable across hosts and travel with the
		// project export bundle.
		for j := range p.Services[i].Volumes {
			p.Services[i].Volumes[j].HostPath = normaliseVolumeHostPath(p.Services[i].Volumes[j].HostPath, p.StackPath)
		}
	}

	if err := validateAndInjectBackup(p); err != nil {
		return err
	}

	return nil
}

// validateAndInjectBackup checks BackupSpec referential integrity and, when
// configured, ensures the host-side backups dir is mounted into the target
// service at output_path. The mount is added with a relative host path
// ("backups") so it's portable across hosts and survives a pack/unpack.
func validateAndInjectBackup(p *types.Project) error {
	if p.Backup == nil {
		return nil
	}
	b := p.Backup
	if b.Service == "" {
		return fmt.Errorf("backup.service required")
	}
	if b.BackupCommand == "" {
		return fmt.Errorf("backup.backup_command required")
	}
	if b.OutputPath == "" {
		return fmt.Errorf("backup.output_path required")
	}
	if !filepath.IsAbs(b.OutputPath) {
		return fmt.Errorf("backup.output_path must be absolute (got %q)", b.OutputPath)
	}

	var target *types.ServiceConfig
	for i := range p.Services {
		if p.Services[i].Name == b.Service {
			target = &p.Services[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("backup.service %q not found among services", b.Service)
	}

	for _, v := range target.Volumes {
		if filepath.Clean(v.ContainerPath) == filepath.Clean(b.OutputPath) {
			return nil // operator already wired it up — leave alone
		}
	}
	target.Volumes = append(target.Volumes, types.VolumeMount{
		HostPath:      "backups",
		ContainerPath: b.OutputPath,
	})
	return nil
}

// normaliseVolumeHostPath makes a volume host path as portable as possible:
//   - "" stays ""
//   - "./foo", "foo/.", "foo/bar/.." all clean to canonical form
//   - absolute paths under stackPath become relative to stackPath
//   - absolute paths outside stackPath are kept as-is (operator-specific)
func normaliseVolumeHostPath(p, stackPath string) string {
	if p == "" {
		return ""
	}
	cleaned := filepath.Clean(p)
	if filepath.IsAbs(cleaned) && stackPath != "" {
		absStack := filepath.Clean(stackPath)
		if rel, err := filepath.Rel(absStack, cleaned); err == nil &&
			!strings.HasPrefix(rel, "..") && rel != "." {
			return rel
		}
	}
	return cleaned
}
