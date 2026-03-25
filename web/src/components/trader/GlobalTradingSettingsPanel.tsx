import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { FlaskConical, Bot, ExternalLink, Database } from 'lucide-react'
import { api } from '../../lib/api'
import { getSystemConfig, invalidateSystemConfig } from '../../lib/config'
import { useAuth } from '../../contexts/AuthContext'
import type { TraderInfo } from '../../types'

interface GlobalTradingSettingsPanelProps {
  traders?: TraderInfo[]
  loadingTraders?: boolean
  showManageButton?: boolean
  className?: string
}

interface RealBacktestRiskForm {
  maxMarginPerTrade: string
  reserveMargin: string
  minEvThresholdPct: string
  maxEvThresholdPct: string
}

interface AdaptiveMemoryForm {
  globalSamples: string
  sectorSamples: string
  symbolSamples: string
}

interface EntryGuardForm {
  adaptiveEntryFloor: string
  adaptiveEntryLambda: string
}

const DEFAULT_REAL_BACKTEST_FORM: RealBacktestRiskForm = {
  maxMarginPerTrade: '5.00',
  reserveMargin: '6.00',
  minEvThresholdPct: '0.10',
  maxEvThresholdPct: '0.25',
}

const DEFAULT_ADAPTIVE_MEMORY_FORM: AdaptiveMemoryForm = {
  globalSamples: '5000',
  sectorSamples: '3000',
  symbolSamples: '1000',
}

const DEFAULT_ENTRY_GUARD_FORM: EntryGuardForm = {
  adaptiveEntryFloor: '45',
  adaptiveEntryLambda: '0.50',
}

function formatInputValue(value: number | undefined, fallback: string) {
  return Number.isFinite(value ?? NaN) ? (value as number).toFixed(2) : fallback
}

function formatPercentInputValue(value: number | undefined, fallback: string) {
  return Number.isFinite(value ?? NaN) ? ((value as number) * 100).toFixed(2) : fallback
}

function parseInputNumber(value: string, label: string) {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) {
    throw new Error(`${label} must be a valid number`)
  }
  return parsed
}

function formatNumberOrFallback(value: number | undefined, fallback: number, digits = 2) {
  const resolved = Number.isFinite(value ?? NaN) ? (value as number) : fallback
  return resolved.toFixed(digits)
}

function formatIntegerOrFallback(value: number | undefined, fallback: string) {
  return Number.isFinite(value ?? NaN) ? Math.round(value as number).toString() : fallback
}

function formatIntegerInputValue(value: number | undefined, fallback: string) {
  return Number.isFinite(value ?? NaN) && (value as number) > 0
    ? Math.round(value as number).toString()
    : fallback
}

function parseInputInteger(value: string, label: string) {
  const trimmed = value.trim()
  if (!trimmed) {
    throw new Error(`${label} must be a valid integer`)
  }

  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || !Number.isInteger(parsed)) {
    throw new Error(`${label} must be a valid integer`)
  }
  return parsed
}

