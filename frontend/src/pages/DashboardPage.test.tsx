import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import DashboardPage from './DashboardPage'
import { api } from '../api'
import type { ProjectWithState } from '../types'

vi.mock('../api', async () => {
  const actual = await vi.importActual('../api')
  return {
    ...actual,
    api: {
      projects: {
        list: vi.fn(),
        import: vi.fn(),
      },
    },
  }
})

function renderDashboard() {
  return render(
    <MemoryRouter>
      <DashboardPage />
    </MemoryRouter>
  )
}

const mockProject: ProjectWithState = {
  id: 'my-app',
  display_name: 'My App',
  config_mode: 'form',
  services: [{
    name: 'my-app',
    registry_image: 'registry.example.com/my-app',
    image_tag: 'latest',
    polled: true,
  }],
  registry_image: 'registry.example.com/my-app',
  image_tag: 'latest',
  stack_path: '/opt/stacks/my-app',
  health_type: 'none',
  health_target: '',
  poll_interval: 300,
  enabled: true,
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
  state: {
    project_id: 'my-app',
    current_digests: {},
    previous_digests: {},
    desired_digests: {},
    status: 'running',
    last_error: '',
  },
}

describe('DashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows empty state when no projects', async () => {
    vi.mocked(api.projects.list).mockResolvedValueOnce([])
    renderDashboard()

    expect(await screen.findByText('No projects yet')).toBeInTheDocument()
  })

  it('renders project list', async () => {
    vi.mocked(api.projects.list).mockResolvedValueOnce([mockProject])
    renderDashboard()

    expect(await screen.findByText('My App')).toBeInTheDocument()
    expect(screen.getByText('running')).toBeInTheDocument()
  })

  it('shows project ID and image info', async () => {
    vi.mocked(api.projects.list).mockResolvedValueOnce([mockProject])
    renderDashboard()

    expect(await screen.findByText(/my-app/)).toBeInTheDocument()
    expect(screen.getByText(/registry\.example\.com\/my-app:latest/)).toBeInTheDocument()
  })

  it('shows disabled label for disabled projects', async () => {
    const disabled = { ...mockProject, enabled: false }
    vi.mocked(api.projects.list).mockResolvedValueOnce([disabled])
    renderDashboard()

    expect(await screen.findByText('disabled')).toBeInTheDocument()
  })
})
