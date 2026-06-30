package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/kingyoung/bbsit/internal/backup"
	"github.com/kingyoung/bbsit/internal/types"
)

func (s *Server) apiAdminBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if s.backups == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup service not initialised"})
		return
	}
	if p.Backup == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project has no backup spec configured"})
		return
	}

	trigger := r.URL.Query().Get("trigger")
	if trigger == "" {
		trigger = "manual"
	}

	run, err := s.backups.Run(r.Context(), p, trigger)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": err.Error(),
			"run":   run,
		})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) apiAdminListBackups(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if s.backups == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup service not initialised"})
		return
	}
	files, err := s.backups.List(p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if files == nil {
		files = []types.BackupFile{}
	}
	writeJSON(w, http.StatusOK, files)
}

func (s *Server) apiAdminBackupHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.db.GetProject(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if s.backups == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup service not initialised"})
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := s.backups.History(id, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if runs == nil {
		runs = []types.BackupRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

type restoreRequest struct {
	File string `json:"file"`           // host path or base name in backups/
	As   string `json:"as,omitempty"`   // optional: restore into a new clone project for verification
}

func (s *Server) apiAdminRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if s.backups == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup service not initialised"})
		return
	}
	if p.Backup == nil || p.Backup.RestoreCommand == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project has no restore_command configured"})
		return
	}

	var req restoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.File == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file required"})
		return
	}

	// Resolve a bare basename against the project's backups dir so callers
	// don't have to know the full host path.
	if !filepath.IsAbs(req.File) && filepath.Base(req.File) == req.File {
		req.File = filepath.Join(p.BackupHostDir(), req.File)
	}

	target, err := s.backups.Restore(r.Context(), p, backup.RestoreOptions{
		File:        req.File,
		AsProjectID: req.As,
	}, s.deployer)
	if err != nil {
		msg := err.Error()
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   msg,
			"project": projectIDOrNil(target),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"project": projectIDOrNil(target),
		"message": fmt.Sprintf("restored %s", target.ID),
	})
}

func projectIDOrNil(p *types.Project) any {
	if p == nil {
		return nil
	}
	return p.ID
}
