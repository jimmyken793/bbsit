import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { shortDigest, fmtTime, ApiError, api } from './api'

describe('shortDigest', () => {
  it('returns dash for empty string', () => {
    expect(shortDigest('')).toBe('—')
  })

  it('truncates long digests to 19 chars', () => {
    const digest = 'sha256:abc123def456789xyz'
    expect(shortDigest(digest)).toBe('sha256:abc123def456')
    expect(shortDigest(digest)).toHaveLength(19)
  })

  it('returns short digest as-is', () => {
    expect(shortDigest('sha256:abc')).toBe('sha256:abc')
  })
})

describe('fmtTime', () => {
  it('returns dash for null/undefined', () => {
    expect(fmtTime(null)).toBe('—')
    expect(fmtTime(undefined)).toBe('—')
    expect(fmtTime('')).toBe('—')
  })

  it('formats ISO date string', () => {
    const result = fmtTime('2025-01-15T10:30:00Z')
    expect(result).toBeTruthy()
    expect(result).not.toBe('—')
  })
})

describe('ApiError', () => {
  it('has status and message', () => {
    const err = new ApiError(404, 'not found')
    expect(err.status).toBe(404)
    expect(err.message).toBe('not found')
    expect(err).toBeInstanceOf(Error)
  })
})

describe('api', () => {
  const mockFetch = vi.fn()

  beforeEach(() => {
    vi.stubGlobal('fetch', mockFetch)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  function mockResponse(body: unknown, status = 200) {
    mockFetch.mockResolvedValueOnce({
      ok: status >= 200 && status < 300,
      status,
      text: () => Promise.resolve(JSON.stringify(body)),
    })
  }

  it('api.auth.status fetches auth status', async () => {
    mockResponse({ setup_required: false, logged_in: true })
    const result = await api.auth.status()
    expect(result).toEqual({ setup_required: false, logged_in: true })
    expect(mockFetch).toHaveBeenCalledWith('/api/auth/status', { credentials: 'same-origin' })
  })

  it('api.auth.login sends password', async () => {
    mockResponse({})
    await api.auth.login('mypassword')
    expect(mockFetch).toHaveBeenCalledWith('/api/auth/login', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ password: 'mypassword' }),
    }))
  })

  it('api.projects.list fetches projects', async () => {
    const projects = [{ id: 'test', display_name: 'Test' }]
    mockResponse(projects)
    const result = await api.projects.list()
    expect(result).toEqual(projects)
  })

  it('throws ApiError on non-ok response', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 400,
      text: () => Promise.resolve('bad request'),
    })
    await expect(api.projects.list()).rejects.toThrow(ApiError)
    await mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      text: () => Promise.resolve(''),
    })
    await expect(api.projects.list()).rejects.toThrow('HTTP 500')
  })

  it('redirects on 401', async () => {
    const originalLocation = window.location.href
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { href: originalLocation },
    })
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 401,
      text: () => Promise.resolve('unauthorized'),
    })
    await api.projects.list()
    expect(window.location.href).toBe('/')
  })
})
