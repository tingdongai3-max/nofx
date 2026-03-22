import useSWR from 'swr'
import type { Language } from '../i18n/translations'
import type {
  AdaptiveFactorState,
  AdaptiveWeightState,
  ShadowSnapshot,
  TraderInfo,
} from '../types'
import { api } from '../lib/api'
import { DeepVoidBackground } from '../components/common/DeepVoidBackground'
import { AdaptiveRadarChart } from '../components/charts/AdaptiveRadarChart'

interface DataLabPageProps {
  language: Language
  selectedTrader?: TraderInfo
  selectedTraderId?: string
  traders?: TraderInfo[]
  onTraderSelect: (traderId: string) => void
}

function formatDecision(actionTaken: number, language: Language) {
  if (actionTaken === 1) {
    return language === 'zh' ? '已开仓' : 'Taken'
  }
  return language === 'zh' ? '错过' : 'Missed'
}

function formatTimestamp(timestamp: number, language: Language) {
  if (!timestamp) return '--'
  return new Date(timestamp).toLocaleString(language === 'zh' ? 'zh-CN' : 'en-US', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function formatPrice(value: number) {
  if (!Number.isFinite(value)) return '--'
  if (Math.abs(value) >= 1000) return value.toFixed(2)
  if (Math.abs(value) >= 1) return value.toFixed(4)
  return value.toFixed(6)
}

function formatHeat(value: number) {
  if (!Number.isFinite(value)) return '--'
  return value.toFixed(1)
}

function formatReturnPct(value: number, filled: boolean) {
  if (!filled || !Number.isFinite(value)) return '--'
  return `${value >= 0 ? '+' : ''}${(value * 100).toFixed(2)}%`
}

function formatWeight(value: number) {
  if (!Number.isFinite(value)) return '--'
  return `${(value * 100).toFixed(1)}%`
}

function formatIC(value: number) {
  if (!Number.isFinite(value)) return '--'
  return `${value >= 0 ? '+' : ''}${value.toFixed(2)}`
}

function formatAlpha(value: number) {
  if (!Number.isFinite(value)) return '--'
  return `${(value * 100).toFixed(0)}%`
}

function factorLabel(name: string, language: Language) {
  switch (name) {
    case 'market':
      return language === 'zh' ? '市场动量' : 'Market'
    case 'trend':
      return language === 'zh' ? '趋势结构' : 'Trend'
    case 'volume_spike':
      return language === 'zh' ? '量价脉冲' : 'Vol-Price Spike'
    case 'quant':
      return language === 'zh' ? '量化流' : 'Quant'
    case 'social':
      return language === 'zh' ? '公众情绪' : 'Social'
    case 'onchain':
      return language === 'zh' ? '链上活跃' : 'On-Chain'
    default:
      return name
  }
}

export function DataLabPage({
  language,
  selectedTrader,
  selectedTraderId,
  traders,
  onTraderSelect,
}: DataLabPageProps) {
  const { data, error, isLoading } = useSWR<ShadowSnapshot[]>(
    selectedTraderId ? `shadow-snapshots-${selectedTraderId}` : null,
    () => api.getShadowSnapshots(selectedTraderId!, 200),
    {
      refreshInterval: 60000,
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  )

  const snapshots = data || []
  const focusSnapshot = snapshots[0]

  const {
    data: adaptiveState,
    error: adaptiveError,
    isLoading: adaptiveLoading,
  } = useSWR<AdaptiveWeightState>(
    selectedTraderId
      ? `adaptive-weights-${selectedTraderId}-${focusSnapshot?.symbol || 'latest'}-${focusSnapshot?.sector || 'sector'}`
      : null,
    () => api.getAdaptiveWeights(selectedTraderId!, focusSnapshot?.symbol, focusSnapshot?.sector),
    {
      refreshInterval: 60000,
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  )

  const adaptiveFactors = adaptiveState?.factors || []
  const sampleCount = adaptiveState?.sector_sample_count || adaptiveState?.sample_count || 0
  const sampleTarget = adaptiveState?.sample_target || 30
  const warmingUp = sampleCount < sampleTarget
  let filledCount = 0
  let missedCount = 0
  let bestMissed: ShadowSnapshot | undefined

  for (const row of snapshots) {
    if (row.filled) filledCount += 1
    if (row.action_taken === 0) {
      missedCount += 1
      if (!bestMissed || row.return_pct > bestMissed.return_pct) {
        bestMissed = row
      }
    }
  }

  return (
    <DeepVoidBackground className="pb-10">
      <section className="mx-auto w-full max-w-[1600px] px-4 pb-10 pt-10 sm:px-6 lg:px-8">
        <div
          className="overflow-hidden rounded-[28px] border"
          style={{
            borderColor: 'rgba(240, 185, 11, 0.20)',
            background:
              'linear-gradient(135deg, rgba(13,18,24,0.96) 0%, rgba(18,26,34,0.90) 50%, rgba(10,14,19,0.98) 100%)',
            boxShadow: '0 28px 100px rgba(0, 0, 0, 0.35)',
          }}
        >
          <div className="border-b border-white/10 px-6 py-6 sm:px-8">
            <div className="flex flex-col gap-6 xl:flex-row xl:items-end xl:justify-between">
              <div className="max-w-3xl">
                <p className="text-xs uppercase tracking-[0.32em] text-[#F0B90B]/80">
                  {language === 'zh' ? 'Data Lab / Shadow Tracker' : 'Data Lab / Shadow Tracker'}
                </p>
                <h1 className="mt-3 text-3xl font-semibold text-white sm:text-[2.4rem]">
                  {language === 'zh' ? '影子快照监控台' : 'Shadow Snapshot Monitor'}
                </h1>
                <p className="mt-3 max-w-2xl text-sm leading-6 text-[#B7BDC6]">
                  {language === 'zh'
                    ? '每次 AI 决策都会把候选池在 T0 的物理截面落盘，Shadow Daemon 会在 T+15m 回填真实收益，专门暴露 AI 错失机会的盲区。'
                    : 'Every AI cycle stores the full T0 candidate cross-section, then the shadow daemon backfills T+15m realized returns to expose missed opportunities.'}
                </p>
              </div>

              <div className="w-full max-w-sm">
                <label className="mb-2 block text-xs uppercase tracking-[0.28em] text-[#848E9C]">
                  {language === 'zh' ? '观察 Trader' : 'Trader'}
                </label>
                <select
                  value={selectedTraderId || ''}
                  onChange={(e) => onTraderSelect(e.target.value)}
                  className="w-full rounded-2xl border px-4 py-3 text-sm outline-none transition"
                  style={{
                    borderColor: 'rgba(240, 185, 11, 0.18)',
                    background: 'rgba(11, 14, 17, 0.78)',
                    color: '#EAECEF',
                  }}
                >
                  {(traders || []).map((trader) => (
                    <option key={trader.trader_id} value={trader.trader_id}>
                      {trader.trader_name}
                    </option>
                  ))}
                </select>
              </div>
            </div>
          </div>

          <div className="border-b border-white/10 px-6 py-6 sm:px-8">
            <div className="mb-5 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
              <div>
                <p className="text-xs uppercase tracking-[0.28em] text-[#22D3EE]/80">
                  {language === 'zh' ? 'Adaptive Radar' : 'Adaptive Radar'}
                </p>
                <h2 className="mt-2 text-2xl font-semibold text-white">
                  {language === 'zh' ? '自适应雷达' : 'Adaptive Radar'}
                </h2>
                <p className="mt-2 max-w-3xl text-sm leading-6 text-[#B7BDC6]">
                  {language === 'zh'
                    ? '基于 Filled=true 的影子样本计算六因子 Pearson IC，再用贝叶斯平滑和默认权重融合，实时覆写热度引擎底层权重。'
                    : 'Six-factor Pearson ICs are computed from filled shadow samples, then blended with default priors to override the live heat-scoring weights.'}
                </p>
              </div>

              <div className="flex flex-wrap gap-3">
                <div className="rounded-full border border-[#F0B90B]/20 bg-[#F0B90B]/10 px-3 py-2 text-xs font-medium text-[#F6D782]">
                  {language === 'zh'
                    ? `焦点币种 ${adaptiveState?.symbol || focusSnapshot?.symbol || '--'}`
                    : `Focus ${adaptiveState?.symbol || focusSnapshot?.symbol || '--'}`}
                </div>
                <div className="rounded-full border border-[#22D3EE]/20 bg-[#22D3EE]/10 px-3 py-2 text-xs font-medium text-[#9FEAF6]">
                  {language === 'zh'
                    ? `有效样本 ${sampleCount} / ${sampleTarget}${warmingUp ? ' · 预热中' : ' · 已接管'}`
                    : `Samples ${sampleCount} / ${sampleTarget}${warmingUp ? ' · Warming Up' : ' · Active'}`}
                </div>
                <div className="rounded-full border border-emerald-400/20 bg-emerald-400/10 px-3 py-2 text-xs font-medium text-emerald-200">
                  {language === 'zh'
                    ? `赛道 ${adaptiveState?.sector || focusSnapshot?.sector || '--'} / 样本 ${adaptiveState?.sector_sample_count || 0}`
                    : `Sector ${adaptiveState?.sector || focusSnapshot?.sector || '--'} / ${adaptiveState?.sector_sample_count || 0}`}
                </div>
                <div className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-2 text-xs font-medium text-[#D1D4DC]">
                  {language === 'zh'
                    ? `专属样本 ${adaptiveState?.coin_sample_count || 0} · α ${formatAlpha(adaptiveState?.alpha || 0)}`
                    : `Coin Samples ${adaptiveState?.coin_sample_count || 0} · α ${formatAlpha(adaptiveState?.alpha || 0)}`}
                </div>
                <div className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-2 text-xs font-medium text-[#D1D4DC]">
                  {language === 'zh'
                    ? `经验占比 ${((adaptiveState?.blend_adaptive || 0) * 100).toFixed(0)}%`
                    : `Adaptive Blend ${((adaptiveState?.blend_adaptive || 0) * 100).toFixed(0)}%`}
                </div>
              </div>
            </div>

            <div className="grid gap-5 xl:grid-cols-[minmax(0,1.2fr)_minmax(320px,0.8fr)]">
              <div className="rounded-[24px] border border-white/10 bg-black/20 p-4 sm:p-5">
                <div className="mb-3 flex items-center justify-between">
                  <div>
                    <div className="text-sm font-semibold text-white">
                      {language === 'zh' ? '权重雷达' : 'Weight Radar'}
                    </div>
                    <div className="text-xs text-[#848E9C]">
                      {language === 'zh'
                        ? '金色为最终生效权重，蓝色为 IC 信号强度。'
                        : 'Gold shows live weights; cyan shows IC signal strength.'}
                    </div>
                  </div>
                  {adaptiveError ? (
                    <span className="text-sm text-[#F6465D]">
                      {language === 'zh' ? '加载失败' : 'Load failed'}
                    </span>
                  ) : null}
                </div>
                <AdaptiveRadarChart factors={adaptiveFactors} language={language} />
              </div>

              <div className="rounded-[24px] border border-white/10 bg-black/20 p-4 sm:p-5">
                <div className="mb-4 flex items-center justify-between">
                  <div>
                    <div className="text-sm font-semibold text-white">
                      {language === 'zh' ? '因子状态' : 'Factor State'}
                    </div>
                    <div className="text-xs text-[#848E9C]">
                      {language === 'zh'
                        ? '实时 IC 与最终权重'
                        : 'Live IC and final weights'}
                    </div>
                  </div>
                </div>

                <div className="space-y-3">
                  {adaptiveLoading ? (
                    <div className="rounded-2xl border border-dashed border-white/10 px-4 py-8 text-center text-sm text-[#848E9C]">
                      {language === 'zh' ? '自适应引擎加载中...' : 'Loading adaptive engine...'}
                    </div>
                  ) : adaptiveFactors.length === 0 ? (
                    <div className="rounded-2xl border border-dashed border-white/10 px-4 py-8 text-center text-sm text-[#848E9C]">
                      {language === 'zh' ? '暂无 IC 样本' : 'No IC samples yet'}
                    </div>
                  ) : (
                    adaptiveFactors.map((factor: AdaptiveFactorState) => {
                      const icColor =
                        factor.ic > 0
                          ? 'text-[#0ECB81]'
                          : factor.ic < 0
                            ? 'text-[#F6465D]'
                            : 'text-[#848E9C]'
                      return (
                        <div
                          key={factor.name}
                          className="rounded-2xl border border-white/10 bg-white/[0.03] px-4 py-3"
                        >
                          <div className="flex items-center justify-between gap-4">
                            <div>
                              <div className="text-sm font-medium text-white">
                                {factorLabel(factor.name, language)}
                              </div>
                              <div className="mt-1 text-xs text-[#848E9C]">
                                {language === 'zh'
                                  ? `默认 ${formatWeight(factor.default_weight)} · 经验 ${formatWeight(factor.empirical_weight)}`
                                  : `Default ${formatWeight(factor.default_weight)} · Empirical ${formatWeight(factor.empirical_weight)}`}
                              </div>
                              <div className="mt-1 text-xs text-[#6EC6FF]">
                                {language === 'zh'
                                  ? `赛道IC ${formatIC(factor.sector_ic)} · 币种IC ${formatIC(factor.coin_ic)}`
                                  : `Sector IC ${formatIC(factor.sector_ic)} · Coin IC ${formatIC(factor.coin_ic)}`}
                              </div>
                            </div>
                            <div className="text-right">
                              <div className={`text-sm font-semibold ${icColor}`}>
                                IC {formatIC(factor.final_ic ?? factor.ic)}
                              </div>
                              <div className="mt-1 text-xs text-[#F0B90B]">
                                {language === 'zh'
                                  ? `当前权重 ${formatWeight(factor.final_weight)}`
                                  : `Weight ${formatWeight(factor.final_weight)}`}
                              </div>
                            </div>
                          </div>
                        </div>
                      )
                    })
                  )}
                </div>
              </div>
            </div>
          </div>

          <div className="grid gap-4 border-b border-white/10 px-6 py-5 sm:grid-cols-3 sm:px-8">
            <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
              <p className="text-xs uppercase tracking-[0.22em] text-[#848E9C]">
                {language === 'zh' ? '快照样本' : 'Snapshots'}
              </p>
              <p className="mt-3 text-3xl font-semibold text-white">{snapshots.length}</p>
              <p className="mt-2 text-sm text-[#B7BDC6]">
                {selectedTrader?.trader_name || '--'}
              </p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
              <p className="text-xs uppercase tracking-[0.22em] text-[#848E9C]">
                {language === 'zh' ? '已完成回填' : 'Filled'}
              </p>
              <p className="mt-3 text-3xl font-semibold text-white">{filledCount}</p>
              <p className="mt-2 text-sm text-[#B7BDC6]">
                {language === 'zh' ? 'T+15m 已有真实收益率' : 'Rows with realized T+15m returns'}
              </p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
              <p className="text-xs uppercase tracking-[0.22em] text-[#848E9C]">
                {language === 'zh' ? '最大错失样本' : 'Best Missed Move'}
              </p>
              <p className="mt-3 text-3xl font-semibold text-[#F0B90B]">
                {bestMissed ? formatReturnPct(bestMissed.return_pct, bestMissed.filled) : '--'}
              </p>
              <p className="mt-2 text-sm text-[#B7BDC6]">
                {bestMissed
                  ? `${bestMissed.symbol} · ${formatHeat(bestMissed.heat_score)} Heat`
                  : language === 'zh'
                    ? `当前错过样本 ${missedCount} 条`
                    : `${missedCount} missed rows tracked`}
              </p>
            </div>
          </div>

          <div className="px-6 py-6 sm:px-8">
            <div className="mb-4 flex items-center justify-between">
              <div>
                <h2 className="text-lg font-semibold text-white">
                  {language === 'zh' ? '影子回填明细' : 'Shadow Fill Detail'}
                </h2>
                <p className="mt-1 text-sm text-[#848E9C]">
                  {language === 'zh'
                    ? '时间 | 币种 | 决策 | T0 热度 | T+15m 实际涨跌幅'
                    : 'Time | Symbol | Decision | T0 Heat | T+15m realized return'}
                </p>
              </div>
              {error ? (
                <span className="text-sm text-[#F6465D]">
                  {language === 'zh' ? '加载失败' : 'Load failed'}
                </span>
              ) : null}
            </div>

            <div className="overflow-x-auto rounded-2xl border border-white/10 bg-black/15">
              <table className="min-w-full divide-y divide-white/10 text-sm">
                <thead className="bg-white/[0.03]">
                  <tr className="text-left text-xs uppercase tracking-[0.18em] text-[#848E9C]">
                    <th className="px-4 py-3">{language === 'zh' ? '时间' : 'Time'}</th>
                    <th className="px-4 py-3">{language === 'zh' ? '币种' : 'Symbol'}</th>
                    <th className="px-4 py-3">{language === 'zh' ? '决策' : 'Decision'}</th>
                    <th className="px-4 py-3">{language === 'zh' ? 'T0 价格' : 'T0 Price'}</th>
                    <th className="px-4 py-3">{language === 'zh' ? 'T0 热度' : 'T0 Heat'}</th>
                    <th className="px-4 py-3">{language === 'zh' ? '波动利用率' : 'Vol Util'}</th>
                    <th className="px-4 py-3">{language === 'zh' ? 'T+15m 涨跌幅' : 'T+15m Return'}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-white/5">
                  {isLoading ? (
                    <tr>
                      <td colSpan={7} className="px-4 py-12 text-center text-[#848E9C]">
                        {language === 'zh' ? '影子快照加载中...' : 'Loading shadow snapshots...'}
                      </td>
                    </tr>
                  ) : snapshots.length === 0 ? (
                    <tr>
                      <td colSpan={7} className="px-4 py-12 text-center text-[#848E9C]">
                        {language === 'zh' ? '暂无影子数据' : 'No shadow data yet'}
                      </td>
                    </tr>
                  ) : (
                    snapshots.map((row) => {
                      const returnColor = !row.filled
                        ? 'text-[#848E9C]'
                        : row.return_pct >= 0
                          ? 'text-[#0ECB81]'
                          : 'text-[#F6465D]'
                      const decisionColor =
                        row.action_taken === 1 ? 'text-[#0ECB81]' : 'text-[#F0B90B]'

                      return (
                        <tr key={row.id} className="transition hover:bg-white/[0.03]">
                          <td className="whitespace-nowrap px-4 py-4 text-[#B7BDC6]">
                            {formatTimestamp(row.decision_time, language)}
                          </td>
                          <td className="whitespace-nowrap px-4 py-4 font-medium text-white">
                            {row.symbol}
                          </td>
                          <td className={`whitespace-nowrap px-4 py-4 font-medium ${decisionColor}`}>
                            {formatDecision(row.action_taken, language)}
                          </td>
                          <td className="whitespace-nowrap px-4 py-4 text-[#EAECEF]">
                            {formatPrice(row.price_t0)}
                          </td>
                          <td className="whitespace-nowrap px-4 py-4 text-[#F0B90B]">
                            {formatHeat(row.heat_score)}
                          </td>
                          <td className="whitespace-nowrap px-4 py-4 text-[#B7BDC6]">
                            {Number.isFinite(row.vol_utilization)
                              ? `${(row.vol_utilization * 100).toFixed(1)}%`
                              : '--'}
                          </td>
                          <td className={`whitespace-nowrap px-4 py-4 font-semibold ${returnColor}`}>
                            {formatReturnPct(row.return_pct, row.filled)}
                          </td>
                        </tr>
                      )
                    })
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </section>
    </DeepVoidBackground>
  )
}
