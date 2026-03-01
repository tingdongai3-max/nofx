import { useState, useEffect, useCallback } from 'react'
import { useLanguage } from '../contexts/LanguageContext'
import { api, type ScreenerFilterCondition, type CoinQuantState } from '../lib/api'
import { Plus, Trash2 } from 'lucide-react'

const TIMEFRAMES = ['5m', '15m', '1h']
const INDICATORS = [
  { value: 'rsi_7', label: 'RSI(7)' },
  { value: 'adx', label: 'ADX(14)' },
  { value: 'bias', label: 'Bias(20)' },
  { value: 'vol_mult', label: 'VolMult' },
  { value: 'emabias', label: 'EMABias' },
  { value: 'boll_pct', label: 'Boll%' },
  { value: 'atr_pct', label: 'ATR%' },
  { value: 'macd', label: 'MACD' },
]
const OPERATORS = [
  { value: '>', label: '>' },
  { value: '<', label: '<' },
  { value: '>=', label: '>=' },
  { value: '<=', label: '<=' },
  { value: '==', label: '=' },
  { value: 'between', label: '介于' },
]

const REFRESH_INTERVAL_MS = 3000

const defaultCondition: ScreenerFilterCondition = {
  timeframe: '5m',
  indicator: 'adx',
  operator: '>',
  value: 25,
}

