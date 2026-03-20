import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import {
  Activity,
  BrainCircuit,
  ChevronDown,
  Crown,
  Radar,
  Rocket,
  Settings2,
  Sparkles,
  TrendingDown,
  TrendingUp,
  X,
  Zap,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '../lib/api'
import { useLanguage } from '../contexts/LanguageContext'
import type {
  AIModel,
  ColliderExperiment,
  ExperimentLogRecord,
  ColliderVariant,
  CoachConfig,
  DecisionRecord,
  TraderInfo,
} from '../types'

type WindowOption = '24h' | '72h' | '168h'

interface ReplayEvent {
  id: string
  traderId: string
  traderName: string
  timestamp: string | number
  action: string
  symbol: string
  confidence: number
  reasoning: string
  prompt: string
}

interface VariantCurvePoint {
  timestamp: string
  total_equity: number
}

function formatPct(value: number) {
  return `${value.toFixed(1)}%`
}

function formatTs(ts: string | number) {
  return new Date(ts).toLocaleString()
}

function parseTimestamp(value: string | number | null | undefined) {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : Number.NaN
  }
  if (typeof value === 'string') {
    const parsed = new Date(value).getTime()
    return Number.isFinite(parsed) ? parsed : Number.NaN
  }
  return Number.NaN
}

function getReturnPct(variant: ColliderVariant) {
  return variant.return_pct ?? 0
}

function getDrawdownPct(variant: ColliderVariant) {
  return variant.drawdown_pct ?? 0
}

function getSyncPulse(variants: ColliderVariant[]) {
  const actions = variants
    .map((variant) => {
      const latestDecision = variant.decisions[variant.decisions.length - 1]
      return latestDecision?.decisions?.[0]?.action?.toLowerCase()
    })
    .filter((action): action is string => Boolean(action))
  if (actions.length === 0) return 0
  const counts = actions.reduce<Record<string, number>>((acc, action) => {
    acc[action] = (acc[action] ?? 0) + 1
    return acc
  }, {})
  return Math.round((Math.max(...Object.values(counts)) / actions.length) * 100)
}

function getCurvePath(
  points: VariantCurvePoint[],
  width: number,
  height: number,
  min: number,
  max: number,
  minTime: number,
  maxTime: number
) {
  if (points.length === 0) return ''
  const range = Math.max(max - min, 1)
  const timeRange = Math.max(maxTime - minTime, 1)
  return points
    .map((point, index) => {
      const pointTime = parseTimestamp(point.timestamp)
      const x = Number.isFinite(pointTime)
        ? ((pointTime - minTime) / timeRange) * width
        : 0
      const y = height - ((point.total_equity - min) / range) * height
      return `${index === 0 ? 'M' : 'L'} ${x.toFixed(2)} ${y.toFixed(2)}`
    })
    .join(' ')
}

function buildReplayEvents(variant: ColliderVariant): ReplayEvent[] {
  return variant.decisions.flatMap((record: DecisionRecord, recordIndex) =>
    (record.decisions ?? []).map((action, actionIndex) => ({
      id: `${variant.trader_id}-${recordIndex}-${actionIndex}`,
      traderId: variant.trader_id,
      traderName: variant.trader_name,
      timestamp: action.timestamp || record.timestamp,
      action: action.action,
      symbol: action.symbol,
      confidence: action.confidence ?? 0,
      reasoning: action.reasoning || record.cot_trace || '',
      prompt:
        record.input_prompt ||
        record.system_prompt ||
        variant.custom_prompt ||
        '',
    }))
  )
}

function windowToApiValue(value: WindowOption) {
  if (value === '168h') return '168h'
  return value
}

function windowToHours(value: WindowOption) {
  if (value === '24h') return 24
  if (value === '168h') return 168
  return 72
}

