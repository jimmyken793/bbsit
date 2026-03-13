import type { AuthStatus, Project, ProjectWithState, ProjectDetail } from './types'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: 'same-origin', ...init })
  if (res.status === 401) {
    window.location.href = '/'
    return undefined as T
  }
  if (!res.ok) {
    const text = await res.text()
    throw new ApiError(res.status, text.trim() || `HTTP ${res.status}`)
  }
  const text = await res.text()
  return (text ? JSON.parse(text) : {}) as T
}

export const api = {
  auth: {
    status: () => apiFetch<AuthStatus>('/api/auth/status'),
    setup: (password: string) =>
      apiFetch<void>('/api/auth/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      }),
    login: (password: string) =>
      apiFetch<void>('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      }),
    logout: () => apiFetch<void>('/api/auth/logout', { method: 'POST' }),
  },
  projects: {
    list: () => apiFetch<ProjectWithState[]>('/api/projects'),
    get: (id: string) => apiFetch<ProjectDetail>(`/api/projects/${id}`),
    create: (p: Partial<Project>) =>
      apiFetch<Project>('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(p),
      }),
    update: (id: string, p: Partial<Project>) =>
      apiFetch<Project>(`/api/projects/${id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(p),
      }),
    delete: (id: string) => apiFetch<void>(`/api/projects/${id}`, { method: 'DELETE' }),
    deploy: (id: string) => apiFetch<void>(`/api/projects/${id}/deploy`, { method: 'POST' }),
    rollback: (id: string) => apiFetch<void>(`/api/projects/${id}/rollback`, { method: 'POST' }),
    stop: (id: string) => apiFetch<void>(`/api/projects/${id}/stop`, { method: 'POST' }),
    start: (id: string) => apiFetch<void>(`/api/projects/${id}/start`, { method: 'POST' }),
    import: (file: File) => {
      const fd = new FormData()
      fd.append('file', file)
      return apiFetch<Project>('/api/projects/import', { method: 'POST', body: fd })
    },
  },
}

export function shortDigest(d: string): string {
  if (!d) return '—'
  // sha256:abc123... → sha256:abc123 (first 19 chars)
  return d.length > 19 ? d.slice(0, 19) : d
}

export function shortDigests(m: Record<string, string> | undefined | null): string {
  if (!m || Object.keys(m).length === 0) return '—'
  return Object.entries(m)
    .map(([svc, d]) => `${svc}: ${shortDigest(d)}`)
    .join(', ')
}

export function hasDigests(m: Record<string, string> | undefined | null): boolean {
  if (!m) return false
  return Object.values(m).some(v => v !== '')
}

export function fmtTime(t?: string | null): string {
  if (!t) return '—'
  return new Date(t).toLocaleString()
}
