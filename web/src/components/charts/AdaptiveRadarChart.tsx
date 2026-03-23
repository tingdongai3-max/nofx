import {
  PolarAngleAxis,
  PolarGrid,
  PolarRadiusAxis,
  Radar,
  RadarChart,
  ResponsiveContainer,
  Tooltip,
} from 'recharts'
import type { AdaptiveFactorState } from '../../types'
import type { Language } from '../../i18n/translations'

interface AdaptiveRadarChartProps {
  factors?: AdaptiveFactorState[]
  language: Language
  warningMessage?: string
}

function factorLabel(name: string, language: Language): string {
  switch (name) {
    case 'market':
      return language === 'zh' ? '市场' : 'Market'
    case 'trend':
      return language === 'zh' ? '趋势' : 'Trend'
    case 'volume_spike':
      return language === 'zh' ? '量价脉冲' : 'Vol-Price Spike'
    case 'quant':
      return language === 'zh' ? '量化' : 'Quant'
    case 'social':
      return language === 'zh' ? '情绪' : 'Social'
    case 'onchain':
      return language === 'zh' ? '链上' : 'On-Chain'
    default:
      return name
  }
}

export function AdaptiveRadarChart({
  factors = [],
  language,
  warningMessage,
}: AdaptiveRadarChartProps) {
  const data = factors.map((factor) => ({
    label: factorLabel(factor.name, language),
    weight_pct: factor.final_weight * 100,
    ic_signal: ((factor.final_ic ?? factor.ic) + 1) * 50,
    raw_ic: factor.final_ic ?? factor.ic,
    raw_weight_pct: factor.final_weight * 100,
  }))

  if (data.length === 0) {
    return (
      <div className="flex h-72 items-center justify-center rounded-2xl border border-dashed border-white/10 bg-black/20 text-sm text-[#848E9C]">
        {language === 'zh' ? '等待自适应样本...' : 'Waiting for adaptive samples...'}
      </div>
    )
  }

  return (
    <div className="relative h-72">
      {warningMessage ? (
        <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center">
          <div className="max-w-[78%] rounded-2xl border border-[#F59E0B]/30 bg-black/70 px-5 py-4 text-center text-xs font-semibold tracking-[0.18em] text-[#FDE68A] shadow-[0_18px_50px_rgba(0,0,0,0.35)]">
            {warningMessage}
          </div>
        </div>
      ) : null}
      <ResponsiveContainer width="100%" height="100%">
        <RadarChart data={data} outerRadius="70%">
          <PolarGrid stroke="rgba(255,255,255,0.10)" />
          <PolarAngleAxis
            dataKey="label"
            tick={{ fill: '#EAECEF', fontSize: 12 }}
          />
          <PolarRadiusAxis
            domain={[0, 100]}
            tick={{ fill: '#848E9C', fontSize: 11 }}
            axisLine={false}
          />
          <Tooltip
            contentStyle={{
              backgroundColor: 'rgba(10, 15, 24, 0.96)',
              border: '1px solid rgba(255,255,255,0.12)',
              borderRadius: '0.75rem',
            }}
            formatter={(value: number, name: string, payload) => {
              if (name === 'Weight') {
                return [`${Number(value).toFixed(1)}%`, language === 'zh' ? '当前权重' : 'Weight']
              }
              const rawIC = Number(payload?.payload?.raw_ic || 0)
              return [
                `${rawIC >= 0 ? '+' : ''}${rawIC.toFixed(2)}`,
                language === 'zh' ? 'IC 信号' : 'IC Signal',
              ]
            }}
          />
          <Radar
            name="Weight"
            dataKey="weight_pct"
            stroke="#F0B90B"
            fill="#F0B90B"
            fillOpacity={0.28}
            strokeWidth={2}
          />
          <Radar
            name="IC Signal"
            dataKey="ic_signal"
            stroke="#22D3EE"
            fill="#22D3EE"
            fillOpacity={0.12}
            strokeWidth={1.6}
          />
        </RadarChart>
      </ResponsiveContainer>
    </div>
  )
}