function CreateExperimentModal({
  isOpen,
  isZh,
  masterTraders,
  masterTraderId,
  variantCount,
  isCreating,
  onClose,
  onMasterTraderChange,
  onVariantCountChange,
  onConfirm,
}: {
  isOpen: boolean
  isZh: boolean
  masterTraders: TraderInfo[]
  masterTraderId: string
  variantCount: number
  isCreating: boolean
  onClose: () => void
  onMasterTraderChange: (value: string) => void
  onVariantCountChange: (value: number) => void
  onConfirm: () => Promise<void>
}) {
  if (!isOpen) return null

  const hasMasters = masterTraders.length > 0

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 px-4 backdrop-blur-sm">
      <div className="w-full max-w-lg overflow-hidden rounded-[28px] border border-white/10 bg-[linear-gradient(180deg,rgba(10,18,28,0.98),rgba(6,11,18,0.98))] shadow-[0_30px_100px_rgba(0,0,0,0.55)]">
        <div className="flex items-start justify-between gap-4 border-b border-white/10 px-6 py-5">
          <div>
            <div className="inline-flex items-center gap-2 rounded-full border border-emerald-400/25 bg-emerald-400/10 px-3 py-1 text-[11px] uppercase tracking-[0.24em] text-emerald-300">
              <Rocket className="h-3.5 w-3.5" />
              {isZh ? '创建实验' : 'Create Experiment'}
            </div>
            <h3 className="mt-3 text-2xl font-semibold text-white">
              {isZh ? '启动一组新的影子变体' : 'Launch a fresh shadow cohort'}
            </h3>
            <p className="mt-2 text-sm leading-6 text-zinc-400">
              {isZh
                ? '选择一个主执行器，系统会按当前配置克隆出新的影子观测者。'
                : 'Pick a master trader and spin up a new set of shadow observers from its current config.'}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={isCreating}
            className="rounded-full border border-white/10 bg-white/5 p-2 text-zinc-400 transition hover:bg-white/10 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
            aria-label={isZh ? '关闭' : 'Close'}
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-5 px-6 py-6">
          <label className="block">
            <div className="mb-2 text-sm font-medium text-zinc-200">
              {isZh ? '选择主执行器' : 'Master Trader'}
            </div>
            <select
              value={masterTraderId}
              onChange={(event) => onMasterTraderChange(event.target.value)}
              disabled={!hasMasters || isCreating}
              className="w-full rounded-2xl border border-white/10 bg-white/5 px-4 py-3 text-sm text-white outline-none transition focus:border-emerald-400/60 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {hasMasters ? (
                masterTraders.map((trader) => (
                  <option key={trader.trader_id} value={trader.trader_id}>
                    {trader.trader_name}
                  </option>
                ))
              ) : (
                <option value="">
                  {isZh ? '暂无可用主执行器' : 'No master traders available'}
                </option>
              )}
            </select>
            {!hasMasters && (
              <p className="mt-2 text-xs text-amber-300">
                {isZh
                  ? '请先创建至少一个非影子交易员。'
                  : 'Create at least one non-shadow trader first.'}
              </p>
            )}
          </label>

          <label className="block">
            <div className="mb-2 flex items-center justify-between gap-3">
              <span className="text-sm font-medium text-zinc-200">
                {isZh ? '变体数量' : 'Variant Count'}
              </span>
              <span className="rounded-full border border-amber-400/20 bg-amber-400/10 px-3 py-1 text-xs text-amber-200">
                {variantCount}
              </span>
            </div>
            <input
              type="range"
              min={3}
              max={30}
              value={variantCount}
              onChange={(event) =>
                onVariantCountChange(Number(event.target.value))
              }
              disabled={isCreating}
              className="w-full accent-amber-400 disabled:cursor-not-allowed"
            />
            <input
              type="number"
              min={3}
              max={30}
              value={variantCount}
              onChange={(event) =>
                onVariantCountChange(Number(event.target.value))
              }
              disabled={isCreating}
              className="mt-3 w-full rounded-2xl border border-white/10 bg-white/5 px-4 py-3 text-sm text-white outline-none transition focus:border-amber-400/60 disabled:cursor-not-allowed disabled:opacity-50"
            />
            <p className="mt-2 text-xs text-zinc-500">
              {isZh ? '范围 3 - 30，默认 5。' : 'Range 3 - 30, default 5.'}
            </p>
          </label>
        </div>

        <div className="flex flex-col-reverse gap-3 border-t border-white/10 px-6 py-5 sm:flex-row sm:justify-end">
          <button
            type="button"
            onClick={onClose}
            disabled={isCreating}
            className="rounded-full border border-white/10 bg-white/5 px-5 py-2.5 text-sm font-medium text-zinc-300 transition hover:bg-white/10 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
          >
            {isZh ? '取消' : 'Cancel'}
          </button>
          <button
            type="button"
            onClick={() => void onConfirm()}
            disabled={!hasMasters || isCreating}
            className="inline-flex items-center justify-center gap-2 rounded-full bg-[linear-gradient(135deg,#fbbf24,#f59e0b)] px-5 py-2.5 text-sm font-semibold text-black shadow-[0_16px_36px_rgba(245,158,11,0.35)] transition hover:brightness-105 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Rocket
              className={`h-4 w-4 ${isCreating ? 'animate-pulse' : ''}`}
            />
            {isCreating
              ? isZh
                ? '创建中...'
                : 'Creating...'
              : isZh
                ? '确认创建'
                : 'Create Experiment'}
          </button>
        </div>
      </div>
    </div>
  )
}

function CoachConfigModal({
  isOpen,
  isZh,
  currentModelId,
  models,
  isSaving,
  isTriggering,
  onClose,
  onModelChange,
  onSave,
  onTrigger,
}: {
  isOpen: boolean
  isZh: boolean
  currentModelId: string
  models: AIModel[]
  isSaving: boolean
  isTriggering: boolean
  onClose: () => void
  onModelChange: (value: string) => void
  onSave: () => Promise<void>
  onTrigger: () => Promise<void>
}) {
  if (!isOpen) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 px-4 backdrop-blur-sm">
      <div className="w-full max-w-lg overflow-hidden rounded-[28px] border border-white/10 bg-[linear-gradient(180deg,rgba(10,18,28,0.98),rgba(6,11,18,0.98))] shadow-[0_30px_100px_rgba(0,0,0,0.55)]">
        <div className="flex items-start justify-between gap-4 border-b border-white/10 px-6 py-5">
          <div>
            <div className="inline-flex items-center gap-2 rounded-full border border-cyan-400/25 bg-cyan-400/10 px-3 py-1 text-[11px] uppercase tracking-[0.24em] text-cyan-300">
              <Settings2 className="h-3.5 w-3.5" />
              {isZh ? '教练 AI 配置' : 'Coach AI Config'}
            </div>
            <h3 className="mt-3 text-2xl font-semibold text-white">
              {isZh
                ? '配置复盘模型并手动发起进化'
                : 'Choose the review model and trigger evolution'}
            </h3>
            <p className="mt-2 text-sm leading-6 text-zinc-400">
              {isZh
                ? '这里控制教练 AI 用哪个模型复盘当前实验表现，并且允许你立刻触发一次替换。'
                : 'Control which model reviews experiment performance and trigger an immediate replacement cycle.'}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={isSaving || isTriggering}
            className="rounded-full border border-white/10 bg-white/5 p-2 text-zinc-400 transition hover:bg-white/10 hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
            aria-label={isZh ? '关闭' : 'Close'}
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-6 px-6 py-6">
          <label className="block">
            <div className="mb-2 text-sm font-medium text-zinc-200">
              {isZh ? '复盘模型' : 'Coach Model'}
            </div>
            <select
              value={currentModelId}
              onChange={(event) => onModelChange(event.target.value)}
              disabled={isSaving || isTriggering}
              className="w-full rounded-2xl border border-white/10 bg-white/5 px-4 py-3 text-sm text-white outline-none transition focus:border-cyan-400/60 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <option value="">
                {isZh
                  ? '自动选择首个可用模型'
                  : 'Auto-select first available model'}
              </option>
              {models.map((model) => (
                <option key={model.id} value={model.id}>
                  {model.name} ({model.provider})
                </option>
              ))}
            </select>
            <p className="mt-2 text-xs text-zinc-500">
              {isZh
                ? '下拉列表读取当前系统已配置的模型；留空时后端会自动挑选首个可用模型。'
                : 'This list comes from configured system models; leaving it empty lets the backend auto-pick the first usable model.'}
            </p>
          </label>

          <button
            type="button"
            onClick={() => void onSave()}
            disabled={isSaving || isTriggering}
            className="inline-flex w-full items-center justify-center gap-2 rounded-full bg-[linear-gradient(135deg,#38bdf8,#06b6d4)] px-5 py-3 text-sm font-semibold text-slate-950 shadow-[0_18px_40px_rgba(34,211,238,0.22)] transition hover:brightness-105 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Settings2
              className={`h-4 w-4 ${isSaving ? 'animate-spin' : ''}`}
            />
            {isSaving
              ? isZh
                ? '保存中...'
                : 'Saving...'
              : isZh
                ? '保存设置'
                : 'Save Settings'}
          </button>

          <div className="border-t border-white/10 pt-6">
            <div className="mb-3 text-xs uppercase tracking-[0.24em] text-zinc-500">
              {isZh ? '手动干预' : 'Manual Override'}
            </div>
            <button
              type="button"
              onClick={() => void onTrigger()}
              disabled={isSaving || isTriggering}
              className="inline-flex w-full items-center justify-center gap-2 rounded-[22px] border border-amber-300/25 bg-[linear-gradient(135deg,rgba(251,191,36,0.22),rgba(245,158,11,0.2))] px-5 py-4 text-sm font-semibold text-amber-100 shadow-[0_18px_40px_rgba(245,158,11,0.18)] transition hover:brightness-110 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Zap
                className={`h-4 w-4 ${isTriggering ? 'animate-pulse' : ''}`}
              />
              {isTriggering
                ? isZh
                  ? '教练正在复盘优化中...'
                  : 'Coach is reviewing and optimizing...'
                : isZh
                  ? '⚡ 立即手动进化'
                  : '⚡ Trigger Evolution Now'}
            </button>
            <p className="mt-3 text-xs leading-6 text-zinc-500">
              {isZh
                ? '这一步会直接调用教练复盘流程，可能持续几十秒。完成后页面会立刻刷新实验数据。'
                : 'This directly runs the coach review flow and can take tens of seconds. The page will refresh experiment data when it completes.'}
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}

