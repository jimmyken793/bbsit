import { useState, useEffect, useRef, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import { useWebSocket } from '../hooks/useWebSocket'
import type { DeployEvent } from '../hooks/useWebSocket'
import type { ProjectWithState } from '../types'

function StatusBadge({ status }: { status: string }) {
  return <span className={`badge badge-${status}`}>{status.replace('_', ' ')}</span>
}

export default function DashboardPage() {
  const [projects, setProjects] = useState<ProjectWithState[]>([])
  const [loading, setLoading] = useState(true)
  const [showSystem, setShowSystem] = useState(false)
  const [importError, setImportError] = useState('')
  const [importOk, setImportOk] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const load = useCallback(() => {
    api.projects.list({ includeSystem: showSystem })
      .then(setProjects)
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [showSystem])

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

  return (
    <>
      <div className="section-header">
        <h2>Projects</h2>
        <div className="btn-group">
          <label className="checkbox-label" style={{ marginRight: 8, fontSize: 13, color: 'var(--muted)' }}>
            <input
              type="checkbox"
              checked={showSystem}
              onChange={e => setShowSystem(e.target.checked)}
            />
            Show system projects
          </label>
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

      {projects.length === 0 ? (
        <div className="empty-state">
          <h3>No projects yet</h3>
          <p>Add a project to start managing deployments.</p>
        </div>
      ) : (
        <div className="project-grid">
          {projects.map(p => (
            <Link key={p.id} to={`/projects/${p.id}`} className="project-card">
              <div>
                <h3>{p.display_name || p.id}</h3>
                <div className="meta">
                  {p.id}
                  {p.services?.length ? ` · ${p.services[0].registry_image}:${p.services[0].image_tag || 'latest'}${p.services.length > 1 ? ` +${p.services.length - 1}` : ''}` : ''}
                </div>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                {p.is_system && (
                  <span
                    title="System project managed by bbsit (e.g. cloudflared tunnel)"
                    style={{ fontSize: 12, color: '#0c4a6e', background: '#e0f2fe', border: '1px solid #bae6fd', borderRadius: 4, padding: '2px 6px' }}
                  >
                    system
                  </span>
                )}
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
    </>
  )
}