export function GlobalTradingSettingsPanel({
  traders,
  loadingTraders = false,
  showManageButton = false,
  className = '',
}: GlobalTradingSettingsPanelProps) {
  const { user } = useAuth()
  const [realBacktestEnabled, setRealBacktestEnabled] = useState(false)
  const [savingRealBacktest, setSavingRealBacktest] = useState(false)
  const [savingAdaptiveMemory, setSavingAdaptiveMemory] = useState(false)
  const [savingEntryGuard, setSavingEntryGuard] = useState(false)
  const [riskForm, setRiskForm] = useState<RealBacktestRiskForm>(DEFAULT_REAL_BACKTEST_FORM)
  const [entryGuardForm, setEntryGuardForm] = useState<EntryGuardForm>(DEFAULT_ENTRY_GUARD_FORM)
  const [adaptiveMemoryForm, setAdaptiveMemoryForm] = useState<AdaptiveMemoryForm>(DEFAULT_ADAPTIVE_MEMORY_FORM)
  const [localTraders, setLocalTraders] = useState<TraderInfo[]>([])
  const [loadingLocalTraders, setLoadingLocalTraders] = useState(false)

  useEffect(() => {
    getSystemConfig()
      .then((config) => {
        setRealBacktestEnabled(Boolean(config.real_backtest_enabled))
        setRiskForm({
          maxMarginPerTrade: formatInputValue(config.rb_max_margin_per_trade, DEFAULT_REAL_BACKTEST_FORM.maxMarginPerTrade),
          reserveMargin: formatInputValue(config.rb_reserve_margin, DEFAULT_REAL_BACKTEST_FORM.reserveMargin),
          minEvThresholdPct: formatPercentInputValue(config.rb_min_ev_threshold, DEFAULT_REAL_BACKTEST_FORM.minEvThresholdPct),
          maxEvThresholdPct: formatPercentInputValue(config.rb_max_ev_threshold, DEFAULT_REAL_BACKTEST_FORM.maxEvThresholdPct),
        })
        setEntryGuardForm({
          adaptiveEntryFloor: formatIntegerOrFallback(
            config.adaptive_entry_floor,
            DEFAULT_ENTRY_GUARD_FORM.adaptiveEntryFloor
          ),
          adaptiveEntryLambda: formatInputValue(
            config.adaptive_entry_lambda,
            DEFAULT_ENTRY_GUARD_FORM.adaptiveEntryLambda
          ),
        })
        setAdaptiveMemoryForm({
          globalSamples: formatIntegerInputValue(
            config.adaptive_global_samples,
            DEFAULT_ADAPTIVE_MEMORY_FORM.globalSamples
          ),
          sectorSamples: formatIntegerInputValue(
            config.adaptive_sector_samples,
            DEFAULT_ADAPTIVE_MEMORY_FORM.sectorSamples
          ),
          symbolSamples: formatIntegerInputValue(
            config.adaptive_symbol_samples,
            DEFAULT_ADAPTIVE_MEMORY_FORM.symbolSamples
          ),
        })
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (traders) {
      return
    }

    setLoadingLocalTraders(true)
    api.getTraders()
      .then((rows) => setLocalTraders(rows))
      .catch(() => {})
      .finally(() => setLoadingLocalTraders(false))
  }, [traders])

  const effectiveTraders = traders ?? localTraders
  const effectiveLoadingTraders = traders ? loadingTraders : loadingLocalTraders
  const runningTraderCount = useMemo(
    () => effectiveTraders.filter((trader) => trader.is_running).length,
    [effectiveTraders]
  )
  const totalTraderCount = effectiveTraders.length
  const aiAutoTradingActive = runningTraderCount > 0

  const persistRealBacktestConfig = async (nextEnabled: boolean) => {
    const maxMarginPerTrade = parseInputNumber(riskForm.maxMarginPerTrade, 'Single-trade margin cap')
    const reserveMargin = parseInputNumber(riskForm.reserveMargin, 'Reserve margin')
    const minEvThresholdPct = parseInputNumber(riskForm.minEvThresholdPct, 'Minimum EV threshold')
    const maxEvThresholdPct = parseInputNumber(riskForm.maxEvThresholdPct, 'Maximum EV threshold')

    if (maxMarginPerTrade <= 0) {
      throw new Error('Single-trade margin cap must be greater than 0')
    }
    if (reserveMargin < 0) {
      throw new Error('Reserve margin must be greater than or equal to 0')
    }
    if (minEvThresholdPct < 0) {
      throw new Error('Minimum EV threshold must be greater than or equal to 0')
    }
    if (maxEvThresholdPct <= minEvThresholdPct) {
      throw new Error('Maximum EV threshold must be greater than the minimum EV threshold')
    }

    const updated = await api.updateRealBacktestConfig({
      enabled: nextEnabled,
      rb_max_margin_per_trade: maxMarginPerTrade,
      rb_reserve_margin: reserveMargin,
      rb_min_ev_threshold: minEvThresholdPct / 100,
      rb_max_ev_threshold: maxEvThresholdPct / 100,
    })

    const nextRealBacktestEnabled = updated.real_backtest_enabled ?? nextEnabled
    setRealBacktestEnabled(Boolean(nextRealBacktestEnabled))
    setRiskForm({
      maxMarginPerTrade: formatNumberOrFallback(updated.rb_max_margin_per_trade, maxMarginPerTrade),
      reserveMargin: formatNumberOrFallback(updated.rb_reserve_margin, reserveMargin),
      minEvThresholdPct: formatNumberOrFallback(updated.rb_min_ev_threshold, minEvThresholdPct / 100, 2),
      maxEvThresholdPct: formatNumberOrFallback(updated.rb_max_ev_threshold, maxEvThresholdPct / 100, 2),
    })
    invalidateSystemConfig()
    return updated
  }

  const persistAdaptiveMemoryConfig = async () => {
    const globalSamples = parseInputInteger(adaptiveMemoryForm.globalSamples, 'Global sample depth')
    const sectorSamples = parseInputInteger(adaptiveMemoryForm.sectorSamples, 'Sector sample depth')
    const symbolSamples = parseInputInteger(adaptiveMemoryForm.symbolSamples, 'Symbol sample depth')

    if (globalSamples < 500 || globalSamples > 10000) {
      throw new Error('Global sample depth must be between 500 and 10000')
    }
    if (sectorSamples < 300 || sectorSamples > 5000) {
      throw new Error('Sector sample depth must be between 300 and 5000')
    }
    if (symbolSamples < 100 || symbolSamples > 3000) {
      throw new Error('Symbol sample depth must be between 100 and 3000')
    }

    const updated = await api.updateAdaptiveMemoryConfig({
      adaptive_global_samples: globalSamples,
      adaptive_sector_samples: sectorSamples,
      adaptive_symbol_samples: symbolSamples,
    })

    setAdaptiveMemoryForm({
      globalSamples: formatIntegerInputValue(updated.adaptive_global_samples, globalSamples.toString()),
      sectorSamples: formatIntegerInputValue(updated.adaptive_sector_samples, sectorSamples.toString()),
      symbolSamples: formatIntegerInputValue(updated.adaptive_symbol_samples, symbolSamples.toString()),
    })
    invalidateSystemConfig()
    return updated
  }

  const persistEntryGuardConfig = async () => {
    const adaptiveEntryFloor = Math.round(
      parseInputNumber(entryGuardForm.adaptiveEntryFloor, 'Adaptive entry floor')
    )
    const adaptiveEntryLambda = parseInputNumber(
      entryGuardForm.adaptiveEntryLambda,
      'Logic coherence penalty'
    )

    if (adaptiveEntryFloor < 0 || adaptiveEntryFloor > 60) {
      throw new Error('Adaptive entry floor must be between 0 and 60')
    }
    if (adaptiveEntryLambda < 0 || adaptiveEntryLambda > 1) {
      throw new Error('Logic coherence penalty must be between 0 and 1')
    }

    const updated = await api.updateResonanceGuardConfig({
      adaptive_entry_floor: adaptiveEntryFloor,
      adaptive_entry_lambda: adaptiveEntryLambda,
    })

    setEntryGuardForm({
      adaptiveEntryFloor: formatIntegerOrFallback(
        updated.adaptive_entry_floor,
        adaptiveEntryFloor.toString()
      ),
      adaptiveEntryLambda: formatNumberOrFallback(
        updated.adaptive_entry_lambda,
        adaptiveEntryLambda
      ),
    })
    invalidateSystemConfig()
    return updated
  }

  const handleToggleRealBacktest = async () => {
    setSavingRealBacktest(true)
    try {
      const updated = await persistRealBacktestConfig(!realBacktestEnabled)
      toast.success(`Real backtest mode ${(updated.real_backtest_enabled ?? !realBacktestEnabled) ? 'enabled' : 'disabled'}`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update real backtest mode')
    } finally {
      setSavingRealBacktest(false)
    }
  }

  const handleSaveRiskConfig = async () => {
    setSavingRealBacktest(true)
    try {
      await persistRealBacktestConfig(realBacktestEnabled)
      toast.success('Real backtest risk settings saved')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to save real backtest risk settings')
    } finally {
      setSavingRealBacktest(false)
    }
  }

  const handleSaveAdaptiveMemoryConfig = async () => {
    setSavingAdaptiveMemory(true)
    try {
      await persistAdaptiveMemoryConfig()
      toast.success('Adaptive memory settings saved')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to save adaptive memory settings')
    } finally {
      setSavingAdaptiveMemory(false)
    }
  }

  const handleSaveEntryGuardConfig = async () => {
    setSavingEntryGuard(true)
    try {
      await persistEntryGuardConfig()
      toast.success('Resonance guard settings saved')
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to save resonance guard settings')
    } finally {
      setSavingEntryGuard(false)
    }
  }

  const handleOpenSettings = () => {
    window.history.pushState({}, '', '/settings')
    window.dispatchEvent(new PopStateEvent('popstate'))
  }

  const handleOpenMonitor = () => {
    window.history.pushState({}, '', '/real-backtest')
    window.dispatchEvent(new PopStateEvent('popstate'))
  }

  return (
    <section className={`rounded-3xl border border-orange-500/60 bg-[radial-gradient(circle_at_top_right,_rgba(249,115,22,0.22),_transparent_35%),linear-gradient(145deg,rgba(24,24,27,0.96),rgba(9,9,11,0.98))] p-5 shadow-[0_0_0_1px_rgba(249,115,22,0.08),0_18px_50px_rgba(0,0,0,0.35)] ${className}`}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <span className="rounded-full border border-orange-400/70 bg-orange-500/15 px-2.5 py-1 text-[10px] font-bold uppercase tracking-[0.22em] text-orange-300">
              Global Trading Settings
            </span>
            <span className="rounded-full border border-red-400/70 bg-red-500/15 px-2.5 py-1 text-[10px] font-bold uppercase tracking-[0.22em] text-red-300">
              [EXPERIMENTAL]
            </span>
          </div>
          <h2 className="text-lg font-semibold text-white">全局交易设置</h2>
          <p className="mt-1 text-sm leading-6 text-zinc-300">
            这里集中展示系统级执行状态。实战回测模式已强制挂载到页面顶部，不受 `is_admin` 或管理员模式控制。
          </p>
        </div>
        <div className="rounded-2xl border border-orange-400/30 bg-black/20 px-3 py-2 text-right">
          <div className="text-[10px] font-semibold uppercase tracking-[0.18em] text-zinc-500">Visible For</div>
          <div className="mt-1 text-sm font-medium text-orange-100">{user?.email || 'Current User'}</div>
        </div>
      </div>

      <div className="mt-5 grid gap-4 md:grid-cols-2">
        <div className="rounded-2xl border border-zinc-700/80 bg-zinc-950/75 p-4">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <div className="mb-2 flex items-center gap-2">
                <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-sky-500/12 text-sky-300">
                  <Bot size={16} />
                </div>
                <div>
                  <h3 className="text-sm font-semibold text-white">AI 自动交易</h3>
                  <p className="text-xs text-zinc-500">按 Trader 单独启停，状态在此汇总显示</p>
                </div>
              </div>
              <p className="text-xs leading-5 text-zinc-400">
                当前账号共有 {totalTraderCount} 个 Trader，正在运行 {runningTraderCount} 个。
              </p>
            </div>
            <div className="flex flex-col items-end gap-2">
              <div className={`relative inline-flex h-7 w-12 items-center rounded-full border ${
                aiAutoTradingActive ? 'border-emerald-400/60 bg-emerald-500/80' : 'border-zinc-600 bg-zinc-700'
              }`}>
                <span
                  className={`inline-block h-5 w-5 transform rounded-full bg-white transition-transform ${
                    aiAutoTradingActive ? 'translate-x-6' : 'translate-x-1'
                  }`}
                />
              </div>
              <span className={`text-xs font-semibold ${
                aiAutoTradingActive ? 'text-emerald-400' : effectiveLoadingTraders ? 'text-zinc-500' : 'text-zinc-400'
              }`}>
                {effectiveLoadingTraders ? 'Loading...' : aiAutoTradingActive ? 'Running' : 'Stopped'}
              </span>
            </div>
          </div>
          {showManageButton && (
            <button
              type="button"
              onClick={handleOpenSettings}
              className="mt-4 inline-flex items-center gap-2 rounded-xl border border-zinc-700 bg-zinc-900 px-3 py-2 text-xs font-medium text-zinc-200 transition-colors hover:border-zinc-500 hover:text-white"
            >
              Open Settings
              <ExternalLink size={14} />
            </button>
          )}
        </div>

        <div className="rounded-2xl border border-orange-400/80 bg-[linear-gradient(160deg,rgba(249,115,22,0.16),rgba(239,68,68,0.08),rgba(9,9,11,0.94))] p-4 shadow-[inset_0_0_0_1px_rgba(251,146,60,0.16)]">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <div className="mb-2 flex items-center gap-2">
                <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-orange-500/18 text-orange-200">
                  <FlaskConical size={16} />
                </div>
                <div>
                  <div className="mb-1 flex flex-wrap items-center gap-2">
                    <h3 className="text-sm font-semibold text-white">实战回测模式</h3>
                    <span className="rounded-full border border-red-400/70 bg-red-500/15 px-2 py-0.5 text-[10px] font-bold uppercase tracking-[0.18em] text-red-300">
                      [BETA]
                    </span>
                  </div>
                  <p className="text-xs text-orange-100/75">Directional Resonance 驱动物理下单与影子系统同步</p>
                </div>
              </div>
              <p className="text-xs leading-5 text-zinc-200/85">
                开启后，实弹回测执行器会每 3 分钟扫描一次，并在成交后执行 T+15 强制离场。
              </p>
              <button
                type="button"
                onClick={handleOpenMonitor}
                className="mt-4 inline-flex items-center gap-2 rounded-xl border border-orange-300/60 bg-black/25 px-3 py-2 text-xs font-semibold uppercase tracking-[0.18em] text-orange-100 transition-colors hover:border-orange-200 hover:bg-orange-500/10"
              >
                Open Monitor
                <ExternalLink size={14} />
              </button>
            </div>
            <div className="flex flex-col items-end gap-2">
              <button
                type="button"
                onClick={handleToggleRealBacktest}
                disabled={savingRealBacktest}
                className={`relative inline-flex h-8 w-14 items-center rounded-full border transition-all ${
                  realBacktestEnabled
                    ? 'border-orange-200 bg-orange-500 shadow-[0_0_18px_rgba(249,115,22,0.35)]'
                    : 'border-orange-400/40 bg-zinc-800'
                } ${savingRealBacktest ? 'cursor-not-allowed opacity-60' : ''}`}
              >
                <span
                  className={`inline-block h-6 w-6 transform rounded-full bg-white transition-transform ${
                    realBacktestEnabled ? 'translate-x-7' : 'translate-x-1'
                  }`}
                />
              </button>
              <span className={`text-xs font-semibold ${
                realBacktestEnabled ? 'text-orange-200' : 'text-orange-100/70'
              }`}>
                {savingRealBacktest ? 'Saving...' : realBacktestEnabled ? 'Armed' : 'Dormant'}
              </span>
            </div>
          </div>

      <div className="mt-4 border-t border-orange-400/20 pt-4">
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <label className="block">
                <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-orange-100/60">
                  单笔最大保证金 (U)
                </span>
                <input
                  type="number"
                  min="0"
                  step="0.01"
                  disabled={savingRealBacktest}
                  value={riskForm.maxMarginPerTrade}
                  onChange={(event) =>
                    setRiskForm((prev) => ({ ...prev, maxMarginPerTrade: event.target.value }))
                  }
                  className="w-full rounded-xl border border-orange-300/20 bg-black/30 px-3 py-2 text-sm text-orange-50 outline-none transition focus:border-orange-200 focus:ring-2 focus:ring-orange-400/25 disabled:cursor-not-allowed disabled:opacity-60"
                  placeholder="5.00"
                />
              </label>

              <label className="block">
                <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-orange-100/60">
                  系统预留保证金 (U)
                </span>
                <input
                  type="number"
                  min="0"
                  step="0.01"
                  disabled={savingRealBacktest}
                  value={riskForm.reserveMargin}
                  onChange={(event) =>
                    setRiskForm((prev) => ({ ...prev, reserveMargin: event.target.value }))
                  }
                  className="w-full rounded-xl border border-orange-300/20 bg-black/30 px-3 py-2 text-sm text-orange-50 outline-none transition focus:border-orange-200 focus:ring-2 focus:ring-orange-400/25 disabled:cursor-not-allowed disabled:opacity-60"
                  placeholder="6.00"
                />
              </label>

              <label className="block">
                <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-orange-100/60">
                  最低平均期望 (%)
                </span>
                <input
                  type="number"
                  min="0"
                  step="0.01"
                  disabled={savingRealBacktest}
                  value={riskForm.minEvThresholdPct}
                  onChange={(event) =>
                    setRiskForm((prev) => ({ ...prev, minEvThresholdPct: event.target.value }))
                  }
                  className="w-full rounded-xl border border-orange-300/20 bg-black/30 px-3 py-2 text-sm text-orange-50 outline-none transition focus:border-orange-200 focus:ring-2 focus:ring-orange-400/25 disabled:cursor-not-allowed disabled:opacity-60"
                  placeholder="0.10"
                />
              </label>

              <label className="block">
                <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-orange-100/60">
                  最高平均期望 (%)
                </span>
                <input
                  type="number"
                  min="0"
                  step="0.01"
                  disabled={savingRealBacktest}
                  value={riskForm.maxEvThresholdPct}
                  onChange={(event) =>
                    setRiskForm((prev) => ({ ...prev, maxEvThresholdPct: event.target.value }))
                  }
                  className="w-full rounded-xl border border-orange-300/20 bg-black/30 px-3 py-2 text-sm text-orange-50 outline-none transition focus:border-orange-200 focus:ring-2 focus:ring-orange-400/25 disabled:cursor-not-allowed disabled:opacity-60"
                  placeholder="0.25"
                />
              </label>
            </div>

            <div className="mt-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <p className="text-[11px] leading-5 text-orange-100/65">
                EV 阈值以百分比输入，后端按小数值存储。示例 0.19% 会触发 76% 的资金使用率。
              </p>
              <button
                type="button"
                onClick={handleSaveRiskConfig}
                disabled={savingRealBacktest}
                className="inline-flex items-center justify-center rounded-xl border border-orange-300/60 bg-orange-500/15 px-4 py-2 text-xs font-semibold uppercase tracking-[0.18em] text-orange-100 transition-colors hover:border-orange-200 hover:bg-orange-500/20 disabled:cursor-not-allowed disabled:opacity-60"
              >
                Save Risk Settings
              </button>
            </div>
          </div>
        </div>
      </div>

      <div className="mt-4 rounded-2xl border border-emerald-400/30 bg-[linear-gradient(160deg,rgba(16,185,129,0.14),rgba(15,23,42,0.88))] p-4 shadow-[inset_0_0_0_1px_rgba(16,185,129,0.10)]">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div className="min-w-0">
            <div className="mb-2 flex items-center gap-2">
              <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-emerald-500/15 text-emerald-200">
                <span className="text-sm font-bold">RG</span>
              </div>
              <div>
                <h3 className="text-sm font-semibold text-white">共振保护核</h3>
                <p className="text-xs text-emerald-100/70">CV Efficiency + Adaptive Logic Floor</p>
              </div>
            </div>
            <p className="text-xs leading-5 text-emerald-50/80">
              基于 CV 模型：均值越高，对因子不一致的容忍度越高；均值越低，不一致将被大幅压缩。
            </p>
          </div>
          <div className="rounded-2xl border border-emerald-300/20 bg-black/20 px-3 py-2 text-right">
            <div className="text-[10px] font-semibold uppercase tracking-[0.18em] text-emerald-100/50">Adaptive</div>
            <div className="mt-1 text-xs text-emerald-50/80">
              Entry Floor / Lambda
            </div>
          </div>
        </div>

        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <label className="block">
            <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-emerald-100/65">
              当前入场逻辑地板
            </span>
            <input
              type="number"
              min="0"
              max="60"
              step="1"
              disabled={savingEntryGuard}
              value={entryGuardForm.adaptiveEntryFloor}
              onChange={(event) =>
                setEntryGuardForm((prev) => ({
                  ...prev,
                  adaptiveEntryFloor: event.target.value,
                }))
              }
              className="w-full rounded-xl border border-emerald-300/20 bg-black/30 px-3 py-2 text-sm text-emerald-50 outline-none transition focus:border-emerald-200 focus:ring-2 focus:ring-emerald-400/25 disabled:cursor-not-allowed disabled:opacity-60"
              placeholder="45"
            />
          </label>

          <label className="block">
            <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-emerald-100/65">
              共振效率敏感度 (Lambda)
            </span>
            <input
              type="number"
              min="0"
              max="1"
              step="0.01"
              disabled={savingEntryGuard}
              value={entryGuardForm.adaptiveEntryLambda}
              onChange={(event) =>
                setEntryGuardForm((prev) => ({
                  ...prev,
                  adaptiveEntryLambda: event.target.value,
                }))
              }
              className="w-full rounded-xl border border-emerald-300/20 bg-black/30 px-3 py-2 text-sm text-emerald-50 outline-none transition focus:border-emerald-200 focus:ring-2 focus:ring-emerald-400/25 disabled:cursor-not-allowed disabled:opacity-60"
              placeholder="0.50"
            />
          </label>
        </div>

        <div className="mt-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-[11px] leading-5 text-emerald-50/70">
            系统会基于 CV 效率模型对入场共振分做物理压缩，并按 45-60 区间自动寻找更平稳的逻辑地板。
          </p>
          <button
            type="button"
            onClick={handleSaveEntryGuardConfig}
            disabled={savingEntryGuard}
            className="inline-flex items-center justify-center rounded-xl border border-emerald-300/60 bg-emerald-500/15 px-4 py-2 text-xs font-semibold uppercase tracking-[0.18em] text-emerald-100 transition-colors hover:border-emerald-200 hover:bg-emerald-500/20 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {savingEntryGuard ? 'Saving...' : 'Save Guard Settings'}
          </button>
        </div>
      </div>

      <div className="mt-4 rounded-2xl border border-cyan-400/30 bg-[linear-gradient(160deg,rgba(8,145,178,0.16),rgba(15,23,42,0.88))] p-4 shadow-[inset_0_0_0_1px_rgba(34,211,238,0.12)]">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div className="min-w-0">
            <div className="mb-2 flex items-center gap-2">
              <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-cyan-500/15 text-cyan-200">
                <Database size={16} />
              </div>
              <div>
                <h3 className="text-sm font-semibold text-white">数据引擎设置</h3>
                <p className="text-xs text-cyan-100/70">Memory Depth for Adaptive IC</p>
              </div>
            </div>
            <p className="text-xs leading-5 text-cyan-50/80">
              系统将根据样本量自动适配指数衰减半衰期。
            </p>
          </div>
          <div className="rounded-2xl border border-cyan-300/20 bg-black/20 px-3 py-2 text-right">
            <div className="text-[10px] font-semibold uppercase tracking-[0.18em] text-cyan-100/50">Live</div>
            <div className="mt-1 text-xs text-cyan-50/80">
              Global / Sector / Symbol
            </div>
          </div>
        </div>

        <div className="mt-4 grid gap-3 lg:grid-cols-3">
          <label className="block">
            <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-cyan-100/65">
              全局样本数 (宏观记忆)
            </span>
            <input
              type="number"
              min="500"
              max="10000"
              step="1"
              disabled={savingAdaptiveMemory}
              value={adaptiveMemoryForm.globalSamples}
              onChange={(event) =>
                setAdaptiveMemoryForm((prev) => ({ ...prev, globalSamples: event.target.value }))
              }
              className="w-full rounded-xl border border-cyan-300/20 bg-black/30 px-3 py-2 text-sm text-cyan-50 outline-none transition focus:border-cyan-200 focus:ring-2 focus:ring-cyan-400/25 disabled:cursor-not-allowed disabled:opacity-60"
              placeholder="5000"
            />
          </label>

          <label className="block">
            <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-cyan-100/65">
              赛道样本数 (中观记忆)
            </span>
            <input
              type="number"
              min="300"
              max="5000"
              step="1"
              disabled={savingAdaptiveMemory}
              value={adaptiveMemoryForm.sectorSamples}
              onChange={(event) =>
                setAdaptiveMemoryForm((prev) => ({ ...prev, sectorSamples: event.target.value }))
              }
              className="w-full rounded-xl border border-cyan-300/20 bg-black/30 px-3 py-2 text-sm text-cyan-50 outline-none transition focus:border-cyan-200 focus:ring-2 focus:ring-cyan-400/25 disabled:cursor-not-allowed disabled:opacity-60"
              placeholder="3000"
            />
          </label>

          <label className="block">
            <span className="mb-1 block text-[10px] font-semibold uppercase tracking-[0.18em] text-cyan-100/65">
              币种样本数 (微观记忆)
            </span>
            <input
              type="number"
              min="100"
              max="3000"
              step="1"
              disabled={savingAdaptiveMemory}
              value={adaptiveMemoryForm.symbolSamples}
              onChange={(event) =>
                setAdaptiveMemoryForm((prev) => ({ ...prev, symbolSamples: event.target.value }))
              }
              className="w-full rounded-xl border border-cyan-300/20 bg-black/30 px-3 py-2 text-sm text-cyan-50 outline-none transition focus:border-cyan-200 focus:ring-2 focus:ring-cyan-400/25 disabled:cursor-not-allowed disabled:opacity-60"
              placeholder="1000"
            />
          </label>
        </div>

        <div className="mt-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-[11px] leading-5 text-cyan-50/70">
            建议值：全局 5000 / 赛道 3000 / 币种 1000。修改后会同步影响 IC 重算与分箱分析。
          </p>
          <button
            type="button"
            onClick={handleSaveAdaptiveMemoryConfig}
            disabled={savingAdaptiveMemory}
            className="inline-flex items-center justify-center rounded-xl border border-cyan-300/60 bg-cyan-500/15 px-4 py-2 text-xs font-semibold uppercase tracking-[0.18em] text-cyan-100 transition-colors hover:border-cyan-200 hover:bg-cyan-500/20 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {savingAdaptiveMemory ? 'Saving...' : 'Save Memory Settings'}
          </button>
        </div>
      </div>
    </section>
  )
}
