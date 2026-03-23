import { useEffect, useState } from 'react'
import useSWR from 'swr'
import type { Language } from '../i18n/translations'
import type {
  AdaptiveFactorState,
  AdaptiveWeightState,
  ScoreBinPerformance,
  ShadowSnapshot,
  TraderInfo,
} from '../types'
import { api } from '../lib/api'
import { DeepVoidBackground } from '../components/common/DeepVoidBackground'
import { AdaptiveRadarChart } from '../components/charts/AdaptiveRadarChart'
import { ScoreBinPerformanceChart } from '../components/charts/ScoreBinPerformanceChart'

interface DataLabPageProps {
  language: Language
  selectedTrader?: TraderInfo
  selectedTraderId?: string
  traders?: TraderInfo[]
  onTraderSelect: (traderId: string) => void
}

type Scope =
  | { type: 'global' }
  | { type: 'sector'; target: string }
  | { type: 'symbol'; target: string }

type PerformanceWindowSize = 5 | 10

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

function formatSymbolTarget(target: string) {
  if (target.endsWith('USDT')) {
    return target.slice(0, -4)
  }
  return target
}

function describeScope(scope: Scope, language: Language) {
  if (scope.type === 'global') {
    return language === 'zh' ? '全局' : 'Global'
  }
  if (scope.type === 'sector') {
    return language === 'zh' ? `赛道: ${scope.target}` : `Sector: ${scope.target}`
  }
  const label = formatSymbolTarget(scope.target)
  return language === 'zh' ? `币种: ${label}` : `Symbol: ${label}`
}

const nestedFactorGroups = {
  trend: {
    title: { zh: '趋势内部结构', en: 'Nested Trend Sub-factors' },
    children: [
      {
        key: 'trend',
        source: 'visible',
        fallbackWeight: 0.5,
        label: { zh: '趋势核心', en: 'Trend Core' },
      },
      {
        key: 'donchian_factor',
        source: 'hidden',
        fallbackWeight: 0.5,
        label: { zh: '唐奇安位置', en: 'Donchian Position' },
      },
    ],
  },
  volume_spike: {
    title: { zh: '量价脉冲内部结构', en: 'Nested Spike Sub-factors' },
    children: [
      {
        key: 'volume_spike',
        source: 'visible',
        fallbackWeight: 0.5,
        label: { zh: '脉冲核心', en: 'Spike Core' },
      },
      {
        key: 'mtf_resonance',
        source: 'hidden',
        fallbackWeight: 0.5,
        label: { zh: 'MTF 共振', en: 'MTF Resonance' },
      },
    ],
  },
  quant: {
    title: { zh: '量化流内部结构', en: 'Nested Quant Sub-factors' },
    children: [
      {
        key: 'quant_oi',
        source: 'hidden',
        fallbackWeight: 0.4,
        label: { zh: 'OI 变化', en: 'OI Delta' },
      },
      {
        key: 'quant_imbalance',
        source: 'hidden',
        fallbackWeight: 0.25,
        label: { zh: '盘口失衡', en: 'Orderbook Imbalance' },
      },
      {
        key: 'quant_netflow',
        source: 'hidden',
        fallbackWeight: 0.35,
        label: { zh: '资金流', en: 'Netflow' },
      },
    ],
  },
  social: {
    title: { zh: '公众情绪内部结构', en: 'Nested Social Sub-factors' },
    children: [
      {
        key: 'social_rank',
        source: 'hidden',
        fallbackWeight: 0.7,
        label: { zh: '热搜排名', en: 'Trending Rank' },
      },
      {
        key: 'social_upvote',
        source: 'hidden',
        fallbackWeight: 0.3,
        label: { zh: '点赞率', en: 'Up-votes' },
      },
    ],
  },
  onchain: {
    title: { zh: '链上活跃内部结构', en: 'Nested On-Chain Sub-factors' },
    children: [
      {
        key: 'onchain_ratio',
        source: 'hidden',
        fallbackWeight: 0.6,
        label: { zh: '链上/CEX 比', en: 'Onchain/CEX Ratio' },
      },
      {
        key: 'onchain_buy_ratio',
        source: 'hidden',
        fallbackWeight: 0.4,
        label: { zh: '买单占比', en: 'Buy Ratio' },
      },
    ],
  },
} as const

