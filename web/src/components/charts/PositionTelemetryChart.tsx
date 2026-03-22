import type { FactorTelemetry } from '../../types'
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

interface PositionTelemetryChartProps {
  telemetry?: FactorTelemetry[]
  title?: string
  emptyLabel?: string
}

function formatTelemetryTime(timestamp: number): string {
  if (!timestamp) return '--:--'
  return new Date(timestamp).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function PositionTelemetryChart({
  telemetry = [],
  title = 'Price vs Heat',
  emptyLabel = 'Telemetry will appear after the next monitor cycle.',
}: PositionTelemetryChartProps) {
  const sortedTelemetry = [...telemetry].sort((a, b) => a.timestamp - b.timestamp)
  const chartData = sortedTelemetry.map((point) => ({
    ...point,
    time_label: formatTelemetryTime(point.timestamp),
  }))

  return (
    <div className="rounded-xl border border-white/10 bg-black/20 p-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <div>
          <div className="text-sm font-semibold text-nofx-text-main">{title}</div>
          <div className="text-xs text-nofx-text-muted">
            Price on left axis, heat pulse on right axis.
          </div>
        </div>
        <div className="rounded-full border border-cyan-400/20 bg-cyan-400/10 px-2.5 py-1 text-[11px] font-mono text-cyan-200">
          {chartData.length} pts
        </div>
      </div>

      {chartData.length > 0 ? (
        <div className="h-72">
          <ResponsiveContainer width="100%" height="100%">
            <ComposedChart data={chartData} margin={{ top: 8, right: 12, left: 0, bottom: 8 }}>
              <defs>
                <linearGradient id="telemetryHeatFill" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#22D3EE" stopOpacity={0.45} />
                  <stop offset="95%" stopColor="#22D3EE" stopOpacity={0.04} />
                </linearGradient>
              </defs>
              <CartesianGrid stroke="rgba(255,255,255,0.08)" strokeDasharray="3 3" />
              <XAxis
                dataKey="time_label"
                tick={{ fill: '#94A3B8', fontSize: 12 }}
                tickLine={false}
                axisLine={{ stroke: 'rgba(255,255,255,0.08)' }}
              />
              <YAxis
                yAxisId="price"
                orientation="left"
                tick={{ fill: '#F8FAFC', fontSize: 12 }}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value) => Number(value).toFixed(2)}
              />
              <YAxis
                yAxisId="heat"
                orientation="right"
                domain={[0, 100]}
                tick={{ fill: '#67E8F9', fontSize: 12 }}
                tickLine={false}
                axisLine={false}
              />
              <Tooltip
                contentStyle={{
                  backgroundColor: 'rgba(10, 15, 24, 0.96)',
                  border: '1px solid rgba(255,255,255,0.12)',
                  borderRadius: '0.75rem',
                }}
                formatter={(value: number, name: string) => {
                  if (name === 'Price') return [Number(value).toFixed(4), name]
                  return [Number(value).toFixed(1), name]
                }}
                labelFormatter={(_, payload) => {
                  const point = payload?.[0]?.payload as FactorTelemetry | undefined
                  if (!point) return ''
                  return new Date(point.timestamp).toLocaleString('zh-CN')
                }}
              />
              <Legend wrapperStyle={{ fontSize: 12 }} />
              <Area
                yAxisId="heat"
                type="monotone"
                dataKey="heat_score"
                name="Heat Score"
                stroke="#22D3EE"
                fill="url(#telemetryHeatFill)"
                strokeWidth={2}
              />
              <Line
                yAxisId="price"
                type="monotone"
                dataKey="price"
                name="Price"
                stroke="#F59E0B"
                strokeWidth={2.5}
                dot={false}
                activeDot={{ r: 4 }}
              />
              <Line
                yAxisId="heat"
                type="monotone"
                dataKey="trading_sub"
                name="Trading Sub"
                stroke="#38BDF8"
                strokeWidth={1.6}
                dot={false}
                strokeDasharray="6 4"
              />
              <Line
                yAxisId="heat"
                type="monotone"
                dataKey="quant_sub"
                name="Quant Sub"
                stroke="#A78BFA"
                strokeWidth={1.6}
                dot={false}
                strokeDasharray="4 4"
              />
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      ) : (
        <div className="flex h-56 items-center justify-center rounded-lg border border-dashed border-white/10 bg-black/20 text-sm text-nofx-text-muted">
          {emptyLabel}
        </div>
      )}
    </div>
  )
}
