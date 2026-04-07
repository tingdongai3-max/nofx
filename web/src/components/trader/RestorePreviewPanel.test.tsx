import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { RestorePreviewPanel } from './RestorePreviewPanel'
import { api } from '../../lib/api'

vi.mock('../../lib/api', () => ({
  api: {
    getRuntimeRestorePreview: vi.fn(),
  },
}))

describe('RestorePreviewPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders before/after restore snapshots and diff evidence', async () => {
    vi.mocked(api.getRuntimeRestorePreview).mockResolvedValue({
      trader_id: 'trader-restore',
      selected_symbol: 'BTCUSDT',
      restore_scope: 'trader:trader-restore symbol:BTCUSDT',
      exchange: 'binance_usdm',
      mode: 'one_way',
      truth_snapshot_version: '2026-04-05T00:00:00Z',
      replayed_from_fixture: '',
      restored_from_snapshot: true,
      migration_fixture_version: '',
      before_snapshot: {
        position_aggregate: {
          total_qty: 0.8,
          avg_entry_price: 65000,
        },
        protection_summary: {
          protected_quantity: 0.8,
        },
        order_summary: {
          pending_reduce_qty: 0.8,
          pending_add_qty: 0,
        },
      },
      after_snapshot: {
        position_aggregate: {
          total_qty: 0.8,
          avg_entry_price: 65000,
        },
        protection_summary: {
          protected_quantity: 0.8,
        },
        order_summary: {
          pending_reduce_qty: 0.8,
          pending_add_qty: 0,
        },
      },
      capability_preview: {
        allowed_actions: [],
        blocked_actions: ['add_position'],
      },
      has_mismatch: true,
      mismatch_reasons: [
        {
          category: 'restore',
          reason: 'selected symbol changed across validation snapshots',
          field: 'selected_symbol',
          symbol: 'BTCUSDT',
          expected: 'BTCUSDT',
          actual: 'ETHUSDT',
        },
      ],
      generated_at: '2026-04-05T00:00:00Z',
    } as any)

    render(<RestorePreviewPanel traderId="trader-restore" selectedSymbol="BTCUSDT" refreshInterval={60_000} />)

    await waitFor(() => {
      expect(screen.getByText('Restore Preview')).toBeInTheDocument()
    })

    expect(screen.getByText('trader:trader-restore symbol:BTCUSDT')).toBeInTheDocument()
    expect(screen.getAllByText('0.8').length).toBeGreaterThan(0)
    expect(screen.getByText(/Blocked: add_position/)).toBeInTheDocument()
    expect(screen.getByText('selected symbol changed across validation snapshots')).toBeInTheDocument()
  })
})