function nestedFactorLabel(parent: keyof typeof nestedFactorGroups, key: string, language: Language) {
  const item = nestedFactorGroups[parent].children.find((child) => child.key === key)
  if (!item) {
    return key
  }
  return language === 'zh' ? item.label.zh : item.label.en
}

function adaptiveFactorStateByName(
  factors: AdaptiveFactorState[] | undefined,
  name: string
) {
  return factors?.find((factor) => factor.name === name)
}

function resolveAdaptiveFactorState(
  visibleFactors: AdaptiveFactorState[] | undefined,
  hiddenFactors: AdaptiveFactorState[] | undefined,
  name: string
) {
  return (
    adaptiveFactorStateByName(visibleFactors, name) ||
    adaptiveFactorStateByName(hiddenFactors, name)
  )
}

function normalizeNestedGroupWeights(
  groupName: keyof typeof nestedFactorGroups,
  nestedWeights: Record<string, number> | undefined
) {
  const group = nestedFactorGroups[groupName]
  const weights = group.children.map((child) => {
    const rawWeight = nestedWeights?.[child.key]
    const value =
      typeof rawWeight === 'number' && Number.isFinite(rawWeight)
        ? Math.max(rawWeight, 0)
        : child.fallbackWeight
    return [child.key, value] as const
  })
  const total = weights.reduce((sum, [, value]) => sum + value, 0)
  if (total <= 0) {
    return Object.fromEntries(
      group.children.map((child) => [child.key, child.fallbackWeight])
    ) as Record<string, number>
  }
  return Object.fromEntries(
    weights.map(([key, value]) => [key, value / total])
  ) as Record<string, number>
}

