import type { ReactNode } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TraderDashboardPage } from './TraderDashboardPage'
import { api } from '../lib/api'

vi.mock('../lib/api', () => ({
  api: {
    getRuntimeReplayPreview: vi.fn(),
    getRuntimeRestorePreview: vi.fn(),
    closePosition: vi.fn(),
  },
}))

vi.mock('../components/charts/ChartTabs', () => ({
  ChartTabs: () => <div>ChartTabs</div>,
}))

vi.mock('../components/trader/DecisionCard', () => ({
  DecisionCard: () => <div>DecisionCard</div>,
}))

vi.mock('../components/trader/PositionHistory', () => ({
  PositionHistory: () => <div>PositionHistory</div>,
}))

vi.mock('../components/common/PunkAvatar', () => ({
  PunkAvatar: () => <div>PunkAvatar</div>,
  getTraderAvatar: () => 'punk-avatar',
}))

vi.mock('../lib/notify', () => ({
  confirmToast: vi.fn(),
  notify: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('../utils/format', () => ({
  formatPrice: (value: number) => String(value ?? ''),
  formatQuantity: (value: number) => String(value ?? ''),
}))

vi.mock('../i18n/translations', () => ({
  t: (key: string) => key,
}))

vi.mock('../components/common/DeepVoidBackground', () => ({
  DeepVoidBackground: ({ children }: { children?: ReactNode }) => (
    <div>
      <div>DeepVoidBackground</div>
      {children}
    </div>
  ),
}))

vi.mock('../components/ui/select', () => ({
  NofxSelect: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
}))

vi.mock('../components/strategy/GridRiskPanel', () => ({
  GridRiskPanel: () => <div>GridRiskPanel</div>,
}))

vi.mock('../components/trader/OrderRegistryPreviewPanel', () => ({
  OrderRegistryPreviewPanel: () => <div>OrderRegistryPreviewPanel</div>,
}))

vi.mock('../components/trader/RuntimeCapabilityPreviewPanel', () => ({
  RuntimeCapabilityPreviewPanel: () => <div>RuntimeCapabilityPreviewPanel</div>,
}))

vi.mock('../components/trader/ProtectionPreviewPanel', () => ({
  ProtectionPreviewPanel: () => <div>ProtectionPreviewPanel</div>,
}))

vi.mock('../components/trader/ProtectionAdjustmentPreviewPanel', () => ({
  ProtectionAdjustmentPreviewPanel: () => <div>ProtectionAdjustmentPreviewPanel</div>,
}))

vi.mock('../components/trader/ScaleOutPreviewPanel', () => ({
  ScaleOutPreviewPanel: () => <div>ScaleOutPreviewPanel</div>,
}))

vi.mock('../components/trader/ScaleInPreviewPanel', () => ({
  ScaleInPreviewPanel: () => <div>ScaleInPreviewPanel</div>,
}))

describe('TraderDashboardPage phase-7 validation panels', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('mounts replay and restore panels together in the dashboard', async () => {
    vi.mocked(api.getRuntimeReplayPreview).mockResolvedValue({
      fixture_id: 'live-readiness-trader-1',
      fixture_version: 'v1',
      replay_mode: 'sequential',
      event_count: 1,
      trader_id: 'trader-1',
      selected_symbol: 'BTCUSDT',
      truth_snapshot_version: '2026-04-05T00:00:00Z',
      replayed_from_fixture: 'live-readiness-trader-1',
      restored_from_snapshot: false,
      migration_fixture_version: '',
      event_sequence: [
        {
          index: 1,
          event_type: 'ORDER_TRADE_UPDATE',
          label: 'replayed fill',
          event_source: 'fixture',
          event_time: '2026-04-05T00:00:00Z',
          payload_json: '{"event":"filled"}',
        },
      ],
      truth_snapshot: {
        trader_id: 'trader-1',
        selected_symbol: 'BTCUSDT',
        exchange: 'binance_usdm',
        mode: 'one_way',
        truth_snapshot_version: '2026-04-05T00:00:00Z',
        replayed_from_fixture: 'live-readiness-trader-1',
        restored_from_snapshot: false,
        migration_fixture_version: '',
        generated_at: '2026-04-05T00:00:00Z',
      },
      capability_preview: {
        allowed_actions: [],
        blocked_actions: ['add_position'],
      },
      has_mismatch: false,
      mismatch_reasons: [],
      generated_at: '2026-04-05T00:00:00Z',
    } as any)

    vi.mocked(api.getRuntimeRestorePreview).mockResolvedValue({
      trader_id: 'trader-1',
      selected_symbol: 'BTCUSDT',
      restore_scope: 'trader:trader-1 symbol:BTCUSDT',
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
        order_summary: {
          pending_reduce_qty: 0.8,
          pending_add_qty: 0,
        },
        protection_summary: {
          protected_quantity: 0.8,
        },
      },
      after_snapshot: {
        position_aggregate: {
          total_qty: 0.8,
          avg_entry_price: 65000,
        },
        order_summary: {
          pending_reduce_qty: 0.8,
          pending_add_qty: 0,
        },
        protection_summary: {
          protected_quantity: 0.8,
        },
      },
      capability_preview: {
        allowed_actions: [],
        blocked_actions: ['add_position'],
      },
      has_mismatch: false,
      mismatch_reasons: [],
      generated_at: '2026-04-05T00:00:00Z',
    } as any)

    render(
      <TraderDashboardPage
        selectedTrader={{
          trader_id: 'trader-1',
          trader_name: 'Trader One',
          ai_model: 'minimax',
          exchange_id: 'exchange-1',
          strategy_name: 'Validation Strategy',
        } as any}
        traders={[
          {
            trader_id: 'trader-1',
            trader_name: 'Trader One',
            ai_model: 'minimax',
            exchange_id: 'exchange-1',
            strategy_name: 'Validation Strategy',
          } as any,
        ]}
        tradersError={undefined}
        selectedTraderId="trader-1"
        onTraderSelect={vi.fn()}
        onNavigateToTraders={vi.fn()}
        status={{ strategy_type: 'swing' } as any}
        account={{ total_equity: 10, available_balance: 5, total_pnl: 0, position_count: 1, margin_used_pct: 50 } as any}
        accountFailed={false}
        positions={[]}
        positionsFailed={false}
        decisions={[]}
        decisionsFailed={false}
        decisionsLimit={10}
        onDecisionsLimitChange={vi.fn()}
        stats={undefined}
        lastUpdate="2026-04-05T00:00:00Z"
        language={'en' as any}
        exchanges={[]}
      />
    )

    await waitFor(() => {
      expect(screen.getByText('Replay Preview')).toBeInTheDocument()
    })

    expect(screen.getByText('Restore Preview')).toBeInTheDocument()
    expect(screen.getByText('live-readiness-trader-1')).toBeInTheDocument()
    expect(screen.getByText('trader:trader-1 symbol:BTCUSDT')).toBeInTheDocument()
  })
})
