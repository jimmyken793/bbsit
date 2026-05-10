// Package tunnel manages Cloudflare tunnels via locally-managed cloudflared instances.
//
// For each Tunnel record bbsit owns a "system project" that runs cloudflared. The
// project's stack directory holds config.yml (ingress rules) and credentials.json
// (tunnel auth). When ingress changes, bbsit rewrites config.yml and restarts the
// cloudflared container.
package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kingyoung/bbsit/internal/cfapi"
	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/types"
)

const (
	// SystemProjectIDPrefix prefixes the auto-generated bbsit project ID for each tunnel.
	SystemProjectIDPrefix = "cf-tunnel-"
	// CloudflaredImage is the official Cloudflare tunnel daemon image.
	CloudflaredImage = "cloudflare/cloudflared"
	// CloudflaredImageTag is pinned to "latest"; bbsit will poll for new digests.
	CloudflaredImageTag = "latest"
)

// Manager wires DB + deployer to keep cloudflared system projects in sync with
// the configured tunnels and per-service public_hostnames.
type Manager struct {
	db        *db.DB
	deployer  *deployer.Deployer
	log       *slog.Logger
	stackRoot string
	runtime   string
	mu        sync.Mutex // serialise reconciliation per process
}

func NewManager(database *db.DB, dep *deployer.Deployer, logger *slog.Logger, stackRoot, runtime string) *Manager {
	return &Manager{db: database, deployer: dep, log: logger, stackRoot: stackRoot, runtime: runtime}
}

// SystemProjectID returns the bbsit project ID for the tunnel's cloudflared instance.
func SystemProjectID(tunnelID string) string {
	return SystemProjectIDPrefix + tunnelID
}

// ReconcileTunnel ensures the cloudflared system project exists for the given tunnel,
// regenerates config.yml/credentials.json, and restarts cloudflared if running.
// Safe to call repeatedly; no-op when nothing has changed.
func (m *Manager) ReconcileTunnel(tunnelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.db.GetTunnel(tunnelID)
	if err != nil {
		return fmt.Errorf("get tunnel %s: %w", tunnelID, err)
	}

	projectID := SystemProjectID(t.ID)
	stackPath := filepath.Join(m.stackRoot, projectID)

	// Build ingress from all enabled, non-system projects' services
	ingress, err := m.collectIngressFor(t.ID)
	if err != nil {
		return fmt.Errorf("collect ingress: %w", err)
	}

	if err := os.MkdirAll(stackPath, 0755); err != nil {
		return fmt.Errorf("mkdir stack: %w", err)
	}
	// Lock the stack dir to operator-only. Container bind mounts are set up by
	// the host, so they don't need to traverse this dir from inside the
	// container — locking it here protects credentials.json from local users.
	if err := os.Chmod(stackPath, 0700); err != nil {
		return fmt.Errorf("chmod stack: %w", err)
	}

	// Write credentials.json (tunnel secret). Mode 0644 so the cloudflared
	// container process (typically UID 65532 / "nonroot" in distroless-based
	// images) can read the bind-mounted file. The stack directory itself is
	// operator-only, so on-host secrecy is enforced by directory perms, not
	// by file perms.
	if err := writeCredentials(stackPath, t); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	// Write config.yml (ingress rules)
	configChanged, err := writeConfigYml(stackPath, t, ingress)
	if err != nil {
		return fmt.Errorf("write config.yml: %w", err)
	}

	// Ensure system project exists; create if not
	existing, _ := m.db.GetProject(projectID)
	desired := buildSystemProject(t, stackPath)

	if existing == nil {
		// First time — create project record and trigger initial deploy
		if err := m.db.CreateProject(desired); err != nil {
			return fmt.Errorf("create system project: %w", err)
		}
		m.log.Info("created cloudflared system project", "tunnel", t.ID, "project", projectID)
		// Initial deploy will be picked up by scheduler on next tick (Polled=true).
		// Force an immediate start so the tunnel comes up without waiting.
		go func() {
			if err := m.deployer.Start(desired); err != nil {
				m.log.Error("initial cloudflared start", "tunnel", t.ID, "error", err)
			}
		}()
		return nil
	}

	// Update existing project (in case stack path / image changed)
	if err := m.db.UpdateProject(desired); err != nil {
		return fmt.Errorf("update system project: %w", err)
	}

	// Restart cloudflared so it picks up new ingress rules
	if !t.Enabled {
		// Tunnel disabled — stop cloudflared
		if err := m.deployer.Stop(desired); err != nil {
			m.log.Warn("stop disabled cloudflared", "tunnel", t.ID, "error", err)
		}
		return nil
	}
	if configChanged {
		go m.restartCloudflared(stackPath, t.ID)
	}

	// Sync DNS records for routed hostnames. Runs in background so a slow
	// Cloudflare API doesn't block the reconcile call (DNS may take seconds
	// per record, and reconciles can be triggered from API handlers).
	if t.CFAPIToken != "" && len(ingress) > 0 {
		go m.syncDNS(t, ingress)
	} else if t.CFAPIToken == "" && len(ingress) > 0 {
		m.log.Info("skipping DNS sync — no Cloudflare API token on tunnel",
			"tunnel", t.ID, "hostnames", len(ingress))
	}
	return nil
}

