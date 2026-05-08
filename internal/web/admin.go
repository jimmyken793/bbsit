package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kingyoung/bbsit/internal/types"
)

// AdminMux returns an http.Handler with operational endpoints and no auth.
// Intended to be served on a permission-gated Unix socket.
func (s *Server) AdminMux() http.Handler {
	mux := http.NewServeMux()

	// Read
	mux.HandleFunc("GET /api/projects", s.apiListProjects)
	mux.HandleFunc("GET /api/projects/{id}", s.apiGetProject)
	mux.HandleFunc("GET /api/projects/{id}/export", s.apiExportProject)

	// Mutate (sync versions: block until done so CLI can report exit code)
	mux.HandleFunc("POST /api/projects/{id}/start", s.apiAdminStart)
	mux.HandleFunc("POST /api/projects/{id}/stop", s.apiAdminStop)
	mux.HandleFunc("POST /api/projects/{id}/deploy", s.apiAdminDeploy)
	mux.HandleFunc("POST /api/projects/{id}/rollback", s.apiAdminRollback)
	mux.HandleFunc("DELETE /api/projects/{id}", s.apiDeleteProject)
	mux.HandleFunc("POST /api/projects/import", s.apiImportProject)

	return mux
}

// ServeAdminSocket binds the admin handler to a Unix socket.
// Caller cancels ctx to stop the listener; the socket file is removed on shutdown.
func (s *Server) ServeAdminSocket(ctx context.Context, path, group string, mode os.FileMode) error {
	if mode == 0 {
		mode = 0660
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			ln.Close()
			return fmt.Errorf("lookup group %s: %w", group, err)
		}
		gid, _ := strconv.Atoi(g.Gid)
		if err := os.Chown(path, -1, gid); err != nil {
			ln.Close()
			return fmt.Errorf("chown %s to group %s: %w", path, group, err)
		}
	}

	srv := &http.Server{Handler: s.AdminMux()}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		os.Remove(path)
	}()

	s.log.Info("admin socket listening", "path", path, "group", group, "mode", fmt.Sprintf("%04o", mode))
	go func() {
		if err := srv.Serve(ln); err != http.ErrServerClosed {
			s.log.Error("admin socket serve", "error", err)
		}
	}()
	return nil
}

// --- Sync action handlers (admin socket) ---

func (s *Server) apiAdminStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if err := s.deployer.Start(p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) apiAdminStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if err := s.deployer.Stop(p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) apiAdminDeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.scheduler.TriggerManualReconcile(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) apiAdminRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	if err := s.deployer.ManualRollback(p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Export ---

// apiExportProject writes the project as YAML (default) or as a tar.gz bundle
// containing project.yaml plus persistent data dirs whose host paths live under
// the project's stack path.
func (s *Server) apiExportProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.db.GetProject(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "yaml"
	}

	switch format {
	case "yaml":
		data, err := marshalProjectYAML(p)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.yaml"`, p.ID))
		w.Write(data)
	case "tar.gz", "tgz":
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, p.ID))
		if err := writeProjectTarball(w, p); err != nil {
			s.log.Error("export tarball", "project", id, "error", err)
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "format must be yaml or tar.gz"})
	}
}

// marshalProjectYAML emits a project as YAML, stripped of fields that are only
// meaningful on the source host (timestamps, stack_path).
func marshalProjectYAML(p *types.Project) ([]byte, error) {
	clone := *p
	clone.CreatedAt = clone.CreatedAt.Truncate(0) // keep but normalize
	clone.UpdatedAt = clone.UpdatedAt.Truncate(0)
	clone.StackPath = "" // let import recompute under target stack_root
	return yaml.Marshal(&clone)
}

// writeProjectTarball builds a gzipped tar containing project.yaml at root and
// any volume host paths that live under StackPath. Absolute paths outside
// StackPath are skipped (they are environment-specific and the operator must
// migrate them separately).
func writeProjectTarball(w io.Writer, p *types.Project) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	yamlBytes, err := marshalProjectYAML(p)
	if err != nil {
		return err
	}
	if err := writeTarFile(tw, "project.yaml", 0644, yamlBytes); err != nil {
		return err
	}

	// .env is regenerated from EnvVars in project.yaml on next deploy — skip.

	// Pack each volume host path that lives under StackPath. Tar entry names
	// are paths relative to StackPath, so on restore they extract to the same
	// spot under the target stack path.
	seen := map[string]bool{}
	for _, svc := range p.Services {
		for _, v := range svc.Volumes {
			hostPath := v.HostPath
			if !filepath.IsAbs(hostPath) {
				hostPath = filepath.Join(p.StackPath, hostPath)
			}
			rel, err := filepath.Rel(p.StackPath, hostPath)
			if err != nil || rel == "" || rel == "." || filepath.IsAbs(rel) || hasParentRef(rel) {
				continue
			}
			if seen[rel] {
				continue
			}
			seen[rel] = true
			if err := addDirToTar(tw, hostPath, rel); err != nil {
				return fmt.Errorf("pack %s: %w", rel, err)
			}
		}
	}
	return nil
}

