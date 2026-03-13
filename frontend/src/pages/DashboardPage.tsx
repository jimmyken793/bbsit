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
  const [importError, setImportError] = useState('')
  const [importOk, setImportOk] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  function load() {
    api.projects.list()
      .then(setProjects)
      .catch(() => {})
      .finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [])

  const projectIds = projects.map(p => p.id)

  const handleEvent = useCallback((event: DeployEvent) => {
    if (event.type === 'state_change' && event.status) {
      setProjects(prev => prev.map(p =>
        p.id === event.project_id
          ? { ...p, state: { ...p.state, status: event.status as ProjectWithState['state']['status'] } }
          : p
      ))
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
          <label className="yaml-import-label" title="Import project from YAML file">
            ↑ Import YAML
            <input
              ref={fileRef}
              type="file"
              accept=".yaml,.yml"
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
                <StatusBadge status={p.state?.status || 'unknown'} />
                {!p.enabled && <span className="text-muted" style={{ fontSize: 12 }}>disabled</span>}
              </div>
            </Link>
          ))}
        </div>
      )}
    </>
  )
}
