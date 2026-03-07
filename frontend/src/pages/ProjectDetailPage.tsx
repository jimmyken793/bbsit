import { useState, useEffect, useCallback } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { api, shortDigest, fmtTime, ApiError } from '../api'
import type { ProjectDetail } from '../types'

function StatusBadge({ status }: { status: string }) {
  return <span className={`badge badge-${status}`}>{status.replace('_', ' ')}</span>
}

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [detail, setDetail] = useState<ProjectDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [actionError, setActionError] = useState('')

  const load = useCallback(() => {
    if (!id) return
    api.projects.get(id)
      .then(d => { setDetail(d); setLoading(false) })
      .catch(() => setLoading(false))
  }, [id])

  useEffect(() => { load() }, [load])

  // Poll while deploying
  useEffect(() => {
    if (detail?.state.status !== 'deploying') return
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [detail?.state.status, load])

  async function action(fn: () => Promise<unknown>, label: string) {
    setActionError('')
    try {
      await fn()
      load()
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : `${label} failed`)
    }
  }

  async function handleDelete() {
    if (!id || !confirm(`Delete project "${id}"? This cannot be undone.`)) return
    try {
      await api.projects.delete(id)
      navigate('/')
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : 'Delete failed')
    }
  }

  if (loading) return <div className="page-loading"><div className="spinner" /></div>
  if (!detail) return <div className="alert alert-danger">Project not found.</div>

  const { project: p, state, deployments } = detail
  const isDeploying = state.status === 'deploying'

  return (
    <>
      <div className="section-header" style={{ marginBottom: 20 }}>
        <div>
          <div style={{ marginBottom: 4 }}>
            <Link to="/" style={{ color: 'var(--muted)', fontSize: 13 }}>← Projects</Link>
          </div>
          <h2 style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            {p.display_name || p.id}
            <StatusBadge status={state.status} />
            {isDeploying && <span className="spinner" />}
          </h2>
          <div className="text-muted" style={{ fontSize: 12 }}>{p.id}</div>
        </div>
        <Link to={`/projects/${p.id}/edit`} className="btn btn-outline btn-sm">Edit</Link>
      </div>

      {actionError && <div className="alert alert-danger">{actionError}</div>}

      <div className="btn-group" style={{ marginBottom: 20 }}>
        <button
          className="btn btn-primary btn-sm"
          onClick={() => action(() => api.projects.deploy(p.id), 'Deploy')}
          disabled={isDeploying}
        >
          {isDeploying ? <><span className="spinner" /> Deploying…</> : '▶ Deploy'}
        </button>
        <button
          className="btn btn-outline btn-sm"
          onClick={() => action(() => api.projects.rollback(p.id), 'Rollback')}
          disabled={isDeploying || !state.previous_digest}
        >
          ↩ Rollback
        </button>
        <button
          className="btn btn-outline btn-sm"
          onClick={() => action(() => api.projects.stop(p.id), 'Stop')}
          disabled={isDeploying}
        >
          ■ Stop
        </button>
        <button
          className="btn btn-outline btn-sm"
          onClick={() => action(() => api.projects.start(p.id), 'Start')}
          disabled={isDeploying}
        >
          ▷ Start
        </button>
        <button className="btn btn-danger btn-sm" onClick={handleDelete}>
          🗑 Delete
        </button>
      </div>

      <div className="detail-grid">
        <div className="card">
          <div className="card-title">State</div>
          <div className="kv-row"><span className="key">Status</span><span className="val"><StatusBadge status={state.status} /></span></div>
          <div className="kv-row"><span className="key">Current</span><span className="val digest">{shortDigest(state.current_digest)}</span></div>
          <div className="kv-row"><span className="key">Desired</span><span className="val digest">{shortDigest(state.desired_digest)}</span></div>
          <div className="kv-row"><span className="key">Previous</span><span className="val digest">{shortDigest(state.previous_digest)}</span></div>
          <div className="kv-row"><span className="key">Last check</span><span className="val">{fmtTime(state.last_check_at)}</span></div>
          <div className="kv-row"><span className="key">Last deploy</span><span className="val">{fmtTime(state.last_deploy_at)}</span></div>
          {state.last_error && (
            <div className="kv-row"><span className="key">Error</span><span className="val" style={{ color: 'var(--danger)' }}>{state.last_error}</span></div>
          )}
        </div>

        <div className="card">
          <div className="card-title">Config</div>
          <div className="kv-row"><span className="key">Mode</span><span className="val">{p.config_mode === 'custom' ? 'Stack config' : 'Form'}</span></div>
          {p.config_mode === 'form' && <>
            <div className="kv-row"><span className="key">Image</span><span className="val">{p.registry_image}:{p.image_tag || 'latest'}</span></div>
            {p.ports?.length ? <div className="kv-row"><span className="key">Ports</span><span className="val">{p.ports.map(pt => `${pt.host_port}:${pt.container_port}`).join(', ')}</span></div> : null}
          </>}
          <div className="kv-row"><span className="key">Stack path</span><span className="val">{p.stack_path}</span></div>
          <div className="kv-row"><span className="key">Health</span><span className="val">{p.health_type}{p.health_target ? ` · ${p.health_target}` : ''}</span></div>
          <div className="kv-row"><span className="key">Poll interval</span><span className="val">{p.poll_interval}s</span></div>
          <div className="kv-row"><span className="key">Enabled</span><span className="val">{p.enabled ? 'Yes' : 'No'}</span></div>
        </div>
      </div>

      <div className="card">
        <div className="card-title">Deployment history</div>
        {deployments.length === 0 ? (
          <p className="text-muted" style={{ fontSize: 13 }}>No deployments yet.</p>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>Started</th>
                <th>Trigger</th>
                <th>From</th>
                <th>To</th>
                <th>Status</th>
                <th>Error</th>
              </tr>
            </thead>
            <tbody>
              {deployments.map(d => (
                <tr key={d.id}>
                  <td>{fmtTime(d.started_at)}</td>
                  <td>{d.trigger}</td>
                  <td className="digest">{shortDigest(d.from_digest)}</td>
                  <td className="digest">{shortDigest(d.to_digest)}</td>
                  <td><span className={`badge badge-${d.status}`}>{d.status}</span></td>
                  <td style={{ color: 'var(--danger)', fontSize: 12 }}>{d.error_message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}
