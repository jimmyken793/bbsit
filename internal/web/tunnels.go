package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kingyoung/bbsit/internal/tunnel"
	"github.com/kingyoung/bbsit/internal/types"
)

// tunnelResponse is the public shape — we never return secrets to clients.
type tunnelResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CFTunnelID  string `json:"cf_tunnel_id"`
	AccountTag  string `json:"account_tag"`
	Enabled     bool   `json:"enabled"`
	HasSecret   bool   `json:"has_secret"`
	HasAPIToken bool   `json:"has_api_token"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

func toTunnelResponse(t *types.Tunnel) tunnelResponse {
	return tunnelResponse{
		ID:          t.ID,
		Name:        t.Name,
		CFTunnelID:  t.CFTunnelID,
		AccountTag:  t.AccountTag,
		Enabled:     t.Enabled,
		HasSecret:   t.TunnelSecret != "",
		HasAPIToken: t.CFAPIToken != "",
		CreatedAt:   t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   t.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) apiListTunnels(w http.ResponseWriter, r *http.Request) {
	tunnels, err := s.db.ListTunnels()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]tunnelResponse, 0, len(tunnels))
	for i := range tunnels {
		out = append(out, toTunnelResponse(&tunnels[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiGetTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.db.GetTunnel(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tunnel not found"})
		return
	}
	writeJSON(w, http.StatusOK, toTunnelResponse(t))
}

// tunnelInput accepts either a separate-fields payload or a pasted credentials.json blob.
type tunnelInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Credentials string `json:"credentials,omitempty"` // raw credentials.json from CF dashboard
	// Or three explicit fields if user prefers
	CFTunnelID   string `json:"cf_tunnel_id,omitempty"`
	AccountTag   string `json:"account_tag,omitempty"`
	TunnelSecret string `json:"tunnel_secret,omitempty"`
	// Cloudflare API token (Zone.DNS:Edit, Zone.Zone:Read) for auto-managing
	// CNAME records that route hostnames to this tunnel. Optional — if empty,
	// the operator manages DNS manually.
	CFAPIToken string `json:"cf_api_token,omitempty"`
}

func (s *Server) apiCreateTunnel(w http.ResponseWriter, r *http.Request) {
	var in tunnelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	t, err := buildTunnel(&in, true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.db.CreateTunnel(t); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "tunnel ID already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.tunnels != nil {
		go func() {
			if err := s.tunnels.ReconcileTunnel(t.ID); err != nil {
				s.log.Error("reconcile new tunnel", "tunnel", t.ID, "error", err)
			}
		}()
	}
	writeJSON(w, http.StatusCreated, toTunnelResponse(t))
}

func (s *Server) apiUpdateTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := s.db.GetTunnel(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tunnel not found"})
		return
	}
	var in tunnelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	in.ID = id
	merged, err := buildTunnel(&in, false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Preserve existing secret/cf id/api token if not provided in this update
	if merged.TunnelSecret == "" {
		merged.TunnelSecret = existing.TunnelSecret
	}
	if merged.CFTunnelID == "" {
		merged.CFTunnelID = existing.CFTunnelID
	}
	if merged.AccountTag == "" {
		merged.AccountTag = existing.AccountTag
	}
	if merged.CFAPIToken == "" {
		merged.CFAPIToken = existing.CFAPIToken
	}
	if err := s.db.UpdateTunnel(merged); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.tunnels != nil {
		go func() {
			if err := s.tunnels.ReconcileTunnel(merged.ID); err != nil {
				s.log.Error("reconcile updated tunnel", "tunnel", merged.ID, "error", err)
			}
		}()
	}
	writeJSON(w, http.StatusOK, toTunnelResponse(merged))
}

// apiListTunnelRoutes returns the (hostname, project, service, port) routes for
// a tunnel along with each hostname's live DNS status (when an API token is set
// on the tunnel). Synchronous — DNS lookups bounded by the cfapi client's 15s
// per-call timeout plus a 30s overall budget inside Manager.RoutesFor.
func (s *Server) apiListTunnelRoutes(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.db.GetTunnel(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tunnel not found"})
		return
	}
	if s.tunnels == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tunnel manager not initialised"})
		return
	}
	routes, err := s.tunnels.RoutesFor(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if routes == nil {
		routes = []tunnel.Route{}
	}
	writeJSON(w, http.StatusOK, routes)
}

// apiSyncTunnel forces a reconcile: regenerate config.yml + credentials.json,
// restart cloudflared if config changed, and (if API token present) push DNS
// records to point at this tunnel. Useful after fixing a token's permissions
// or when an existing CNAME points at the wrong tunnel and the operator wants
// bbsit to take over.
func (s *Server) apiSyncTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.db.GetTunnel(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tunnel not found"})
		return
	}
	if s.tunnels == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tunnel manager not initialised"})
		return
	}
	// Reconcile is the synchronous part (writes config + restarts cloudflared);
	// DNS sync is launched as a goroutine inside it. We block on the
	// synchronous bit so the UI knows the file-level state is consistent before
	// it re-fetches routes; the UI's RoutesFor call right after will re-check
	// DNS regardless of the goroutine's progress.
	if err := s.tunnels.ReconcileTunnel(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) apiDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.db.GetTunnel(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tunnel not found"})
		return
	}
	if s.tunnels != nil {
		if err := s.tunnels.DeleteTunnel(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	} else {
		_ = s.db.DeleteTunnel(id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func buildTunnel(in *tunnelInput, isNew bool) (*types.Tunnel, error) {
	if isNew {
		if in.ID == "" {
			return nil, fmt.Errorf("tunnel ID is required")
		}
		if !isValidID(in.ID) {
			return nil, fmt.Errorf("tunnel ID must be lowercase alphanumeric with hyphens only")
		}
	}

	t := &types.Tunnel{
		ID:           in.ID,
		Name:         in.Name,
		CFTunnelID:   in.CFTunnelID,
		AccountTag:   in.AccountTag,
		TunnelSecret: in.TunnelSecret,
		CFAPIToken:   strings.TrimSpace(in.CFAPIToken),
		Enabled:      true,
	}
	if in.Enabled != nil {
		t.Enabled = *in.Enabled
	}

	// If a credentials.json blob was pasted, parse it (overrides explicit fields)
	if blob := strings.TrimSpace(in.Credentials); blob != "" {
		var creds types.TunnelCredentials
		if err := json.Unmarshal([]byte(blob), &creds); err != nil {
			return nil, fmt.Errorf("parse credentials.json: %w", err)
		}
		if creds.TunnelID == "" || creds.AccountTag == "" || creds.TunnelSecret == "" {
			return nil, fmt.Errorf("credentials.json missing TunnelID, AccountTag, or TunnelSecret")
		}
		t.CFTunnelID = creds.TunnelID
		t.AccountTag = creds.AccountTag
		t.TunnelSecret = creds.TunnelSecret
	}

	if isNew {
		if t.CFTunnelID == "" || t.AccountTag == "" || t.TunnelSecret == "" {
			return nil, fmt.Errorf("credentials are required: paste credentials.json or provide cf_tunnel_id, account_tag, tunnel_secret")
		}
	}
	return t, nil
}