export function DataLabPage({
  language,
  selectedTrader,
  selectedTraderId,
  traders,
  onTraderSelect,
}: DataLabPageProps) {
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({})
  const [currentScope, setCurrentScope] = useState<Scope>({ type: 'global' })
  const [realtimeBackcast, setRealtimeBackcast] = useState(false)
  const [performanceWindowSize, setPerformanceWindowSize] =
    useState<PerformanceWindowSize>(5)
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
  const availableSectors: string[] = []
  const availableSymbols: string[] = []
  const seenSectors = new Set<string>()
  const seenSymbols = new Set<string>()
  for (const snapshot of snapshots) {
    if (snapshot.sector && !seenSectors.has(snapshot.sector)) {
      seenSectors.add(snapshot.sector)
      availableSectors.push(snapshot.sector)
    }
    if (snapshot.symbol && !seenSymbols.has(snapshot.symbol)) {
      seenSymbols.add(snapshot.symbol)
      availableSymbols.push(snapshot.symbol)
    }
  }
  const defaultSectorTarget = focusSnapshot?.sector || availableSectors[0] || ''
  const defaultSymbolTarget = focusSnapshot?.symbol || availableSymbols[0] || ''

  useEffect(() => {
    setCurrentScope({ type: 'global' })
    setExpandedGroups({})
    setRealtimeBackcast(false)
    setPerformanceWindowSize(5)
  }, [selectedTraderId])

  useEffect(() => {
    if (currentScope.type === 'sector') {
      if (availableSectors.includes(currentScope.target)) {
        return
      }
      if (defaultSectorTarget) {
        setCurrentScope({ type: 'sector', target: defaultSectorTarget })
        return
      }
      setCurrentScope({ type: 'global' })
      return
    }
    if (currentScope.type === 'symbol') {
      if (availableSymbols.includes(currentScope.target)) {
        return
      }
      if (defaultSymbolTarget) {
        setCurrentScope({ type: 'symbol', target: defaultSymbolTarget })
        return
      }
      setCurrentScope({ type: 'global' })
    }
  }, [
    currentScope,
    defaultSectorTarget,
    defaultSymbolTarget,
    availableSectors,
    availableSymbols,
  ])

  const {
    data: adaptiveState,
    error: adaptiveError,
    isLoading: adaptiveLoading,
  } = useSWR<AdaptiveWeightState>(
    selectedTraderId
      ? `adaptive-weights-${selectedTraderId}-${currentScope.type}-${currentScope.type === 'global' ? 'all' : currentScope.target}`
      : null,
    () =>
      api.getAdaptiveWeights(
        selectedTraderId!,
        currentScope.type,
        currentScope.type === 'global' ? undefined : currentScope.target
      ),
    {
      refreshInterval: 60000,
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  )
  const {
    data: performanceBins,
    error: performanceBinsError,
    isLoading: performanceBinsLoading,
  } = useSWR<ScoreBinPerformance[]>(
    selectedTraderId
      ? `performance-bins-${realtimeBackcast ? 'backcast' : 'snapshot'}-${selectedTraderId}-${currentScope.type}-${currentScope.type === 'global' ? 'all' : currentScope.target}-window-${performanceWindowSize}`
      : null,
    () =>
      realtimeBackcast
        ? api.getBackcastPerformanceBins(
            selectedTraderId!,
            currentScope.type,
            currentScope.type === 'global' ? undefined : currentScope.target,
            performanceWindowSize
          )
        : api.getPerformanceBins(
            selectedTraderId!,
            currentScope.type,
            currentScope.type === 'global' ? undefined : currentScope.target,
            performanceWindowSize
          ),
    {
      refreshInterval: 60000,
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  )

  const adaptiveFactors = adaptiveState?.factors || []
  const hiddenFactors = adaptiveState?.hidden_factors || []
  const nestedWeights = adaptiveState?.nested_weights || {}
  const performanceBinRows = performanceBins || []
  const peakWindowTradeCount = performanceBinRows.reduce(
    (max, row) => Math.max(max, row.trade_count),
    0
  )
  const scopeDescription = describeScope(currentScope, language)
  const performanceModeLabel = realtimeBackcast
    ? language === 'zh'
      ? '实时权重重算'
      : 'Real-time Back-cast'
    : language === 'zh'
      ? '快照分数'
      : 'Snapshot Scores'
  const sampleTarget = adaptiveState?.sample_target || 2000
  const coinSampleTarget = 500
  const scopeSampleCount =
    currentScope.type === 'symbol'
      ? adaptiveState?.coin_sample_count || 0
      : currentScope.type === 'sector'
        ? adaptiveState?.sector_sample_count || adaptiveState?.sample_count || 0
        : adaptiveState?.sample_count || 0
  const scopeSampleTarget =
    currentScope.type === 'symbol' ? coinSampleTarget : sampleTarget
  const warmingUp = scopeSampleCount < scopeSampleTarget
  const symbolSampleInsufficient =
    currentScope.type === 'symbol' && scopeSampleCount < 10
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

  const scopeOptions =
    currentScope.type === 'sector' ? availableSectors : availableSymbols
  const handleScopeTypeChange = (nextType: Scope['type']) => {
    if (nextType === 'global') {
      setCurrentScope({ type: 'global' })
      return
    }
    if (nextType === 'sector') {
      if (!defaultSectorTarget) {
        setCurrentScope({ type: 'global' })
        return
      }
      setCurrentScope({
        type: 'sector',
        target:
          currentScope.type === 'sector' && currentScope.target
            ? currentScope.target
            : defaultSectorTarget,
      })
      return
    }
    if (!defaultSymbolTarget) {
      setCurrentScope({ type: 'global' })
      return
    }
    setCurrentScope({
      type: 'symbol',
      target:
        currentScope.type === 'symbol' && currentScope.target
          ? currentScope.target
          : defaultSymbolTarget,
    })
  }

  const handleScopeTargetChange = (target: string) => {
    if (currentScope.type === 'global') {
      return
    }
    setCurrentScope({ type: currentScope.type, target })
  }

  const smoothingWindowLabel =
    performanceWindowSize === 10
      ? language === 'zh'
        ? '宽窗 ±5'
        : 'Wide ±5'
      : language === 'zh'
        ? '标准 ±2.5'
        : 'Standard ±2.5'

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
                    ? '基于 Filled=true 的影子样本计算六因子 Spearman Rank IC，再用贝叶斯平滑和默认权重融合，实时覆写热度引擎底层权重。'
                    : 'Six-factor Spearman Rank ICs are computed from filled shadow samples, then blended with default priors to override the live heat-scoring weights.'}
                </p>
              </div>

              <div className="flex flex-col gap-3 lg:items-end">
                <div className="flex flex-wrap items-center gap-3">
                  <div className="inline-flex rounded-full border border-white/10 bg-black/20 p-1">
                    {(['global', 'sector', 'symbol'] as const).map((type) => {
                      const active = currentScope.type === type
                      return (
                        <button
                          key={type}
                          type="button"
                          onClick={() => handleScopeTypeChange(type)}
                          className="rounded-full px-4 py-2 text-xs font-semibold tracking-[0.2em] transition"
                          style={{
                            background: active ? 'rgba(240, 185, 11, 0.18)' : 'transparent',
                            color: active ? '#F6D782' : '#94A3B8',
                          }}
                        >
                          {type === 'global'
                            ? language === 'zh'
                              ? '全局'
                              : 'GLOBAL'
                            : type === 'sector'
                              ? language === 'zh'
                                ? '赛道'
                                : 'SECTOR'
                              : language === 'zh'
                                ? '币种'
                                : 'SYMBOL'}
                        </button>
                      )
                    })}
                  </div>
                  {currentScope.type !== 'global' ? (
                    <select
                      value={currentScope.target}
                      onChange={(e) => handleScopeTargetChange(e.target.value)}
                      className="rounded-full border px-4 py-2 text-xs font-medium outline-none transition"
                      style={{
                        borderColor: 'rgba(240, 185, 11, 0.18)',
                        background: 'rgba(11, 14, 17, 0.78)',
                        color: '#EAECEF',
                      }}
                    >
                      {scopeOptions.map((option) => (
                        <option key={option} value={option}>
                          {currentScope.type === 'symbol'
                            ? formatSymbolTarget(option)
                            : option}
                        </option>
                      ))}
                    </select>
                  ) : null}
                </div>
                <div className="text-right text-xs text-[#7F8A98]">
                  {language === 'zh'
                    ? `当前维度 ${scopeDescription}`
                    : `Current Scope ${scopeDescription}`}
                </div>
              </div>
            </div>

            <div className="mb-5 flex flex-wrap gap-3">
                <div className="rounded-full border border-[#F0B90B]/20 bg-[#F0B90B]/10 px-3 py-2 text-xs font-medium text-[#F6D782]">
                  {language === 'zh'
                    ? `观测维度 ${scopeDescription}`
                    : `Scope ${scopeDescription}`}
                </div>
                <div className="rounded-full border border-[#22D3EE]/20 bg-[#22D3EE]/10 px-3 py-2 text-xs font-medium text-[#9FEAF6]">
                  {language === 'zh'
                    ? `有效样本 ${scopeSampleCount} / ${scopeSampleTarget}${warmingUp ? ' · 预热中' : ' · 已接管'}`
                    : `Samples ${scopeSampleCount} / ${scopeSampleTarget}${warmingUp ? ' · Warming Up' : ' · Active'}`}
                </div>
                <div className="rounded-full border border-emerald-400/20 bg-emerald-400/10 px-3 py-2 text-xs font-medium text-emerald-200">
                  {currentScope.type === 'global'
                    ? language === 'zh'
                      ? `覆盖范围 全部赛道 / 样本 ${adaptiveState?.sample_count || 0}`
                      : `Coverage All Sectors / ${adaptiveState?.sample_count || 0}`
                    : language === 'zh'
                      ? `赛道 ${adaptiveState?.sector || focusSnapshot?.sector || '--'} / 样本 ${adaptiveState?.sector_sample_count || 0}`
                      : `Sector ${adaptiveState?.sector || focusSnapshot?.sector || '--'} / ${adaptiveState?.sector_sample_count || 0}`}
                </div>
                {currentScope.type === 'symbol' ? (
                  <div className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-2 text-xs font-medium text-[#D1D4DC]">
                    {language === 'zh'
                      ? `专属样本 ${adaptiveState?.coin_sample_count || 0} / ${coinSampleTarget} · α ${formatAlpha(adaptiveState?.alpha || 0)}`
                      : `Coin Samples ${adaptiveState?.coin_sample_count || 0} / ${coinSampleTarget} · α ${formatAlpha(adaptiveState?.alpha || 0)}`}
                  </div>
                ) : null}
                <div className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-2 text-xs font-medium text-[#D1D4DC]">
                  {language === 'zh'
                    ? `经验占比 ${((adaptiveState?.blend_adaptive || 0) * 100).toFixed(0)}%`
                    : `Adaptive Blend ${((adaptiveState?.blend_adaptive || 0) * 100).toFixed(0)}%`}
                </div>
            </div>

            <div className="grid gap-5 xl:grid-cols-[minmax(0,1.2fr)_minmax(320px,0.8fr)]">
              <div className="rounded-[24px] border border-white/10 bg-black/20 p-4 sm:p-5">
                <div className="mb-3 flex items-center justify-between">
                  <div>
                    <div className="text-sm font-semibold text-white">
                      {language === 'zh'
                        ? `权重雷达 · ${scopeDescription}`
                        : `Weight Radar · ${scopeDescription}`}
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
                <AdaptiveRadarChart
                  factors={adaptiveFactors}
                  language={language}
                  warningMessage={
                    symbolSampleInsufficient
                      ? language === 'zh'
                        ? `[! 样本不足] 正在参考 ${adaptiveState?.sector || focusSnapshot?.sector || '--'} 赛道均值进行决策`
                        : `[! Sample Thin] Referencing ${adaptiveState?.sector || focusSnapshot?.sector || '--'} sector mean for decisions`
                      : undefined
                  }
                />
              </div>

              <div className="rounded-[24px] border border-white/10 bg-black/20 p-4 sm:p-5">
                <div className="mb-4 flex items-center justify-between">
                  <div>
                    <div className="text-sm font-semibold text-white">
                      {language === 'zh' ? '因子状态' : 'Factor State'}
                    </div>
                    <div className="text-xs text-[#848E9C]">
                      {language === 'zh'
                        ? '实时 Spearman Rank IC 与最终权重'
                        : 'Live Spearman Rank IC and final weights'}
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
                      const groupConfig =
                        nestedFactorGroups[
                          factor.name as keyof typeof nestedFactorGroups
                        ]
                      const isExpandable = !!groupConfig
                      const isExpanded = !!expandedGroups[factor.name]
                      const groupWeights = groupConfig
                        ? normalizeNestedGroupWeights(
                            factor.name as keyof typeof nestedFactorGroups,
                            nestedWeights
                          )
                        : null
                      const groupWeightTotal = groupConfig
                        ? groupConfig.children.reduce(
                            (sum, child) => sum + (groupWeights?.[child.key] || 0),
                            0
                          )
                        : 0
                      const icColor =
                        factor.ic > 0
                          ? 'text-[#0ECB81]'
                          : factor.ic < 0
                            ? 'text-[#F6465D]'
                            : 'text-[#848E9C]'
                      const scopeICSummary =
                        currentScope.type === 'global'
                          ? language === 'zh'
                            ? `全局 Rank IC ${formatIC(factor.final_ic ?? factor.ic)}`
                            : `Global Rank IC ${formatIC(factor.final_ic ?? factor.ic)}`
                          : currentScope.type === 'sector'
                            ? language === 'zh'
                              ? `赛道 Rank IC ${formatIC(factor.sector_ic)}`
                              : `Sector Rank IC ${formatIC(factor.sector_ic)}`
                            : language === 'zh'
                              ? `赛道 Rank IC ${formatIC(factor.sector_ic)} · 币种 Rank IC ${formatIC(factor.coin_ic)}`
                              : `Sector Rank IC ${formatIC(factor.sector_ic)} · Coin Rank IC ${formatIC(factor.coin_ic)}`
                      return (
                        <div
                          key={factor.name}
                          className="rounded-2xl border border-white/10 bg-white/[0.03] px-4 py-3"
                        >
                          <div className="flex items-center justify-between gap-4">
                            <div>
                              <div className="flex items-center gap-2">
                                <div className="text-sm font-medium text-white">
                                  {factorLabel(factor.name, language)}
                                </div>
                                {isExpandable ? (
                                  <button
                                    type="button"
                                    onClick={() =>
                                      setExpandedGroups((current) => ({
                                        ...current,
                                        [factor.name]: !current[factor.name],
                                      }))
                                    }
                                    className="rounded-full border border-[#22D3EE]/20 bg-[#22D3EE]/10 px-2 py-0.5 text-[11px] font-medium text-[#9FEAF6] transition hover:border-[#22D3EE]/30 hover:bg-[#22D3EE]/15"
                                  >
                                    {language === 'zh'
                                      ? isExpanded
                                        ? '收起子项'
                                        : '展开子项'
                                      : isExpanded
                                        ? 'Hide Sub-factors'
                                        : 'Show Sub-factors'}
                                  </button>
                                ) : null}
                              </div>
                              <div className="mt-1 text-xs text-[#848E9C]">
                                {language === 'zh'
                                  ? `默认 ${formatWeight(factor.default_weight)} · 经验 ${formatWeight(factor.empirical_weight)}`
                                  : `Default ${formatWeight(factor.default_weight)} · Empirical ${formatWeight(factor.empirical_weight)}`}
                              </div>
                              <div className="mt-1 text-xs text-[#6EC6FF]">
                                {scopeICSummary}
                              </div>
                            </div>
                            <div className="text-right">
                              <div className={`text-sm font-semibold ${icColor}`}>
                                Spearman Rank IC {formatIC(factor.final_ic ?? factor.ic)}
                              </div>
                              <div className="mt-1 text-xs text-[#F0B90B]">
                                {language === 'zh'
                                  ? `当前权重 ${formatWeight(factor.final_weight)}`
                                  : `Weight ${formatWeight(factor.final_weight)}`}
                              </div>
                              {isExpandable && groupConfig && groupWeights ? (
                                <div className="mt-1 text-[11px] text-[#9CA7B4]">
                                  {language === 'zh'
                                    ? `内部已归一化，合计 ${formatWeight(groupWeightTotal)}`
                                    : `Nested normalized total ${formatWeight(groupWeightTotal)}`}
                                </div>
                              ) : null}
                            </div>
                          </div>
                          {isExpandable && isExpanded && groupConfig && groupWeights ? (
                            <div className="mt-3 border-l border-[#22D3EE]/20 pl-4">
                              <div className="mb-2 text-[11px] uppercase tracking-[0.22em] text-[#6B7787]">
                                {language === 'zh'
                                  ? groupConfig.title.zh
                                  : groupConfig.title.en}
                              </div>
                              <div className="space-y-2">
                                {groupConfig.children.map((child) => {
                                  const childState = resolveAdaptiveFactorState(
                                    adaptiveFactors,
                                    hiddenFactors,
                                    child.key
                                  )
                                  const subIC =
                                    childState?.final_ic ?? childState?.ic ?? NaN
                                  const subICColor =
                                    subIC > 0
                                      ? 'text-[#0ECB81]'
                                      : subIC < 0
                                        ? 'text-[#F6465D]'
                                        : 'text-[#848E9C]'
                                  return (
                                    <div
                                      key={child.key}
                                      className="rounded-xl border border-white/10 bg-[#0C1320]/55 px-3 py-3"
                                    >
                                      <div className="flex items-center justify-between gap-4">
                                        <div>
                                          <div className="text-xs font-medium text-[#DCE3EA]">
                                            {nestedFactorLabel(
                                              factor.name as keyof typeof nestedFactorGroups,
                                              child.key,
                                              language
                                            )}
                                          </div>
                                          <div className="mt-1 text-[11px] text-[#8E99A8]">
                                            {language === 'zh'
                                              ? '在主因子权重内部的归一化分配'
                                              : 'Normalized inside the parent factor bucket'}
                                          </div>
                                        </div>
                                        <div className="text-right">
                                          <div className={`text-xs font-semibold ${subICColor}`}>
                                            Spearman Rank IC {formatIC(subIC)}
                                          </div>
                                          <div className="mt-1 text-[11px] text-[#9FEAF6]">
                                            {language === 'zh'
                                              ? `内部权重 ${formatWeight(groupWeights[child.key] || 0)}`
                                              : `Nested Weight ${formatWeight(groupWeights[child.key] || 0)}`}
                                          </div>
                                        </div>
                                      </div>
                                    </div>
                                  )
                                })}
                              </div>
                              <div className="mt-2 text-[11px] text-[#7F8A98]">
                                {language === 'zh'
                                  ? `子权重合计 ${formatWeight(groupWeightTotal)} · 在 ${factorLabel(factor.name, language)} 主权重 ${formatWeight(factor.final_weight)} 内部瓜分`
                                  : `Nested total ${formatWeight(groupWeightTotal)} · Split inside the ${factorLabel(factor.name, language)} weight ${formatWeight(factor.final_weight)}`}
                              </div>
                            </div>
                          ) : null}
                        </div>
                      )
                    })
                  )}
                </div>
              </div>
            </div>
          </div>

          <div className="border-b border-white/10 px-6 py-6 sm:px-8">
            <div className="mb-5 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
              <div>
                <p className="text-xs uppercase tracking-[0.28em] text-[#F59E0B]/80">
                  {language === 'zh'
                    ? 'Score Binning Analytics'
                    : 'Score Binning Analytics'}
                </p>
                <h2 className="mt-2 text-2xl font-semibold text-white">
                  {language === 'zh' ? '分数分箱表现' : 'Score Bin Performance'}
                </h2>
                <p className="mt-2 max-w-3xl text-sm leading-6 text-[#B7BDC6]">
                  {language === 'zh'
                    ? 'Filled 影子样本会按 1 分步长做滑动窗口统计，每个点都使用中心分数附近的重叠样本来生成更连续的 EV 趋势。开启实时权重重算后，会用当前自适应权重回溯重算历史原始因子的逻辑分数。'
                    : 'Filled shadow samples are now evaluated with a 1-point sliding window so each point blends nearby scores into a continuous EV curve. Real-time back-cast rescored historical raw factors with the current adaptive weights.'}
                </p>
              </div>
              <div className="flex flex-wrap items-center gap-3">
                <label className="inline-flex items-center gap-3 rounded-full border border-[#22D3EE]/20 bg-[#22D3EE]/10 px-3 py-2 text-xs font-medium text-[#9FEAF6]">
                  <input
                    type="checkbox"
                    checked={realtimeBackcast}
                    onChange={(e) => setRealtimeBackcast(e.target.checked)}
                    className="h-4 w-4 rounded border-white/20 bg-transparent accent-[#22D3EE]"
                  />
                  <span>
                    {language === 'zh'
                      ? '实时权重重算'
                      : 'Real-time Back-cast'}
                  </span>
                </label>
                <div className="inline-flex rounded-full border border-white/10 bg-black/20 p-1">
                  {([5, 10] as const).map((windowSize) => {
                    const active = performanceWindowSize === windowSize
                    const label =
                      windowSize === 10
                        ? language === 'zh'
                          ? '宽窗 ±5'
                          : 'Wide ±5'
                        : language === 'zh'
                          ? '标准 ±2.5'
                          : 'Std ±2.5'
                    return (
                      <button
                        key={windowSize}
                        type="button"
                        onClick={() => setPerformanceWindowSize(windowSize)}
                        className="rounded-full px-4 py-2 text-xs font-semibold tracking-[0.18em] transition"
                        style={{
                          background: active ? 'rgba(245, 158, 11, 0.18)' : 'transparent',
                          color: active ? '#FDE68A' : '#94A3B8',
                        }}
                      >
                        {label}
                      </button>
                    )
                  })}
                </div>
                <div className="rounded-full border border-[#F59E0B]/20 bg-[#F59E0B]/10 px-3 py-2 text-xs font-medium text-[#FDE68A]">
                  {language === 'zh'
                    ? `${scopeDescription} · ${performanceModeLabel} · ${performanceBinRows.length} 个滑窗点`
                    : `${scopeDescription} · ${performanceModeLabel} · ${performanceBinRows.length} points`}
                </div>
                <div className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-2 text-xs font-medium text-[#D1D4DC]">
                  {language === 'zh'
                    ? `峰值窗口 N ${peakWindowTradeCount}`
                    : `Peak Window N ${peakWindowTradeCount}`}
                </div>
                <div className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-2 text-xs font-medium text-[#D1D4DC]">
                  {language === 'zh'
                    ? `平滑强度 ${smoothingWindowLabel}`
                    : `Smoothing ${smoothingWindowLabel}`}
                </div>
              </div>
            </div>

            <div className="rounded-[24px] border border-white/10 bg-black/20 p-4 sm:p-5">
              <div className="mb-3 flex items-center justify-between">
                <div>
                  <div className="text-sm font-semibold text-white">
                    {language === 'zh'
                      ? `分箱表现 · ${scopeDescription} · ${performanceModeLabel}`
                      : `Performance Bins · ${scopeDescription} · ${performanceModeLabel}`}
                  </div>
                  <div className="text-xs text-[#848E9C]">
                    {language === 'zh'
                      ? realtimeBackcast
                        ? '当前模式会用最新自适应权重重算历史原始因子，并以 1 分步长滑动窗口生成平滑 EV 曲线。Y 轴只参考 N>10 的稳健区间，稀疏样本会被截断并自动淡化。'
                        : '图表已切到滑动窗口统计。每个分数点都会参考附近样本；Y 轴只参考 N>10 的稳健 EV 区间。金柱/蓝柱代表 PF，金实线/蓝实线代表 Mean EV，Tooltip 同时显示 Median EV 与 Weighted_Rank；红虚线是 EV=0，灰虚线是 PF=1.0。'
                      : realtimeBackcast
                        ? 'This mode rescored historical raw factors with the latest adaptive weights and rebuilds the EV curve with a 1-point sliding window. The EV axis follows the robust N>10 range, while sparse samples are clipped and faded.'
                        : 'The chart now uses a sliding window, so each score also references nearby samples. The EV axis follows the robust N>10 range. Amber/blue bars show PF, amber/blue solid lines show mean EV, the tooltip also includes median EV and Weighted_Rank, the red dashed line marks EV=0, and the gray dashed line marks PF=1.0.'}
                  </div>
                </div>
                {performanceBinsError ? (
                  <span className="text-sm text-[#F6465D]">
                    {language === 'zh' ? '加载失败' : 'Load failed'}
                  </span>
                ) : null}
              </div>

              {performanceBinsLoading ? (
                <div className="rounded-2xl border border-dashed border-white/10 px-4 py-12 text-center text-sm text-[#848E9C]">
                  {language === 'zh'
                    ? realtimeBackcast
                      ? '实时重算分箱加载中...'
                      : '分箱表现加载中...'
                    : realtimeBackcast
                      ? 'Loading real-time back-cast bins...'
                      : 'Loading score bin performance...'}
                </div>
              ) : (
                <ScoreBinPerformanceChart
                  data={performanceBinRows}
                  language={language}
                  realtimeBackcast={realtimeBackcast}
                  windowSize={performanceWindowSize}
                />
              )}
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