function VariantCurveCard({
  isZh,
  variant,
  window,
  isExpanded,
  isReplaySelected,
  onToggle,
}: {
  isZh: boolean
  variant: ColliderVariant
  window: WindowOption
  isExpanded: boolean
  isReplaySelected: boolean
  onToggle: () => void
}) {
  const hours = windowToHours(window)
  const { data: history, isLoading } = useSWR<VariantCurvePoint[]>(
    isExpanded ? `variant-lab-equity-${variant.trader_id}-${hours}` : null,
    async () => {
      const points = await api.getEquityHistory(variant.trader_id, hours)
      return points.map((point) => ({
        timestamp: point.timestamp,
        total_equity: point.total_equity,
      }))
    },
    {
      refreshInterval: 30000,
      revalidateOnFocus: false,
    }
  )

  const curve = history ?? []
  const minEquity = curve.reduce(
    (min, point) => Math.min(min, point.total_equity),
    Number.POSITIVE_INFINITY
  )
  const maxEquity = curve.reduce(
    (max, point) => Math.max(max, point.total_equity),
    Number.NEGATIVE_INFINITY
  )
  const times = curve
    .map((point) => parseTimestamp(point.timestamp))
    .filter(Number.isFinite)
  const minTime = times.length > 0 ? Math.min(...times) : Date.now()
  const maxTime = times.length > 0 ? Math.max(...times) : minTime + 1
  const path =
    curve.length > 1
      ? getCurvePath(
          curve,
          640,
          180,
          Number.isFinite(minEquity) ? minEquity : variant.equity_last,
          Number.isFinite(maxEquity) ? maxEquity : variant.equity_last + 1,
          minTime,
          maxTime
        )
      : ''
  const latestCoachLog = variant.coach_logs?.[0]

  return (
    <div
      className={`rounded-2xl border p-4 transition ${isReplaySelected ? 'border-amber-300/40 bg-amber-300/8' : 'border-white/10 bg-white/5'}`}
    >
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-start justify-between gap-3 text-left"
      >
        <div>
          <div className="flex items-center gap-2 text-white">
            <span className="font-medium">{variant.trader_name}</span>
            {!variant.is_shadow && <Crown className="h-4 w-4 text-amber-300" />}
          </div>
          <div className="mt-1 text-xs text-zinc-500">
            {variant.is_shadow
              ? isZh
                ? '影子观测者，只落虚拟仓位'
                : 'Shadow observer, virtual positions only'
              : isZh
                ? '主执行器，允许真实下单'
                : 'Master executor, real exchange orders enabled'}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span
            className={`rounded-full px-3 py-1 text-xs ${variant.is_running ? 'bg-emerald-400/15 text-emerald-200' : 'bg-zinc-500/15 text-zinc-300'}`}
          >
            {variant.is_running
              ? isZh
                ? '运行中'
                : 'Running'
              : isZh
                ? '已停止'
                : 'Stopped'}
          </span>
          <ChevronDown
            className={`h-4 w-4 text-zinc-400 transition ${isExpanded ? 'rotate-180' : ''}`}
          />
        </div>
      </button>

      <p className="mt-3 line-clamp-3 text-sm leading-7 text-zinc-300">
        {variant.custom_prompt ||
          (isZh ? '未设置 custom_prompt。' : 'No custom_prompt configured.')}
      </p>

      <div className="mt-4 grid grid-cols-4 gap-2 text-xs">
        <div className="rounded-xl bg-black/30 px-3 py-2 text-zinc-300">
          <div className="text-zinc-500">{isZh ? '收益率' : 'Return'}</div>
          <div
            className={`mt-1 ${variant.return_pct >= 0 ? 'text-emerald-300' : 'text-rose-300'}`}
          >
            {formatPct(variant.return_pct ?? 0)}
          </div>
        </div>
        <div className="rounded-xl bg-black/30 px-3 py-2 text-zinc-300">
          <div className="text-zinc-500">{isZh ? '回撤' : 'Drawdown'}</div>
          <div className="mt-1 text-white">
            {formatPct(variant.drawdown_pct ?? 0)}
          </div>
        </div>
        <div className="rounded-xl bg-black/30 px-3 py-2 text-zinc-300">
          <div className="text-zinc-500">{isZh ? '决策数' : 'Decisions'}</div>
          <div className="mt-1 text-white">
            {variant.decision_count ?? variant.decisions.length}
          </div>
        </div>
        <div className="rounded-xl bg-black/30 px-3 py-2 text-zinc-300">
          <div className="text-zinc-500">
            {isZh ? '最新净值' : 'Last Equity'}
          </div>
          <div className="mt-1 text-white">
            {variant.equity_last?.toFixed(2) ?? '0.00'}
          </div>
        </div>
      </div>

      {isExpanded && (
        <div className="mt-4 space-y-4">
          <div className="rounded-2xl border border-white/10 bg-black/25 p-4">
            <div className="mb-3 flex items-center justify-between gap-3">
              <div>
                <div className="text-xs uppercase tracking-[0.22em] text-zinc-500">
                  {isZh ? '资金曲线' : 'Equity Curve'}
                </div>
                <div className="mt-1 text-sm text-zinc-300">
                  {isZh
                    ? `懒加载最近 ${hours} 小时曲线`
                    : `Lazy-loaded last ${hours}h window`}
                </div>
              </div>
              <div className="text-xs text-zinc-500">{window}</div>
            </div>
            {isLoading ? (
              <div className="h-[180px] animate-pulse rounded-xl bg-white/5" />
            ) : curve.length > 1 ? (
              <svg viewBox="0 0 640 180" className="h-[180px] w-full">
                {[0, 1, 2, 3].map((line) => (
                  <line
                    key={line}
                    x1="0"
                    x2="640"
                    y1={line * 60}
                    y2={line * 60}
                    stroke="rgba(255,255,255,0.08)"
                    strokeDasharray="4 8"
                  />
                ))}
                <path
                  d={path}
                  fill="none"
                  stroke={variant.is_shadow ? '#38bdf8' : '#fbbf24'}
                  strokeWidth={variant.is_shadow ? 2 : 3}
                  strokeLinecap="round"
                />
              </svg>
            ) : (
              <div className="flex h-[180px] items-center justify-center rounded-xl border border-dashed border-white/10 text-sm text-zinc-500">
                {isZh
                  ? '当前窗口暂无足够曲线点。'
                  : 'Not enough curve points in this window.'}
              </div>
            )}
          </div>

          <div className="rounded-2xl border border-cyan-400/15 bg-cyan-400/8 p-4">
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="text-xs uppercase tracking-[0.22em] text-zinc-500">
                  {isZh ? '教练日志' : 'Coach Log'}
                </div>
                <div className="mt-1 text-sm text-zinc-300">
                  {latestCoachLog
                    ? isZh
                      ? `最近一次洗脑/重装：${formatTs(latestCoachLog.created_at)}`
                      : `Latest coach rewrite: ${formatTs(latestCoachLog.created_at)}`
                    : isZh
                      ? '这个变体还没有教练改造记录'
                      : 'No coach rewrite recorded for this variant yet'}
                </div>
              </div>
              {latestCoachLog && (
                <div className="rounded-full bg-black/30 px-3 py-1 text-xs text-cyan-100">
                  {latestCoachLog.coach_model_id}
                </div>
              )}
            </div>

            {latestCoachLog ? (
              <div className="mt-4 space-y-4">
                <div className="rounded-xl border border-white/10 bg-black/20 p-3">
                  <div className="text-xs uppercase tracking-[0.22em] text-zinc-500">
                    {isZh ? '教练推理' : 'Coach Reasoning'}
                  </div>
                  <p className="mt-3 whitespace-pre-wrap break-words text-sm leading-7 text-zinc-200">
                    {latestCoachLog.coach_reasoning ||
                      (isZh
                        ? '该次进化未保存 reasoning。'
                        : 'No reasoning stored for this evolution.')}
                  </p>
                </div>

                <div className="grid gap-4 lg:grid-cols-2">
                  <CoachPromptPanel
                    isZh={isZh}
                    title={isZh ? 'Prompt Diff' : 'Prompt Diff'}
                    content={latestCoachLog.prompt_diff}
                    emptyLabel={
                      isZh ? '没有可展示的差异。' : 'No diff available.'
                    }
                  />
                  <CoachPromptPanel
                    isZh={isZh}
                    title={isZh ? '新 Prompt' : 'New Prompt'}
                    content={latestCoachLog.new_prompt}
                    emptyLabel={
                      isZh ? '没有保存新的 prompt。' : 'No new prompt saved.'
                    }
                  />
                </div>

                {variant.coach_logs.length > 1 && (
                  <div className="rounded-xl border border-white/10 bg-black/20 p-3">
                    <div className="text-xs uppercase tracking-[0.22em] text-zinc-500">
                      {isZh ? '近期进化记录' : 'Recent Evolutions'}
                    </div>
                    <div className="mt-3 space-y-3">
                      {variant.coach_logs
                        .slice(0, 4)
                        .map((log: ExperimentLogRecord) => (
                          <div
                            key={log.id}
                            className="rounded-xl border border-white/5 bg-white/5 p-3"
                          >
                            <div className="flex items-center justify-between gap-3 text-xs text-zinc-500">
                              <span>{formatTs(log.created_at)}</span>
                              <span>{log.coach_model_id}</span>
                            </div>
                            <p className="mt-2 text-sm leading-6 text-zinc-300">
                              {log.summary}
                            </p>
                          </div>
                        ))}
                    </div>
                  </div>
                )}
              </div>
            ) : null}
          </div>
        </div>
      )}
    </div>
  )
}

