import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import SetupPage from './SetupPage'
import { api, ApiError } from '../api'

vi.mock('../api', async () => {
  const actual = await vi.importActual('../api')
  return {
    ...actual,
    api: {
      auth: {
        setup: vi.fn(),
      },
    },
  }
})

describe('SetupPage', () => {
  const onDone = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders setup form with two password fields', () => {
    render(<SetupPage onDone={onDone} />)
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
    expect(screen.getByLabelText('Confirm password')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Set password' })).toBeInTheDocument()
  })

  it('shows error when password is too short', async () => {
    const user = userEvent.setup()
    render(<SetupPage onDone={onDone} />)

    await user.type(screen.getByLabelText('Password'), 'short')
    await user.type(screen.getByLabelText('Confirm password'), 'short')
    await user.click(screen.getByRole('button', { name: 'Set password' }))

    expect(await screen.findByText('Password must be at least 8 characters')).toBeInTheDocument()
    expect(api.auth.setup).not.toHaveBeenCalled()
  })

  it('shows error when passwords do not match', async () => {
    const user = userEvent.setup()
    render(<SetupPage onDone={onDone} />)

    await user.type(screen.getByLabelText('Password'), 'longpassword1')
    await user.type(screen.getByLabelText('Confirm password'), 'longpassword2')
    await user.click(screen.getByRole('button', { name: 'Set password' }))

    expect(await screen.findByText('Passwords do not match')).toBeInTheDocument()
    expect(api.auth.setup).not.toHaveBeenCalled()
  })

  it('calls api.auth.setup on valid submit', async () => {
    const user = userEvent.setup()
    vi.mocked(api.auth.setup).mockResolvedValueOnce(undefined)

    render(<SetupPage onDone={onDone} />)
    await user.type(screen.getByLabelText('Password'), 'validpassword')
    await user.type(screen.getByLabelText('Confirm password'), 'validpassword')
    await user.click(screen.getByRole('button', { name: 'Set password' }))

    expect(api.auth.setup).toHaveBeenCalledWith('validpassword')
    expect(onDone).toHaveBeenCalled()
  })

  it('shows API error on setup failure', async () => {
    const user = userEvent.setup()
    vi.mocked(api.auth.setup).mockRejectedValueOnce(new ApiError(409, 'already set up'))

    render(<SetupPage onDone={onDone} />)
    await user.type(screen.getByLabelText('Password'), 'validpassword')
    await user.type(screen.getByLabelText('Confirm password'), 'validpassword')
    await user.click(screen.getByRole('button', { name: 'Set password' }))

    expect(await screen.findByText('already set up')).toBeInTheDocument()
    expect(onDone).not.toHaveBeenCalled()
  })
})
