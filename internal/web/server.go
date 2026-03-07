package web

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/scheduler"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

type Server struct {
	db        *db.DB
	deployer  *deployer.Deployer
	scheduler *scheduler.Scheduler
	log       *slog.Logger
	sessions  sync.Map // token -> expiry
	stackRoot string
}

func NewServer(database *db.DB, dep *deployer.Deployer, sched *scheduler.Scheduler, logger *slog.Logger, stackRoot string) *Server {
	return &Server{
		db:        database,
		deployer:  dep,
		scheduler: sched,
		log:       logger,
		stackRoot: stackRoot,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Auth API (public)
	mux.HandleFunc("GET /api/auth/status", s.apiAuthStatus)
	mux.HandleFunc("POST /api/auth/setup", s.apiSetup)
	mux.HandleFunc("POST /api/auth/login", s.apiLogin)
	mux.HandleFunc("POST /api/auth/logout", s.apiAuth(s.apiLogout))

	// Projects API (protected) — import must be registered before /{id}
	mux.HandleFunc("POST /api/projects/import", s.apiAuth(s.apiImportProject))
	mux.HandleFunc("GET /api/projects", s.apiAuth(s.apiListProjects))
	mux.HandleFunc("POST /api/projects", s.apiAuth(s.apiCreateProject))
	mux.HandleFunc("GET /api/projects/{id}", s.apiAuth(s.apiGetProject))
	mux.HandleFunc("PUT /api/projects/{id}", s.apiAuth(s.apiUpdateProject))
	mux.HandleFunc("DELETE /api/projects/{id}", s.apiAuth(s.apiDeleteProject))
	mux.HandleFunc("POST /api/projects/{id}/deploy", s.apiAuth(s.apiDeploy))
	mux.HandleFunc("POST /api/projects/{id}/rollback", s.apiAuth(s.apiRollback))
	mux.HandleFunc("POST /api/projects/{id}/stop", s.apiAuth(s.apiStop))
	mux.HandleFunc("POST /api/projects/{id}/start", s.apiAuth(s.apiStart))

	// SPA fallback — serve embedded React app for all other routes
	dist, _ := fs.Sub(frontendFS, "frontend/dist")
	mux.Handle("/", spaHandler(http.FS(dist)))

	return mux
}

// spaHandler serves static files and falls back to index.html for client-side routing.
func spaHandler(fsys http.FileSystem) http.Handler {
	fileServer := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := fsys.Open(r.URL.Path)
		if err != nil {
			r2 := *r
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, &r2)
			return
		}
		f.Close()
		fileServer.ServeHTTP(w, r)
	})
}

// apiAuth returns 401 JSON for unauthenticated requests.
func (s *Server) apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		expiry, ok := s.sessions.Load(cookie.Value)
		if !ok || time.Now().After(expiry.(time.Time)) {
			s.sessions.Delete(cookie.Value)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
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

func isValidID(id string) bool {
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return len(id) > 0 && id[0] != '-' && id[len(id)-1] != '-'
}
