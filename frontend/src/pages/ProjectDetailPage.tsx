import { useState, useEffect, useCallback, useRef } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { api, shortDigests, hasDigests, fmtTime, ApiError } from '../api'
import { useWebSocket } from '../hooks/useWebSocket'
import type { DeployEvent } from '../hooks/useWebSocket'
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

  // Periodically re-fetch so fields updated by routine polling (e.g. last_check_at)
  // stay fresh — routine polls don't emit WebSocket events.
  useEffect(() => {
    const t = setInterval(load, 30000)
    return () => clearInterval(t)
  }, [load])

  const [logLines, setLogLines] = useState<DeployEvent[]>([])
  const logContainerRef = useRef<HTMLDivElement>(null)

  const projectIds = id ? [id] : []

  const handleEvent = useCallback((event: DeployEvent) => {
    if (event.type === 'state_change' && event.status) {
      setDetail(prev => prev ? {
        ...prev,
        state: { ...prev.state, status: event.status as ProjectDetail['state']['status'] }
      } : prev)
    }
    if (event.type === 'deploy_done' || event.type === 'project_updated') {
      load()
    }
    if (event.type === 'project_deleted') {
      navigate('/')
      return
    }
    if (event.type === 'poll_done') {
      // Refresh state to pick up last_check_at / last_error changes
      load()
      return
    }
    setLogLines(prev => [...prev, event])
  }, [load, navigate])

  useWebSocket(projectIds, handleEvent)

  // Clear log when a new deploy starts
  useEffect(() => {
    if (detail?.state.status === 'deploying') {
      setLogLines([])
    }
  }, [detail?.state.status])

  // Auto-scroll log container only
  useEffect(() => {
    const el = logContainerRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [logLines])

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
  const isBusy = ['deploying', 'stopping', 'starting'].includes(state.status)

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
            {isBusy && <span className="spinner" />}
          </h2>
          <div className="text-muted" style={{ fontSize: 12 }}>{p.id}</div>
        </div>
        {!p.is_system && (
          <Link to={`/projects/${p.id}/edit`} className="btn btn-outline btn-sm">Edit</Link>
        )}
      </div>

      {p.is_system && (
        <div className="alert alert-info">
          This is a <strong>system project</strong> managed by bbsit (cloudflared tunnel runner).
          Configuration is regenerated automatically from tunnel and project settings — edit the
          tunnel or its routed projects instead.
        </div>
      )}

      {actionError && <div className="alert alert-danger">{actionError}</div>}

      {!p.enabled && (
        <div className="alert alert-warning">
          Auto-update polling is <strong>off</strong> for this project — the scheduler won't check for
          new image versions.{' '}
          <Link to={`/projects/${p.id}/edit`}>Edit</Link> to turn it back on. Manual Deploy still works.
        </div>
      )}

      <div className="btn-group" style={{ marginBottom: 20 }}>
        <button
          className="btn btn-primary btn-sm"
          onClick={() => action(() => api.projects.deploy(p.id), 'Deploy')}
          disabled={isBusy}
        >
          {isBusy ? <><span className="spinner" /> {state.status === 'stopping' ? 'Stopping…' : state.status === 'starting' ? 'Starting…' : 'Deploying…'}</> : '▶ Deploy'}
        </button>
        <button
          className="btn btn-outline btn-sm"
          onClick={() => action(() => api.projects.rollback(p.id), 'Rollback')}
          disabled={isBusy || !hasDigests(state.previous_digests)}
        >
          ↩ Rollback
        </button>
        <button
          className="btn btn-outline btn-sm"
          onClick={() => action(() => api.projects.stop(p.id), 'Stop')}
          disabled={isBusy}
        >
          ■ Stop
        </button>
        <button
          className="btn btn-outline btn-sm"
          onClick={() => action(() => api.projects.start(p.id), 'Start')}
          disabled={isBusy}
        >
          ▷ Start
        </button>
        <a
          className="btn btn-outline btn-sm"
          href={`/api/projects/${p.id}/export?format=tar.gz`}
          title="Download project bundle (project.yaml + bind-mount data dirs)"
        >
          ↓ Export bundle
        </a>
        <a
          className="btn btn-outline btn-sm"
          href={`/api/projects/${p.id}/export?format=yaml`}
          title="Download project config as YAML (no data dirs)"
        >
          ↓ Export YAML
        </a>
        {!p.is_system && (
          <button className="btn btn-danger btn-sm" onClick={handleDelete}>
            🗑 Delete
          </button>
        )}
      </div>

      {logLines.length > 0 && (
        <div className="card" style={{ marginBottom: 20 }}>
          <div className="card-title">Deploy log</div>
          <div className="deploy-log" ref={logContainerRef}>
            {logLines.map((line, i) => (
              <div key={i} className={`log-line ${line.type}${line.error ? ' log-error' : ''}`}>
                <span className="log-time">{new Date(line.timestamp).toLocaleTimeString()}</span>
                {line.type === 'step_start' && <span className="log-step">▶ {line.step}</span>}
                {line.type === 'step_done' && <span className="log-step">{line.error ? '✗' : '✓'} {line.step}</span>}
                {line.type === 'log' && <span className="log-msg">{line.message}</span>}
                {line.type === 'state_change' && <span className="log-status">→ {line.status}</span>}
                {line.type === 'deploy_done' && <span className="log-status">{line.error ? '✗ Failed' : '✓ Done'}: {line.status}</span>}
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="detail-grid">
        <div className="card">
          <div className="card-title">State</div>
          <div className="kv-row"><span className="key">Status</span><span className="val"><StatusBadge status={state.status} /></span></div>
          <div className="kv-row"><span className="key">Current</span><span className="val digest">{shortDigests(state.current_digests)}</span></div>
          <div className="kv-row"><span className="key">Desired</span><span className="val digest">{shortDigests(state.desired_digests)}</span></div>
          <div className="kv-row"><span className="key">Previous</span><span className="val digest">{shortDigests(state.previous_digests)}</span></div>
          <div className="kv-row"><span className="key">Last check</span><span className="val">{fmtTime(state.last_check_at)}</span></div>
          <div className="kv-row"><span className="key">Last deploy</span><span className="val">{fmtTime(state.last_deploy_at)}</span></div>
          {state.last_error && (
            <div className="kv-row"><span className="key">Error</span><span className="val" style={{ color: 'var(--danger)' }}>{state.last_error}</span></div>
          )}
        </div>

        <div className="card">
          <div className="card-title">Config</div>
          {p.services?.map(svc => (
            <div key={svc.name} className="kv-row">
              <span className="key">{svc.name}</span>
              <span className="val">
                {svc.registry_image}:{svc.image_tag || 'latest'}
                {svc.polled ? ' (polled)' : ''}
                {svc.ports?.length ? ` · ${svc.ports.map(pt => `${pt.host_port}:${pt.container_port}`).join(', ')}` : ''}
              </span>
            </div>
          ))}
          <div className="kv-row"><span className="key">Stack path</span><span className="val">{p.stack_path}</span></div>
          <div className="kv-row"><span className="key">Health</span><span className="val">{p.health_type}{p.health_target ? ` · ${p.health_target}` : ''}</span></div>
          <div className="kv-row"><span className="key">Poll interval</span><span className="val">{p.poll_interval}s</span></div>
          <div className="kv-row"><span className="key">Auto-update</span><span className="val">{p.enabled ? 'On' : 'Off'}</span></div>
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
                  <td className="digest">{shortDigests(d.from_digests)}</td>
                  <td className="digest">{shortDigests(d.to_digests)}</td>
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
