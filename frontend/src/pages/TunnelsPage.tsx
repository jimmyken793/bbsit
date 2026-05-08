import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import type { Tunnel, TunnelInput } from '../types'

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
        <div className="project-grid">
          {tunnels.map(t => (
            <div key={t.id} className="project-card" style={{ cursor: 'default' }}>
              <div>
                <h3>{t.name || t.id}</h3>
                <div className="meta">
                  {t.id} · CF: {t.cf_tunnel_id.slice(0, 8)}…
                </div>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                {!t.enabled && (
                  <span style={{ fontSize: 12, color: '#664d03', background: '#fff3cd', border: '1px solid #ffe69c', borderRadius: 4, padding: '2px 6px' }}>
                    disabled
                  </span>
                )}
                <button className="btn btn-outline btn-sm" onClick={() => { setEditing(t); setShowForm(true) }}>
                  Edit
                </button>
                <button className="btn btn-outline btn-sm" onClick={() => handleDelete(t.id)}>
                  Delete
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </>
  )
}

function TunnelForm({ tunnel, onCancel, onSaved }: { tunnel: Tunnel | null; onCancel: () => void; onSaved: () => void }) {
  const isEdit = !!tunnel
  const [form, setForm] = useState<TunnelInput>({
    id: tunnel?.id || '',
    name: tunnel?.name || '',
    enabled: tunnel?.enabled ?? true,
    credentials: '',
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
