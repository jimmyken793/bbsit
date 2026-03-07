import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import LoginPage from './LoginPage'
import { api, ApiError } from '../api'

vi.mock('../api', async () => {
  const actual = await vi.importActual('../api')
  return {
    ...actual,
    api: {
      auth: {
        login: vi.fn(),
      },
    },
  }
})

describe('LoginPage', () => {
  const onDone = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders login form', () => {
    render(<LoginPage onDone={onDone} />)
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })

  it('calls api.auth.login on submit and invokes onDone', async () => {
    const user = userEvent.setup()
    vi.mocked(api.auth.login).mockResolvedValueOnce(undefined)

    render(<LoginPage onDone={onDone} />)
    await user.type(screen.getByLabelText('Password'), 'mypassword')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(api.auth.login).toHaveBeenCalledWith('mypassword')
    expect(onDone).toHaveBeenCalled()
  })

  it('shows error on failed login', async () => {
    const user = userEvent.setup()
    vi.mocked(api.auth.login).mockRejectedValueOnce(new ApiError(401, 'invalid password'))

    render(<LoginPage onDone={onDone} />)
    await user.type(screen.getByLabelText('Password'), 'wrong')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByText('invalid password')).toBeInTheDocument()
    expect(onDone).not.toHaveBeenCalled()
  })

  it('shows generic error for non-ApiError', async () => {
    const user = userEvent.setup()
    vi.mocked(api.auth.login).mockRejectedValueOnce(new Error('network error'))

    render(<LoginPage onDone={onDone} />)
    await user.type(screen.getByLabelText('Password'), 'test')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByText('Login failed')).toBeInTheDocument()
  })
})
