package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/scheduler"
	"github.com/kingyoung/bbsit/internal/types"
)

type Server struct {
	db        *db.DB
	deployer  *deployer.Deployer
	scheduler *scheduler.Scheduler
	log       *slog.Logger
	tmpl      *template.Template
	sessions  sync.Map // token -> expiry
	stackRoot string
}

func NewServer(database *db.DB, dep *deployer.Deployer, sched *scheduler.Scheduler, logger *slog.Logger, stackRoot string) *Server {
	funcMap := template.FuncMap{
		"shortDigest": deployer.ShortDigest,
		"fmtTime": func(t *time.Time) string {
			if t == nil {
				return "—"
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"statusClass": func(s types.ProjectStatus) string {
			switch s {
			case types.StatusRunning:
				return "status-ok"
			case types.StatusFailed:
				return "status-error"
			case types.StatusRolledBack:
				return "status-warn"
			case types.StatusDeploying:
				return "status-info"
			default:
				return "status-unknown"
			}
		},
		"json": func(v interface{}) string {
			b, _ := json.MarshalIndent(v, "", "  ")
			return string(b)
		},
		"addr": func(t time.Time) *time.Time {
			return &t
		},
	}

	s := &Server{
		db:        database,
		deployer:  dep,
		scheduler: sched,
		log:       logger,
		stackRoot: stackRoot,
	}

	s.tmpl = template.Must(template.New("").Funcs(funcMap).ParseGlob("templates/*.html"))
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /setup", s.handleSetupPage)
	mux.HandleFunc("POST /setup", s.handleSetup)

	// Protected
	mux.HandleFunc("GET /", s.auth(s.handleDashboard))
	mux.HandleFunc("GET /projects/new", s.auth(s.handleProjectForm))
	mux.HandleFunc("GET /projects/{id}", s.auth(s.handleProjectDetail))
	mux.HandleFunc("GET /projects/{id}/edit", s.auth(s.handleProjectForm))
	mux.HandleFunc("POST /projects", s.auth(s.handleProjectSave))
	mux.HandleFunc("POST /projects/{id}/delete", s.auth(s.handleProjectDelete))
	mux.HandleFunc("POST /projects/{id}/deploy", s.auth(s.handleProjectDeploy))
	mux.HandleFunc("POST /projects/{id}/rollback", s.auth(s.handleProjectRollback))
	mux.HandleFunc("POST /projects/{id}/stop", s.auth(s.handleProjectStop))
	mux.HandleFunc("POST /projects/{id}/start", s.auth(s.handleProjectStart))
	mux.HandleFunc("POST /logout", s.auth(s.handleLogout))

	// Static files
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	return mux
}

// --- Auth middleware ---

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		expiry, ok := s.sessions.Load(cookie.Value)
		if !ok || time.Now().After(expiry.(time.Time)) {
			s.sessions.Delete(cookie.Value)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) createSession(w http.ResponseWriter) {
	token := make([]byte, 32)
	rand.Read(token)
	sessionID := hex.EncodeToString(token)
	expiry := time.Now().Add(24 * time.Hour)

	s.sessions.Store(sessionID, expiry)
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionID,
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// --- Auth handlers ---

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	// Only show setup if no password exists
	if _, err := s.db.GetPasswordHash(); err == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.tmpl.ExecuteTemplate(w, "setup.html", nil)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.GetPasswordHash(); err == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	password := r.FormValue("password")
	if len(password) < 8 {
		s.tmpl.ExecuteTemplate(w, "setup.html", map[string]string{"Error": "Password must be at least 8 characters"})
		return
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err := s.db.SetPassword(string(hash)); err != nil {
		s.tmpl.ExecuteTemplate(w, "setup.html", map[string]string{"Error": err.Error()})
		return
	}

	s.createSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.GetPasswordHash(); err != nil {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.tmpl.ExecuteTemplate(w, "login.html", nil)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	hash, err := s.db.GetPasswordHash()
	if err != nil {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("password"))) != nil {
		s.tmpl.ExecuteTemplate(w, "login.html", map[string]string{"Error": "Invalid password"})
		return
	}

	s.createSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session"); err == nil {
		s.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- Dashboard ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	projects, err := s.db.ListProjectsWithState()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.tmpl.ExecuteTemplate(w, "dashboard.html", map[string]interface{}{
		"Projects": projects,
	})
}

// --- Project CRUD ---

func (s *Server) handleProjectForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p *types.Project
	if id != "" {
		var err error
		p, err = s.db.GetProject(id)
		if err != nil {
			http.Error(w, "Project not found", 404)
			return
		}
	}
	s.tmpl.ExecuteTemplate(w, "project_form.html", map[string]interface{}{
		"Project":   p,
		"StackRoot": s.stackRoot,
	})
}

func (s *Server) handleProjectSave(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	p := &types.Project{
		ID:            r.FormValue("id"),
		DisplayName:   r.FormValue("display_name"),
		ConfigMode:    types.ConfigMode(r.FormValue("config_mode")),
		RegistryImage: r.FormValue("registry_image"),
		ImageTag:      r.FormValue("image_tag"),
		CustomCompose: r.FormValue("custom_compose"),
		StackPath:     r.FormValue("stack_path"),
		HealthType:    types.HealthType(r.FormValue("health_type")),
		HealthTarget:  r.FormValue("health_target"),
		Enabled:       r.FormValue("enabled") == "on",
		ExtraOptions:  r.FormValue("extra_options"),
	}

	if p.ImageTag == "" {
		p.ImageTag = "latest"
	}

	// Parse poll interval
	if v, err := strconv.Atoi(r.FormValue("poll_interval")); err == nil && v > 0 {
		p.PollInterval = v
	} else {
		p.PollInterval = 300
	}

	// Parse ports (JSON array from form)
	if portsStr := r.FormValue("ports_json"); portsStr != "" {
		json.Unmarshal([]byte(portsStr), &p.Ports)
	}

	// Parse volumes (JSON array from form)
	if volsStr := r.FormValue("volumes_json"); volsStr != "" {
		json.Unmarshal([]byte(volsStr), &p.Volumes)
	}

	// Parse env vars (JSON object from form)
	if envStr := r.FormValue("env_vars_json"); envStr != "" {
		json.Unmarshal([]byte(envStr), &p.EnvVars)
	}

	// Auto-generate stack path if not set
	if p.StackPath == "" {
		p.StackPath = fmt.Sprintf("%s/%s", s.stackRoot, p.ID)
	}

	// Validate
	if p.ID == "" {
		s.renderFormError(w, p, "Project ID is required")
		return
	}
	if !isValidID(p.ID) {
		s.renderFormError(w, p, "Project ID must be lowercase alphanumeric with hyphens only")
		return
	}

	// Check if update or create
	isNew := r.FormValue("_is_new") == "true"
	if isNew {
		if err := s.db.CreateProject(p); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				s.renderFormError(w, p, "Project ID already exists")
				return
			}
			s.renderFormError(w, p, err.Error())
			return
		}
	} else {
		if err := s.db.UpdateProject(p); err != nil {
			s.renderFormError(w, p, err.Error())
			return
		}
	}

	http.Redirect(w, r, "/projects/"+p.ID, http.StatusSeeOther)
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		http.Error(w, "Project not found", 404)
		return
	}
	state, _ := s.db.GetState(id)
	deployments, _ := s.db.ListDeployments(id, 20)

	s.tmpl.ExecuteTemplate(w, "project_detail.html", map[string]interface{}{
		"Project":     p,
		"State":       state,
		"Deployments": deployments,
	})
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		http.Error(w, "Not found", 404)
		return
	}
	// Stop the stack first
	s.deployer.Stop(p)
	s.db.DeleteProject(id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- Actions ---

func (s *Server) handleProjectDeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	go s.scheduler.TriggerManualReconcile(id)
	http.Redirect(w, r, "/projects/"+id, http.StatusSeeOther)
}

func (s *Server) handleProjectRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		http.Error(w, "Not found", 404)
		return
	}
	go s.deployer.ManualRollback(p)
	http.Redirect(w, r, "/projects/"+id, http.StatusSeeOther)
}

func (s *Server) handleProjectStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		http.Error(w, "Not found", 404)
		return
	}
	s.deployer.Stop(p)
	http.Redirect(w, r, "/projects/"+id, http.StatusSeeOther)
}

func (s *Server) handleProjectStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		http.Error(w, "Not found", 404)
		return
	}
	s.deployer.Start(p)
	http.Redirect(w, r, "/projects/"+id, http.StatusSeeOther)
}

// --- Helpers ---

func (s *Server) renderFormError(w http.ResponseWriter, p *types.Project, errMsg string) {
	s.tmpl.ExecuteTemplate(w, "project_form.html", map[string]interface{}{
		"Project":   p,
		"Error":     errMsg,
		"StackRoot": s.stackRoot,
	})
}

func isValidID(id string) bool {
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return len(id) > 0 && id[0] != '-' && id[len(id)-1] != '-'
}
