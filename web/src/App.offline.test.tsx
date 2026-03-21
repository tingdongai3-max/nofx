import type { ReactNode } from 'react'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { App } from './App'
import { BACKEND_SERVICE_OFFLINE } from './lib/config'

vi.mock('./contexts/LanguageContext', () => ({
  LanguageProvider: ({ children }: { children: ReactNode }) => children,
  useLanguage: () => ({
    language: 'zh',
    setLanguage: vi.fn(),
  }),
}))

vi.mock('./contexts/AuthContext', () => ({
  AuthProvider: ({ children }: { children: ReactNode }) => children,
  useAuth: () => ({
    user: null,
    token: null,
    logout: vi.fn(),
    isLoading: false,
  }),
}))

vi.mock('./components/common/ConfirmDialog', () => ({
  ConfirmDialogProvider: ({ children }: { children: ReactNode }) => children,
}))

vi.mock('./hooks/useSystemConfig', () => ({
  useSystemConfig: () => ({
    config: null,
    loading: false,
    error: BACKEND_SERVICE_OFFLINE,
  }),
}))

vi.mock('./i18n/translations', () => ({
  t: () => 'loading',
}))

describe('App offline fallback', () => {
  it('shows a maintenance message when the backend is offline', () => {
    window.history.pushState({}, '', '/traders')

    render(<App />)

    expect(screen.getByText('系统维护中')).toBeInTheDocument()
    expect(screen.getByText('API 连接失败，请稍后重试。')).toBeInTheDocument()
  })
})