// syncDNS ensures each routed hostname has a proxied CNAME pointing at the
// tunnel's *.cfargotunnel.com address. Errors are logged per-hostname and do
// not fail the call — partial sync is better than no sync.
func (m *Manager) syncDNS(t *types.Tunnel, ingress []ingressEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := cfapi.New(t.CFAPIToken)
	zones, err := client.ListZones(ctx)
	if err != nil {
		m.log.Error("DNS sync: list zones", "tunnel", t.ID, "error", err)
		return
	}
	zoneNames := make([]string, len(zones))
	for i, z := range zones {
		zoneNames[i] = z.Name
	}
	m.log.Info("DNS sync: zones visible to token",
		"tunnel", t.ID, "count", len(zones), "zones", zoneNames)
	target := t.CFTunnelID + ".cfargotunnel.com"

	for _, e := range ingress {
		zone := cfapi.FindZoneForHostname(zones, e.Hostname)
		if zone == nil {
			m.log.Warn("DNS sync: no matching zone for hostname — check token's Zone Resources scope",
				"tunnel", t.ID, "hostname", e.Hostname, "visible_zones", zoneNames)
			continue
		}
		if err := ensureCNAME(ctx, client, zone.ID, e.Hostname, target); err != nil {
			m.log.Error("DNS sync: ensure CNAME",
				"tunnel", t.ID, "hostname", e.Hostname, "zone", zone.Name, "error", err)
			continue
		}
		m.log.Info("DNS sync: CNAME ok",
			"tunnel", t.ID, "hostname", e.Hostname, "target", target, "zone", zone.Name)
	}
}

// ensureCNAME creates or updates a proxied CNAME record so that name -> target.
// It does NOT delete other records that might exist for the same name — if the
// operator has, say, a TXT record for ACME alongside, it stays.
func ensureCNAME(ctx context.Context, client *cfapi.Client, zoneID, name, target string) error {
	recs, err := client.ListDNSRecords(ctx, zoneID, name)
	if err != nil {
		return fmt.Errorf("list dns: %w", err)
	}
	desired := cfapi.DNSRecord{
		Type:    "CNAME",
		Name:    name,
		Content: target,
		TTL:     1, // 1 = "automatic" in CF API
		Proxied: true,
	}
	// Look for an existing CNAME or A/AAAA record under this name. If a CNAME
	// exists, update it. If an A/AAAA exists (legacy origin), update it
	// in-place to a CNAME (Cloudflare allows replacing the type via PUT).
	for _, r := range recs {
		if r.Type == "CNAME" || r.Type == "A" || r.Type == "AAAA" {
			if r.Type == "CNAME" && r.Content == target && r.Proxied {
				return nil // already correct
			}
			_, err := client.UpdateDNSRecord(ctx, zoneID, r.ID, desired)
			return err
		}
	}
	_, err = client.CreateDNSRecord(ctx, zoneID, desired)
	return err
}

