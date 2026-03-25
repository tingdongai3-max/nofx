import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { Activity, Crosshair, ShieldAlert, TimerReset, Zap } from 'lucide-react'
import type { Language } from '../i18n/translations'
import type {
  RealFireAttribution,
  RealFireAttributionDimension,
  RealBacktestLogEntry,
  RealBacktestDimension,
  RealBacktestMonitorPosition,
  RealBacktestMonitorResponse,
} from '../types'
import { api } from '../lib/api'
import { DeepVoidBackground } from '../components/common/DeepVoidBackground'
import { RealFireAttributionRadar } from '../components/charts/RealFireAttributionRadar'

interface RealLiveMonitorProps {
  language: Language
}

function tMonitor(language: Language, zh: string, en: string) {
  return language === 'zh' ? zh : en
}

function formatCurrency(value: number) {
  if (!Number.isFinite(value)) return '--'
  return `$${value.toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`
}

function formatNumber(value: number, digits = 2) {
  if (!Number.isFinite(value)) return '--'
  return value.toFixed(digits)
}

function formatSignedPct(value: number | undefined) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return `${value >= 0 ? '+' : ''}${value.toFixed(2)}%`
}

function formatSignedEV(value: number | undefined) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return `${value >= 0 ? '+' : ''}${(value * 100).toFixed(2)}%`
}

function optionalSignedEV(value?: number) {
  return typeof value === 'number' ? formatSignedEV(value) : '--'
}

const MAHALANOBIS_THRESHOLD = 2.0

function normalizeAttributionWeights(raw?: Record<string, number>) {
  const weights = {
    global: 1 / 3,
    sector: 1 / 3,
    symbol: 1 / 3,
  }
  if (!raw) {
    return weights
  }

  const global = raw.global ?? raw.macro ?? raw.macro_weight ?? weights.global
  const sector = raw.sector ?? weights.sector
  const symbol = raw.symbol ?? raw.micro ?? weights.symbol
  const total = [global, sector, symbol].reduce((sum, value) => (Number.isFinite(value) && value > 0 ? sum + value : sum), 0)
  if (total <= 0) {
    return weights
  }

  weights.global = Math.max(0, Number.isFinite(global) ? global : 0) / total
  weights.sector = Math.max(0, Number.isFinite(sector) ? sector : 0) / total
  weights.symbol = Math.max(0, Number.isFinite(symbol) ? symbol : 0) / total
  return weights
}

function resolveEntryDecisionWeights(
  log: RealBacktestLogEntry,
  attribution: RealFireAttribution | undefined
) {
  if (log.attribution_weights) {
    return normalizeAttributionWeights(log.attribution_weights)
  }

  const raw: Record<string, number> = {}
  for (const dimension of attribution?.dimensions || []) {
    if (dimension.name === 'global') raw.global = dimension.real_weight
    if (dimension.name === 'sector') raw.sector = dimension.real_weight
    if (dimension.name === 'symbol') raw.symbol = dimension.real_weight
  }
  return normalizeAttributionWeights(raw)
}

function formatEntryDecisionSummary(
  log: RealBacktestLogEntry,
  attribution: RealFireAttribution | undefined,
  language: Language
) {
  const weights = resolveEntryDecisionWeights(log, attribution)
  const finalEV =
    typeof log.final_entry_ev === 'number' && Number.isFinite(log.final_entry_ev)
      ? log.final_entry_ev
      : (log.global_ev ?? 0) * weights.global + (log.sector_ev ?? 0) * weights.sector + (log.symbol_ev ?? 0) * weights.symbol

  const globalContrib = (log.global_ev ?? 0) * weights.global
  const sectorContrib = (log.sector_ev ?? 0) * weights.sector
  const symbolContrib = (log.symbol_ev ?? 0) * weights.symbol
  const ignored = weights.symbol < 0.1 && symbolContrib < 0 ? ' (Ignored)' : ''
  const triggerLine = resolveEntryTriggerLabel(weights, language)
  const mahalanobisDistance = typeof log.mahalanobis_distance === 'number' ? log.mahalanobis_distance : undefined
  const mahalanobisThreshold = MAHALANOBIS_THRESHOLD
  const mahalanobisResonant = typeof mahalanobisDistance === 'number' ? mahalanobisDistance <= mahalanobisThreshold : false
  const mahalanobisLabel = mahalanobisResonant
    ? language === 'zh'
      ? '基因匹配'
      : 'Resonant'
    : language === 'zh'
      ? '异形屏蔽'
      : 'Outlier'
  const mahalanobisLine = typeof mahalanobisDistance === 'number'
    ? `${language === 'zh' ? '马氏 D' : 'Mahalanobis D'} ${mahalanobisDistance.toFixed(2)} / ${mahalanobisThreshold.toFixed(2)} · ${mahalanobisLabel}${log.feature_vector_focus ? ` · ${featureVectorLabel(log.feature_vector_focus, language)}${typeof log.feature_vector_deviation === 'number' ? ` ${log.feature_vector_deviation >= 0 ? '+' : ''}${log.feature_vector_deviation.toFixed(2)}` : ''}` : ''}`
    : ''
  const gateLine = typeof log.entry_allowed === 'boolean'
    ? `${language === 'zh' ? '开火许可' : 'Fire Permit'} ${log.entry_allowed ? (language === 'zh' ? '许可' : 'Allowed') : (language === 'zh' ? '结构性禁入' : 'Structural Block')}${log.entry_block_reason ? ` · ${log.entry_block_reason}` : ''}`
    : ''

  return {
    finalLine: `Entry Decision: Final EV ${formatSignedEV(finalEV)}`,
    weightLine: `Weight Mix: Macro ${(weights.global * 100).toFixed(1)}% | Sector ${(weights.sector * 100).toFixed(1)}% | Symbol ${(weights.symbol * 100).toFixed(1)}%`,
    contribLine: `Contrib: Macro ${formatSignedEV(globalContrib)} | Sector ${formatSignedEV(sectorContrib)} | Symbol Bias ${formatSignedEV(symbolContrib)}${ignored}`,
    triggerLine,
    mahalanobisLine,
    gateLine,
  }
}