export function QuantDashboardPage() {
  const { language } = useLanguage()
  const [filters, setFilters] = useState<ScreenerFilterCondition[]>([defaultCondition])
  const [symbols, setSymbols] = useState<CoinQuantState[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const fetchFilter = useCallback(async () => {
    if (filters.length === 0) {
      setSymbols([])
      return
    }
    setLoading(true)
    setError(null)
    try {
      const res = await api.screenerFilter(filters)
      setSymbols(res.symbols || [])
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Request failed')
      setSymbols([])
    } finally {
      setLoading(false)
    }
  }, [filters])

  useEffect(() => {
    fetchFilter()
  }, [fetchFilter])

  useEffect(() => {
    const t = setInterval(fetchFilter, REFRESH_INTERVAL_MS)
    return () => clearInterval(t)
  }, [fetchFilter])

  const addCondition = () => {
    setFilters((prev) => [...prev, { ...defaultCondition }])
  }

  const removeCondition = (i: number) => {
    setFilters((prev) => prev.filter((_, j) => j !== i))
  }

  const updateCondition = (i: number, patch: Partial<ScreenerFilterCondition>) => {
    setFilters((prev) => {
      const next = [...prev]
      next[i] = { ...next[i], ...patch }
      return next
    })
  }

  const isZh = language === 'zh'

  return (
    <div className="max-w-[1920px] mx-auto px-4 py-6">
      <h1 className="text-2xl font-bold text-nofx-gold mb-6">
        {isZh ? '量化雷达 / Screener' : 'Quant Screener'}
      </h1>

      {/* 顶部控制台：多条件过滤 */}
      <div
        className="rounded-xl border p-4 mb-6"
        style={{ borderColor: '#2B3139', background: '#1E2329' }}
      >
        <div className="flex flex-wrap items-center gap-2 mb-2">
          <span className="text-sm font-medium text-gray-300">
            {isZh ? '过滤条件' : 'Filters'}
          </span>
          <button
            type="button"
            onClick={addCondition}
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg text-sm font-medium transition-colors"
            style={{ background: '#F0B90B', color: '#0B0E11' }}
          >
            <Plus className="w-4 h-4" />
            {isZh ? '添加条件' : 'Add'}
          </button>
        </div>
        <div className="flex flex-col gap-3">
          {filters.map((f, i) => (
            <div key={i} className="flex flex-wrap items-center gap-2">
              <select
                value={f.timeframe}
                onChange={(e) => updateCondition(i, { timeframe: e.target.value })}
                className="rounded-lg px-3 py-2 text-sm border bg-black/30"
                style={{ borderColor: '#2B3139', color: '#EAECEF' }}
              >
                {TIMEFRAMES.map((tf) => (
                  <option key={tf} value={tf}>
                    {tf}
                  </option>
                ))}
              </select>
              <select
                value={f.indicator}
                onChange={(e) => updateCondition(i, { indicator: e.target.value })}
                className="rounded-lg px-3 py-2 text-sm border bg-black/30 min-w-[100px]"
                style={{ borderColor: '#2B3139', color: '#EAECEF' }}
              >
                {INDICATORS.map((ind) => (
                  <option key={ind.value} value={ind.value}>
                    {ind.label}
                  </option>
                ))}
              </select>
              <select
                value={f.operator}
                onChange={(e) => updateCondition(i, { operator: e.target.value })}
                className="rounded-lg px-3 py-2 text-sm border bg-black/30"
                style={{ borderColor: '#2B3139', color: '#EAECEF' }}
              >
                {OPERATORS.map((op) => (
                  <option key={op.value} value={op.value}>
                    {op.label}
                  </option>
                ))}
              </select>
              <input
                type="number"
                value={f.value}
                onChange={(e) =>
                  updateCondition(i, { value: parseFloat(e.target.value) || 0 })
                }
                step="0.1"
                className="rounded-lg px-3 py-2 text-sm border w-20 bg-black/30"
                style={{ borderColor: '#2B3139', color: '#EAECEF' }}
              />
              {f.operator === 'between' && (
                <input
                  type="number"
                  value={f.value2 ?? 0}
                  onChange={(e) =>
                    updateCondition(i, { value2: parseFloat(e.target.value) || 0 })
                  }
                  step="0.1"
                  placeholder="max"
                  className="rounded-lg px-3 py-2 text-sm border w-20 bg-black/30"
                  style={{ borderColor: '#2B3139', color: '#EAECEF' }}
                />
              )}
              <button
                type="button"
                onClick={() => removeCondition(i)}
                className="p-2 rounded-lg text-red-400 hover:bg-red-400/10"
                title={isZh ? '删除条件' : 'Remove'}
              >
                <Trash2 className="w-4 h-4" />
              </button>
            </div>
          ))}
        </div>
        <p className="text-xs text-gray-500 mt-2">
          {isZh ? '每 3 秒静默刷新，纯内存查询' : 'Auto refresh every 3s, in-memory only'}
        </p>
      </div>

      {error && (
        <div className="mb-4 py-2 px-4 rounded-lg bg-red-500/10 text-red-400 text-sm">
          {error}
        </div>
      )}

      {/* 实时数据表 */}
      <div
        className="rounded-xl border overflow-hidden"
        style={{ borderColor: '#2B3139', background: '#1E2329' }}
      >
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr style={{ borderBottom: '1px solid #2B3139' }}>
                <th className="text-left py-3 px-4 font-semibold text-gray-300">
                  {isZh ? '币种' : 'Symbol'}
                </th>
                <th className="text-right py-3 px-4 font-semibold text-gray-300">
                  {isZh ? '价格' : 'Price'}
                </th>
                <th className="text-right py-3 px-4 font-semibold text-gray-300">5m RSI</th>
                <th className="text-right py-3 px-4 font-semibold text-gray-300">5m ADX</th>
                <th className="text-right py-3 px-4 font-semibold text-gray-300">5m Bias</th>
                <th className="text-right py-3 px-4 font-semibold text-gray-300">15m RSI</th>
                <th className="text-right py-3 px-4 font-semibold text-gray-300">15m ADX</th>
                <th className="text-right py-3 px-4 font-semibold text-gray-300">1h RSI</th>
                <th className="text-right py-3 px-4 font-semibold text-gray-300">1h ADX</th>
              </tr>
            </thead>
            <tbody>
              {loading && symbols.length === 0 ? (
                <tr>
                  <td colSpan={9} className="py-8 text-center text-gray-500">
                    {isZh ? '加载中...' : 'Loading...'}
                  </td>
                </tr>
              ) : (
                symbols.map((row) => (
                  <tr
                    key={row.symbol}
                    className="hover:bg-white/5 transition-colors"
                    style={{ borderBottom: '1px solid #2B3139' }}
                  >
                    <td className="py-2.5 px-4 font-medium text-nofx-gold">{row.symbol}</td>
                    <td className="py-2.5 px-4 text-right text-gray-300">
                      {row.price != null ? row.price.toFixed(4) : '-'}
                    </td>
                    <td className="py-2.5 px-4 text-right text-gray-300">
                      {row.by_tf?.['5m']?.rsi != null
                        ? Number(row.by_tf['5m'].rsi).toFixed(2)
                        : '-'}
                    </td>
                    <td className="py-2.5 px-4 text-right text-gray-300">
                      {row.by_tf?.['5m']?.adx != null
                        ? Number(row.by_tf['5m'].adx).toFixed(2)
                        : '-'}
                    </td>
                    <td className="py-2.5 px-4 text-right text-gray-300">
                      {row.by_tf?.['5m']?.bias != null
                        ? Number(row.by_tf['5m'].bias).toFixed(2)
                        : '-'}
                    </td>
                    <td className="py-2.5 px-4 text-right text-gray-300">
                      {row.by_tf?.['15m']?.rsi != null
                        ? Number(row.by_tf['15m'].rsi).toFixed(2)
                        : '-'}
                    </td>
                    <td className="py-2.5 px-4 text-right text-gray-300">
                      {row.by_tf?.['15m']?.adx != null
                        ? Number(row.by_tf['15m'].adx).toFixed(2)
                        : '-'}
                    </td>
                    <td className="py-2.5 px-4 text-right text-gray-300">
                      {row.by_tf?.['1h']?.rsi != null
                        ? Number(row.by_tf['1h'].rsi).toFixed(2)
                        : '-'}
                    </td>
                    <td className="py-2.5 px-4 text-right text-gray-300">
                      {row.by_tf?.['1h']?.adx != null
                        ? Number(row.by_tf['1h'].adx).toFixed(2)
                        : '-'}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <div
          className="py-2 px-4 text-xs text-gray-500"
          style={{ borderTop: '1px solid #2B3139' }}
        >
          {isZh ? `共 ${symbols.length} 个匹配` : `${symbols.length} matched`}
        </div>
      </div>
    </div>
  )
}
