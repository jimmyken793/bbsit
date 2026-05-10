import { useState, useEffect, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import type { Tunnel, TunnelInput, TunnelRoute, TunnelRouteDNSStatus } from '../types'

export default function TunnelsPage() {
  const [tunnels, setTunnels] = useState<Tunnel[]>([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState<Tunnel | null>(null)
  const [error, setError] = useState('')

  function load() {
    api.tunnels.list()
      .then(setTunnels)
      .catch(() => {})
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [])

  async function handleDelete(id: string) {
    if (!confirm(`Delete tunnel "${id}"? This stops cloudflared and removes the system project.`)) return
    setError('')
    try {
      await api.tunnels.delete(id)
      load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Delete failed')
    }
  }

  if (loading) return <div className="page-loading"><div className="spinner" /></div>

  return (
    <>
      <div style={{ marginBottom: 4 }}>
        <Link to="/" style={{ color: 'var(--muted)', fontSize: 13 }}>&larr; Projects</Link>
      </div>
      <div className="section-header">
        <h2>Cloudflare Tunnels</h2>
        <button className="btn btn-primary" onClick={() => { setEditing(null); setShowForm(true) }}>
          + New tunnel
        </button>
      </div>

      {error && <div className="alert alert-danger">{error}</div>}

      {showForm && (
        <TunnelForm
          tunnel={editing}
          onCancel={() => setShowForm(false)}
          onSaved={() => { setShowForm(false); load() }}
        />
      )}

      {tunnels.length === 0 && !showForm ? (
        <div className="empty-state">
          <h3>No tunnels yet</h3>
          <p>
            Create a tunnel in the Cloudflare dashboard, download its credentials.json, then
            click "+ New tunnel" above and paste the file contents.
          </p>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {tunnels.map(t => (
            <TunnelCard
              key={t.id}
              tunnel={t}
              onEdit={() => { setEditing(t); setShowForm(true) }}
              onDelete={() => handleDelete(t.id)}
            />
          ))}
        </div>
      )}
    </>
  )
}

function TunnelCard({ tunnel: t, onEdit, onDelete }: { tunnel: Tunnel; onEdit: () => void; onDelete: () => void }) {
  const [routes, setRoutes] = useState<TunnelRoute[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [routesError, setRoutesError] = useState('')
  const [syncMsg, setSyncMsg] = useState('')

  const loadRoutes = useCallback(() => {
    setLoading(true)
    setRoutesError('')
    api.tunnels.routes(t.id)
      .then(setRoutes)
      .catch(err => setRoutesError(err instanceof ApiError ? err.message : 'Failed to load routes'))
      .finally(() => setLoading(false))
  }, [t.id])

  useEffect(() => { loadRoutes() }, [loadRoutes])

  const handleSync = useCallback(async () => {
    setSyncing(true)
    setSyncMsg('')
    setRoutesError('')
    try {
      await api.tunnels.sync(t.id)
      setSyncMsg('Synced — config regenerated, cloudflared restarted, DNS update kicked off.')
      // Re-check after a short delay so the async DNS goroutine has time to PUT records.
      setTimeout(() => { loadRoutes() }, 1500)
    } catch (err) {
      setRoutesError(err instanceof ApiError ? err.message : 'Sync failed')
    } finally {
      setSyncing(false)
    }
  }, [t.id, loadRoutes])

  return (
    <div className="card" style={{ padding: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12 }}>
        <div>
          <h3 style={{ margin: 0 }}>{t.name || t.id}</h3>
          <div className="meta" style={{ fontSize: 12, color: 'var(--muted)' }}>
            {t.id} · CF: {t.cf_tunnel_id.slice(0, 8)}…
          </div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {t.has_api_token ? (
            <span
              title="DNS records auto-managed via Cloudflare API"
              style={{ fontSize: 12, color: '#0f5132', background: '#d1e7dd', border: '1px solid #badbcc', borderRadius: 4, padding: '2px 6px' }}
            >
              DNS auto
            </span>
          ) : (
            <span
              title="No API token — DNS CNAME records must be created manually in Cloudflare"
              style={{ fontSize: 12, color: '#664d03', background: '#fff3cd', border: '1px solid #ffe69c', borderRadius: 4, padding: '2px 6px' }}
            >
              DNS manual
            </span>
          )}
          {!t.enabled && (
            <span style={{ fontSize: 12, color: '#664d03', background: '#fff3cd', border: '1px solid #ffe69c', borderRadius: 4, padding: '2px 6px' }}>
              disabled
            </span>
          )}
          <button className="btn btn-outline btn-sm" onClick={loadRoutes} disabled={loading} title="Re-check DNS only (read-only)">
            {loading ? <span className="spinner" /> : '↻'}
          </button>
          <button
            className="btn btn-primary btn-sm"
            onClick={handleSync}
            disabled={syncing || !t.enabled}
            title={
              !t.enabled
                ? 'Enable the tunnel first'
                : 'Regenerate cloudflared config and (if API token set) push correct DNS records'
            }
          >
            {syncing ? <><span className="spinner" /> Syncing…</> : 'Sync DNS now'}
          </button>
          <button className="btn btn-outline btn-sm" onClick={onEdit}>Edit</button>
          <button className="btn btn-outline btn-sm" onClick={onDelete}>Delete</button>
        </div>
      </div>

      {syncMsg && (
        <div className="alert alert-info" style={{ marginTop: 12, marginBottom: 0 }}>{syncMsg}</div>
      )}

      <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--border, #e5e7eb)' }}>
        {routesError ? (
          <div className="alert alert-danger" style={{ margin: 0 }}>{routesError}</div>
        ) : loading && routes === null ? (
          <div style={{ fontSize: 13, color: 'var(--muted)' }}>Loading routes…</div>
        ) : !routes || routes.length === 0 ? (
          <div style={{ fontSize: 13, color: 'var(--muted)' }}>
            No public hostnames route through this tunnel yet. Add one in a project's service →
            Public hostnames.
          </div>
        ) : (
          <>
            <table className="table" style={{ marginBottom: 0 }}>
              <thead>
                <tr>
                  <th>Hostname</th>
                  <th>Project · Service</th>
                  <th>Local port</th>
                  <th>DNS</th>
                </tr>
              </thead>
              <tbody>
                {routes.map((r, i) => (
                  <tr key={i}>
                    <td><code>{r.hostname}</code></td>
                    <td>
                      <Link to={`/projects/${r.project_id}`}>{r.project_id}</Link>
                      {' · '}{r.service}
                    </td>
                    <td><code>localhost:{r.port}</code></td>
                    <td><DNSCell route={r} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
            <Troubleshooting routes={routes} hasToken={t.has_api_token} />
          </>
        )}
      </div>
    </div>
  )
}

// Troubleshooting renders an inline help block listing actionable fixes for
// each unique non-OK DNS status seen in the routes. Hidden when everything is OK.
function Troubleshooting({ routes, hasToken }: { routes: TunnelRoute[]; hasToken: boolean }) {
  const seen = new Set<TunnelRouteDNSStatus>()
  for (const r of routes) {
    if (r.dns.status !== 'ok') seen.add(r.dns.status)
  }
  if (seen.size === 0) return null

  return (
    <details style={{ marginTop: 12 }}>
      <summary style={{ cursor: 'pointer', fontSize: 13, color: 'var(--muted)', userSelect: 'none' }}>
        Troubleshooting ({seen.size} issue{seen.size === 1 ? '' : 's'})
      </summary>
      <div style={{ marginTop: 8, fontSize: 13, lineHeight: 1.5, color: 'var(--text)' }}>
        {seen.has('no_token') && <NoTokenHelp />}
        {seen.has('no_zone') && <NoZoneHelp hasToken={hasToken} />}
        {seen.has('wrong_target') && <WrongTargetHelp />}
        {seen.has('not_proxied') && <NotProxiedHelp />}
        {seen.has('wrong_type') && <WrongTypeHelp />}
        {seen.has('missing') && <MissingHelp hasToken={hasToken} />}
        {seen.has('error') && <ErrorHelp />}
      </div>
    </details>
  )
}

function HelpBlock({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={{ padding: '8px 12px', marginBottom: 8, background: 'var(--bg)', border: '1px solid var(--border, #e5e7eb)', borderRadius: 6 }}>
      <div style={{ fontWeight: 600, marginBottom: 4 }}>{title}</div>
      {children}
    </div>
  )
}

function NoTokenHelp() {
  return (
    <HelpBlock title="not checked — no Cloudflare API token on this tunnel">
      <p style={{ margin: '4px 0' }}>
        Without a token bbsit can't verify or auto-create DNS records. You can still run
        the tunnel; just create CNAMEs manually in Cloudflare (Hostname → CNAME →{' '}
        <code>{'<UUID>.cfargotunnel.com'}</code>, proxied).
      </p>
      <p style={{ margin: '4px 0' }}>
        To enable auto-DNS: <strong>Edit</strong> this tunnel and paste a Cloudflare API
        token. See <em>"no_zone"</em> below for the exact permissions needed.
      </p>
    </HelpBlock>
  )
}

function NoZoneHelp({ hasToken }: { hasToken: boolean }) {
  return (
    <HelpBlock title="no zone — token can't see the hostname's domain">
      {!hasToken && (
        <p style={{ margin: '4px 0' }}>(This shouldn't happen without a token — see "not checked" above.)</p>
      )}
      <p style={{ margin: '4px 0' }}>
        bbsit asked Cloudflare for the list of zones this token has access to, and your
        hostname's domain wasn't in the list. Check{' '}
        <code>journalctl -u bbsit | grep "zones visible"</code> to see exactly what zones
        the token returned. Three common causes:
      </p>
      <ol style={{ margin: '4px 0', paddingLeft: 20 }}>
        <li>
          <strong>Token has the wrong permission category.</strong> In Cloudflare → My
          Profile → API Tokens, edit the token. Each Permission row's first dropdown must
          be <code>Zone</code> (not <code>Account</code>). You need at least two:
          <ul style={{ margin: 4, paddingLeft: 20 }}>
            <li><code>Zone</code> · <code>Zone</code> · <code>Read</code></li>
            <li><code>Zone</code> · <code>DNS</code> · <code>Edit</code></li>
          </ul>
          The "Account · DNS Settings" rows under the Account permission category are a
          different feature and don't grant access to ordinary zone DNS records.
        </li>
        <li>
          <strong>Zone Resources too narrow.</strong> Set Zone Resources →{' '}
          <code>Include</code> → <code>All zones from an account</code> → pick the account.
        </li>
        <li>
          <strong>Wrong account.</strong> The hostname's zone lives in a different
          Cloudflare account than the one that owns this token. Recreate the token from
          the account that owns the zone.
        </li>
      </ol>
      <p style={{ margin: '4px 0' }}>
        After fixing the token, click <strong>Sync DNS now</strong> above (or paste a new
        token via Edit if the value changed).
      </p>
    </HelpBlock>
  )
}

function WrongTargetHelp() {
  return (
    <HelpBlock title="wrong target — CNAME points at a different tunnel UUID">
      <p style={{ margin: '4px 0' }}>
        There's already a CNAME for this hostname pointing at a different
        <code>.cfargotunnel.com</code> address (probably a previous tunnel). bbsit will
        rewrite it to point at <em>this</em> tunnel.
      </p>
      <p style={{ margin: '4px 0' }}>
        Click <strong>Sync DNS now</strong> above — bbsit issues a PUT on the existing
        record, no need to delete anything by hand.
      </p>
      <p style={{ margin: '4px 0', color: 'var(--muted)' }}>
        If you actually want this hostname to keep using the other tunnel, remove its{' '}
        <strong>Public hostname</strong> entry from the project's service instead, so bbsit
        stops trying to manage it.
      </p>
    </HelpBlock>
  )
}

function NotProxiedHelp() {
  return (
    <HelpBlock title="not proxied — CNAME correct but proxy is off">
      <p style={{ margin: '4px 0' }}>
        The CNAME points at the right tunnel, but Cloudflare's orange-cloud proxy is off,
        so traffic would bypass Cloudflare (and the tunnel) entirely.
      </p>
      <p style={{ margin: '4px 0' }}>
        Click <strong>Sync DNS now</strong> — bbsit always sets <code>proxied=true</code>{' '}
        when it owns the record.
      </p>
    </HelpBlock>
  )
}

function WrongTypeHelp() {
  return (
    <HelpBlock title="wrong type — A or AAAA record found instead of a CNAME">
      <p style={{ margin: '4px 0' }}>
        There's a legacy A/AAAA record at this name pointing at an origin IP. bbsit will
        replace it with a CNAME to the tunnel on Sync.
      </p>
      <p style={{ margin: '4px 0', color: '#7f1d1d' }}>
        ⚠ The origin server at that IP will stop receiving traffic for this hostname after
        Sync. Make sure that's intentional — if the origin is supposed to be reachable
        directly, remove the public_hostname from the project instead.
      </p>
    </HelpBlock>
  )
}

function MissingHelp({ hasToken }: { hasToken: boolean }) {
  return (
    <HelpBlock title="missing — no DNS record at this name">
      {hasToken ? (
        <p style={{ margin: '4px 0' }}>
          bbsit will create the record on Sync. Click <strong>Sync DNS now</strong>.
        </p>
      ) : (
        <p style={{ margin: '4px 0' }}>
          Add an API token to this tunnel so bbsit can create records automatically, or
          create the CNAME manually in Cloudflare.
        </p>
      )}
    </HelpBlock>
  )
}

function ErrorHelp() {
  return (
    <HelpBlock title="error — Cloudflare API call failed">
      <p style={{ margin: '4px 0' }}>
        The hover tooltip on the badge has the verbatim error from Cloudflare. Common
        causes: token expired, rate limited, or zone removed from the account. Check
        <code>journalctl -u bbsit -n 200</code> for more context.
      </p>
    </HelpBlock>
  )
}

function DNSCell({ route: r }: { route: TunnelRoute }) {
  const { dns } = r
  const tone = dnsTone(dns.status)
  const label = dnsLabel(dns.status)
  const detail = dnsDetail(dns)
  return (
    <span title={detail} style={{
      fontSize: 12,
      color: tone.fg,
      background: tone.bg,
      border: `1px solid ${tone.border}`,
      borderRadius: 4,
      padding: '2px 6px',
      display: 'inline-block',
    }}>
      {label}
      {dns.actual_target && dns.status !== 'ok' && (
        <span style={{ marginLeft: 6, opacity: 0.8 }}>→ {dns.actual_target}</span>
      )}
    </span>
  )
}

function dnsTone(s: TunnelRouteDNSStatus): { fg: string; bg: string; border: string } {
  switch (s) {
    case 'ok':           return { fg: '#0f5132', bg: '#d1e7dd', border: '#badbcc' }
    case 'no_token':     return { fg: '#475569', bg: '#f1f5f9', border: '#cbd5e1' }
    case 'missing':
    case 'no_zone':      return { fg: '#664d03', bg: '#fff3cd', border: '#ffe69c' }
    case 'wrong_target':
    case 'wrong_type':
    case 'not_proxied':
    case 'error':
    default:             return { fg: '#7f1d1d', bg: '#fee2e2', border: '#fecaca' }
  }
}

function dnsLabel(s: TunnelRouteDNSStatus): string {
  switch (s) {
    case 'ok':           return '✓ OK'
    case 'wrong_target': return '✗ wrong target'
    case 'wrong_type':   return '✗ wrong type'
    case 'not_proxied':  return '⚠ not proxied'
    case 'missing':      return '— missing'
    case 'no_zone':      return '— no zone'
    case 'no_token':     return 'not checked'
    case 'error':        return '✗ error'
  }
}

function dnsDetail(dns: TunnelRoute['dns']): string {
  switch (dns.status) {
    case 'ok':
      return `Proxied CNAME → ${dns.actual_target}`
    case 'wrong_target':
      return `CNAME points at ${dns.actual_target}, expected ${dns.expected_target}`
    case 'wrong_type':
      return `${dns.actual_type} record found (${dns.actual_target}); should be CNAME → ${dns.expected_target}`
    case 'not_proxied':
      return `CNAME correct but proxied=false — traffic would bypass Cloudflare and the tunnel`
    case 'missing':
      return `No record found in zone. Create CNAME → ${dns.expected_target} (proxied)`
    case 'no_zone':
      return `No zone visible to this tunnel's API token matches the hostname. Check the token's Zone Resources scope.`
    case 'no_token':
      return `Tunnel has no Cloudflare API token; DNS not checked. Add a token to enable verification + auto-routing.`
    case 'error':
      return dns.error || 'DNS check failed'
  }
}

function TunnelForm({ tunnel, onCancel, onSaved }: { tunnel: Tunnel | null; onCancel: () => void; onSaved: () => void }) {
  const isEdit = !!tunnel
  const [form, setForm] = useState<TunnelInput>({
    id: tunnel?.id || '',
    name: tunnel?.name || '',
    enabled: tunnel?.enabled ?? true,
    credentials: '',
    cf_api_token: '',
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setSaving(true)
    try {
      const payload: TunnelInput = {
        id: form.id,
        name: form.name,
        enabled: form.enabled,
      }
      if (form.credentials?.trim()) {
        payload.credentials = form.credentials
      }
      if (form.cf_api_token?.trim()) {
        payload.cf_api_token = form.cf_api_token.trim()
      }
      if (isEdit) {
        await api.tunnels.update(tunnel!.id, payload)
      } else {
        await api.tunnels.create(payload)
      }
      onSaved()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="card" style={{ marginBottom: 16 }}>
      <div className="card-title">{isEdit ? `Edit ${tunnel!.id}` : 'New tunnel'}</div>
      {error && <div className="alert alert-danger">{error}</div>}

      <div className="form-group">
        <label>Tunnel ID</label>
        <input
          className="form-control"
          value={form.id || ''}
          onChange={e => setForm(f => ({ ...f, id: e.target.value }))}
          disabled={isEdit}
          placeholder="my-tunnel"
          required
        />
        <div className="form-hint">bbsit-side identifier. Lowercase, hyphens. Cannot be changed.</div>
      </div>

      <div className="form-group">
        <label>Display name</label>
        <input
          className="form-control"
          value={form.name || ''}
          onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
          placeholder="Production"
        />
      </div>

      <div className="form-group">
        <label>credentials.json {isEdit && <span style={{ color: 'var(--muted)', fontSize: 12, fontWeight: 'normal' }}>(leave empty to keep existing)</span>}</label>
        <textarea
          className="form-control"
          rows={6}
          value={form.credentials || ''}
          onChange={e => setForm(f => ({ ...f, credentials: e.target.value }))}
          placeholder={'{\n  "AccountTag": "...",\n  "TunnelSecret": "...",\n  "TunnelID": "..."\n}'}
          required={!isEdit}
        />
        <div className="form-hint">
          Cloudflare dashboard → Networks → Tunnels → Configure → download credentials.json. Paste the file contents here.
        </div>
      </div>

      <div className="form-group">
        <label>
          Cloudflare API token{' '}
          {isEdit && tunnel?.has_api_token && (
            <span style={{ color: 'var(--muted)', fontSize: 12, fontWeight: 'normal' }}>
              (set — leave empty to keep)
            </span>
          )}
        </label>
        <input
          type="password"
          className="form-control"
          value={form.cf_api_token || ''}
          onChange={e => setForm(f => ({ ...f, cf_api_token: e.target.value }))}
          placeholder="optional — paste a token to enable DNS auto-routing"
          autoComplete="off"
        />
        <div className="form-hint">
          Optional. With a token (Zone.Zone:Read + Zone.DNS:Edit on routed zones), bbsit will
          automatically create/update CNAME records for each public hostname so you don't have
          to touch the Cloudflare dashboard. Generate at My Profile → API Tokens → Create Token →
          "Edit zone DNS" template.
        </div>
      </div>

      <div className="form-group">
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={form.enabled ?? true}
            onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))}
          />
          Enabled
        </label>
        <div className="form-hint">When off, cloudflared is stopped — public hostnames stop responding.</div>
      </div>

      <div style={{ display: 'flex', gap: 8 }}>
        <button type="submit" className="btn btn-primary" disabled={saving}>
          {saving ? <><span className="spinner" /> Saving&hellip;</> : 'Save'}
        </button>
        <button type="button" className="btn btn-outline" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  )
}