// hasParentRef reports whether a cleaned relative path escapes its base.
func hasParentRef(rel string) bool {
	clean := filepath.Clean(rel)
	if clean == ".." {
		return true
	}
	return strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func writeTarFile(tw *tar.Writer, name string, mode int64, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(data)),
	}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func addDirToTar(tw *tar.Writer, srcDir, prefix string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to pack
		}
		return err
	}
	if !info.IsDir() {
		// File, not dir: pack as single entry
		return addFileToTar(tw, srcDir, prefix)
	}
	return filepath.Walk(srcDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, path)
		name := prefix
		if rel != "." {
			name = filepath.Join(prefix, rel)
		}
		if fi.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name:     name + "/",
				Mode:     int64(fi.Mode().Perm()),
				Typeflag: tar.TypeDir,
				ModTime:  fi.ModTime(),
			})
		}
		if !fi.Mode().IsRegular() {
			return nil // skip symlinks/sockets/etc.
		}
		return addFileToTar(tw, path, name)
	})
}

// importTarball extracts a project bundle (project.yaml + data/<dir>/...) and
// upserts the project. Data dirs are extracted under the project's stack path.
func (s *Server) importTarball(w http.ResponseWriter, data []byte) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid gzip: " + err.Error()})
		return
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var yamlBytes []byte
	type pendingFile struct {
		rel  string
		mode int64
		data []byte
	}
	type pendingDir struct {
		rel  string
		mode int64
	}
	var files []pendingFile
	var dirs []pendingDir

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read tar: " + err.Error()})
			return
		}
		name := filepath.Clean(hdr.Name)
		if hasParentRef(name) || filepath.IsAbs(name) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tar entry escapes root: " + hdr.Name})
			return
		}
		if name == "project.yaml" {
			yamlBytes, err = io.ReadAll(tr)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read project.yaml: " + err.Error()})
				return
			}
			continue
		}
		// Everything else extracts to stack path verbatim
		if hdr.Typeflag == tar.TypeDir {
			dirs = append(dirs, pendingDir{rel: name, mode: hdr.Mode})
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read tar entry: " + err.Error()})
			return
		}
		files = append(files, pendingFile{rel: name, mode: hdr.Mode, data: b})
	}

	if yamlBytes == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tarball missing project.yaml"})
		return
	}

	p := &types.Project{}
	if err := yaml.Unmarshal(yamlBytes, p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid project.yaml: " + err.Error()})
		return
	}

	// Defaults need stack_root; validateAndDefaultProject sets StackPath.
	if err := s.validateAndDefaultProject(p, true); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Extract data dirs to stack path
	if err := os.MkdirAll(p.StackPath, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir stack: " + err.Error()})
		return
	}
	for _, d := range dirs {
		if d.rel == "" {
			continue
		}
		mode := os.FileMode(d.mode).Perm()
		if mode == 0 {
			mode = 0755
		}
		if err := os.MkdirAll(filepath.Join(p.StackPath, d.rel), mode); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir " + d.rel + ": " + err.Error()})
			return
		}
	}
	for _, f := range files {
		dst := filepath.Join(p.StackPath, f.rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		mode := os.FileMode(f.mode).Perm()
		if mode == 0 {
			mode = 0644
		}
		if err := os.WriteFile(dst, f.data, mode); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write " + f.rel + ": " + err.Error()})
			return
		}
	}

	s.upsertProject(w, p)
}

func addFileToTar(tw *tar.Writer, srcFile, name string) error {
	fi, err := os.Stat(srcFile)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:    name,
		Mode:    int64(fi.Mode().Perm()),
		Size:    fi.Size(),
		ModTime: fi.ModTime(),
	}); err != nil {
		return err
	}
	f, err := os.Open(srcFile)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}
