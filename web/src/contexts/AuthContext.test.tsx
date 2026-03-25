import type { ReactNode } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { AuthProvider, useAuth } from './AuthContext'

vi.mock('../lib/config', () => ({
  getSystemConfig: vi.fn(() => Promise.resolve({ initialized: true })),
}))

function AuthState() {
  const { user, token, isLoading } = useAuth()

  return (
    <div>
      <span data-testid="loading">{isLoading ? 'loading' : 'ready'}</span>
      <span data-testid="user">{user ? user.email : 'none'}</span>
      <span data-testid="token">{token || 'none'}</span>
    </div>
  )
}

function renderAuthProvider(children: ReactNode) {
  return render(<AuthProvider>{children}</AuthProvider>)
}

describe('AuthProvider startup', () => {
  it('skips invalid cached auth data instead of hanging the app', async () => {
    localStorage.setItem('auth_token', 'legacy-token')
    localStorage.setItem('auth_user', 'not-json')

    renderAuthProvider(<AuthState />)

    await waitFor(() => {
      expect(screen.getByTestId('loading')).toHaveTextContent('ready')
    })
    expect(screen.getByTestId('user')).toHaveTextContent('none')
    expect(screen.getByTestId('token')).toHaveTextContent('none')
    expect(localStorage.getItem('auth_token')).toBeNull()
    expect(localStorage.getItem('auth_user')).toBeNull()
  })
})
