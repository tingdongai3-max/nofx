import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { ReplayPreviewPanel } from './ReplayPreviewPanel'
import { api } from '../../lib/api'

vi.mock('../../lib/api', () => ({
  api: {
    getRuntimeReplayPreview: vi.fn(),
  },
}))

describe('ReplayPreviewPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders replay fixture details, truth snapshot, and mismatch evidence', async () => {
    vi.mocked(api.getRuntimeReplayPreview).mockResolvedValue({
      fixture_id: 'fixture-phase7',
      fixture_version: 'v1',
      replay_mode: 'duplicate',
      event_count: 2,
      trader_id: 'trader-1',
      selected_symbol: 'BTCUSDT',
      truth_snapshot_version: '2026-04-05T00:00:00Z',
      replayed_from_fixture: 'fixture-phase7',
      restored_from_snapshot: false,
      migration_fixture_version: '',
      event_sequence: [
        {
          index: 1,
          event_type: 'ORDER_TRADE_UPDATE',
          label: 'filled scale-out level',
          event_source: 'recorded',
          event_time: '2026-04-05T00:00:00Z',
          payload_json: '{"event":"filled"}',
        },
      ],
      truth_snapshot: {
        trader_id: 'trader-1',
        selected_symbol: 'BTCUSDT',
        exchange: 'binance_usdm',
        mode: 'one_way',
        generated_at: '2026-04-05T00:00:00Z',
        truth_snapshot_version: '2026-04-05T00:00:00Z',
        replayed_from_fixture: 'fixture-phase7',
        restored_from_snapshot: false,
        migration_fixture_version: '',
        position_aggregate: null,
        order_summary: {
          pending_add_qty: 0,
          pending_reduce_qty: 0.8,
        },
        protection_summary: {
          protected_quantity: 0.8,
        },
        scale_out_summary: null,
        scale_in_summary: null,
      } as any,
      capability_preview: {
        allowed_actions: ['close_position'],
        blocked_actions: ['add_position'],
      } as any,
      has_mismatch: true,
      mismatch_reasons: [
        {
          category: 'order_state',
          reason: 'registry has a working order but the exchange snapshot does not',
          field: 'reduce',
          symbol: 'BTCUSDT',
          expected: '',
          actual: '7102',
        },
      ],
      generated_at: '2026-04-05T00:00:00Z',
    } as any)

    render(<ReplayPreviewPanel fixtureId="fixture-phase7" replayMode="duplicate" refreshInterval={60_000} />)

    await waitFor(() => {
      expect(screen.getByText('Replay Preview')).toBeInTheDocument()
    })

    expect(screen.getByText('fixture-phase7')).toBeInTheDocument()
    expect(screen.getByText('duplicate')).toBeInTheDocument()
    expect(screen.getByText('filled scale-out level')).toBeInTheDocument()
    expect(screen.getAllByText('0.8').length).toBeGreaterThan(0)
    expect(screen.getByText(/Allowed: close_position/)).toBeInTheDocument()
    expect(screen.getByText('registry has a working order but the exchange snapshot does not')).toBeInTheDocument()
  })
})
