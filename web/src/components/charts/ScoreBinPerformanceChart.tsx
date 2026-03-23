import {
  Bar,
  CartesianGrid,
  Cell,
  ComposedChart,
  LabelList,
  Legend,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { Language } from '../../i18n/translations'
import type { ScoreBinPerformance } from '../../types'

interface ScoreBinPerformanceChartProps {
  data: ScoreBinPerformance[]
  language: Language
  realtimeBackcast?: boolean
  windowSize?: number
}

function finiteNumber(value: number | undefined, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

interface CurveTierStyle {
  key: 'thin' | 'mid' | 'strong'
  strokeWidth: number
  strokeOpacity: number
}

interface ChartRow extends ScoreBinPerformance {
  ev_long: number
  ev_short: number
  median_ev_long: number
  median_ev_short: number
  profit_factor_long: number
  profit_factor_short: number
  ev_long_visual: number
  ev_short_visual: number
  ev_long_thin: number | null
  ev_long_mid: number | null
  ev_long_strong: number | null
  ev_short_thin: number | null
  ev_short_mid: number | null
  ev_short_strong: number | null
  trade_count_label: string
  bar_opacity: number
  weighted_rank: number
  window_label: string
}

const curveTierStyles: CurveTierStyle[] = [
  { key: 'thin', strokeWidth: 1, strokeOpacity: 0.2 },
  { key: 'mid', strokeWidth: 2, strokeOpacity: 0.6 },
  { key: 'strong', strokeWidth: 4, strokeOpacity: 1.0 },
]

function resolveCurveTier(tradeCount: number): CurveTierStyle {
  if (tradeCount <= 5) {
    return curveTierStyles[0]
  }
  if (tradeCount <= 30) {
    return curveTierStyles[1]
  }
  return curveTierStyles[2]
}

function clampValue(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max)
}

function expandDomainEdge(value: number, isMin: boolean): number {
  if (value === 0) {
    return 0
  }
  if (isMin) {
    return value < 0 ? value * 1.2 : value * 0.8
  }
  return value > 0 ? value * 1.2 : value * 0.8
}

function buildExpectedValueDomain(rows: ScoreBinPerformance[]) {
  const robustSource =
    rows.filter((row) => row.trade_count > 10) || []
  const domainSource = robustSource.length > 0 ? robustSource : rows
  const values = domainSource.flatMap((row) => [
    finiteNumber(row.ev_long),
    finiteNumber(row.ev_short),
  ])

  if (values.length === 0) {
    return { min: -0.005, max: 0.005 }
  }

  const minValue = Math.min(...values)
  const maxValue = Math.max(...values)
  if (Math.abs(maxValue-minValue) <= 1e-9) {
    const padding = Math.max(Math.abs(maxValue) * 0.2, 0.005)
    return {
      min: minValue - padding,
      max: maxValue + padding,
    }
  }

  let domainMin = expandDomainEdge(minValue, true)
  let domainMax = expandDomainEdge(maxValue, false)
  if (Math.abs(domainMax-domainMin) < 0.005) {
    const midpoint = (domainMax + domainMin) / 2
    const padding = 0.0025
    domainMin = midpoint - padding
    domainMax = midpoint + padding
  }

  return {
    min: domainMin,
    max: domainMax,
  }
}

function average(values: number[]) {
  if (values.length === 0) {
    return 0
  }
  return values.reduce((sum, value) => sum + value, 0) / values.length
}

function computeSeparationScore(rows: ChartRow[]): number | null {
  const robustRows = rows.filter((row) => row.trade_count > 10)
  const source = robustRows.length > 0 ? robustRows : rows
  if (source.length < 2) {
    return null
  }

  let highRows = source.filter((row) => row.bin_start >= 50)
  let lowRows = source.filter((row) => row.bin_start < 50)

  if (highRows.length === 0 || lowRows.length === 0) {
    const sorted = [...source].sort((left, right) => left.bin_start - right.bin_start)
    const midpointIndex = Math.ceil(sorted.length / 2)
    lowRows = sorted.slice(0, midpointIndex)
    highRows = sorted.slice(midpointIndex)
  }

  if (highRows.length === 0 || lowRows.length === 0) {
    return null
  }

  return Math.abs(
    average(highRows.map((row) => row.ev_long)) -
      average(lowRows.map((row) => row.ev_long))
  )
}

function formatExpectedValueAxis(value: number) {
  const percent = value * 100
  return `${percent > 0 ? '+' : ''}${percent.toFixed(1)}%`
}

function formatExpectedValueTooltip(value: number) {
  const percent = value * 100
  return `${percent > 0 ? '+' : ''}${percent.toFixed(2)}%`
}

function formatProfitFactor(value: number) {
  if (!Number.isFinite(value)) return '--'
  return value >= 99.9 ? '99.9' : value.toFixed(2)
}

function formatWeightedRank(value: number) {
  return `P${value.toFixed(1)}`
}

function formatScorePoint(center: number, language: Language) {
  return language === 'zh' ? `分数 ${center}` : `Score ${center}`
}

function formatWindowNumber(value: number) {
  if (!Number.isFinite(value)) {
    return '--'
  }
  const rounded = Math.round(value * 10) / 10
  return Number.isInteger(rounded) ? rounded.toFixed(0) : rounded.toFixed(1)
}

function formatSlidingWindowLabel(center: number, windowSize: number, language: Language) {
  const halfWindow = windowSize / 2
  const lower = center - halfWindow
  const upper = center + halfWindow
  if (language === 'zh') {
    return `窗口 ${formatWindowNumber(lower)} - ${formatWindowNumber(upper)}`
  }
  return `Window ${formatWindowNumber(lower)} - ${formatWindowNumber(upper)}`
}

function resolveTickEvery(rowCount: number) {
  if (rowCount > 70) {
    return 10
  }
  if (rowCount > 35) {
    return 5
  }
  if (rowCount > 18) {
    return 2
  }
  return 1
}

function shouldRenderSparseLabel(index: number, rowCount: number) {
  const tickEvery = resolveTickEvery(rowCount)
  return index === 0 || index === rowCount - 1 || index%tickEvery === 0
}

function smoothingHint(row: ScoreBinPerformance | undefined, language: Language) {
  if (!row?.smoothed) {
    return null
  }
  if (row.smoothed_by === 'global') {
    return language === 'zh'
      ? '[数据稀疏] 已由全局均值进行贝叶斯平滑'
      : '[Sparse Data] Bayesian-smoothed by the global mean'
  }
  return language === 'zh'
    ? '[数据稀疏] 已由赛道均值进行贝叶斯平滑'
    : '[Sparse Data] Bayesian-smoothed by the sector mean'
}

export function ScoreBinPerformanceChart({
  data,
  language,
  realtimeBackcast = false,
  windowSize = 5,
}: ScoreBinPerformanceChartProps) {
  const sanitizedRows = [...data]
    .sort((left, right) => left.bin_start - right.bin_start)
    .map((row) => {
      const evLong = finiteNumber(row.ev_long)
      const evShort = finiteNumber(row.ev_short)
      const medianEvLong = finiteNumber(row.median_ev_long, evLong)
      const medianEvShort = finiteNumber(row.median_ev_short, evShort)
      return {
        ...row,
        ev_long: evLong,
        ev_short: evShort,
        median_ev_long: medianEvLong,
        median_ev_short: medianEvShort,
        profit_factor_long: finiteNumber(row.profit_factor_long),
        profit_factor_short: finiteNumber(row.profit_factor_short),
      }
    })
  const { min: leftAxisMin, max: leftAxisMax } = buildExpectedValueDomain(sanitizedRows)
  const maxTradeCount = sanitizedRows.reduce(
    (max, row) => Math.max(max, Math.max(row.trade_count, 0)),
    0
  )

  const chartData: ChartRow[] = sanitizedRows.map((row, index) => {
    const tier = resolveCurveTier(row.trade_count)
    const evLongVisual = clampValue(row.ev_long, leftAxisMin, leftAxisMax)
    const evShortVisual = clampValue(row.ev_short, leftAxisMin, leftAxisMax)
    const localSupportRank =
      maxTradeCount > 0 ? (Math.max(row.trade_count, 0) / maxTradeCount) * 100 : 0

    return {
      ...row,
      ev_long_visual: evLongVisual,
      ev_short_visual: evShortVisual,
      ev_long_thin: tier.key === 'thin' ? evLongVisual : null,
      ev_long_mid: tier.key === 'mid' ? evLongVisual : null,
      ev_long_strong: tier.key === 'strong' ? evLongVisual : null,
      ev_short_thin: tier.key === 'thin' ? evShortVisual : null,
      ev_short_mid: tier.key === 'mid' ? evShortVisual : null,
      ev_short_strong: tier.key === 'strong' ? evShortVisual : null,
      weighted_rank: localSupportRank,
      trade_count_label:
        shouldRenderSparseLabel(index, sanitizedRows.length) ? `N=${row.trade_count}` : '',
      bar_opacity: row.trade_count < 10 ? 0.3 : 1,
      window_label: formatSlidingWindowLabel(row.bin_start, windowSize, language),
    }
  })

  const maxProfitFactor = chartData.reduce(
    (max, row) =>
      Math.max(
        max,
        finiteNumber(row.profit_factor_long),
        finiteNumber(row.profit_factor_short)
      ),
    1
  )
  const rightAxisMax = Math.max(1.5, Math.min(Math.ceil(maxProfitFactor + 0.5), 100))
  const separationScore = realtimeBackcast ? computeSeparationScore(chartData) : null

  if (chartData.length === 0) {
    return (
      <div className="rounded-[24px] border border-dashed border-white/10 bg-black/20 px-4 py-12 text-center text-sm text-[#848E9C]">
        {language === 'zh'
          ? '暂无已完成样本，无法生成分箱统计。'
          : 'No filled samples yet, performance bins are not available.'}
      </div>
    )
  }

  return (
    <div className="relative h-[340px]">
      {realtimeBackcast && separationScore !== null ? (
        <div className="absolute right-2 top-0 z-10 rounded-2xl border border-[#22D3EE]/20 bg-[#08131D]/90 px-3 py-2 text-right shadow-[0_12px_32px_rgba(0,0,0,0.25)]">
          <div className="text-[10px] uppercase tracking-[0.2em] text-[#7DD3FC]">
            {language === 'zh' ? '权重区分度' : 'Separation Score'}
          </div>
          <div className="mt-1 text-sm font-semibold text-[#CFFAFE]">
            {formatExpectedValueTooltip(separationScore)}
          </div>
        </div>
      ) : null}
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={chartData} margin={{ top: 20, right: 18, left: 4, bottom: 18 }}>
          <defs>
            <linearGradient id="scoreBinLongBarFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#F59E0B" stopOpacity={0.72} />
              <stop offset="95%" stopColor="#F59E0B" stopOpacity={0.18} />
            </linearGradient>
            <linearGradient id="scoreBinShortBarFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#3B82F6" stopOpacity={0.72} />
              <stop offset="95%" stopColor="#3B82F6" stopOpacity={0.18} />
            </linearGradient>
          </defs>

          <CartesianGrid stroke="rgba(255,255,255,0.08)" strokeDasharray="3 3" vertical={false} />
          <XAxis
            dataKey="bin_label"
            tickFormatter={(value, index) =>
              shouldRenderSparseLabel(index, chartData.length) ? String(value) : ''
            }
            tick={{ fill: '#94A3B8', fontSize: 11 }}
            tickLine={false}
            axisLine={{ stroke: 'rgba(255,255,255,0.08)' }}
            minTickGap={0}
          />
          <YAxis
            yAxisId="expected_value"
            orientation="left"
            domain={[leftAxisMin, leftAxisMax]}
            allowDataOverflow
            tickFormatter={(value) => formatExpectedValueAxis(Number(value))}
            tick={{ fill: '#67E8F9', fontSize: 11 }}
            tickLine={false}
            axisLine={false}
            width={68}
            label={{
              value: language === 'zh' ? '期望收益率 (EV %)' : 'Expected Value (EV %)',
              angle: -90,
              position: 'insideLeft',
              offset: 2,
              fill: '#67E8F9',
              fontSize: 11,
            }}
          />
          <YAxis
            yAxisId="profit_factor"
            orientation="right"
            domain={[0, rightAxisMax]}
            tick={{ fill: '#FCD34D', fontSize: 11 }}
            tickFormatter={(value) => formatProfitFactor(Number(value))}
            tickLine={false}
            axisLine={false}
            width={44}
          />
          <ReferenceLine
            yAxisId="expected_value"
            y={0}
            stroke="#F87171"
            strokeDasharray="6 4"
            strokeWidth={2}
          />
          <Tooltip
            contentStyle={{
              backgroundColor: 'rgba(10, 15, 24, 0.96)',
              border: '1px solid rgba(255,255,255,0.12)',
              borderRadius: '0.75rem',
            }}
            content={({ active, payload }) => {
              if (!active || !payload || payload.length === 0) {
                return null
              }

              const row = payload[0]?.payload as ChartRow | undefined
              if (!row) {
                return null
              }

              const hint = smoothingHint(row, language)
              return (
                <div className="rounded-xl border border-white/10 bg-[rgba(10,15,24,0.96)] px-3 py-2 text-xs text-[#E5E7EB] shadow-[0_14px_40px_rgba(0,0,0,0.35)]">
                  <div className="font-semibold text-white">
                    {formatScorePoint(row.bin_start, language)}
                  </div>
                  <div className="text-[#CBD5E1]">{row.window_label}</div>
                  <div className="mt-1 text-[#CBD5E1]">{`N=${row.trade_count}`}</div>
                  <div className="text-[#CBD5E1]">
                    Weighted_Rank {formatWeightedRank(row.weighted_rank)}
                  </div>
                  <div className="mt-1 text-[#FCD34D]">
                    {language === 'zh' ? '做多平均期望回报' : 'Long Mean EV'}{' '}
                    {formatExpectedValueTooltip(row.ev_long)}
                  </div>
                  <div className="text-[#E7C76B]">
                    {language === 'zh' ? '做多中位期望回报' : 'Long Median EV'}{' '}
                    {formatExpectedValueTooltip(row.median_ev_long)}
                  </div>
                  <div className="text-[#FCD34D]">
                    {language === 'zh' ? '做多盈利因子' : 'Long PF'} {formatProfitFactor(row.profit_factor_long)}
                  </div>
                  <div className="mt-1 text-[#93C5FD]">
                    {language === 'zh' ? '做空平均期望回报' : 'Short Mean EV'}{' '}
                    {formatExpectedValueTooltip(row.ev_short)}
                  </div>
                  <div className="text-[#A9CBF8]">
                    {language === 'zh' ? '做空中位期望回报' : 'Short Median EV'}{' '}
                    {formatExpectedValueTooltip(row.median_ev_short)}
                  </div>
                  <div className="text-[#93C5FD]">
                    {language === 'zh' ? '做空盈利因子' : 'Short PF'} {formatProfitFactor(row.profit_factor_short)}
                  </div>
                  {hint ? (
                    <div className="mt-2 max-w-[16rem] text-[11px] leading-5 text-[#FDE68A]">
                      {hint}
                    </div>
                  ) : null}
                </div>
              )
            }}
          />
          <Legend wrapperStyle={{ fontSize: 12 }} />
          <ReferenceLine
            yAxisId="profit_factor"
            y={1}
            stroke="#9CA3AF"
            strokeDasharray="6 4"
            strokeWidth={1}
          />
          <Bar
            yAxisId="profit_factor"
            dataKey="profit_factor_long"
            name={language === 'zh' ? 'Long PF' : 'Long PF'}
            fill="url(#scoreBinLongBarFill)"
            radius={[8, 8, 0, 0]}
            maxBarSize={chartData.length > 30 ? 10 : 18}
          >
            {chartData.map((row) => (
              <Cell
                key={`pf-long-${row.bin_start}`}
                fill="url(#scoreBinLongBarFill)"
                fillOpacity={row.bar_opacity}
                strokeOpacity={row.bar_opacity}
              />
            ))}
            <LabelList
              dataKey="trade_count_label"
              position="top"
              fill="#7F8A98"
              fontSize={10}
              offset={8}
            />
          </Bar>
          <Bar
            yAxisId="profit_factor"
            dataKey="profit_factor_short"
            name={language === 'zh' ? 'Short PF' : 'Short PF'}
            fill="url(#scoreBinShortBarFill)"
            radius={[8, 8, 0, 0]}
            maxBarSize={chartData.length > 30 ? 10 : 18}
          >
            {chartData.map((row) => (
              <Cell
                key={`pf-short-${row.bin_start}`}
                fill="url(#scoreBinShortBarFill)"
                fillOpacity={row.bar_opacity}
                strokeOpacity={row.bar_opacity}
              />
            ))}
          </Bar>
          <Line
            yAxisId="expected_value"
            type="monotone"
            dataKey="ev_long_visual"
            name={language === 'zh' ? 'EV Long' : 'EV Long'}
            stroke="#F59E0B"
            strokeWidth={1}
            strokeOpacity={0.18}
            dot={false}
            activeDot={{ r: 5 }}
          />
          {curveTierStyles.map((tier) => (
            <Line
              key={`long-${tier.key}`}
              yAxisId="expected_value"
              type="monotone"
              dataKey={`ev_long_${tier.key}`}
              stroke="#F59E0B"
              strokeWidth={tier.strokeWidth}
              strokeOpacity={tier.strokeOpacity}
              dot={false}
              legendType="none"
              connectNulls={false}
              activeDot={false}
            />
          ))}
          <Line
            yAxisId="expected_value"
            type="monotone"
            dataKey="ev_short_visual"
            name={language === 'zh' ? 'EV Short' : 'EV Short'}
            stroke="#60A5FA"
            strokeWidth={1}
            strokeOpacity={0.18}
            dot={false}
            activeDot={{ r: 5 }}
          />
          {curveTierStyles.map((tier) => (
            <Line
              key={`short-${tier.key}`}
              yAxisId="expected_value"
              type="monotone"
              dataKey={`ev_short_${tier.key}`}
              stroke="#60A5FA"
              strokeWidth={tier.strokeWidth}
              strokeOpacity={tier.strokeOpacity}
              dot={false}
              legendType="none"
              connectNulls={false}
              activeDot={false}
            />
          ))}
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}
