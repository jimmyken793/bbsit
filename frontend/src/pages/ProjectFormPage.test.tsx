import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import ProjectFormPage from './ProjectFormPage'
import { api } from '../api'

vi.mock('../api', async () => {
  const actual = await vi.importActual('../api')
  return {
    ...actual,
    api: {
      projects: {
        get: vi.fn(),
        create: vi.fn(),
        update: vi.fn(),
      },
      tunnels: {
        list: vi.fn().mockResolvedValue([]),
      },
    },
  }
})

describe('ProjectFormPage environment variables', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.tunnels.list).mockResolvedValue([])
  })

  it('shows the key and masks only the value', async () => {
    const user = userEvent.setup()
    vi.mocked(api.projects.update).mockResolvedValue({} as never)
    vi.mocked(api.projects.get).mockResolvedValue({
      project: {
        id: 'demo',
        display_name: 'Demo',
        config_mode: 'form',
        services: [{
          name: 'app',
          registry_image: 'reg/app',
          image_tag: 'latest',
          polled: true,
        }],
        stack_path: '/opt/bbsit/stacks/demo',
        health_type: 'none',
        health_target: '',
        poll_interval: 300,
        enabled: true,
        env_vars: { APP_MODE: 'production', API_TOKEN: '********' },
        secret_env_keys: ['API_TOKEN'],
        created_at: '2026-08-10T00:00:00Z',
        updated_at: '2026-08-10T00:00:00Z',
      },
      state: {
        project_id: 'demo',
        current_digests: {},
        previous_digests: {},
        desired_digests: {},
        status: 'running',
        last_error: '',
      },
      deployments: [],
    })

    render(
      <MemoryRouter initialEntries={['/projects/demo/edit']}>
        <Routes>
          <Route path="/projects/:id/edit" element={<ProjectFormPage />} />
          <Route path="/projects/:id" element={<div>Project detail</div>} />
        </Routes>
      </MemoryRouter>,
    )

    const keys = await screen.findAllByLabelText('Environment variable key')
    const key = keys.find(input => (input as HTMLInputElement).value === 'API_TOKEN')
    expect(key).toBeDefined()
    const value = screen.getByLabelText('Value for API_TOKEN')
    const publicValue = screen.getByLabelText('Value for APP_MODE')

    expect(key).toHaveValue('API_TOKEN')
    expect(key).toHaveAttribute('type', 'text')
    expect(value).toHaveValue('********')
    expect(value).toHaveAttribute('type', 'password')
    expect(publicValue).toHaveValue('production')
    expect(publicValue).toHaveAttribute('type', 'text')
    expect(screen.getByLabelText('Secret API_TOKEN')).toBeChecked()
    expect(screen.getByLabelText('Secret APP_MODE')).not.toBeChecked()

    await user.click(screen.getByLabelText('Secret APP_MODE'))
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(api.projects.update).toHaveBeenCalledWith(
      'demo',
      expect.objectContaining({ secret_env_keys: ['APP_MODE', 'API_TOKEN'] }),
    )
  })
})
