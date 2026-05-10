import { useState, useEffect, useRef, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import { useWebSocket } from '../hooks/useWebSocket'
import type { DeployEvent } from '../hooks/useWebSocket'
import type { ProjectWithState, Tunnel } from '../types'

const SYSTEM_PROJECT_PREFIX = 'cf-tunnel-'

function StatusBadge({ status }: { status: string }) {
  return <span className={`badge badge-${status}`}>{status.replace('_', ' ')}</span>
}

export default function DashboardPage() {
  const [projects, setProjects] = useState<ProjectWithState[]>([])
  const [tunnels, setTunnels] = useState<Tunnel[]>([])
  const [loading, setLoading] = useState(true)
  const [importError, setImportError] = useState('')
  const [importOk, setImportOk] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const load = useCallback(() => {
    Promise.all([
      api.projects.list({ includeSystem: true }),
      api.tunnels.list().catch(() => [] as Tunnel[]),
    ])
      .then(([ps, ts]) => {
        setProjects(ps)
        setTunnels(ts)
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  const projectIds = projects.map(p => p.id)

  const handleEvent = useCallback((event: DeployEvent) => {
    if (event.type === 'state_change' && event.status) {
      setProjects(prev => prev.map(p =>
        p.id === event.project_id
          ? { ...p, state: { ...p.state, status: event.status as ProjectWithState['state']['status'] } }
          : p
      ))
    }
    if (event.type === 'project_deleted') {
      setProjects(prev => prev.filter(p => p.id !== event.project_id))
    }
    if (event.type === 'project_updated') {
      load()
    }
  }, [])

  useWebSocket(projectIds, handleEvent)

  async function handleImport(file: File) {
    setImportError('')
    setImportOk('')
    try {
      const p = await api.projects.import(file)
      setImportOk(`Imported "${p.display_name || p.id}" successfully.`)
      load()
    } catch (err) {
      setImportError(err instanceof ApiError ? err.message : 'Import failed')
    }
  }

  if (loading) return <div className="page-loading"><div className="spinner" /></div>

  const userProjects = projects.filter(p => !p.is_system)
  const systemProjects = projects.filter(p => p.is_system)

  // Tunnel ID -> count of public hostnames routed through it (across all user projects)
  const tunnelRouteCounts: Record<string, number> = {}
  for (const p of userProjects) {
    for (const svc of p.services || []) {
      for (const h of svc.public_hostnames || []) {
        tunnelRouteCounts[h.tunnel_id] = (tunnelRouteCounts[h.tunnel_id] || 0) + 1
      }
    }
  }
  const tunnelById: Record<string, Tunnel> = {}
  for (const t of tunnels) tunnelById[t.id] = t

  return (
    <>
      <div className="section-header">
        <h2>Projects</h2>
        <div className="btn-group">
          <label className="yaml-import-label" title="Import project from YAML file or bundle (.tar.gz)">
            ↑ Import YAML/Bundle
            <input
              ref={fileRef}
              type="file"
              accept=".yaml,.yml,.tar.gz,.tgz,.gz"
              style={{ display: 'none' }}
              onChange={e => {
                const f = e.target.files?.[0]
                if (f) handleImport(f)
                e.target.value = ''
              }}
            />
          </label>
          <Link to="/projects/new" className="btn btn-primary">+ New project</Link>
        </div>
      </div>

      {importError && <div className="alert alert-danger">{importError}</div>}
      {importOk && <div className="alert alert-info">{importOk}</div>}

      {userProjects.length === 0 ? (
        <div className="empty-state">
          <h3>No projects yet</h3>
          <p>Add a project to start managing deployments.</p>
        </div>
      ) : (
        <div className="project-grid">
          {userProjects.map(p => (
            <Link key={p.id} to={`/projects/${p.id}`} className="project-card">
              <div>
                <h3>{p.display_name || p.id}</h3>
                <div className="meta">
                  {p.id}
                  {p.services?.length ? ` · ${p.services[0].registry_image}:${p.services[0].image_tag || 'latest'}${p.services.length > 1 ? ` +${p.services.length - 1}` : ''}` : ''}
                </div>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <StatusBadge status={p.state?.status || 'unknown'} />
                {!p.enabled && (
                  <span
                    title="Auto-update polling is off; scheduler skips this project"
                    style={{ fontSize: 12, color: '#664d03', background: '#fff3cd', border: '1px solid #ffe69c', borderRadius: 4, padding: '2px 6px' }}
                  >
                    auto-update off
                  </span>
                )}
              </div>
            </Link>
          ))}
        </div>
      )}

      {systemProjects.length > 0 && (
        <details style={{ marginTop: 24 }}>
          <summary
            style={{
              cursor: 'pointer',
              fontSize: 13,
              color: 'var(--muted)',
              padding: '8px 0',
              userSelect: 'none',
            }}
          >
            System projects ({systemProjects.length}) — managed by bbsit
          </summary>
          <div className="project-grid" style={{ marginTop: 8, opacity: 0.9 }}>
            {systemProjects.map(p => {
              const tunnelID = p.id.startsWith(SYSTEM_PROJECT_PREFIX)
                ? p.id.slice(SYSTEM_PROJECT_PREFIX.length)
                : ''
              const tunnel = tunnelID ? tunnelById[tunnelID] : undefined
              const routes = tunnelID ? (tunnelRouteCounts[tunnelID] || 0) : 0
              return (
                <Link
                  key={p.id}
                  to={`/projects/${p.id}`}
                  className="project-card"
                  title="System project managed by bbsit — view-only"
                  style={{ background: 'var(--surface-muted, #f8fafc)' }}
                >
                  <div>
                    <h3>{p.display_name || p.id}</h3>
                    <div className="meta">
                      {p.id}
                      {tunnel && ` · CF ${tunnel.cf_tunnel_id.slice(0, 8)}…`}
                      {tunnelID && ` · ${routes} route${routes === 1 ? '' : 's'}`}
                    </div>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    <span
                      style={{ fontSize: 12, color: '#0c4a6e', background: '#e0f2fe', border: '1px solid #bae6fd', borderRadius: 4, padding: '2px 6px' }}
                    >
                      system
                    </span>
                    {tunnel && !tunnel.enabled && (
                      <span
                        title="Tunnel disabled in bbsit"
                        style={{ fontSize: 12, color: '#664d03', background: '#fff3cd', border: '1px solid #ffe69c', borderRadius: 4, padding: '2px 6px' }}
                      >
                        tunnel disabled
                      </span>
                    )}
                    {tunnel && !tunnel.has_secret && (
                      <span
                        title="Tunnel credentials missing — cloudflared cannot start"
                        style={{ fontSize: 12, color: '#7f1d1d', background: '#fee2e2', border: '1px solid #fecaca', borderRadius: 4, padding: '2px 6px' }}
                      >
                        no credentials
                      </span>
                    )}
                    <StatusBadge status={p.state?.status || 'unknown'} />
                  </div>
                </Link>
              )
            })}
          </div>
        </details>
      )}
    </>
  )
}
