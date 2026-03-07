import { useState, useEffect } from 'react'
import type { FormEvent } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import type { Project, PortMapping, VolumeMount, ConfigMode, HealthType } from '../types'

const defaultProject: Partial<Project> = {
  config_mode: 'form',
  image_tag: 'latest',
  health_type: 'none',
  poll_interval: 300,
  enabled: true,
  ports: [],
  volumes: [],
  env_vars: {},
}

export default function ProjectFormPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const isEdit = !!id

  const [form, setForm] = useState<Partial<Project>>(defaultProject)
  const [ports, setPorts] = useState<PortMapping[]>([])
  const [volumes, setVolumes] = useState<VolumeMount[]>([])
  const [envPairs, setEnvPairs] = useState<[string, string][]>([])
  const [loading, setLoading] = useState(isEdit)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!isEdit) return
    api.projects.get(id!).then(d => {
      const p = d.project
      setForm(p)
      setPorts(p.ports || [])
      setVolumes(p.volumes || [])
      setEnvPairs(Object.entries(p.env_vars || {}))
      setLoading(false)
    }).catch(() => setLoading(false))
  }, [id, isEdit])

  function set<K extends keyof Project>(key: K, val: Project[K]) {
    setForm(f => ({ ...f, [key]: val }))
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')

    const payload: Partial<Project> = {
      ...form,
      ports,
      volumes,
      env_vars: Object.fromEntries(envPairs.filter(([k]) => k.trim())),
    }

    setSaving(true)
    try {
      if (isEdit) {
        await api.projects.update(id!, payload)
      } else {
        await api.projects.create(payload)
      }
      navigate(isEdit ? `/projects/${id}` : `/projects/${payload.id}`)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <div className="page-loading"><div className="spinner" /></div>

  return (
    <>
      <div style={{ marginBottom: 20 }}>
        <div style={{ marginBottom: 4 }}>
          <Link to={isEdit ? `/projects/${id}` : '/'} style={{ color: 'var(--muted)', fontSize: 13 }}>
            ← {isEdit ? 'Back to project' : 'Projects'}
          </Link>
        </div>
        <h2>{isEdit ? `Edit ${form.display_name || id}` : 'New project'}</h2>
      </div>

      {error && <div className="alert alert-danger">{error}</div>}

      <form onSubmit={handleSubmit}>
        <div className="card">
          <div className="form-group">
            <label>Project ID</label>
            <input
              className="form-control"
              value={form.id || ''}
              onChange={e => set('id', e.target.value)}
              disabled={isEdit}
              placeholder="my-app"
              required
            />
            <div className="form-hint">Lowercase letters, numbers, hyphens only. Cannot be changed after creation.</div>
          </div>

          <div className="form-group">
            <label>Display name</label>
            <input
              className="form-control"
              value={form.display_name || ''}
              onChange={e => set('display_name', e.target.value)}
              placeholder="My App"
            />
          </div>

          <div className="form-group">
            <label>Config mode</label>
            <div className="mode-tabs">
              <button
                type="button"
                className={`mode-tab ${form.config_mode === 'form' ? 'active' : ''}`}
                onClick={() => set('config_mode', 'form' as ConfigMode)}
              >
                Form
              </button>
              <button
                type="button"
                className={`mode-tab ${form.config_mode === 'custom' ? 'active' : ''}`}
                onClick={() => set('config_mode', 'custom' as ConfigMode)}
              >
                Stack config
              </button>
            </div>
          </div>

          {form.config_mode === 'form' ? (
            <>
              <div className="form-group">
                <label>Registry image</label>
                <input
                  className="form-control"
                  value={form.registry_image || ''}
                  onChange={e => set('registry_image', e.target.value)}
                  placeholder="registry.example.com/my-app"
                />
              </div>
              <div className="form-group">
                <label>Image tag</label>
                <input
                  className="form-control"
                  value={form.image_tag || ''}
                  onChange={e => set('image_tag', e.target.value)}
                  placeholder="latest"
                />
              </div>

              <div className="form-group">
                <label>Port bind address</label>
                <select
                  className="form-control"
                  value={form.bind_host || '127.0.0.1'}
                  onChange={e => set('bind_host', e.target.value)}
                  style={{ maxWidth: 200 }}
                >
                  <option value="127.0.0.1">127.0.0.1 (localhost only)</option>
                  <option value="0.0.0.0">0.0.0.0 (all interfaces)</option>
                </select>
                <div className="form-hint">Which host IP to bind ports to. Use 0.0.0.0 to expose ports externally.</div>
              </div>

              <div className="form-section">
                <h3>Ports</h3>
                {ports.map((pt, i) => (
                  <div key={i} className="list-row">
                    <input
                      type="number"
                      className="form-control"
                      placeholder="Host port"
                      value={pt.host_port || ''}
                      onChange={e => {
                        const next = [...ports]
                        next[i] = { ...next[i], host_port: +e.target.value }
                        setPorts(next)
                      }}
                    />
                    <span>:</span>
                    <input
                      type="number"
                      className="form-control"
                      placeholder="Container port"
                      value={pt.container_port || ''}
                      onChange={e => {
                        const next = [...ports]
                        next[i] = { ...next[i], container_port: +e.target.value }
                        setPorts(next)
                      }}
                    />
                    <select
                      className="form-control"
                      style={{ maxWidth: 80 }}
                      value={pt.protocol || 'tcp'}
                      onChange={e => {
                        const next = [...ports]
                        next[i] = { ...next[i], protocol: e.target.value }
                        setPorts(next)
                      }}
                    >
                      <option value="tcp">TCP</option>
                      <option value="udp">UDP</option>
                    </select>
                    <button type="button" className="btn btn-outline btn-sm" onClick={() => setPorts(ports.filter((_, j) => j !== i))}>✕</button>
                  </div>
                ))}
                <button type="button" className="btn btn-outline btn-sm" onClick={() => setPorts([...ports, { host_port: 0, container_port: 0 }])}>
                  + Add port
                </button>
              </div>

              <div className="form-section">
                <h3>Volumes</h3>
                {volumes.map((v, i) => (
                  <div key={i} className="list-row">
                    <input
                      className="form-control"
                      placeholder="Host path"
                      value={v.host_path}
                      onChange={e => {
                        const next = [...volumes]
                        next[i] = { ...next[i], host_path: e.target.value }
                        setVolumes(next)
                      }}
                    />
                    <span>:</span>
                    <input
                      className="form-control"
                      placeholder="Container path"
                      value={v.container_path}
                      onChange={e => {
                        const next = [...volumes]
                        next[i] = { ...next[i], container_path: e.target.value }
                        setVolumes(next)
                      }}
                    />
                    <label style={{ whiteSpace: 'nowrap', display: 'flex', alignItems: 'center', gap: 4, fontSize: 12 }}>
                      <input
                        type="checkbox"
                        checked={v.readonly || false}
                        onChange={e => {
                          const next = [...volumes]
                          next[i] = { ...next[i], readonly: e.target.checked }
                          setVolumes(next)
                        }}
                      />
                      ro
                    </label>
                    <button type="button" className="btn btn-outline btn-sm" onClick={() => setVolumes(volumes.filter((_, j) => j !== i))}>✕</button>
                  </div>
                ))}
                <button type="button" className="btn btn-outline btn-sm" onClick={() => setVolumes([...volumes, { host_path: '', container_path: '' }])}>
                  + Add volume
                </button>
              </div>

              <div className="form-section">
                <h3>Environment variables</h3>
                {envPairs.map(([k, v], i) => (
                  <div key={i} className="list-row">
                    <input
                      className="form-control"
                      placeholder="KEY"
                      value={k}
                      onChange={e => {
                        const next = [...envPairs] as [string, string][]
                        next[i] = [e.target.value, next[i][1]]
                        setEnvPairs(next)
                      }}
                    />
                    <span>=</span>
                    <input
                      className="form-control"
                      placeholder="value"
                      value={v}
                      onChange={e => {
                        const next = [...envPairs] as [string, string][]
                        next[i] = [next[i][0], e.target.value]
                        setEnvPairs(next)
                      }}
                    />
                    <button type="button" className="btn btn-outline btn-sm" onClick={() => setEnvPairs(envPairs.filter((_, j) => j !== i))}>✕</button>
                  </div>
                ))}
                <button type="button" className="btn btn-outline btn-sm" onClick={() => setEnvPairs([...envPairs, ['', '']])}>
                  + Add variable
                </button>
              </div>

              <div className="form-section">
                <h3>Extra options</h3>
                <textarea
                  className="form-control"
                  rows={4}
                  value={form.extra_options || ''}
                  onChange={e => set('extra_options', e.target.value)}
                  placeholder={'# Raw YAML merged into the service definition\ndeploy:\n  restart_policy:\n    condition: on-failure'}
                />
                <div className="form-hint">Raw YAML fragment merged into the compose service block.</div>
              </div>
            </>
          ) : (
            <>
              <div className="form-group">
                <label>Stack path</label>
                <input
                  className="form-control"
                  value={form.stack_path || ''}
                  onChange={e => set('stack_path', e.target.value)}
                  placeholder="/opt/stacks/my-app"
                />
                <div className="form-hint">Directory where the generated compose.yaml will be placed.</div>
              </div>
              <div className="form-group">
                <label>Stack config</label>
                <textarea
                  className="form-control"
                  rows={16}
                  value={form.custom_compose || ''}
                  onChange={e => set('custom_compose', e.target.value)}
                  placeholder={'registry_image: registry.example.com/my-app\nimage_tag: latest\n\nports:\n  - host_port: 8080\n    container_port: 80\n\nvolumes:\n  - host_path: ./data\n    container_path: /app/data\n\nenv_vars:\n  KEY: value'}
                  required
                />
              </div>
            </>
          )}
        </div>

        <div className="card" style={{ marginTop: 16 }}>
          <div className="card-title">Health &amp; scheduling</div>

          <div className="form-group">
            <label>Health check</label>
            <select
              className="form-control"
              value={form.health_type || 'none'}
              onChange={e => set('health_type', e.target.value as HealthType)}
              style={{ maxWidth: 200 }}
            >
              <option value="none">None</option>
              <option value="http">HTTP</option>
              <option value="tcp">TCP</option>
            </select>
          </div>

          {form.health_type !== 'none' && (
            <div className="form-group">
              <label>Health target</label>
              <input
                className="form-control"
                value={form.health_target || ''}
                onChange={e => set('health_target', e.target.value)}
                placeholder={form.health_type === 'http' ? 'http://127.0.0.1:8080/healthz' : '127.0.0.1:8080'}
              />
            </div>
          )}

          <div className="form-group">
            <label>Poll interval (seconds)</label>
            <input
              type="number"
              className="form-control"
              value={form.poll_interval || 300}
              onChange={e => set('poll_interval', +e.target.value)}
              min={10}
              style={{ maxWidth: 140 }}
            />
            <div className="form-hint">How often to check for a new image digest.</div>
          </div>

          <div className="form-group">
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={form.enabled ?? true}
                onChange={e => set('enabled', e.target.checked)}
              />
              Enabled
            </label>
            <div className="form-hint">Disabled projects are skipped during polling.</div>
          </div>
        </div>

        <div style={{ marginTop: 16, display: 'flex', gap: 8 }}>
          <button type="submit" className="btn btn-primary" disabled={saving}>
            {saving ? <><span className="spinner" /> Saving…</> : (isEdit ? 'Save changes' : 'Create project')}
          </button>
          <Link to={isEdit ? `/projects/${id}` : '/'} className="btn btn-outline">Cancel</Link>
        </div>
      </form>
    </>
  )
}
