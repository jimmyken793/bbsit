import { useState, useEffect } from 'react'
import type { FormEvent } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import { formToYaml, yamlToForm } from '../stackYaml'
import type { Project, ServiceConfig, PortMapping, VolumeMount, HealthType } from '../types'

const emptyService: ServiceConfig = {
  name: '',
  registry_image: '',
  image_tag: 'latest',
  polled: true,
}

const defaultProject: Partial<Project> = {
  config_mode: 'form',
  health_type: 'none',
  poll_interval: 300,
  enabled: true,
  services: [],
  env_vars: {},
}

export default function ProjectFormPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const isEdit = !!id

  const [form, setForm] = useState<Partial<Project>>(defaultProject)
  const [services, setServices] = useState<ServiceConfig[]>([])
  const [envPairs, setEnvPairs] = useState<[string, string][]>([])
  const [viewMode, setViewMode] = useState<'form' | 'yaml'>('form')
  const [yamlText, setYamlText] = useState('')
  const [yamlError, setYamlError] = useState('')
  const [loading, setLoading] = useState(isEdit)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!isEdit) return
    api.projects.get(id!).then(d => {
      const p = d.project
      setForm({ ...p, config_mode: 'form' })
      // Populate services from project
      if (p.services && p.services.length > 0) {
        setServices(p.services)
        setEnvPairs(Object.entries(p.env_vars || {}))
      } else if (p.config_mode === 'custom' && p.custom_compose) {
        // Auto-convert custom_compose to form state
        try {
          const result = yamlToForm(p.custom_compose)
          setServices(result.services.map((s, i) => ({
            ...s,
            name: s.name || (i === 0 ? p.id : `service-${i + 1}`),
          })))
          setEnvPairs(result.envPairs)
        } catch {
          setServices([])
          setEnvPairs(Object.entries(p.env_vars || {}))
        }
      } else if (p.registry_image) {
        setServices([{
          name: p.id,
          registry_image: p.registry_image,
          image_tag: p.image_tag || 'latest',
          polled: true,
          ports: p.ports,
          volumes: p.volumes,
          extra_options: p.extra_options,
        }])
        setEnvPairs(Object.entries(p.env_vars || {}))
      } else {
        setEnvPairs(Object.entries(p.env_vars || {}))
      }
      setLoading(false)
    }).catch(() => setLoading(false))
  }, [id, isEdit])

  function set<K extends keyof Project>(key: K, val: Project[K]) {
    setForm(f => ({ ...f, [key]: val }))
  }

  function updateService(i: number, updates: Partial<ServiceConfig>) {
    setServices(svcs => svcs.map((s, j) => j === i ? { ...s, ...updates } : s))
  }

  function updateServicePort(svcIdx: number, portIdx: number, updates: Partial<PortMapping>) {
    setServices(svcs => svcs.map((s, j) => {
      if (j !== svcIdx) return s
      const ports = [...(s.ports || [])]
      ports[portIdx] = { ...ports[portIdx], ...updates }
      return { ...s, ports }
    }))
  }

  function updateServiceVolume(svcIdx: number, volIdx: number, updates: Partial<VolumeMount>) {
    setServices(svcs => svcs.map((s, j) => {
      if (j !== svcIdx) return s
      const volumes = [...(s.volumes || [])]
      volumes[volIdx] = { ...volumes[volIdx], ...updates }
      return { ...s, volumes }
    }))
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')

    // If on YAML view, parse YAML into form state before saving
    let finalServices = services
    let finalEnvPairs = envPairs
    if (viewMode === 'yaml') {
      try {
        const result = yamlToForm(yamlText)
        finalServices = result.services
        finalEnvPairs = result.envPairs
        setServices(finalServices)
        setEnvPairs(finalEnvPairs)
        setYamlError('')
      } catch (e) {
        setYamlError(e instanceof Error ? e.message : 'Invalid YAML')
        return
      }
    }

    const payload: Partial<Project> = {
      ...form,
      config_mode: 'form',
      custom_compose: '',
      services: finalServices,
      env_vars: Object.fromEntries(finalEnvPairs.filter(([k]) => k.trim())),
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
            &larr; {isEdit ? 'Back to project' : 'Projects'}
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
            <label>Edit mode</label>
            <div className="mode-tabs">
              <button
                type="button"
                className={`mode-tab ${viewMode === 'form' ? 'active' : ''}`}
                onClick={() => {
                  if (viewMode === 'yaml') {
                    try {
                      const result = yamlToForm(yamlText)
                      setServices(result.services)
                      setEnvPairs(result.envPairs)
                      setYamlError('')
                      setViewMode('form')
                    } catch (e) {
                      setYamlError(e instanceof Error ? e.message : 'Invalid YAML')
                    }
                  }
                }}
              >
                Form
              </button>
              <button
                type="button"
                className={`mode-tab ${viewMode === 'yaml' ? 'active' : ''}`}
                onClick={() => {
                  setYamlText(formToYaml(services, envPairs))
                  setYamlError('')
                  setViewMode('yaml')
                }}
              >
                YAML
              </button>
            </div>
          </div>

          {viewMode === 'form' ? (
            <>
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
                <h3>Services</h3>
                {services.map((svc, si) => (
                  <div key={si} className="card" style={{ marginBottom: 12, padding: 12, background: 'var(--bg)' }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                      <strong>Service {si + 1}</strong>
                      {services.length > 1 && (
                        <button type="button" className="btn btn-outline btn-sm" onClick={() => setServices(services.filter((_, j) => j !== si))}>
                          Remove
                        </button>
                      )}
                    </div>

                    <div className="form-group">
                      <label>Service name</label>
                      <input
                        className="form-control"
                        value={svc.name}
                        onChange={e => updateService(si, { name: e.target.value })}
                        placeholder="app"
                        required
                      />
                      <div className="form-hint">Used as the compose service name.</div>
                    </div>

                    <div className="form-group">
                      <label>Registry image</label>
                      <input
                        className="form-control"
                        value={svc.registry_image}
                        onChange={e => updateService(si, { registry_image: e.target.value })}
                        placeholder="registry.example.com/my-app"
                      />
                    </div>

                    <div style={{ display: 'flex', gap: 8 }}>
                      <div className="form-group" style={{ flex: 1 }}>
                        <label>Image tag</label>
                        <input
                          className="form-control"
                          value={svc.image_tag}
                          onChange={e => updateService(si, { image_tag: e.target.value })}
                          placeholder="latest"
                        />
                      </div>
                      <div className="form-group">
                        <label className="checkbox-label" style={{ marginTop: 24 }}>
                          <input
                            type="checkbox"
                            checked={svc.polled}
                            onChange={e => updateService(si, { polled: e.target.checked })}
                          />
                          Poll for updates
                        </label>
                      </div>
                    </div>

                    <div className="form-section" style={{ marginTop: 8 }}>
                      <label style={{ fontWeight: 600, fontSize: 13 }}>Ports</label>
                      {(svc.ports || []).map((pt, pi) => (
                        <div key={pi} className="list-row">
                          <input type="number" className="form-control" placeholder="Host" value={pt.host_port || ''} onChange={e => updateServicePort(si, pi, { host_port: +e.target.value })} />
                          <span>:</span>
                          <input type="number" className="form-control" placeholder="Container" value={pt.container_port || ''} onChange={e => updateServicePort(si, pi, { container_port: +e.target.value })} />
                          <select className="form-control" style={{ maxWidth: 80 }} value={pt.protocol || 'tcp'} onChange={e => updateServicePort(si, pi, { protocol: e.target.value })}>
                            <option value="tcp">TCP</option>
                            <option value="udp">UDP</option>
                          </select>
                          <button type="button" className="btn btn-outline btn-sm" onClick={() => updateService(si, { ports: (svc.ports || []).filter((_, j) => j !== pi) })}>✕</button>
                        </div>
                      ))}
                      <button type="button" className="btn btn-outline btn-sm" onClick={() => updateService(si, { ports: [...(svc.ports || []), { host_port: 0, container_port: 0 }] })}>
                        + Add port
                      </button>
                    </div>

                    <div className="form-section" style={{ marginTop: 8 }}>
                      <label style={{ fontWeight: 600, fontSize: 13 }}>Volumes</label>
                      {(svc.volumes || []).map((v, vi) => (
                        <div key={vi} className="list-row">
                          <input className="form-control" placeholder="Host path" value={v.host_path} onChange={e => updateServiceVolume(si, vi, { host_path: e.target.value })} />
                          <span>:</span>
                          <input className="form-control" placeholder="Container path" value={v.container_path} onChange={e => updateServiceVolume(si, vi, { container_path: e.target.value })} />
                          <label style={{ whiteSpace: 'nowrap', display: 'flex', alignItems: 'center', gap: 4, fontSize: 12 }}>
                            <input type="checkbox" checked={v.readonly || false} onChange={e => updateServiceVolume(si, vi, { readonly: e.target.checked })} />
                            ro
                          </label>
                          <button type="button" className="btn btn-outline btn-sm" onClick={() => updateService(si, { volumes: (svc.volumes || []).filter((_, j) => j !== vi) })}>✕</button>
                        </div>
                      ))}
                      <button type="button" className="btn btn-outline btn-sm" onClick={() => updateService(si, { volumes: [...(svc.volumes || []), { host_path: '', container_path: '' }] })}>
                        + Add volume
                      </button>
                    </div>

                    <div className="form-section" style={{ marginTop: 8 }}>
                      <label style={{ fontWeight: 600, fontSize: 13 }}>Extra options</label>
                      <textarea
                        className="form-control"
                        rows={3}
                        value={svc.extra_options || ''}
                        onChange={e => updateService(si, { extra_options: e.target.value })}
                        placeholder={'# Raw YAML merged into this service\ndeploy:\n  restart_policy:\n    condition: on-failure'}
                      />
                    </div>
                  </div>
                ))}
                <button type="button" className="btn btn-outline btn-sm" onClick={() => setServices([...services, { ...emptyService }])}>
                  + Add service
                </button>
              </div>

              <div className="form-section">
                <h3>Environment variables</h3>
                <div className="form-hint" style={{ marginBottom: 8 }}>Shared across all services via .env file.</div>
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
            </>
          ) : (
            <>
              {yamlError && <div className="alert alert-danger">{yamlError}</div>}
              <div className="form-group">
                <label>Stack config (YAML)</label>
                <textarea
                  className="form-control"
                  rows={16}
                  value={yamlText}
                  onChange={e => { setYamlText(e.target.value); setYamlError('') }}
                  placeholder={'services:\n  - name: app\n    registry_image: registry.example.com/my-app\n    image_tag: latest\n    polled: true\n    ports:\n      - host_port: 8080\n        container_port: 80\n\nenv_vars:\n  KEY: value'}
                />
                <div className="form-hint">Edit as YAML. Switch to Form to see structured fields.</div>
              </div>
            </>
          )}
        </div>

        <div className="card" style={{ marginTop: 16 }}>
          <div className="card-title">Health &amp; scheduling</div>

          <div className="form-group">
            <label>Health check (stack-level)</label>
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
            <div className="form-hint">How often to check for new image digests.</div>
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
            {saving ? <><span className="spinner" /> Saving&hellip;</> : (isEdit ? 'Save changes' : 'Create project')}
          </button>
          <Link to={isEdit ? `/projects/${id}` : '/'} className="btn btn-outline">Cancel</Link>
        </div>
      </form>
    </>
  )
}
