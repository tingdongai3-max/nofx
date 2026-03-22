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

function formatPrice(value: number) {
  if (!Number.isFinite(value)) return '--'
  if (value >= 1000) return value.toFixed(2)
  if (value >= 1) return value.toFixed(4)
  return value.toFixed(6)
}

function formatNumber(value?: number) {
  if (value == null || !Number.isFinite(value)) return '--'
  if (Math.abs(value) >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (Math.abs(value) >= 1_000) return `${(value / 1_000).toFixed(2)}K`
  return value.toFixed(2)
}

function emaStateLabel(
  state: CandidateMarketItem['ema_signals']['state'],
  language: Language
) {
  const zhMap = {
    bullish_stack: '📈 多头排列 (强趋势)',
    bearish_stack: '📉 空头排列 (弱趋势)',
    mixed: '🔀 趋势混沌 (横盘)',
    single_ema: '➖ 单均线参考',
    unavailable: '⚪ 暂无信号',
  }
  const enMap = {
    bullish_stack: 'Bullish Stack',
    bearish_stack: 'Bearish Stack',
    mixed: 'Mixed',
    single_ema: 'Single EMA',
    unavailable: 'Unavailable',
  }
  return language === 'zh' ? zhMap[state] : enMap[state]
}

function donchianStateLabel(state?: string, language?: Language) {
  if (!state) return '--'
  if (language !== 'zh') return state

  const map: Record<string, string> = {
    'Price is currently above the upper bound': '🚀 向上突破',
    'Price is currently below the lower bound': '📉 向下破位',
    'Price is within 2% range of the upper bound': '⚡ 接近压力位',
    'Price is within 2% range of the lower bound': '🛡️ 接近支撑位',
    'Price is currently between the upper and lower bounds': '↔️ 箱体震荡',
  }
  return map[state] || state
}

function sortedDonchianBoxes(item: CandidateMarketItem) {
  const entries = Object.entries(item.donchian_boxes || {})
    .map(([period, box]) => ({ ...box, period: Number(period) }))
    .filter((box) => Number.isFinite(box.period))
    .sort((a, b) => a.period - b.period)
  return entries
}

function primaryDonchianBox(item: CandidateMarketItem) {
  const boxes = sortedDonchianBoxes(item)
  return boxes[0]
}

function donchianSegments(item: CandidateMarketItem, language: Language) {
  const boxes = sortedDonchianBoxes(item)
  if (boxes.length === 0) return []

  const picks = [
    { key: 'short', label: language === 'zh' ? '短' : 'Short', box: boxes[0] },
    {
      key: 'mid',
      label: language === 'zh' ? '中' : 'Mid',
      box: boxes[Math.floor(boxes.length / 2)],
    },
    {
      key: 'long',
      label: language === 'zh' ? '长' : 'Long',
      box: boxes[boxes.length - 1],
    },
  ]

  const seen = new Set<number>()
  return picks
    .filter(({ box }) => {
      if (!box || seen.has(box.period)) return false
      seen.add(box.period)
      return true
    })
    .map(({ key, label, box }) => ({
      key,
      label,
      period: box.period,
      stateLabel: donchianStateLabel(box.state, language),
    }))
}

function preferredTrendContext(item: CandidateMarketItem) {
  const candidates = ['1h', '4h', '15m']
  for (const tf of candidates) {
    const ctx = item.trend_contexts?.[tf]
    if (ctx) return ctx
  }

  const fallbackKey = Object.keys(item.trend_contexts || {}).sort()[0]
  return fallbackKey ? item.trend_contexts?.[fallbackKey] : undefined
}

function oiTone(item: CandidateMarketItem) {
  const latest = item.open_interest?.Latest
  const average = item.open_interest?.Average
  if (!latest || !average || average <= 0) return null
  return latest > average * 1.1 ? 'up' : null
}

function proximityTone(item: CandidateMarketItem) {
  const box = primaryDonchianBox(item)
  if (!box) return {}
  const width = box.upper - box.lower
  if (width <= 0) return {}
  const upperGap = Math.abs(box.upper - item.current_price) / width
  const lowerGap = Math.abs(item.current_price - box.lower) / width
  if (item.current_price >= box.upper || upperGap <= 0.02) {
    return {
      background: 'rgba(14, 203, 129, 0.12)',
      borderColor: 'rgba(14, 203, 129, 0.35)',
    }
  }
  if (item.current_price <= box.lower || lowerGap <= 0.02) {
    return {
      background: 'rgba(246, 70, 93, 0.12)',
      borderColor: 'rgba(246, 70, 93, 0.35)',
    }
  }
  return {}
}

function ai500Label(item: CandidateMarketItem) {
  if (item.ai500_score != null) {
    return item.ai500_score.toFixed(2)
  }
  if ((item.sources || []).includes('ai500')) {
    return 'AI500'
  }
  return '--'
}

function depthLabel(item: CandidateMarketItem, language: Language) {
  const imbalance = item.orderbook?.imbalance
  if (imbalance == null || !Number.isFinite(imbalance)) return '--'
  if (imbalance > 0.1)
    return language === 'zh' ? '🟢 买盘占优' : '🟢 Buy Depth Dominant'
  if (imbalance < -0.1)
    return language === 'zh' ? '🔴 卖盘占优' : '🔴 Sell Depth Dominant'
  return language === 'zh' ? '⚪ 挂单均衡' : '⚪ Balanced Depth'
}

function depthPercent(item: CandidateMarketItem) {
  const imbalance = item.orderbook?.imbalance
  if (imbalance == null || !Number.isFinite(imbalance)) return '--'
  return `${imbalance >= 0 ? '+' : ''}${(imbalance * 100).toFixed(1)}%`
}

function depthBarStyle(item: CandidateMarketItem) {
  const bidTotal = item.orderbook?.bid_total || 0
  const askTotal = item.orderbook?.ask_total || 0
  const total = bidTotal + askTotal
  const bidShare = total > 0 ? (bidTotal / total) * 100 : 50
  const askShare = total > 0 ? (askTotal / total) * 100 : 50
  return {
    background: `linear-gradient(90deg, rgba(14, 203, 129, 0.75) 0%, rgba(14, 203, 129, 0.75) ${bidShare}%, rgba(246, 70, 93, 0.75) ${bidShare}%, rgba(246, 70, 93, 0.75) ${bidShare + askShare}%)`,
  }
}

function efficiencyLabel(item: CandidateMarketItem, language: Language) {
  const vu = item.vol_utilization
  if (vu == null || !Number.isFinite(vu)) return '--'
  if (vu > 0.8)
    return language === 'zh' ? '🚀 趋势高利用' : '🚀 High Efficiency'
  if (vu >= 0.4) return language === 'zh' ? '🔄 正常波动' : '🔄 Normal Movement'
  return language === 'zh' ? '⚠️ 无效磨损' : '⚠️ Inefficient Movement'
}

function efficiencyPercent(item: CandidateMarketItem) {
  const vu = item.vol_utilization
  if (vu == null || !Number.isFinite(vu)) return '--'
  return `${(vu * 100).toFixed(1)}%`
}

function efficiencyTone(item: CandidateMarketItem) {
  const vu = item.vol_utilization
  if (vu == null || !Number.isFinite(vu)) return 'text-nofx-text-main'
  if (vu > 0.8) return 'text-[#0ECB81] font-semibold'
  if (vu >= 0.4) return 'text-nofx-text-main'
  return 'text-[#F0B90B] font-semibold'
}

function compactMacdStateLabel(state?: string, language?: Language) {
  if (!state) return '--'
  if (language !== 'zh') return state
  if (state === 'Bullish') return 'MACD 偏多'
  if (state === 'Bearish') return 'MACD 偏空'
  return 'MACD 中性'
}

function heatLabel(item: CandidateMarketItem, language: Language) {
  const score = item.heat_score?.composite_score
  if (score == null || !Number.isFinite(score)) return '--'
  if (score > 80) return language === 'zh' ? '🔥 极度狂热' : '🔥 Extreme Heat'
  if (score >= 60)
    return language === 'zh' ? '🟠 资金涌入' : '🟠 Capital Inflow'
  if (score >= 40) return language === 'zh' ? '🟢 正常热度' : '🟢 Normal Heat'
  return language === 'zh' ? '❄️ 冷清' : '❄️ Cold'
}

function heatPercent(item: CandidateMarketItem) {
  const score = item.heat_score?.composite_score
  if (score == null || !Number.isFinite(score)) return '--'
  return `${score.toFixed(1)} / 100`
}

function heatTone(item: CandidateMarketItem) {
  const score = item.heat_score?.composite_score
  if (score == null || !Number.isFinite(score)) {
    return {
      labelClass: 'text-nofx-text-main',
      fillColor: 'rgba(148, 163, 184, 0.7)',
      width: '0%',
    }
  }
  if (score > 80) {
    return {
      labelClass: 'text-[#F6465D] font-semibold',
      fillColor: 'rgba(246, 70, 93, 0.8)',
      width: `${score}%`,
    }
  }
  if (score >= 60) {
    return {
      labelClass: 'text-[#F0B90B] font-semibold',
      fillColor: 'rgba(240, 185, 11, 0.8)',
      width: `${score}%`,
    }
  }
  if (score >= 40) {
    return {
      labelClass: 'text-[#0ECB81] font-semibold',
      fillColor: 'rgba(14, 203, 129, 0.8)',
      width: `${score}%`,
    }
  }
  return {
    labelClass: 'text-[#94A3B8] font-semibold',
    fillColor: 'rgba(148, 163, 184, 0.7)',
    width: `${score}%`,
  }
}

function heatSourceTags(item: CandidateMarketItem, language: Language) {
  const weights = item.heat_score?.source_weights || {}
  const dexTooltip =
    item.dex_screener?.volume_h1 != null && Number.isFinite(item.dex_screener.volume_h1)
      ? `${language === 'zh' ? 'DEX 1h 成交量' : 'DEX Vol 1h'}: ${formatNumber(item.dex_screener.volume_h1)}`
      : undefined
  const publicTooltip =
    item.gecko_sentiment &&
    (Number.isFinite(item.gecko_sentiment.public_interest_score) ||
      Number.isFinite(item.gecko_sentiment.sentiment_votes_up_percentage))
      ? `${
          language === 'zh' ? '公众关注度' : 'Public Interest'
        }: ${item.gecko_sentiment.public_interest_score?.toFixed(2) ?? '--'} | ${
          language === 'zh' ? '看多比例' : 'Bullish Votes'
        }: ${item.gecko_sentiment.sentiment_votes_up_percentage?.toFixed(1) ?? '--'}%`
      : undefined
  const tags: Array<{ label: string; key: string; active: boolean; title?: string }> = []
  if ((weights.market || 0) + (weights.trend || 0) > 0) {
    tags.push({
      key: 'market',
      label: language === 'zh' ? '行情' : 'Market',
      active: true,
    })
  }
  if ((weights.quant || 0) > 0) {
    tags.push({
      key: 'quant',
      label: language === 'zh' ? '量化' : 'Quant',
      active: true,
    })
  }
  tags.push({
    key: 'social',
    label:
      (item.gecko_sentiment?.using_private_key ?? false)
        ? language === 'zh'
          ? '✦ 公众'
          : '✦ Public'
        : language === 'zh'
          ? '公众'
          : 'Public',
    active: (weights.social || 0) > 0,
    title: publicTooltip,
  })
  tags.push({
    key: 'onchain',
    label: language === 'zh' ? '链上' : 'OnChain',
    active: (weights.onchain || 0) > 0,
    title: dexTooltip,
  })
  return tags
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
    language === 'zh' ? '候选币实时看板' : 'Candidate Coins Live Dashboard'
  const subtitle =
    language === 'zh'
      ? '展示交易员最近一轮实际扫描并发送给 AI 的候选币指标快照。'
      : 'Shows the latest candidate snapshot from the trader cycle that was actually sent to AI.'

  return (
    <DeepVoidBackground className="min-h-screen pb-12" disableAnimation>
      <div className="w-full px-4 md:px-8 relative z-10 pt-6">
        <div className="nofx-glass rounded-2xl p-6 mb-6">
          <div className="flex flex-col lg:flex-row lg:items-end lg:justify-between gap-4">
            <div>
              <h1 className="text-2xl md:text-3xl font-semibold text-nofx-text-main">
                {title}
              </h1>
              <p className="text-sm mt-2 text-nofx-text-muted">{subtitle}</p>
            </div>
            <div className="flex flex-col gap-2 min-w-[260px]">
              <label className="text-xs uppercase tracking-[0.16em] text-nofx-text-muted">
                {language === 'zh' ? '交易员' : 'Trader'}
              </label>
              <select
                value={selectedTraderId || ''}
                onChange={(e) => onTraderSelect(e.target.value)}
                className="rounded-xl px-4 py-3 bg-[#131722] border border-white/10 text-nofx-text-main outline-none"
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
          <div className="nofx-glass rounded-2xl overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full min-w-[1280px]">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-[0.14em] text-nofx-text-muted border-b border-white/10">
                    <th className="px-4 py-4">
                      {language === 'zh' ? '币种' : 'Symbol'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? '现价' : 'Price'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? '周期' : 'Timeframes'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? 'Donchian 盒子' : 'Donchian Box'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? 'EMA 状态' : 'EMA State'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? 'OI' : 'Open Interest'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? '深度' : 'Depth'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? '利用率' : 'Efficiency'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? '热度' : 'Heat'}
                    </th>
                    <th className="px-4 py-4">
                      {language === 'zh' ? 'AI500' : 'AI500'}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {data.candidates.map((item) => (
                    <tr
                      key={item.symbol}
                      className="border-b border-white/5"
                      style={proximityTone(item)}
                    >
                      <td className="px-4 py-4">
                        <div className="font-semibold text-nofx-text-main">
                          {item.symbol}
                        </div>
                        <div className="text-xs text-nofx-text-muted">
                          {(item.sources || []).join(', ') || '--'}
                        </div>
                      </td>
                      <td className="px-4 py-4 text-nofx-text-main">
                        {formatPrice(item.current_price)}
                      </td>
                      <td className="px-4 py-4 text-nofx-text-muted">
                        {item.timeframes.join(', ') || '--'}
                      </td>
                      <td className="px-4 py-4">
                        {donchianSegments(item, language).length > 0 ? (
                          <div className="flex flex-wrap gap-2">
                            {donchianSegments(item, language).map((segment) => (
                              <span
                                key={segment.key}
                                className="inline-flex items-center gap-1 rounded-full border border-white/10 bg-white/5 px-3 py-1 text-sm font-semibold text-nofx-text-main"
                              >
                                <span className="text-xs text-nofx-text-muted">
                                  {segment.label}:
                                </span>
                                <span>{segment.stateLabel}</span>
                                <span className="text-xs text-nofx-text-muted">
                                  P{segment.period}
                                </span>
                              </span>
                            ))}
                          </div>
                        ) : (
                          <span className="text-nofx-text-muted">--</span>
                        )}
                        {preferredTrendContext(item)?.donchian_state ? (
                          <div className="mt-2 text-xs text-nofx-text-muted">
                            {preferredTrendContext(item)?.timeframe}:{' '}
                            {donchianStateLabel(
                              preferredTrendContext(item)?.donchian_state,
                              language
                            )}
                          </div>
                        ) : null}
                      </td>
                      <td className="px-4 py-4">
                        <div className="font-semibold text-nofx-text-main">
                          {emaStateLabel(item.ema_signals.state, language)}
                        </div>
                        {preferredTrendContext(item) ? (
                          <div className="mt-2 text-xs text-nofx-text-muted space-y-1">
                            <div>
                              {preferredTrendContext(item)?.timeframe}:{' '}
                              {emaStateLabel(
                                (preferredTrendContext(item)
                                  ?.ema_state as CandidateMarketItem['ema_signals']['state']) ||
                                  'unavailable',
                                language
                              )}
                            </div>
                            <div>
                              RSI:{' '}
                              {preferredTrendContext(item)?.rsi != null
                                ? preferredTrendContext(item)?.rsi?.toFixed(1)
                                : '--'}{' '}
                              |{' '}
                              {compactMacdStateLabel(
                                preferredTrendContext(item)?.macd_state,
                                language
                              )}
                            </div>
                          </div>
                        ) : null}
                      </td>
                      <td className="px-4 py-4">
                        {oiTone(item) === 'up' && (
                          <div className="mb-1 text-xs font-semibold text-[#0ECB81]">
                            {language === 'zh'
                              ? '↑ 高于均值 10%+'
                              : '↑ Above average by 10%+'}
                          </div>
                        )}
                        <div className="text-nofx-text-main">
                          {language === 'zh' ? '当前' : 'Current'}:{' '}
                          {formatNumber(item.open_interest?.Latest)}
                        </div>
                        <div className="text-xs text-nofx-text-muted">
                          {language === 'zh' ? '平均' : 'Average'}:{' '}
                          {formatNumber(item.open_interest?.Average)}
                        </div>
                        {item.open_interest?.sample_count ? (
                          <div className="text-xs text-nofx-text-muted">
                            {language === 'zh'
                              ? `样本: ${item.open_interest.sample_count} x ${item.open_interest.period || '--'}`
                              : `Samples: ${item.open_interest.sample_count} x ${item.open_interest.period || '--'}`}
                          </div>
                        ) : null}
                      </td>
                      <td className="px-4 py-4 min-w-[220px]">
                        <div className="font-semibold text-nofx-text-main">
                          {depthLabel(item, language)}
                        </div>
                        <div className="text-xs text-nofx-text-muted mt-1">
                          {depthPercent(item)}
                        </div>
                        <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-white/10">
                          <div
                            className="h-full w-full"
                            style={depthBarStyle(item)}
                          />
                        </div>
                      </td>
                      <td className="px-4 py-4 min-w-[180px]">
                        <div className={efficiencyTone(item)}>
                          {efficiencyLabel(item, language)}
                        </div>
                        <div className="text-xs text-nofx-text-muted mt-1">
                          {efficiencyPercent(item)}
                        </div>
                        <div className="text-xs text-nofx-text-muted mt-1">
                          {language === 'zh'
                            ? `基于 ${item.vol_util_basis || '--'}`
                            : `Based on ${item.vol_util_basis || '--'}`}
                        </div>
                      </td>
                      <td className="px-4 py-4 min-w-[220px]">
                        <div className={heatTone(item).labelClass}>
                          {heatLabel(item, language)}
                        </div>
                        <div className="text-xs text-nofx-text-muted mt-1">
                          {heatPercent(item)}
                        </div>
                        <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-white/10">
                          <div
                            className="h-full rounded-full"
                            style={{
                              width: heatTone(item).width,
                              background: heatTone(item).fillColor,
                            }}
                          />
                        </div>
                        <div className="text-xs text-nofx-text-muted mt-1">
                          {language === 'zh'
                            ? '基于 24h rolling window'
                            : 'Heat based on 24h rolling window'}
                        </div>
                        <div className="mt-2 flex flex-wrap gap-1">
                          {heatSourceTags(item, language).map((tag) => (
                            <span
                              key={tag.key}
                              title={tag.title}
                              className={`inline-flex items-center rounded-full border px-2 py-0.5 text-[10px] ${
                                tag.active
                                  ? 'border-[#0ECB81]/40 bg-[#0ECB81]/12 text-[#B6F5D8]'
                                  : 'border-white/10 bg-white/5 text-nofx-text-muted'
                              }`}
                            >
                              {tag.label}
                            </span>
                          ))}
                        </div>
                      </td>
                      <td className="px-4 py-4 text-nofx-text-main">
                        {ai500Label(item)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {selectedTrader && (
          <div className="mt-4 text-xs text-nofx-text-muted">
            {language === 'zh'
              ? `当前交易员：${selectedTrader.trader_name}。表格数据来自最近一次候选币扫描快照。`
              : `Current trader: ${selectedTrader.trader_name}. Table data comes from the latest candidate-scan snapshot.`}
          </div>
        )}
      </div>
    </DeepVoidBackground>
  )
}
