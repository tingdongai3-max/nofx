import {
  PolarAngleAxis,
  PolarGrid,
  PolarRadiusAxis,
  Radar,
  RadarChart,
  ResponsiveContainer,
  Tooltip,
} from 'recharts'
import type { Language } from '../../i18n/translations'
import type { RealFireAttribution } from '../../types'

interface RealFireAttributionRadarProps {
  attribution?: RealFireAttribution
  language: Language
}

function labelForDimension(name: string, language: Language) {
  switch (name) {
    case 'global':
      return language === 'zh' ? '宏观' : 'Global'
    case 'sector':
      return language === 'zh' ? '中观' : 'Sector'
    case 'symbol':
      return language === 'zh' ? '微观' : 'Symbol'
    default:
      return name
  }
}

export function RealFireAttributionRadar({
  attribution,
  language,
}: RealFireAttributionRadarProps) {
  const dimensions = attribution?.dimensions ?? []
  const data = dimensions.map((dimension) => ({
    label: labelForDimension(dimension.name, language),
    real_weight: dimension.real_weight * 100,
    shadow_weight: dimension.shadow_weight * 100,
    correlation: dimension.correlation,
    retention: dimension.average_retention,
  }))

  if (data.length === 0 || (attribution?.sample_count ?? 0) === 0) {
    return (
      <div className="flex h-72 items-center justify-center rounded-2xl border border-dashed border-white/10 bg-black/20 text-sm text-zinc-500">
        {language === 'zh' ? '等待已平仓实盘样本...' : 'Waiting for closed live samples...'}
      </div>
    )
  }

  return (
    <div className="relative h-80">
      <ResponsiveContainer width="100%" height="100%">
        <RadarChart data={data} outerRadius="70%">
          <PolarGrid stroke="rgba(255,255,255,0.10)" />
          <PolarAngleAxis dataKey="label" tick={{ fill: '#EAECEF', fontSize: 12 }} />
          <PolarRadiusAxis
            domain={[0, 100]}
            tick={{ fill: '#71717A', fontSize: 11 }}
            axisLine={false}
          />
          <Tooltip
            contentStyle={{
              backgroundColor: 'rgba(8, 12, 20, 0.96)',
              border: '1px solid rgba(255,255,255,0.12)',
              borderRadius: '0.75rem',
            }}
            formatter={(value: number, name: string, payload) => {
              if (name === 'Shadow') {
                return [`${Number(value).toFixed(1)}%`, language === 'zh' ? '影子强度' : 'Shadow Strength']
              }
              if (name === 'Real') {
                return [`${Number(value).toFixed(1)}%`, language === 'zh' ? '实盘贡献' : 'Real Contribution']
              }
              return [
                `${Number(payload?.payload?.correlation ?? 0).toFixed(2)} / ${Number(payload?.payload?.retention ?? 0).toFixed(2)}x`,
                language === 'zh' ? '相关/保持率' : 'Corr / Retention',
              ]
            }}
          />
          <Radar
            name="Shadow"
            dataKey="shadow_weight"
            stroke="#F59E0B"
            fill="#F59E0B"
            fillOpacity={0.16}
            strokeWidth={1.8}
          />
          <Radar
            name="Real"
            dataKey="real_weight"
            stroke="#22C55E"
            fill="#22C55E"
            fillOpacity={0.22}
            strokeWidth={2.2}
          />
        </RadarChart>
      </ResponsiveContainer>
    </div>
  )
}