function formatMonitorTimestamp(value: number, language: Language) {
  if (!value) return '--'
  return new Date(value).toLocaleString(language === 'zh' ? 'zh-CN' : 'en-US', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function formatCountdown(targetMs: number, nowMs: number) {
  if (!targetMs) return '--'
  const remaining = Math.max(0, Math.ceil((targetMs - nowMs) / 1000))
  const minutes = Math.floor(remaining / 60)
  const seconds = remaining % 60
  return `${minutes.toString().padStart(2, '0')}:${seconds.toString().padStart(2, '0')}`
}

function summarizeAction(action: string, language: Language) {
  switch (action) {
    case 'open_long':
      return language === 'zh' ? '开多' : 'Open Long'
    case 'open_short':
      return language === 'zh' ? '开空' : 'Open Short'
    case 'close_long':
      return language === 'zh' ? '平多' : 'Close Long'
    case 'close_short':
      return language === 'zh' ? '平空' : 'Close Short'
    default:
      return action
  }
}

function formatSide(side: string, language: Language) {
  switch (side.toLowerCase()) {
    case 'long':
      return language === 'zh' ? '多头' : 'LONG'
    case 'short':
      return language === 'zh' ? '空头' : 'SHORT'
    default:
      return side
  }
}

function formatSignal(signal: string | undefined, language: Language) {
  if (!signal) return '--'
  switch (signal.toUpperCase()) {
    case 'LONG':
      return language === 'zh' ? '做多' : 'LONG'
    case 'SHORT':
      return language === 'zh' ? '做空' : 'SHORT'
    case 'NO_SIGNAL':
      return language === 'zh' ? '无信号' : 'NO_SIGNAL'
    default:
      return signal
  }
}

function formatModeLabel(armed: boolean | undefined, language: Language) {
  if (armed) {
    return language === 'zh' ? '已挂弹 / 运行中' : 'ARMED'
  }
  return language === 'zh' ? '未挂弹 / 待机' : 'DORMANT'
}

type MonitorDirection = 'long' | 'short'

function normalizeMonitorDirection(side: string): MonitorDirection {
  return side.toLowerCase() === 'short' ? 'short' : 'long'
}

function oppositeMonitorDirection(direction: MonitorDirection): MonitorDirection {
  return direction === 'short' ? 'long' : 'short'
}

function formatMonitorDirection(direction: MonitorDirection, language: Language) {
  if (language === 'zh') {
    return direction === 'short' ? '空头' : '多头'
  }
  return direction === 'short' ? 'SHORT' : 'LONG'
}

function formatCurrentOppositeLabel(kind: 'current' | 'opposite', language: Language) {
  if (language === 'zh') {
    return kind === 'current' ? '当前方向' : '反方向'
  }
  return kind === 'current' ? 'Current' : 'Opposite'
}

function resolveOppositeDirectionalValues(
  dimension: RealBacktestDimension | undefined,
  direction: MonitorDirection
) {
  if (!dimension) {
    return { mean: undefined, median: undefined }
  }

  const oppositeDirection = oppositeMonitorDirection(direction)
  if (oppositeDirection === 'short') {
    return {
      mean: dimension.expected_value_short,
      median: dimension.median_expected_value_short,
    }
  }
  return {
    mean: dimension.expected_value_long,
    median: dimension.median_expected_value_long,
  }
}

interface DirectionalEvCardProps {
  title: string
  currentDirection: MonitorDirection
  currentMean: number | undefined
  currentMedian: number | undefined
  oppositeMean: number | undefined
  oppositeMedian: number | undefined
  language: Language
}

function DirectionalEvCard({
  title,
  currentDirection,
  currentMean,
  currentMedian,
  oppositeMean,
  oppositeMedian,
  language,
}: DirectionalEvCardProps) {
  const oppositeDirection = oppositeMonitorDirection(currentDirection)
  return (
    <div className="rounded-xl border border-zinc-800 bg-black/30 px-3 py-3">
      <div className="flex items-center justify-between gap-2">
        <div className="text-zinc-500">{title}</div>
        <div className="rounded-full border border-emerald-400/20 bg-emerald-500/10 px-2 py-0.5 text-[10px] font-semibold tracking-[0.18em] text-emerald-200/80">
          {language === 'zh' ? '净 EV' : 'Net EV'}
        </div>
      </div>
      <div className="mt-2 grid gap-2 sm:grid-cols-2">
        <div className="rounded-lg border border-emerald-400/20 bg-emerald-500/10 px-3 py-2">
          <div className="text-[11px] font-semibold tracking-[0.12em] text-emerald-200/90">
            {formatCurrentOppositeLabel('current', language)} · {formatMonitorDirection(currentDirection, language)}
          </div>
          <div className="mt-2 space-y-1 text-xs">
            <div className="flex items-center justify-between gap-3">
              <span className="text-emerald-100/65">{language === 'zh' ? '平均' : 'Mean'}</span>
              <span className="font-semibold text-emerald-300">{formatSignedEV(currentMean)}</span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-emerald-100/55">{language === 'zh' ? '中位' : 'Median'}</span>
              <span className="font-semibold text-emerald-300">{formatSignedEV(currentMedian)}</span>
            </div>
          </div>
        </div>
        <div className="rounded-lg border border-zinc-700 bg-zinc-900/55 px-3 py-2 opacity-70">
          <div className="text-[11px] font-semibold tracking-[0.12em] text-zinc-400">
            {formatCurrentOppositeLabel('opposite', language)} · {formatMonitorDirection(oppositeDirection, language)}
          </div>
          <div className="mt-2 space-y-1 text-xs">
            <div className="flex items-center justify-between gap-3">
              <span className="text-zinc-500">{language === 'zh' ? '平均' : 'Mean'}</span>
              <span className="font-medium text-zinc-300">{formatSignedEV(oppositeMean)}</span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-zinc-500">{language === 'zh' ? '中位' : 'Median'}</span>
              <span className="font-medium text-zinc-300">{formatSignedEV(oppositeMedian)}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function featureVectorLabel(name: string | undefined, language: Language) {
  switch (name) {
    case 'market':
      return language === 'zh' ? '市场动量' : 'Market'
    case 'trend':
      return language === 'zh' ? '趋势结构' : 'Trend'
    case 'volume_spike':
      return language === 'zh' ? 'Volume Spike' : 'Volume Spike'
    case 'mtf_resonance':
      return language === 'zh' ? 'MTF 共振' : 'MTF Resonance'
    case 'quant_oi':
      return language === 'zh' ? 'OI 变化' : 'OI Delta'
    case 'quant_imbalance':
      return language === 'zh' ? '盘口失衡' : 'Orderbook Imbalance'
    case 'quant_netflow':
      return language === 'zh' ? '资金流' : 'Netflow'
    case 'social_rank':
      return language === 'zh' ? '热搜排名' : 'Trending Rank'
    case 'social_upvote':
      return language === 'zh' ? '点赞率' : 'Up-votes'
    case 'onchain_ratio':
      return language === 'zh' ? '链上/CEX 比' : 'Onchain/CEX Ratio'
    case 'onchain_buy_ratio':
      return language === 'zh' ? '买单占比' : 'Buy Ratio'
    case 'orderbook_imbalance':
      return language === 'zh' ? '盘口失衡' : 'Orderbook Imbalance'
    case 'vol_utilization':
      return language === 'zh' ? '波动利用率' : 'Vol Utilization'
    case 'funding_rate':
      return language === 'zh' ? '资金费率' : 'Funding Rate'
    default:
      return name || '--'
  }
}

function resolveAttributionWeights(attribution: RealFireAttribution | undefined) {
  const raw: Record<string, number> = {}
  for (const dimension of attribution?.dimensions || []) {
    if (dimension.name === 'global') raw.global = dimension.real_weight
    if (dimension.name === 'sector') raw.sector = dimension.real_weight
    if (dimension.name === 'symbol') raw.symbol = dimension.real_weight
  }
  return normalizeAttributionWeights(raw)
}

function resolveEntryTriggerLabel(weights: { global: number; sector: number; symbol: number }, language: Language) {
  const leading = Math.max(weights.global, weights.sector, weights.symbol)
  if (leading <= 0) {
    return language === 'zh' ? '触发: 待评估' : 'Trigger: Pending'
  }
  if (leading === weights.sector && weights.sector >= weights.symbol) {
    return language === 'zh' ? '触发: 赛道强势' : 'Trigger: Sector Strength'
  }
  if (leading === weights.symbol) {
    return language === 'zh' ? '触发: 微观突破' : 'Trigger: Micro Breakout'
  }
  return language === 'zh' ? '触发: 宏观护航' : 'Trigger: Macro Tailwind'
}

interface MahalanobisStatusCardProps {
  distance?: number
  threshold?: number
  resonant?: boolean
  focus?: string
  deviation?: number
  language: Language
}

function MahalanobisStatusCard({
  distance,
  threshold,
  resonant,
  focus,
  deviation,
  language,
}: MahalanobisStatusCardProps) {
  void threshold
  const safeThreshold = MAHALANOBIS_THRESHOLD
  const safeDistance = typeof distance === 'number' && Number.isFinite(distance) ? Math.max(distance, 0) : 0
  const ratio = safeThreshold > 0 ? Math.min((safeDistance / safeThreshold) * 100, 100) : 0
  const resonantActive = typeof distance === 'number' ? safeDistance <= safeThreshold : (resonant ?? false)
  const statusClass = resonantActive
    ? 'border-emerald-400/25 bg-emerald-500/10 text-emerald-200'
    : 'border-zinc-600/25 bg-zinc-500/10 text-zinc-300'
  const barClass = resonantActive ? 'bg-emerald-400' : 'bg-zinc-500'
  const statusLabel = resonantActive
    ? language === 'zh'
      ? '基因匹配'
      : 'Resonant'
    : language === 'zh'
      ? '异形屏蔽'
      : 'Outlier'

  return (
    <div className="rounded-[24px] border border-white/10 bg-black/20 p-4 sm:p-5">
      <div className="text-[11px] uppercase tracking-[0.24em] text-zinc-500">
        {language === 'zh' ? '马氏基因状态卡' : 'Mahalanobis DNA Status'}
      </div>
      <div className="mt-3 flex items-center justify-between gap-3">
        <div>
          <div className="text-sm font-semibold text-white">
            {language === 'zh' ? '马氏距离' : 'Mahalanobis Distance'}
          </div>
          <div className="mt-1 text-xs text-zinc-400">
            {language === 'zh' ? '当前信号是否属于高胜率基因簇' : 'Whether the signal belongs to the high-win-rate DNA cluster'}
          </div>
        </div>
        <span className={`rounded-full border px-3 py-1 text-[10px] font-bold uppercase tracking-[0.22em] ${statusClass}`}>
          {statusLabel}
        </span>
      </div>

      <div className="mt-4">
        <div className="flex items-center justify-between text-xs text-zinc-400">
          <span>D</span>
          <span>{safeDistance.toFixed(2)} / {safeThreshold.toFixed(2)}</span>
        </div>
        <div className="mt-2 h-3 overflow-hidden rounded-full border border-white/10 bg-zinc-950">
          <div
            className={`h-full rounded-full transition-all ${barClass}`}
            style={{ width: `${ratio}%` }}
          />
        </div>
        <div className="mt-2 flex items-center justify-between text-[11px] text-zinc-500">
          <span>{language === 'zh' ? '阈值' : 'Threshold'}</span>
          <span>{safeDistance <= safeThreshold ? 'D <= Threshold' : 'D > Threshold'}</span>
        </div>
      </div>

      <div className="mt-4 rounded-2xl border border-white/8 bg-white/[0.03] px-4 py-3">
        <div className="text-[11px] uppercase tracking-[0.2em] text-zinc-500">
          {language === 'zh' ? '特征向量健康度' : 'Feature Vector Health'}
        </div>
        <div className="mt-2 text-sm font-semibold text-white">
          {focus ? featureVectorLabel(focus, language) : (language === 'zh' ? '暂无特征偏离' : 'No dominant deviation')}
        </div>
        <div className="mt-1 text-xs text-zinc-400">
          {focus
            ? `${language === 'zh' ? '偏离' : 'Deviation'} ${typeof deviation === 'number' && Number.isFinite(deviation) ? `${deviation >= 0 ? '+' : ''}${deviation.toFixed(2)}` : '--'}`
            : language === 'zh'
              ? '等权回退或样本不足时不显示偏离特征'
              : 'No deviation shown when the cluster is unavailable or sample-poor'}
        </div>
      </div>
    </div>
  )
}

interface FirePermitCardProps {
  entryAllowed?: boolean
  blockReason?: string
  distance?: number
  threshold?: number
  language: Language
}

function FirePermitCard({
  entryAllowed,
  blockReason,
  distance,
  threshold,
  language,
}: FirePermitCardProps) {
  void threshold
  const safeThreshold = MAHALANOBIS_THRESHOLD
  const safeDistance = typeof distance === 'number' && Number.isFinite(distance) ? Math.max(distance, 0) : 0
  const hasStatus = typeof entryAllowed === 'boolean'
  const permitted = typeof distance === 'number'
    ? safeDistance <= safeThreshold && (entryAllowed ?? true)
    : (entryAllowed ?? false)
  const statusClass = !hasStatus
    ? 'border-amber-400/25 bg-amber-500/10 text-amber-200'
    : permitted
      ? 'border-emerald-400/25 bg-emerald-500/10 text-emerald-200'
      : 'border-rose-400/25 bg-rose-500/10 text-rose-200'
  const statusLabel = !hasStatus
    ? language === 'zh'
      ? '待评估'
      : 'Pending'
    : permitted
      ? language === 'zh'
        ? '许可'
        : 'Allowed'
      : language === 'zh'
        ? '结构性禁入'
        : 'Structural Block'

  return (
    <div className="rounded-[24px] border border-white/10 bg-black/20 p-4 sm:p-5">
      <div className="text-[11px] uppercase tracking-[0.24em] text-zinc-500">
        {language === 'zh' ? '开火许可' : 'Fire Permit'}
      </div>
      <div className="mt-3 flex items-center justify-between gap-3">
        <div>
          <div className="text-sm font-semibold text-white">
            {language === 'zh' ? '结构性门禁' : 'Structural Gate'}
          </div>
          <div className="mt-1 text-xs text-zinc-400">
            {!hasStatus
              ? language === 'zh'
                ? '等待最新决策元数据'
                : 'Waiting for decision metadata'
              : permitted
                ? language === 'zh'
                  ? 'D 未越线，且动能未触发熔断'
                  : 'D remains inside the hard gate and momentum is intact'
                : blockReason || (language === 'zh' ? '等候硬熔断解除' : 'Waiting for the hard gate to clear')}
          </div>
        </div>
        <span className={`rounded-full border px-3 py-1 text-[10px] font-bold uppercase tracking-[0.22em] ${statusClass}`}>
          {statusLabel}
        </span>
      </div>

      <div className="mt-4 rounded-2xl border border-white/8 bg-white/[0.03] px-4 py-3">
        <div className="flex items-center justify-between text-xs text-zinc-400">
          <span>{language === 'zh' ? '硬门槛' : 'Hard Gate'}</span>
          <span>{safeDistance.toFixed(2)} / {safeThreshold.toFixed(2)}</span>
        </div>
        <div className="mt-1 text-sm font-semibold text-white">
          {!hasStatus
            ? language === 'zh'
              ? '待评估'
              : 'Pending'
            : permitted
              ? language === 'zh'
                ? '开火许可'
                : 'Permission Granted'
              : language === 'zh'
                ? '结构性禁入'
                : 'Structural Block'}
        </div>
        <div className="mt-1 text-xs text-zinc-400">
          {!hasStatus
            ? language === 'zh'
              ? '等待信号元数据落地'
              : 'Waiting for signal metadata'
            : permitted
              ? language === 'zh'
                ? '当前结构通过马氏质检与动能检查'
                : 'Current structure passes Mahalanobis and momentum checks'
              : blockReason || (language === 'zh' ? '结构性熔断已触发' : 'Structural kill-switch engaged')}
        </div>
      </div>
    </div>
  )
}

interface LiveAttributionWeightsCardProps {
  attribution?: RealFireAttribution
  position?: RealBacktestMonitorPosition | null
  language: Language
}

function LiveAttributionWeightsCard({
  attribution,
  position,
  language,
}: LiveAttributionWeightsCardProps) {
  const weights = resolveAttributionWeights(attribution)
  const finalEV = position
    ? (position.global_ev ?? 0) * weights.global +
      (position.sector_ev ?? 0) * weights.sector +
      (position.symbol_ev ?? 0) * weights.symbol
    : 0
  const maxWeight = Math.max(weights.global, weights.sector, weights.symbol, 0.0001)
  const barClass = (active: boolean) =>
    active ? 'bg-gradient-to-r from-emerald-400 via-cyan-300 to-emerald-500' : 'bg-zinc-500/70'

  return (
    <div className="rounded-[24px] border border-white/10 bg-black/20 p-4 sm:p-5">
      <div className="text-[11px] uppercase tracking-[0.24em] text-zinc-500">
        {language === 'zh' ? '实盘归因权重条' : 'Live Attribution Weights'}
      </div>
      <div className="mt-3 grid gap-3">
        {[
          { key: 'global', label: language === 'zh' ? 'Macro / Global' : 'Macro / Global', value: weights.global },
          { key: 'sector', label: language === 'zh' ? 'Meso / Sector' : 'Meso / Sector', value: weights.sector },
          { key: 'symbol', label: language === 'zh' ? 'Micro / Symbol' : 'Micro / Symbol', value: weights.symbol },
        ].map((item) => (
          <div key={item.key} className="space-y-1">
            <div className="flex items-center justify-between text-xs text-zinc-300">
              <span>{item.label}</span>
              <span className="font-semibold text-white">{(item.value * 100).toFixed(1)}%</span>
            </div>
            <div className="h-2 overflow-hidden rounded-full border border-white/10 bg-zinc-950">
              <div
                className={`h-full rounded-full ${barClass(item.value === maxWeight)}`}
                style={{ width: `${Math.max(0, Math.min(100, item.value * 100))}%` }}
              />
            </div>
          </div>
        ))}
      </div>
      <div className="mt-4 rounded-2xl border border-white/8 bg-white/[0.03] px-4 py-3">
        <div className="text-[11px] uppercase tracking-[0.2em] text-zinc-500">
          {language === 'zh' ? '最终入场 EV' : 'Final Entry EV'}
        </div>
        <div className="mt-2 text-lg font-semibold text-white">
          {formatSignedEV(finalEV)}
        </div>
        <div className="mt-1 text-xs text-zinc-400">
          {language === 'zh'
            ? 'Final_EV = Σ(Weight × Dimension_EV)'
            : 'Final_EV = Σ(Weight × Dimension_EV)'}
        </div>
      </div>
    </div>
  )
}

function localizeLogMessage(message: string | undefined, language: Language) {
  if (!message) {
    return tMonitor(language, '实战回测执行记录', 'Real backtest execution')
  }

  if (language !== 'zh') {
    return message
  }

  if (message === 'real backtest resonance') {
    return '实战回测: 动态贝叶斯入场'
  }

  const timedExit = message.match(/^Real backtest timed exit after (\d+)m(\d+)s$/i)
  if (timedExit) {
    return `实战回测: ${timedExit[1]}分钟强制到点离场`
  }

  const resonance = message.match(/^Real backtest directional resonance ([A-Z_]+): logic=([0-9.]+) bin=([0-9]+) sector=(.+)$/i)
  if (resonance) {
    return `实战回测: 加权入场 ${formatSignal(resonance[1], language)}, 逻辑分=${resonance[2]}, 分箱=${resonance[3]}, 赛道=${resonance[4]}`
  }

  return message
}

function tradeLogTone(log: RealBacktestLogEntry) {
  if (!log.success) return 'border-red-500/40 bg-red-500/10 text-red-100'
  if (log.auto_closed) return 'border-sky-400/40 bg-sky-500/10 text-sky-100'
  if (log.action.startsWith('open_')) return 'border-emerald-400/40 bg-emerald-500/10 text-emerald-100'
  return 'border-zinc-600/60 bg-zinc-900/60 text-zinc-100'
}

function dimensionLabel(name: string | undefined, language: Language) {
  switch (name) {
    case 'global':
      return language === 'zh' ? '宏观影响' : 'Global'
    case 'sector':
      return language === 'zh' ? '中观影响' : 'Sector'
    case 'symbol':
      return language === 'zh' ? '微观影响' : 'Symbol'
    default:
      return language === 'zh' ? '样本不足' : 'Insufficient Sample'
  }
}

function dominantAttributionNarrative(attribution: RealFireAttribution | undefined, language: Language) {
  if (!attribution?.dominant_dimension) {
    return language === 'zh' ? '当前实盘归因信号仍在建立中' : 'Real-trade attribution is still forming'
  }
  return language === 'zh'
    ? `当前实盘受 [${dimensionLabel(attribution.dominant_dimension, language)}] 较大`
    : `Live attribution currently leans toward [${dimensionLabel(attribution.dominant_dimension, language)}]`
}

function strongestDeviationDimension(attribution: RealFireAttribution | undefined) {
  const dimensions = attribution?.dimensions ?? []
  return dimensions.reduce<RealFireAttributionDimension | null>((best, dimension) => {
    if (!best || Math.abs(dimension.weight_delta) > Math.abs(best.weight_delta)) {
      return dimension
    }
    return best
  }, null)
}

export function RealLiveMonitor({ language }: RealLiveMonitorProps) {
  const [nowMs, setNowMs] = useState(() => Date.now())
  const isChinese = language === 'zh'

  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])

  const { data, error, isLoading } = useSWR<RealBacktestMonitorResponse>(
    'real-backtest-monitor',
    () => api.getRealBacktestMonitor(),
    {
      refreshInterval: 3000,
      revalidateOnFocus: false,
      dedupingInterval: 1500,
    }
  )

  const positions = data?.positions ?? []
  const logs = data?.logs ?? []
  const attribution = data?.attribution
  const attributionDimensions = attribution?.sample_count ? attribution.dimensions ?? [] : []
  const topPosition = useMemo(
    () => positions.reduce<RealBacktestMonitorPosition | null>((best, row) => {
      if (!best || row.logic_score > best.logic_score) return row
      return best
    }, null),
    [positions]
  )
  const deviation = useMemo(
    () => strongestDeviationDimension(attribution?.sample_count ? attribution : undefined),
    [attribution]
  )

  return (
    <main className="relative min-h-screen overflow-hidden bg-[#04070d] pb-16 pt-20 text-zinc-100">
      <DeepVoidBackground />

      <div className="relative mx-auto flex w-full max-w-[1600px] flex-col gap-6 px-4 sm:px-6 lg:px-8">
        <section className="overflow-hidden rounded-[32px] border border-orange-500/40 bg-[radial-gradient(circle_at_top_left,_rgba(249,115,22,0.22),_transparent_30%),radial-gradient(circle_at_bottom_right,_rgba(16,185,129,0.18),_transparent_28%),linear-gradient(145deg,rgba(5,8,16,0.94),rgba(8,12,24,0.98))] p-6 shadow-[0_30px_90px_rgba(0,0,0,0.45)]">
          <div className="flex flex-col gap-6 xl:flex-row xl:items-end xl:justify-between">
            <div className="max-w-3xl">
              <div className="mb-3 flex flex-wrap items-center gap-2">
                <span className="rounded-full border border-orange-400/70 bg-orange-500/15 px-3 py-1 text-[10px] font-bold uppercase tracking-[0.28em] text-orange-200">
                  {tMonitor(language, '实盘回测火控监视器', 'Real-Backtest Fire Control')}
                </span>
                <span className={`rounded-full px-3 py-1 text-[10px] font-bold uppercase tracking-[0.28em] ${
                  data?.armed
                    ? 'border border-emerald-300/60 bg-emerald-500/15 text-emerald-200'
                    : 'border border-zinc-600 bg-zinc-800/80 text-zinc-300'
                }`}>
                  {formatModeLabel(data?.armed, language)}
                </span>
              </div>
              <h1 className="text-3xl font-semibold tracking-[0.04em] text-white sm:text-5xl">
                {formatCurrency(data?.total_equity ?? 0)}
              </h1>
              <p className="mt-3 max-w-2xl text-sm leading-6 text-zinc-300 sm:text-base">
                {language === 'zh'
                  ? '实盘入场监视视图。页面每 3 秒自动刷新一次账户与仓位状态，底部日志保留 T+15 自动平仓记录。'
                  : 'Live sniper entry monitor with 3-second polling and timed-exit logs.'}
              </p>
            </div>

            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4 xl:min-w-[840px]">
              <div className="rounded-2xl border border-orange-400/35 bg-black/30 p-4">
                <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.24em] text-orange-200/80">
                  <Zap size={14} />
                  {tMonitor(language, '模式状态', 'Mode Status')}
                </div>
                <div className="mt-3 text-2xl font-semibold text-white">{formatModeLabel(data?.armed, language)}</div>
                <p className="mt-2 text-xs text-zinc-400">
                  {data?.daemon_running
                    ? tMonitor(language, '全局狙击手在线', 'Global Sniper online')
                    : tMonitor(language, '全局狙击手离线', 'Daemon not running')}
                </p>
              </div>

              <div className="rounded-2xl border border-cyan-400/30 bg-black/30 p-4">
                <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.24em] text-cyan-200/80">
                  <Crosshair size={14} />
                  {tMonitor(language, '活跃仓位', 'Active Positions')}
                </div>
                <div className="mt-3 text-2xl font-semibold text-white">{data?.position_count ?? 0}</div>
                <p className="mt-2 text-xs text-zinc-400">
                  {topPosition
                    ? `${topPosition.symbol} ${formatSide(topPosition.side, language)} @ ${formatNumber(topPosition.logic_score, 1)}`
                    : tMonitor(language, '当前没有实盘入场仓位', 'No live entry position')}
                </p>
              </div>

              <div className="rounded-2xl border border-lime-400/30 bg-black/30 p-4">
                <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.24em] text-lime-200/80">
                  <TimerReset size={14} />
                  {tMonitor(language, '下次强制平仓', 'Next Forced Exit')}
                </div>
                <div className="mt-3 text-2xl font-semibold text-white">
                  {topPosition ? formatCountdown(topPosition.close_at, nowMs) : '--'}
                </div>
                <p className="mt-2 text-xs text-zinc-400">
                  {topPosition
                  ? `${topPosition.symbol} ${tMonitor(language, 'T+15 倒计时', 'T+15 countdown')}`
                    : tMonitor(language, '等待下一次入场', 'Awaiting new entry')}
                </p>
              </div>

              <FirePermitCard
                entryAllowed={topPosition?.entry_allowed}
                blockReason={topPosition?.entry_block_reason}
                distance={topPosition?.mahalanobis_distance}
                threshold={topPosition?.mahalanobis_threshold}
                language={language}
              />
            </div>
          </div>
        </section>

        <section className="grid gap-4 xl:grid-cols-2">
          <MahalanobisStatusCard
            distance={topPosition?.mahalanobis_distance}
            threshold={topPosition?.mahalanobis_threshold}
            resonant={topPosition?.mahalanobis_resonant}
            focus={topPosition?.feature_vector_focus}
            deviation={topPosition?.feature_vector_deviation}
            language={language}
          />
          <LiveAttributionWeightsCard
            attribution={attribution}
            position={topPosition}
            language={language}
          />
        </section>

        <section className="rounded-[28px] border border-zinc-800 bg-[linear-gradient(180deg,rgba(8,11,18,0.96),rgba(7,10,17,0.94))] p-5 shadow-[0_24px_70px_rgba(0,0,0,0.35)]">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.26em] text-zinc-500">
                <Activity size={14} />
                {tMonitor(language, '活跃入场仓位', 'Active Entry Positions')}
              </div>
              <h2 className="mt-2 text-xl font-semibold text-white">{tMonitor(language, '入场 DNA 实时网格', 'Sniper Entry DNA Grid')}</h2>
            </div>
            <div className="text-right text-xs text-zinc-500">
              <div>
                {isLoading
                  ? tMonitor(language, '监视器加载中...', 'Loading monitor...')
                  : error
                    ? tMonitor(language, '监视器拉取失败', 'Monitor fetch failed')
                    : `${tMonitor(language, '更新时间', 'Updated')} ${formatMonitorTimestamp(data?.updated_at ?? 0, language)}`}
              </div>
              <div className="mt-1">{tMonitor(language, '分箱仅显示原始 1 点位', 'Bin view shows raw 1-point bins')}</div>
            </div>
          </div>

          <div className="overflow-x-auto">
            <table className="min-w-[1180px] w-full border-separate border-spacing-y-3">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-[0.22em] text-zinc-500">
                  <th className="px-3">{tMonitor(language, '仓位', 'Position')}</th>
                  <th className="px-3">{tMonitor(language, '逻辑分 / 分箱', 'Logic / Bin')}</th>
                  <th className="px-3">
                    {tMonitor(language, '加权期望值 (Net EV)', 'Weighted Entry EV')}
                    <div className="mt-1 text-[10px] font-medium tracking-[0.14em] text-emerald-200/70">
                      {tMonitor(language, '已扣 0.15% 双边手续费', '0.15% round-trip fee included')}
                    </div>
                  </th>
                  <th className="px-3">{tMonitor(language, '实时盈亏', 'PnL')}</th>
                  <th className="px-3">{tMonitor(language, '马氏基因状态', 'Mahalanobis DNA')}</th>
                  <th className="px-3">{tMonitor(language, '平仓倒计时', 'Close Countdown')}</th>
                </tr>
              </thead>
              <tbody>
                {positions.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-3 py-10 text-center text-sm text-zinc-500">
                      {tMonitor(language, '当前物理账户中没有活跃入场仓位。', 'No active entry position in the physical account.')}
                    </td>
                  </tr>
                )}
                {positions.map((position) => (
                  <tr key={`${position.trader_id}-${position.symbol}-${position.side}`} className="rounded-2xl border border-zinc-800 bg-zinc-950/65">
                    <td className="rounded-l-2xl border-y border-l border-zinc-800 px-3 py-4 align-top">
                      <div className="text-sm font-semibold text-white">{position.symbol}</div>
                      <div className={`mt-1 text-xs text-zinc-500 ${isChinese ? '' : 'uppercase tracking-[0.2em]'}`}>
                        {formatSide(position.side, language)} • {position.trader_name}
                      </div>
                      <div className="mt-3 space-y-1 text-xs text-zinc-300">
                        <div>{tMonitor(language, '入场', 'Entry')} {formatCurrency(position.entry_price)}</div>
                        <div>{tMonitor(language, '现价', 'Mark')} {formatCurrency(position.mark_price)}</div>
                        <div>{tMonitor(language, '数量', 'Qty')} {formatNumber(position.quantity, 4)} • {position.leverage}x</div>
                      </div>
                    </td>

                    <td className="border-y border-zinc-800 px-3 py-4 align-top">
                      <div className="text-sm font-semibold text-white">{formatNumber(position.logic_score, 1)}</div>
                      <div className="mt-1 text-xs text-zinc-500">
                        {tMonitor(language, '分箱', 'Bin')}: {formatNumber(position.bin_center, 0)}
                      </div>
                      <div className="mt-3 space-y-1 text-xs text-zinc-300">
                        <div>{tMonitor(language, '信号', 'Signal')} {formatSignal(position.signal, language)}</div>
                        <div>{tMonitor(language, '赛道', 'Sector')} {position.sector || '--'}</div>
                        <div>{tMonitor(language, '开仓时间', 'Opened')} {formatMonitorTimestamp(position.entry_time, language)}</div>
                      </div>
                    </td>

                    <td className="border-y border-zinc-800 px-3 py-4 align-top">
                      <div className="grid gap-2 text-xs">
                        {(() => {
                          const currentDirection = normalizeMonitorDirection(position.side)
                          const globalOpposite = resolveOppositeDirectionalValues(position.global_bin, currentDirection)
                          const sectorOpposite = resolveOppositeDirectionalValues(position.sector_bin, currentDirection)
                          const symbolOpposite = resolveOppositeDirectionalValues(position.symbol_bin, currentDirection)
                          return (
                            <>
                              <DirectionalEvCard
                                title={tMonitor(language, '全局期望', 'Global EV')}
                                currentDirection={currentDirection}
                                currentMean={position.global_ev}
                                currentMedian={position.global_median_ev}
                                oppositeMean={globalOpposite.mean}
                                oppositeMedian={globalOpposite.median}
                                language={language}
                              />
                              <DirectionalEvCard
                                title={tMonitor(language, '赛道期望', 'Sector EV')}
                                currentDirection={currentDirection}
                                currentMean={position.sector_ev}
                                currentMedian={position.sector_median_ev}
                                oppositeMean={sectorOpposite.mean}
                                oppositeMedian={sectorOpposite.median}
                                language={language}
                              />
                              <DirectionalEvCard
                                title={tMonitor(language, '币种期望', 'Symbol EV')}
                                currentDirection={currentDirection}
                                currentMean={position.symbol_ev}
                                currentMedian={position.symbol_median_ev}
                                oppositeMean={symbolOpposite.mean}
                                oppositeMedian={symbolOpposite.median}
                                language={language}
                              />
                            </>
                          )
                        })()}
                      </div>
                    </td>

                    <td className="border-y border-zinc-800 px-3 py-4 align-top">
                      <div className={`text-lg font-semibold ${position.unrealized_pnl >= 0 ? 'text-emerald-300' : 'text-red-300'}`}>
                        {formatCurrency(position.unrealized_pnl)}
                      </div>
                      <div className="mt-1 text-xs text-zinc-500">{formatSignedPct(position.unrealized_pnl_pct)}</div>
                      <div className="mt-4 text-xs text-zinc-300">
                        {tMonitor(language, '保证金', 'Margin')} {formatCurrency(position.margin_used)}
                      </div>
                    </td>

                    <td className="border-y border-zinc-800 px-3 py-4 align-top">
                      <MahalanobisStatusCard
                        distance={position.mahalanobis_distance}
                        threshold={position.mahalanobis_threshold}
                        resonant={position.mahalanobis_resonant}
                        focus={position.feature_vector_focus}
                        deviation={position.feature_vector_deviation}
                        language={language}
                      />
                    </td>

                    <td className="rounded-r-2xl border-y border-r border-zinc-800 px-3 py-4 align-top">
                      <div className="text-2xl font-semibold text-white">
                        {formatCountdown(position.close_at, nowMs)}
                      </div>
                      <div className="mt-2 text-xs text-zinc-500">
                        {tMonitor(language, '自动平仓于', 'Auto-close at')} {formatMonitorTimestamp(position.close_at, language)}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className="rounded-[28px] border border-zinc-800 bg-[linear-gradient(180deg,rgba(12,16,28,0.98),rgba(7,10,18,0.94))] p-5 shadow-[0_24px_70px_rgba(0,0,0,0.35)]">
          <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.26em] text-zinc-500">
                <Crosshair size={14} />
                {tMonitor(language, '实盘归因', 'Real-Trade Attribution')}
              </div>
              <h2 className="mt-2 text-xl font-semibold text-white">
                {tMonitor(language, '实盘雷达 vs 影子雷达', 'Live Radar vs Shadow Radar')}
              </h2>
            </div>
            <div className="text-right text-xs text-zinc-500">
              <div>
                {tMonitor(language, '已平仓样本', 'Closed Samples')} {attribution?.sample_count ?? 0}
              </div>
              <div className="mt-1">
                {deviation
                  ? `${tMonitor(language, '最大偏差', 'Largest Drift')} ${dimensionLabel(deviation.name, language)} ${deviation.weight_delta >= 0 ? '+' : ''}${(deviation.weight_delta * 100).toFixed(1)}%`
                  : tMonitor(language, '等待归因偏差形成', 'Waiting for attribution drift')}
              </div>
            </div>
          </div>

          <div className="grid gap-5 xl:grid-cols-[1.1fr_0.9fr]">
            <div className="rounded-[24px] border border-white/8 bg-black/20 p-4">
              <RealFireAttributionRadar attribution={attribution} language={language} />
            </div>

            <div className="grid gap-4">
              <div className="rounded-[24px] border border-emerald-400/25 bg-emerald-500/8 p-4">
                <div className="text-[11px] uppercase tracking-[0.24em] text-emerald-200/80">
                  {tMonitor(language, '结论标签', 'Conclusion')}
                </div>
                <div className="mt-3 text-2xl font-semibold text-white">
                  {dominantAttributionNarrative(attribution, language)}
                </div>
                <p className="mt-3 text-sm leading-6 text-zinc-300">
                  {language === 'zh'
                    ? '绿色层代表真实盈亏归因，橙色层代表影子强度预期。两层偏离越大，说明影子回测越可能高估了该维度。'
                    : 'Green shows realized attribution, while orange shows shadow intensity. Larger divergence suggests shadow backtest optimism on that dimension.'}
                </p>
              </div>

              <div className="grid gap-3">
                {attributionDimensions.map((dimension) => (
                  <div
                    key={dimension.name}
                    className="rounded-2xl border border-zinc-800 bg-zinc-950/60 p-4"
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="text-sm font-semibold text-white">
                        {dimensionLabel(dimension.name, language)}
                      </div>
                      <div className={`text-xs font-medium ${dimension.weight_delta >= 0 ? 'text-emerald-300' : 'text-orange-300'}`}>
                        {tMonitor(language, '偏差', 'Drift')} {dimension.weight_delta >= 0 ? '+' : ''}{(dimension.weight_delta * 100).toFixed(1)}%
                      </div>
                    </div>

                    <div className="mt-3 grid gap-3 sm:grid-cols-2">
                      <div>
                        <div className="text-[11px] uppercase tracking-[0.18em] text-zinc-500">
                          {tMonitor(language, '实盘贡献', 'Real Contribution')}
                        </div>
                        <div className="mt-1 text-lg font-semibold text-emerald-200">
                          {(dimension.real_weight * 100).toFixed(1)}%
                        </div>
                      </div>
                      <div>
                        <div className="text-[11px] uppercase tracking-[0.18em] text-zinc-500">
                          {tMonitor(language, '影子强度', 'Shadow Strength')}
                        </div>
                        <div className="mt-1 text-lg font-semibold text-orange-200">
                          {(dimension.shadow_weight * 100).toFixed(1)}%
                        </div>
                      </div>
                      <div>
                        <div className="text-[11px] uppercase tracking-[0.18em] text-zinc-500">
                          {tMonitor(language, '入场 EV 均值', 'Entry EV Avg')}
                        </div>
                        <div className="mt-1 text-sm font-medium text-zinc-100">
                          {formatSignedEV(dimension.entry_average_ev)}
                        </div>
                      </div>
                      <div>
                        <div className="text-[11px] uppercase tracking-[0.18em] text-zinc-500">
                          {tMonitor(language, '持仓保持率', 'Hold Retention')}
                        </div>
                        <div className="mt-1 text-sm font-medium text-zinc-100">
                          {dimension.average_retention.toFixed(2)}x
                        </div>
                      </div>
                    </div>
                  </div>
                ))}

                {attributionDimensions.length === 0 && (
                  <div className="rounded-2xl border border-dashed border-zinc-800 bg-zinc-950/50 px-4 py-10 text-center text-sm text-zinc-500">
                    {tMonitor(language, '暂无已平仓实盘样本，无法生成归因雷达。', 'No closed live trades yet, attribution radar unavailable.')}
                  </div>
                )}
              </div>
            </div>
          </div>
        </section>

        <section className="rounded-[28px] border border-zinc-800 bg-[linear-gradient(180deg,rgba(10,14,24,0.96),rgba(8,10,16,0.92))] p-5 shadow-[0_24px_70px_rgba(0,0,0,0.35)]">
          <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.26em] text-zinc-500">
                <ShieldAlert size={14} />
                {tMonitor(language, '实时成交日志', 'Live Trade Log')}
              </div>
              <h2 className="mt-2 text-xl font-semibold text-white">{tMonitor(language, 'T+15 物理执行历史', 'T+15 Execution History')}</h2>
            </div>
            <div className="text-xs text-zinc-500">{tMonitor(language, '最新记录优先', 'Newest entries first')}</div>
          </div>

          <div className="grid gap-3">
            {logs.length === 0 && (
              <div className="rounded-2xl border border-dashed border-zinc-800 bg-zinc-950/50 px-4 py-10 text-center text-sm text-zinc-500">
                {tMonitor(language, '暂无实盘回测执行日志。', 'No real-backtest execution log available yet.')}
              </div>
            )}

            {logs.map((log) => (
              <article
                key={`${log.trader_id}-${log.timestamp}-${log.symbol}-${log.action}`}
                className={`rounded-2xl border px-4 py-4 ${tradeLogTone(log)}`}
              >
                <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm font-semibold text-white">{log.symbol}</span>
                      <span className="rounded-full border border-white/10 bg-black/20 px-2 py-0.5 text-[10px] uppercase tracking-[0.22em]">
                        {summarizeAction(log.action, language)}
                      </span>
                      {log.auto_closed && (
                        <span className="rounded-full border border-sky-300/40 bg-sky-500/15 px-2 py-0.5 text-[10px] uppercase tracking-[0.22em] text-sky-100">
                          {tMonitor(language, 'T+15 自动离场', 'T+15 Auto Exit')}
                        </span>
                      )}
                    </div>
                    <div className="mt-2 text-xs text-zinc-300">
                      {log.trader_name} • {formatSide(log.side, language)} • {formatMonitorTimestamp(log.timestamp, language)}
                    </div>
                    <p className="mt-3 text-sm leading-6 text-zinc-200/90">{localizeLogMessage(log.message, language)}</p>
                  </div>

                  <div className="grid gap-2 text-sm lg:min-w-[360px] lg:grid-cols-2">
                    <div className="rounded-xl border border-white/10 bg-black/20 px-3 py-2">
                      <div className="text-[11px] uppercase tracking-[0.2em] text-zinc-400">{tMonitor(language, '成交价/数量', 'Fill')}</div>
                      <div className="mt-1 font-semibold text-white">
                        {formatCurrency(log.price)} • {formatNumber(log.quantity, 4)}
                      </div>
                    </div>
                    <div className="rounded-xl border border-white/10 bg-black/20 px-3 py-2">
                      <div className="text-[11px] uppercase tracking-[0.2em] text-zinc-400">{tMonitor(language, '逻辑/信号', 'Logic / Signal')}</div>
                      <div className="mt-1 font-semibold text-white">
                        {formatNumber(log.logic_score, 1)} • {formatSignal(log.signal, language)}
                      </div>
                    </div>
                    <div className="rounded-xl border border-white/10 bg-black/20 px-3 py-2">
                      <div className="text-[11px] uppercase tracking-[0.2em] text-zinc-400">{tMonitor(language, '三维期望副本', '3D EV Copy')}</div>
                      <div className="mt-1 text-xs font-medium text-zinc-100">
                        {isChinese ? '全' : 'G'} {optionalSignedEV(log.global_ev)} / {isChinese ? '赛' : 'S'} {optionalSignedEV(log.sector_ev)} / {isChinese ? '币' : 'C'} {optionalSignedEV(log.symbol_ev)}
                      </div>
                    </div>
                    <div className="rounded-xl border border-white/10 bg-black/20 px-3 py-2">
                      <div className="text-[11px] uppercase tracking-[0.2em] text-zinc-400">{tMonitor(language, '已实现盈亏', 'Realized')}</div>
                      <div className={`mt-1 font-semibold ${
                        typeof log.realized_pnl === 'number' && log.realized_pnl < 0 ? 'text-red-200' : 'text-emerald-200'
                      }`}>
                        {typeof log.realized_pnl === 'number' ? formatCurrency(log.realized_pnl) : '--'}
                      </div>
                    </div>
                    <div className="rounded-xl border border-white/10 bg-black/20 px-3 py-2 sm:col-span-2">
                      <div className="text-[11px] uppercase tracking-[0.2em] text-zinc-400">{tMonitor(language, '入场决策', 'Entry Decision')}</div>
                      {(() => {
                        const summary = formatEntryDecisionSummary(log, attribution, language)
                        return (
                          <div className="mt-1 space-y-1 text-xs font-medium text-zinc-100">
                            <div>{summary.finalLine}</div>
                            <div className="text-zinc-300">{summary.weightLine}</div>
                            <div className="text-zinc-300">{summary.contribLine}</div>
                            <div className="text-zinc-300">{summary.triggerLine}</div>
                            {summary.mahalanobisLine ? <div className="text-zinc-300">{summary.mahalanobisLine}</div> : null}
                            {summary.gateLine ? <div className="text-zinc-300">{summary.gateLine}</div> : null}
                          </div>
                        )
                      })()}
                    </div>
                  </div>
                </div>
              </article>
            ))}
          </div>
        </section>
      </div>
    </main>
  )
}
