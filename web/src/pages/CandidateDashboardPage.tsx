import useSWR from 'swr'
import type {
  TraderInfo,
  CandidateMarketItem,
  CandidateSnapshotResponse,
} from '../types'
import { api } from '../lib/api'
import { DeepVoidBackground } from '../components/common/DeepVoidBackground'
import type { Language } from '../i18n/translations'

interface CandidateDashboardPageProps {
  language: Language
  selectedTrader?: TraderInfo
  selectedTraderId?: string
  traders?: TraderInfo[]
  onTraderSelect: (traderId: string) => void
}

function finiteNumber(value: number | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function formatPrice(value: number) {
  if (!Number.isFinite(value)) return '--'
  if (Math.abs(value) >= 1000) return value.toFixed(2)
  if (Math.abs(value) >= 1) return value.toFixed(4)
  return value.toFixed(6)
}

function formatLogicScore(value: number | null) {
  if (value == null) return '--'
  return value.toFixed(1)
}

function formatExpectedEV(value: number | null) {
  if (value == null) return '--'
  const percent = value * 100
  return `${percent >= 0 ? '+' : ''}${percent.toFixed(2)}%`
}

function resolveLogicScore(item: CandidateMarketItem) {
  return finiteNumber(item.logic_score)
}

function resolveExpectedEV(item: CandidateMarketItem) {
  const direct = finiteNumber(item.expected_ev)
  if (direct != null) {
    return direct
  }

  const bin = item._debug_bin_stats
  if (!bin) {
    return null
  }

  if (item.bias === 'LONG') {
    return finiteNumber(bin.ev_long)
  }
  if (item.bias === 'SHORT') {
    return finiteNumber(bin.ev_short)
  }

  const longPF = finiteNumber(bin.profit_factor_long) ?? 0
  const shortPF = finiteNumber(bin.profit_factor_short) ?? 0
  if (longPF > shortPF || (longPF === shortPF && (bin.ev_long ?? 0) >= (bin.ev_short ?? 0))) {
    return finiteNumber(bin.ev_long)
  }
  return finiteNumber(bin.ev_short)
}

function resolveBias(item: CandidateMarketItem) {
  const bias = item.bias?.toUpperCase()
  if (bias === 'LONG' || bias === 'SHORT') {
    return bias
  }
  return 'WAIT'
}

function biasTone(bias: string) {
  switch (bias) {
    case 'LONG':
      return {
        border: 'rgba(14, 203, 129, 0.28)',
        background: 'rgba(14, 203, 129, 0.12)',
        color: '#A7F3D0',
      }
    case 'SHORT':
      return {
        border: 'rgba(96, 165, 250, 0.28)',
        background: 'rgba(96, 165, 250, 0.12)',
        color: '#BFDBFE',
      }
    default:
      return {
        border: 'rgba(148, 163, 184, 0.20)',
        background: 'rgba(148, 163, 184, 0.08)',
        color: '#CBD5E1',
      }
  }
}

export function CandidateDashboardPage({
  language,
  selectedTrader,
  selectedTraderId,
  traders,
  onTraderSelect,
}: CandidateDashboardPageProps) {
  const { data, error, isLoading } = useSWR<CandidateSnapshotResponse>(
    selectedTraderId ? `trader-candidates-${selectedTraderId}` : null,
    () => api.getTraderCandidates(selectedTraderId!),
    {
      refreshInterval: 15000,
      revalidateOnFocus: false,
      shouldRetryOnError: false,
    }
  )

  const title =
    language === 'zh' ? '候选币极简看板' : 'Minimal Candidate Dashboard'
  const subtitle =
    language === 'zh'
      ? '只保留符号、现价、逻辑分、期望值和方向偏置。LONG / SHORT 仅在 EV > 0 且 PF > 1.2 时出现，否则一律 WAIT。'
      : 'Only symbol, price, logic score, expected value, and directional bias remain. LONG / SHORT appear only when EV > 0 and PF > 1.2; otherwise the row stays WAIT.'

  return (
    <DeepVoidBackground className="min-h-screen pb-12" disableAnimation>
      <div className="relative z-10 w-full px-4 pt-6 md:px-8">
        <div className="nofx-glass mb-6 rounded-2xl p-6">
          <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
            <div className="max-w-3xl">
              <h1 className="text-2xl font-semibold text-nofx-text-main md:text-3xl">
                {title}
              </h1>
              <p className="mt-2 text-sm text-nofx-text-muted">{subtitle}</p>
            </div>
            <div className="flex min-w-[260px] flex-col gap-2">
              <label className="text-xs uppercase tracking-[0.16em] text-nofx-text-muted">
                {language === 'zh' ? '交易员' : 'Trader'}
              </label>
              <select
                value={selectedTraderId || ''}
                onChange={(e) => onTraderSelect(e.target.value)}
                className="rounded-xl border border-white/10 bg-[#131722] px-4 py-3 text-nofx-text-main outline-none"
              >
                {(traders || []).map((trader) => (
                  <option key={trader.trader_id} value={trader.trader_id}>
                    {trader.trader_name}
                  </option>
                ))}
              </select>
              <div className="text-xs text-nofx-text-muted">
                {language === 'zh' ? '最后快照时间：' : 'Last snapshot: '}
                {data?.updated_at
                  ? new Date(data.updated_at).toLocaleString()
                  : '--'}
              </div>
            </div>
          </div>
        </div>

        {!selectedTraderId ? (
          <div className="nofx-glass rounded-2xl p-8 text-center text-nofx-text-muted">
            {language === 'zh' ? '请先选择交易员。' : 'Select a trader first.'}
          </div>
        ) : error ? (
          <div className="nofx-glass rounded-2xl p-8 text-center text-nofx-text-muted">
            {language === 'zh'
              ? '候选币快照读取失败。'
              : 'Failed to load candidate snapshot.'}
          </div>
        ) : isLoading ? (
          <div className="nofx-glass rounded-2xl p-8 text-center text-nofx-text-muted">
            {language === 'zh'
              ? '正在加载候选币快照…'
              : 'Loading candidate snapshot...'}
          </div>
        ) : !data || data.candidates.length === 0 ? (
          <div className="nofx-glass rounded-2xl p-8 text-center text-nofx-text-muted">
            {language === 'zh'
              ? '当前还没有候选币快照。请先运行交易员至少一轮。'
              : 'No candidate snapshot yet. Run the trader for at least one cycle.'}
          </div>
        ) : (
          <div className="nofx-glass overflow-hidden rounded-2xl">
            <div className="border-b border-white/10 px-5 py-4 text-xs uppercase tracking-[0.18em] text-nofx-text-muted">
              {language === 'zh'
                ? `当前候选 ${data.candidates.length} 个`
                : `${data.candidates.length} live candidates`}
            </div>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[760px]">
                <thead>
                  <tr className="border-b border-white/10 text-left text-xs uppercase tracking-[0.14em] text-nofx-text-muted">
                    <th className="px-5 py-4">{language === 'zh' ? '币种' : 'Symbol'}</th>
                    <th className="px-5 py-4">{language === 'zh' ? '现价' : 'Price'}</th>
                    <th className="px-5 py-4">{language === 'zh' ? '逻辑分' : 'Logic Score'}</th>
                    <th className="px-5 py-4">{language === 'zh' ? '建议方向' : 'Bias'}</th>
                    <th className="px-5 py-4">{language === 'zh' ? '预期回报' : 'Expected EV%'}</th>
                  </tr>
                </thead>
                <tbody>
                  {data.candidates.map((item) => {
                    const logicScore = resolveLogicScore(item)
                    const bias = resolveBias(item)
                    const expectedEV = resolveExpectedEV(item)
                    const tone = biasTone(bias)

                    return (
                      <tr key={item.symbol} className="border-b border-white/5">
                        <td className="px-5 py-4 text-sm font-semibold text-nofx-text-main">
                          {item.symbol}
                        </td>
                        <td className="px-5 py-4 text-sm text-nofx-text-main">
                          {formatPrice(item.current_price)}
                        </td>
                        <td className="px-5 py-4 text-sm text-nofx-text-main">
                          {formatLogicScore(logicScore)}
                        </td>
                        <td className="px-5 py-4">
                          <span
                            className="inline-flex rounded-full border px-3 py-1 text-xs font-semibold tracking-[0.16em]"
                            style={{
                              borderColor: tone.border,
                              background: tone.background,
                              color: tone.color,
                            }}
                          >
                            {bias}
                          </span>
                        </td>
                        <td className="px-5 py-4 text-sm text-nofx-text-main">
                          {formatExpectedEV(expectedEV)}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {selectedTrader && (
          <div className="mt-4 text-xs text-nofx-text-muted">
            {language === 'zh'
              ? `当前交易员：${selectedTrader.trader_name}。方向偏置只反映当前分数命中的平滑 EV / PF 事实，不含额外技术描述。`
              : `Current trader: ${selectedTrader.trader_name}. Bias reflects only the smoothed EV / PF facts at the current score, with no extra technical narration.`}
          </div>
        )}
      </div>
    </DeepVoidBackground>
  )
}