// ReconcileAll reconciles every tunnel. Used after project edits — a project
// may add/remove hostnames across multiple tunnels in one save.
func (m *Manager) ReconcileAll() error {
	tunnels, err := m.db.ListTunnels()
	if err != nil {
		return err
	}
	var firstErr error
	for _, t := range tunnels {
		if err := m.ReconcileTunnel(t.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Route is one (hostname → service:port) mapping exposed via a tunnel,
// optionally annotated with the live DNS status from Cloudflare.
type Route struct {
	Hostname  string   `json:"hostname"`
	ProjectID string   `json:"project_id"`
	Service   string   `json:"service"`
	Port      int      `json:"port"`
	DNS       RouteDNS `json:"dns"`
}

// RouteDNS describes whether the public DNS record for a hostname currently
// points at this tunnel.
type RouteDNS struct {
	// Status is one of:
	//   ok            — proxied CNAME pointing at <tunnel>.cfargotunnel.com
	//   wrong_target  — record exists but points somewhere else
	//   not_proxied   — CNAME correct but proxied=false (would bypass tunnel)
	//   wrong_type    — non-CNAME record (A/AAAA) found at this name
	//   missing       — no record found in the matched zone
	//   no_zone       — no zone in the token's scope matches the hostname
	//   no_token      — tunnel has no API token; status not checked
	//   error         — Cloudflare API call failed (see Error)
	Status         string `json:"status"`
	ExpectedTarget string `json:"expected_target,omitempty"`
	ActualTarget   string `json:"actual_target,omitempty"`
	ActualType     string `json:"actual_type,omitempty"`
	Proxied        bool   `json:"proxied,omitempty"`
	Error          string `json:"error,omitempty"`
}

// RoutesFor returns every (hostname, service, port) route that cloudflared
// would serve for this tunnel, plus a per-hostname DNS verification when the
// tunnel has an API token. Safe to call from HTTP handlers — the underlying CF
// calls have a 15s timeout each.
func (m *Manager) RoutesFor(tunnelID string) ([]Route, error) {
	t, err := m.db.GetTunnel(tunnelID)
	if err != nil {
		return nil, fmt.Errorf("get tunnel: %w", err)
	}
	entries, err := m.collectIngressFor(tunnelID)
	if err != nil {
		return nil, err
	}
	expected := t.CFTunnelID + ".cfargotunnel.com"
	routes := make([]Route, len(entries))
	for i, e := range entries {
		routes[i] = Route{
			Hostname:  e.Hostname,
			ProjectID: e.ProjectID,
			Service:   e.Service,
			Port:      e.Port,
			DNS:       RouteDNS{Status: "no_token", ExpectedTarget: expected},
		}
	}

	if t.CFAPIToken == "" || len(routes) == 0 {
		return routes, nil
	}

	// One zone listing covers all routes.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := cfapi.New(t.CFAPIToken)
	zones, err := client.ListZones(ctx)
	if err != nil {
		// Token is set but unusable — surface the error per-route so the user
		// sees it in the UI rather than a vague no-data state.
		for i := range routes {
			routes[i].DNS = RouteDNS{
				Status:         "error",
				ExpectedTarget: expected,
				Error:          fmt.Sprintf("list zones: %v", err),
			}
		}
		return routes, nil
	}

	for i, e := range entries {
		zone := cfapi.FindZoneForHostname(zones, e.Hostname)
		if zone == nil {
			routes[i].DNS = RouteDNS{Status: "no_zone", ExpectedTarget: expected}
			continue
		}
		recs, err := client.ListDNSRecords(ctx, zone.ID, e.Hostname)
		if err != nil {
			routes[i].DNS = RouteDNS{
				Status:         "error",
				ExpectedTarget: expected,
				Error:          err.Error(),
			}
			continue
		}
		routes[i].DNS = classifyDNS(expected, recs)
		routes[i].DNS.ExpectedTarget = expected
	}
	return routes, nil
}

// classifyDNS picks the most relevant record at the hostname and decides whether
// it correctly points at the tunnel.
func classifyDNS(expectedTarget string, recs []cfapi.DNSRecord) RouteDNS {
	if len(recs) == 0 {
		return RouteDNS{Status: "missing"}
	}
	// Prefer CNAME, then A/AAAA, then anything else (TXT etc. are sibling-only).
	var pick *cfapi.DNSRecord
	for i, r := range recs {
		if r.Type == "CNAME" {
			pick = &recs[i]
			break
		}
	}
	if pick == nil {
		for i, r := range recs {
			if r.Type == "A" || r.Type == "AAAA" {
				pick = &recs[i]
				break
			}
		}
	}
	if pick == nil {
		// Only sibling-only records (TXT etc.) — treat as missing for routing.
		return RouteDNS{Status: "missing"}
	}
	if pick.Type != "CNAME" {
		return RouteDNS{
			Status:       "wrong_type",
			ActualType:   pick.Type,
			ActualTarget: pick.Content,
			Proxied:      pick.Proxied,
		}
	}
	if pick.Content != expectedTarget {
		return RouteDNS{
			Status:       "wrong_target",
			ActualType:   pick.Type,
			ActualTarget: pick.Content,
			Proxied:      pick.Proxied,
		}
	}
	if !pick.Proxied {
		return RouteDNS{
			Status:       "not_proxied",
			ActualType:   pick.Type,
			ActualTarget: pick.Content,
			Proxied:      pick.Proxied,
		}
	}
	return RouteDNS{
		Status:       "ok",
		ActualType:   pick.Type,
		ActualTarget: pick.Content,
		Proxied:      pick.Proxied,
	}
}

// DeleteTunnel stops the cloudflared system project and deletes both the project
// and the tunnel record.
func (m *Manager) DeleteTunnel(tunnelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	projectID := SystemProjectID(tunnelID)
	if p, err := m.db.GetProject(projectID); err == nil {
		_ = m.deployer.Stop(p)
		_ = m.db.DeleteProject(projectID)
		// Best-effort cleanup of stack dir
		_ = os.RemoveAll(p.StackPath)
	}
	return m.db.DeleteTunnel(tunnelID)
}

// ingressEntry is one route in cloudflared's config.yml.
type ingressEntry struct {
	Hostname string
	Port     int
	// Source — for logging / "owner" tracking
	ProjectID string
	Service   string
}

// collectIngressFor walks all enabled, non-system projects and returns ingress
// entries pointing at the given tunnel.
func (m *Manager) collectIngressFor(tunnelID string) ([]ingressEntry, error) {
	projects, err := m.db.ListProjects()
	if err != nil {
		return nil, err
	}
	var entries []ingressEntry
	for _, p := range projects {
		if p.IsSystem || !p.Enabled {
			continue
		}
		for _, svc := range p.Services {
			for _, h := range svc.PublicHostnames {
				if h.TunnelID != tunnelID || h.Hostname == "" || h.Port <= 0 {
					continue
				}
				entries = append(entries, ingressEntry{
					Hostname:  h.Hostname,
					Port:      h.Port,
					ProjectID: p.ID,
					Service:   svc.Name,
				})
			}
		}
	}
	// Stable order so config.yml diffs are meaningful
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Hostname < entries[j].Hostname
	})
	return entries, nil
}

func writeCredentials(stackPath string, t *types.Tunnel) error {
	creds := types.TunnelCredentials{
		AccountTag:   t.AccountTag,
		TunnelSecret: t.TunnelSecret,
		TunnelID:     t.CFTunnelID,
	}
	b, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	path := filepath.Join(stackPath, "credentials.json")
	return os.WriteFile(path, b, 0644)
}

// writeConfigYml writes the cloudflared config.yml file.
// Returns true if content changed from what's on disk.
func writeConfigYml(stackPath string, t *types.Tunnel, entries []ingressEntry) (bool, error) {
	var b strings.Builder
	b.WriteString("# Auto-generated by bbsit. Do not edit manually.\n")
	b.WriteString(fmt.Sprintf("tunnel: %s\n", t.CFTunnelID))
	b.WriteString("credentials-file: /etc/cloudflared/credentials.json\n")
	b.WriteString("ingress:\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("  - hostname: %s\n", e.Hostname))
		b.WriteString(fmt.Sprintf("    service: http://localhost:%d\n", e.Port))
	}
	// catch-all (cloudflared requires the last rule to match anything)
	b.WriteString("  - service: http_status:404\n")

	path := filepath.Join(stackPath, "config.yml")
	newContent := b.String()

	old, _ := os.ReadFile(path)
	if string(old) == newContent {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(newContent), 0644)
}

// buildSystemProject constructs the bbsit Project that runs cloudflared for the tunnel.
// Uses host network so it can reach 127.0.0.1 services. Mounts config.yml and
// credentials.json into the standard cloudflared paths.
func buildSystemProject(t *types.Tunnel, stackPath string) *types.Project {
	displayName := fmt.Sprintf("Cloudflared: %s", t.Name)
	if t.Name == "" {
		displayName = fmt.Sprintf("Cloudflared (%s)", t.ID)
	}
	svc := types.ServiceConfig{
		Name:          "cloudflared",
		RegistryImage: CloudflaredImage,
		ImageTag:      CloudflaredImageTag,
		Polled:        true,
		Volumes: []types.VolumeMount{
			{HostPath: "config.yml", ContainerPath: "/etc/cloudflared/config.yml", ReadOnly: true},
			{HostPath: "credentials.json", ContainerPath: "/etc/cloudflared/credentials.json", ReadOnly: true},
		},
		ExtraOptions: "network_mode: host\ncommand: tunnel --no-autoupdate --config /etc/cloudflared/config.yml run",
	}
	return &types.Project{
		ID:           SystemProjectID(t.ID),
		DisplayName:  displayName,
		ConfigMode:   types.ConfigModeForm,
		Services:     []types.ServiceConfig{svc},
		BindHost:     "127.0.0.1",
		StackPath:    stackPath,
		HealthType:   types.HealthNone,
		PollInterval: 3600, // hourly is enough for cloudflared image updates
		Enabled:      t.Enabled,
		IsSystem:     true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}

func (m *Manager) restartCloudflared(stackPath, tunnelID string) {
	cmd := exec.Command(m.runtime, "compose", "-f", filepath.Join(stackPath, "compose.yaml"), "restart", "cloudflared")
	cmd.Dir = stackPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.log.Warn("restart cloudflared", "tunnel", tunnelID, "error", err, "output", string(out))
		return
	}
	m.log.Info("restarted cloudflared after ingress change", "tunnel", tunnelID)
}