function CoachPromptPanel({
  isZh,
  title,
  content,
  emptyLabel,
}: {
  isZh: boolean
  title: string
  content?: string
  emptyLabel: string
}) {
  return (
    <div className="rounded-xl border border-white/10 bg-black/20 p-3">
      <div className="text-xs uppercase tracking-[0.22em] text-zinc-500">
        {title}
      </div>
      <p className="mt-3 max-h-[20rem] overflow-y-auto whitespace-pre-wrap break-words text-sm leading-7 text-zinc-200">
        {content?.trim() || emptyLabel}
      </p>
      {!content?.trim() && (
        <div className="mt-2 text-xs text-zinc-500">
          {isZh
            ? '等待下一次进化后生成。'
            : 'Generated after the next evolution.'}
        </div>
      )}
    </div>
  )
}

export function PromptVariantsLabPage() {
  const { language } = useLanguage()
  const isZh = language === 'zh'
  const [window, setWindow] = useState<WindowOption>('72h')
  const [drawdownThreshold, setDrawdownThreshold] = useState(12)
  const [selectedExperimentId, setSelectedExperimentId] = useState<
    string | null
  >(null)
  const [selectedReplayId, setSelectedReplayId] = useState<string | null>(null)
  const [showCreateModal, setShowCreateModal] = useState(false)
  const [showCoachConfigModal, setShowCoachConfigModal] = useState(false)
  const [selectedMasterTraderId, setSelectedMasterTraderId] = useState('')
  const [variantCount, setVariantCount] = useState(5)
  const [isCreatingExperiment, setIsCreatingExperiment] = useState(false)
  const [coachModelId, setCoachModelId] = useState('')
  const [isSavingCoachConfig, setIsSavingCoachConfig] = useState(false)
  const [isTriggeringCoachEvolution, setIsTriggeringCoachEvolution] =
    useState(false)
  const [expandedTraderId, setExpandedTraderId] = useState<string | null>(null)

  const experimentsKey = `experiments-${window}`
  const { data, mutate: mutateExperiments } = useSWR(
    experimentsKey,
    () => api.getExperiments(windowToApiValue(window)),
    { refreshInterval: 15000 }
  )
  const { data: traders } = useSWR<TraderInfo[]>(
    'collider-master-traders',
    api.getTraders,
    {
      refreshInterval: 30000,
    }
  )
  const { data: coachConfig, mutate: mutateCoachConfig } = useSWR<CoachConfig>(
    'coach-config',
    api.getCoachConfig
  )
  const { data: coachModels } = useSWR<AIModel[]>('models', api.getModelConfigs)

  const experiments = data?.experiments ?? []
  const masterTraders = useMemo(
    () => (traders ?? []).filter((trader) => !trader.is_shadow),
    [traders]
  )

  useEffect(() => {
    if (!selectedExperimentId && experiments.length > 0) {
      setSelectedExperimentId(experiments[0].id)
    }
  }, [experiments, selectedExperimentId])

  useEffect(() => {
    if (!showCreateModal) return
    if (
      masterTraders.some(
        (trader) => trader.trader_id === selectedMasterTraderId
      )
    )
      return
    setSelectedMasterTraderId(masterTraders[0]?.trader_id ?? '')
  }, [masterTraders, selectedMasterTraderId, showCreateModal])

  useEffect(() => {
    setCoachModelId(coachConfig?.coach_model_id ?? '')
  }, [coachConfig?.coach_model_id])

  const selectedExperiment = useMemo<ColliderExperiment | undefined>(
    () =>
      experiments.find((item) => item.id === selectedExperimentId) ??
      experiments[0],
    [experiments, selectedExperimentId]
  )

  const variants = selectedExperiment?.variants ?? []
  const masterVariant =
    variants.find((variant) => !variant.is_shadow) ?? variants[0]
  const sortedVariants = [...variants].sort(
    (a, b) => getReturnPct(b) - getReturnPct(a)
  )
  const replayEvents = sortedVariants.flatMap(buildReplayEvents)
  const selectedReplay =
    replayEvents.find((item) => item.id === selectedReplayId) ?? replayEvents[0]

  const syncPulse = getSyncPulse(variants)
  const activeObservers = variants.filter((variant) => variant.is_shadow).length
  const eliminatedCount = variants.filter(
    (variant) => getDrawdownPct(variant) > drawdownThreshold
  ).length
  const totalDecisions = variants.reduce(
    (sum, variant) =>
      sum + (variant.decision_count ?? variant.decisions?.length ?? 0),
    0
  )

  const symbolCounts = replayEvents.reduce<Record<string, number>>(
    (acc, event) => {
      acc[event.symbol] = (acc[event.symbol] ?? 0) + 1
      return acc
    },
    {}
  )
  const topSymbols = Object.entries(symbolCounts)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 6)

  const availableCoachModels = (coachModels ?? []).filter(
    (model) => model.enabled
  )

  useEffect(() => {
    if (!sortedVariants.length) {
      setExpandedTraderId(null)
      return
    }
    if (
      expandedTraderId &&
      sortedVariants.some((variant) => variant.trader_id === expandedTraderId)
    ) {
      return
    }
    setExpandedTraderId(sortedVariants[0].trader_id)
  }, [expandedTraderId, sortedVariants])

  const handleVariantCountChange = (value: number) => {
    if (!Number.isFinite(value)) {
      setVariantCount(5)
      return
    }
    setVariantCount(Math.min(30, Math.max(3, Math.round(value))))
  }

  const openCreateModal = () => {
    setVariantCount(5)
    setSelectedMasterTraderId(masterTraders[0]?.trader_id ?? '')
    setShowCreateModal(true)
  }

  const closeCreateModal = () => {
    if (isCreatingExperiment) return
    setShowCreateModal(false)
  }

  const openCoachConfigModal = () => {
    setCoachModelId(coachConfig?.coach_model_id ?? '')
    setShowCoachConfigModal(true)
  }

  const closeCoachConfigModal = () => {
    if (isSavingCoachConfig || isTriggeringCoachEvolution) return
    setShowCoachConfigModal(false)
  }

  const handleCreateExperiment = async () => {
    if (!selectedMasterTraderId) {
      toast.error(isZh ? '请选择主执行器' : 'Select a master trader')
      return
    }

    setIsCreatingExperiment(true)
    try {
      const response = await api.createExperiment(
        selectedMasterTraderId,
        variantCount
      )
      setShowCreateModal(false)
      toast.success(
        isZh
          ? '实验已启动，影子变体已就绪'
          : 'Experiment started, shadow variants are ready'
      )
      const refreshed = await mutateExperiments()
      const nextExperiments = refreshed?.experiments ?? []
      const nextSelected =
        nextExperiments.find(
          (experiment) => experiment.id === response.experiment.id
        )?.id ?? response.experiment.id
      setSelectedExperimentId(nextSelected)
      setSelectedReplayId(null)
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : isZh
            ? '创建实验失败'
            : 'Failed to create experiment'
      )
    } finally {
      setIsCreatingExperiment(false)
    }
  }

  const handleSaveCoachConfig = async () => {
    setIsSavingCoachConfig(true)
    try {
      await api.updateCoachConfig(coachModelId)
      await mutateCoachConfig()
      toast.success(isZh ? '教练配置已保存' : 'Coach configuration saved')
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : isZh
            ? '保存教练配置失败'
            : 'Failed to save coach configuration'
      )
    } finally {
      setIsSavingCoachConfig(false)
    }
  }

  const handleTriggerCoachEvolution = async () => {
    setIsTriggeringCoachEvolution(true)
    try {
      if (coachModelId !== (coachConfig?.coach_model_id ?? '')) {
        await api.updateCoachConfig(coachModelId)
      }
      await api.triggerCoachEvolution()
      toast.success(
        isZh ? '✅ 手动进化完成！' : '✅ Manual evolution completed!'
      )
      await Promise.all([mutateCoachConfig(), mutateExperiments()])
      setSelectedReplayId(null)
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : isZh
            ? '手动进化失败'
            : 'Manual evolution failed'
      )
    } finally {
      setIsTriggeringCoachEvolution(false)
    }
  }

  return (
    <div className="max-w-[1920px] mx-auto px-4 py-6">
      <div className="relative overflow-hidden rounded-[28px] border border-white/10 bg-[radial-gradient(circle_at_top_left,_rgba(16,185,129,0.18),_transparent_28%),radial-gradient(circle_at_top_right,_rgba(251,191,36,0.18),_transparent_24%),linear-gradient(180deg,_rgba(6,10,14,0.96),_rgba(4,7,10,0.98))] p-6 shadow-[0_24px_80px_rgba(0,0,0,0.45)]">
        <div className="absolute inset-0 opacity-30 [background-image:linear-gradient(rgba(255,255,255,0.06)_1px,transparent_1px),linear-gradient(90deg,rgba(255,255,255,0.04)_1px,transparent_1px)] [background-size:32px_32px]" />
        <div className="relative flex flex-col gap-6">
          <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
            <div className="max-w-4xl">
              <div className="mb-3 inline-flex items-center gap-2 rounded-full border border-emerald-400/25 bg-emerald-400/10 px-3 py-1 text-xs uppercase tracking-[0.28em] text-emerald-300">
                <Sparkles className="h-3.5 w-3.5" />
                {isZh ? '提示词变体对撞机' : 'Prompt Variant Collider'}
              </div>
              <h1 className="text-3xl font-semibold tracking-tight text-white md:text-5xl">
                {isZh
                  ? '主交易员真实下单，影子变体只记录虚拟仓位。'
                  : 'The master places real trades while shadows record only virtual positions.'}
              </h1>
              <p className="mt-4 max-w-3xl text-sm leading-7 text-zinc-300 md:text-base">
                {isZh
                  ? '这里直接读取 experiments 关联的真实 decision_records 和 trader_equity_snapshots，不再生成随机曲线。'
                  : 'This page now reads live decision_records and trader_equity_snapshots from experiments instead of generating synthetic curves.'}
              </p>
            </div>

            <div className="flex flex-col gap-3">
              <div className="flex flex-col gap-3 sm:flex-row">
                <button
                  type="button"
                  onClick={openCreateModal}
                  disabled={masterTraders.length === 0}
                  className="inline-flex items-center justify-center gap-2 rounded-full bg-[linear-gradient(135deg,#34d399,#10b981)] px-5 py-3 text-sm font-semibold text-slate-950 shadow-[0_18px_40px_rgba(16,185,129,0.28)] transition hover:brightness-105 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  <Rocket className="h-4 w-4" />
                  {isZh ? '🚀 新建变体实验' : '🚀 Create Experiment'}
                </button>
                <button
                  type="button"
                  onClick={openCoachConfigModal}
                  className="inline-flex items-center justify-center gap-2 rounded-full border border-cyan-300/20 bg-cyan-400/10 px-5 py-3 text-sm font-semibold text-cyan-100 shadow-[0_18px_40px_rgba(34,211,238,0.12)] transition hover:bg-cyan-400/16"
                >
                  <Settings2 className="h-4 w-4" />
                  {isZh ? '⚙️ 教练 AI 配置' : '⚙️ Coach AI Config'}
                </button>
              </div>
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                <label className="rounded-2xl border border-white/10 bg-white/5 px-4 py-3 text-sm text-zinc-200">
                  <div className="mb-2 text-[11px] uppercase tracking-[0.22em] text-zinc-500">
                    {isZh ? '观察窗口' : 'Window'}
                  </div>
                  <select
                    value={window}
                    onChange={(event) =>
                      setWindow(event.target.value as WindowOption)
                    }
                    className="w-full bg-transparent text-sm outline-none"
                  >
                    <option value="24h">24h</option>
                    <option value="72h">72h</option>
                    <option value="168h">7d</option>
                  </select>
                </label>
                <label className="rounded-2xl border border-white/10 bg-white/5 px-4 py-3 text-sm text-zinc-200">
                  <div className="mb-2 text-[11px] uppercase tracking-[0.22em] text-zinc-500">
                    {isZh ? '实验' : 'Experiment'}
                  </div>
                  <select
                    value={selectedExperiment?.id ?? ''}
                    onChange={(event) =>
                      setSelectedExperimentId(event.target.value)
                    }
                    className="w-full bg-transparent text-sm outline-none"
                  >
                    {experiments.map((experiment) => (
                      <option key={experiment.id} value={experiment.id}>
                        {experiment.id}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="rounded-2xl border border-white/10 bg-white/5 px-4 py-3 text-sm text-zinc-200">
                  <div className="mb-2 text-[11px] uppercase tracking-[0.22em] text-zinc-500">
                    {isZh ? '淘汰回撤阈值' : 'DD Threshold'}
                  </div>
                  <input
                    type="range"
                    min={5}
                    max={30}
                    value={drawdownThreshold}
                    onChange={(event) =>
                      setDrawdownThreshold(Number(event.target.value))
                    }
                    className="w-full accent-amber-400"
                  />
                  <div className="mt-1 text-xs text-amber-300">
                    {drawdownThreshold}%
                  </div>
                </label>
              </div>
            </div>
          </div>

          <div className="grid gap-4 lg:grid-cols-4">
            {[
              {
                label: isZh ? '同步脉冲' : 'Sync Pulse',
                value: `${syncPulse}%`,
                hint: isZh
                  ? '最新一轮动作一致性'
                  : 'Consensus across latest actions',
                icon: Radar,
                tone: 'text-emerald-300',
              },
              {
                label: isZh ? '影子观测者' : 'Shadow Variants',
                value: `${activeObservers}`,
                hint: isZh ? '本地虚拟执行' : 'Local virtual execution only',
                icon: Activity,
                tone: 'text-cyan-300',
              },
              {
                label: isZh ? '超阈回撤' : 'Over DD Limit',
                value: `${eliminatedCount}`,
                hint: isZh
                  ? '回撤超过当前阈值'
                  : 'Variants beyond drawdown threshold',
                icon: TrendingDown,
                tone: 'text-rose-300',
              },
              {
                label: isZh ? '已记录决策' : 'Recorded Decisions',
                value: `${totalDecisions}`,
                hint: isZh
                  ? '来自真实 decision_records'
                  : 'Backed by real decision_records',
                icon: BrainCircuit,
                tone: 'text-amber-300',
              },
            ].map((item) => (
              <div
                key={item.label}
                className="rounded-2xl border border-white/10 bg-black/25 p-4 backdrop-blur"
              >
                <div className="flex items-center justify-between">
                  <span className="text-xs uppercase tracking-[0.24em] text-zinc-500">
                    {item.label}
                  </span>
                  <item.icon className={`h-4 w-4 ${item.tone}`} />
                </div>
                <div className={`mt-4 text-3xl font-semibold ${item.tone}`}>
                  {item.value}
                </div>
                <div className="mt-2 text-sm text-zinc-400">{item.hint}</div>
              </div>
            ))}
          </div>
        </div>
      </div>

      <div className="mt-6 grid items-start gap-6 xl:grid-cols-[1.35fr_0.85fr]">
        <section className="rounded-[28px] border border-white/10 bg-[#071019]/90 p-5">
          <div className="flex flex-col gap-3 border-b border-white/10 pb-4 lg:flex-row lg:items-center lg:justify-between">
            <div>
              <div className="text-xs uppercase tracking-[0.24em] text-zinc-500">
                {isZh ? '变体名册' : 'Variant Roster'}
              </div>
              <h2 className="mt-2 text-2xl font-semibold text-white">
                {isZh
                  ? '首屏只渲染摘要，曲线按卡片展开后再异步加载。'
                  : 'Summaries render first; each curve loads only after its card is expanded.'}
              </h2>
            </div>
            <div className="rounded-full border border-emerald-300/20 bg-emerald-300/10 px-4 py-2 text-xs text-emerald-200">
              {isZh
                ? '默认 72h 窗口，避免全量历史阻塞'
                : 'Default 72h window, no full-history blocking'}
            </div>
          </div>

          <div className="mt-5 space-y-3">
            {sortedVariants.map((variant) => (
              <VariantCurveCard
                key={variant.trader_id}
                isZh={isZh}
                variant={variant}
                window={window}
                isExpanded={expandedTraderId === variant.trader_id}
                isReplaySelected={
                  selectedReplay?.traderId === variant.trader_id
                }
                onToggle={() =>
                  setExpandedTraderId((current) =>
                    current === variant.trader_id ? null : variant.trader_id
                  )
                }
              />
            ))}
          </div>
        </section>

        <section className="self-start rounded-[28px] border border-white/10 bg-[#0b1118]/90 p-5 xl:sticky xl:top-24 xl:flex xl:max-h-[calc(100vh-7rem)] xl:flex-col xl:overflow-hidden">
          <div className="text-xs uppercase tracking-[0.24em] text-zinc-500">
            {isZh ? '事件复盘' : 'Decision Replay'}
          </div>
          <h2 className="mt-2 text-2xl font-semibold text-white">
            {isZh
              ? '直接读取原始 prompt、理由与动作。'
              : 'Read the original prompt, rationale, and action directly from stored records.'}
          </h2>

          {selectedReplay ? (
            <div className="mt-5 space-y-4 xl:min-h-0 xl:flex-1 xl:overflow-y-auto xl:pr-1">
              <div className="rounded-2xl border border-amber-400/20 bg-amber-400/8 p-4">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <div className="text-sm font-semibold text-white">
                      {selectedReplay.traderName}
                    </div>
                    <div className="mt-1 text-xs text-zinc-400">
                      {formatTs(selectedReplay.timestamp)} ·{' '}
                      {selectedReplay.symbol} · {selectedReplay.action}
                    </div>
                  </div>
                  <div className="rounded-full bg-black/30 px-3 py-1 text-xs text-amber-200">
                    {isZh
                      ? `置信度 ${selectedReplay.confidence}%`
                      : `Confidence ${selectedReplay.confidence}%`}
                  </div>
                </div>
              </div>

              <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
                <div className="text-xs uppercase tracking-[0.22em] text-zinc-500">
                  Prompt
                </div>
                <div className="mt-3 max-h-[20rem] overflow-y-auto overflow-x-auto rounded-xl border border-white/5 bg-black/20 p-3">
                  <p className="whitespace-pre-wrap break-words text-sm leading-7 text-zinc-200">
                    {selectedReplay.prompt ||
                      (isZh
                        ? '该条记录未保存 prompt。'
                        : 'Prompt not stored for this record.')}
                  </p>
                </div>
              </div>

              <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
                <div className="text-xs uppercase tracking-[0.22em] text-zinc-500">
                  {isZh ? '逻辑推演' : 'Reasoning'}
                </div>
                <div className="mt-3 max-h-[16rem] overflow-y-auto overflow-x-auto rounded-xl border border-white/5 bg-black/20 p-3">
                  <p className="whitespace-pre-wrap break-words text-sm leading-7 text-zinc-200">
                    {selectedReplay.reasoning ||
                      (isZh
                        ? '该条记录未保存 reasoning。'
                        : 'Reasoning not stored for this record.')}
                  </p>
                </div>
              </div>
            </div>
          ) : (
            <div className="mt-6 rounded-[24px] border border-dashed border-white/10 bg-white/5 p-8 text-sm leading-7 text-zinc-400">
              {isZh
                ? '当前实验还没有产生 decision_records。'
                : 'This experiment has not produced any decision_records yet.'}
            </div>
          )}
        </section>
      </div>

      <div className="mt-6">
        <section className="rounded-[28px] border border-white/10 bg-[#091016]/90 p-5">
          <div className="text-xs uppercase tracking-[0.24em] text-zinc-500">
            {isZh ? '实验摘要' : 'Experiment Summary'}
          </div>
          <h2 className="mt-2 text-2xl font-semibold text-white">
            {isZh
              ? '基于真实记录的即时摘要。'
              : 'An at-a-glance summary driven by stored experiment data.'}
          </h2>
          <div className="mt-5 space-y-4">
            <div className="rounded-2xl border border-emerald-400/15 bg-emerald-400/7 p-4">
              <div className="flex items-center gap-2 text-emerald-300">
                <TrendingUp className="h-4 w-4" />
                <span className="text-sm font-medium">
                  {isZh ? '当前领先者' : 'Current Leader'}
                </span>
              </div>
              <p className="mt-3 text-sm leading-7 text-zinc-200">
                {sortedVariants[0]
                  ? `${sortedVariants[0].trader_name} · ${formatPct(getReturnPct(sortedVariants[0]))}`
                  : isZh
                    ? '暂无数据。'
                    : 'No data yet.'}
              </p>
            </div>

            <div className="rounded-2xl border border-cyan-400/15 bg-cyan-400/7 p-4">
              <div className="flex items-center gap-2 text-cyan-300">
                <Radar className="h-4 w-4" />
                <span className="text-sm font-medium">
                  {isZh ? '高频标的' : 'Most Active Symbols'}
                </span>
              </div>
              <div className="mt-3 flex flex-wrap gap-2">
                {topSymbols.length > 0 ? (
                  topSymbols.map(([symbol, count]) => (
                    <span
                      key={symbol}
                      className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-sm text-zinc-200"
                    >
                      {symbol} · {count}
                    </span>
                  ))
                ) : (
                  <span className="text-sm text-zinc-400">
                    {isZh ? '暂无决策。' : 'No decisions yet.'}
                  </span>
                )}
              </div>
            </div>

            <div className="rounded-2xl border border-amber-400/15 bg-amber-400/7 p-4">
              <div className="flex items-center gap-2 text-amber-300">
                <BrainCircuit className="h-4 w-4" />
                <span className="text-sm font-medium">
                  {isZh ? '主提示词' : 'Master Prompt'}
                </span>
              </div>
              <p className="mt-3 whitespace-pre-wrap text-sm leading-7 text-zinc-200">
                {masterVariant?.custom_prompt ||
                  (isZh
                    ? '当前主交易员未配置 custom_prompt。'
                    : 'The current master trader has no custom_prompt configured.')}
              </p>
            </div>
          </div>
        </section>
      </div>

      <CreateExperimentModal
        isOpen={showCreateModal}
        isZh={isZh}
        masterTraders={masterTraders}
        masterTraderId={selectedMasterTraderId}
        variantCount={variantCount}
        isCreating={isCreatingExperiment}
        onClose={closeCreateModal}
        onMasterTraderChange={setSelectedMasterTraderId}
        onVariantCountChange={handleVariantCountChange}
        onConfirm={handleCreateExperiment}
      />
      <CoachConfigModal
        isOpen={showCoachConfigModal}
        isZh={isZh}
        currentModelId={coachModelId}
        models={availableCoachModels}
        isSaving={isSavingCoachConfig}
        isTriggering={isTriggeringCoachEvolution}
        onClose={closeCoachConfigModal}
        onModelChange={setCoachModelId}
        onSave={handleSaveCoachConfig}
        onTrigger={handleTriggerCoachEvolution}
      />
    </div>
  )
}
