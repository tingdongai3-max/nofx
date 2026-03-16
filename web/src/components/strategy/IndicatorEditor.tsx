import { Clock, Activity, TrendingUp, BarChart2, Info, Lock, ExternalLink, Zap, Check, AlertCircle, Key } from 'lucide-react'
import type { IndicatorConfig } from '../../types'

// Default NofxOS API Key
const DEFAULT_NOFXOS_API_KEY = 'cm_568c67eae410d912c54c'

interface IndicatorEditorProps {
  config: IndicatorConfig
  onChange: (config: IndicatorConfig) => void
  disabled?: boolean
  language: string
}

// 所有可用时间周期
const allTimeframes = [
  { value: '1m', label: '1m', category: 'scalp' },
  { value: '3m', label: '3m', category: 'scalp' },
  { value: '5m', label: '5m', category: 'scalp' },
  { value: '15m', label: '15m', category: 'intraday' },
  { value: '30m', label: '30m', category: 'intraday' },
  { value: '1h', label: '1h', category: 'intraday' },
  { value: '2h', label: '2h', category: 'swing' },
  { value: '4h', label: '4h', category: 'swing' },
  { value: '6h', label: '6h', category: 'swing' },
  { value: '8h', label: '8h', category: 'swing' },
  { value: '12h', label: '12h', category: 'swing' },
  { value: '1d', label: '1D', category: 'position' },
  { value: '3d', label: '3D', category: 'position' },
  { value: '1w', label: '1W', category: 'position' },
]

export function IndicatorEditor({
  config,
  onChange,
  disabled,
  language,
}: IndicatorEditorProps) {
  const t = (key: string) => {
    const translations: Record<string, Record<string, string>> = {
      // Section titles
      marketData: { zh: '市场数据', en: 'Market Data' },
      marketDataDesc: { zh: 'AI 分析所需的核心价格数据', en: 'Core price data for AI analysis' },
      technicalIndicators: { zh: '技术指标', en: 'Technical Indicators' },
      technicalIndicatorsDesc: { zh: '可选的技术分析指标，AI 可自行计算', en: 'Optional indicators, AI can calculate them' },
      marketSentiment: { zh: '市场情绪', en: 'Market Sentiment' },
      marketSentimentDesc: { zh: '持仓量、资金费率等市场情绪数据', en: 'OI, funding rate and market sentiment data' },
      quantData: { zh: '量化数据', en: 'Quant Data' },
      quantDataDesc: { zh: '资金流向、大户动向', en: 'Netflow, whale movements' },

      // Timeframes
      timeframes: { zh: '时间周期', en: 'Timeframes' },
      timeframesDesc: { zh: '选择 K 线分析周期，★ 为主周期（双击设置）', en: 'Select K-line timeframes, ★ = primary (double-click)' },
      timeframeKlineCounts: { zh: '各周期 K 线数量', en: 'K-line count per timeframe' },
      timeframeKlineCountsDesc: { zh: '为每个周期设置获取的 K 线根数，未填则用默认', en: 'Set count per timeframe; empty uses default' },
      auxiliaryTimeframe: { zh: '辅助周期', en: 'Auxiliary Timeframe' },
      auxiliaryTimeframeTag: { zh: '趋势辅助', en: 'Trend Only' },
      auxiliaryTimeframeDesc: { zh: '仅发送指标趋势判断，不发送K线数据，可帮助减少上下文长度', en: 'Only sends indicator trends, no K-lines, helps reduce context length' },
      auxiliaryTimeframeNone: { zh: '不使用辅助周期', en: 'No auxiliary timeframe' },
      none: { zh: '无', en: 'None' },
      scalp: { zh: '超短', en: 'Scalp' },
      intraday: { zh: '日内', en: 'Intraday' },
      swing: { zh: '波段', en: 'Swing' },
      position: { zh: '趋势', en: 'Position' },

      // Data types
      rawKlines: { zh: 'OHLCV 原始 K 线', en: 'Raw OHLCV K-lines' },
      rawKlinesDesc: { zh: '必须 - 开高低收量原始数据，AI 核心分析依据', en: 'Required - Open/High/Low/Close/Volume data for AI' },
      required: { zh: '必须', en: 'Required' },

      // Indicators
      ema: { zh: 'EMA 均线', en: 'EMA' },
      emaDesc: { zh: '指数移动平均线', en: 'Exponential Moving Average' },
      macd: { zh: 'MACD', en: 'MACD' },
      macdDesc: { zh: '异同移动平均线', en: 'Moving Average Convergence Divergence' },
      rsi: { zh: 'RSI', en: 'RSI' },
      rsiDesc: { zh: '相对强弱指标', en: 'Relative Strength Index' },
      atr: { zh: 'ATR', en: 'ATR' },
      atrDesc: { zh: '真实波幅均值', en: 'Average True Range' },
      adx: { zh: 'ADX', en: 'ADX' },
      adxDesc: { zh: '平均趋向指数', en: 'Average Directional Index' },
      boll: { zh: 'BOLL 布林带', en: 'Bollinger Bands' },
      bollDesc: { zh: '布林带指标（上中下轨）', en: 'Upper/Middle/Lower Bands' },
      bias: { zh: 'BIAS 乖离率', en: 'BIAS' },
      biasDesc: { zh: '收盘价相对均线的偏离程度', en: 'Deviation of price from moving average' },
      fibonacci: { zh: '斐波那契回撤', en: 'Fibonacci' },
      fibonacciDesc: { zh: '阻力/支撑位写入 AI 文本', en: 'Resistance/support levels in AI prompt' },
      czsc: { zh: '缠论 CZSC', en: 'Chan Theory (CZSC)' },
      czscDesc: { zh: '笔/线段/中枢/买卖点注入 AI，依标签做浪浪交易法', en: 'Bi/segment/zhongshu/buy-sell points in AI prompt for wave trading' },
      volume: { zh: '成交量', en: 'Volume' },
      volumeDesc: { zh: '交易量分析', en: 'Trading volume analysis' },
      volMult: { zh: '放量', en: 'Vol Mult' },
      volMultDesc: { zh: '当前 5 分钟滚动成交量，相对于「最近 N 小时」平均每 5 分钟成交量的倍数', en: '5‑min rolling volume vs avg 5‑min volume over last N hours' },
      volumeBaselineHours: { zh: '成交量参考基准（小时）', en: 'Volume Baseline (hours)' },
      liquidation: { zh: '爆仓数据', en: 'Liquidations' },
      liquidationDesc: {
        zh: '最近一段时间内的多空爆仓金额与多空爆仓比，用于识别“杀多/杀空”行情',
        en: 'Recent long/short liquidation notional and long/short ratio, to detect stop-hunting flows',
      },
      volumePOC: { zh: '筹码分布 (POC)', en: 'Volume POC' },
      volumePOCDesc: {
        zh: '主筹码密集区价格及 POC 偏离度，帮助判断当前位置是“筹码上方追高”还是“筹码下方淘金”',
        en: 'Volume point of control and deviation, to see whether price trades above or below main inventory zone',
      },
      orderBookDepth: { zh: '深度图墙体', en: 'Order Book Depth' },
      orderBookDepthDesc: {
        zh: 'Top20 档订单簿 1% 买卖深度与最近挂单大墙（包含 Spoofing 风险提示）',
        en: 'Top-20 order book 1% bid/ask depth and nearest large walls (with spoofing risk warning)',
      },
      oi: { zh: '持仓量', en: 'Open Interest' },
      oiDesc: { zh: '合约未平仓量', en: 'Futures open interest' },
      fundingRate: { zh: '资金费率', en: 'Funding Rate' },
      fundingRateDesc: { zh: '永续合约资金费率', en: 'Perpetual funding rate' },

      // OI Ranking
      oiRanking: { zh: 'OI 排行', en: 'OI Ranking' },
      oiRankingDesc: { zh: '持仓量增减排行', en: 'OI change ranking' },
      oiRankingNote: { zh: '显示持仓量增加/减少的币种排行，帮助发现资金流向', en: 'Shows coins with OI increase/decrease, helps identify capital flow' },

      // NetFlow Ranking
      netflowRanking: { zh: '资金流向', en: 'NetFlow' },
      netflowRankingDesc: { zh: '机构/散户资金流向', en: 'Institution/retail fund flow' },
      netflowRankingNote: { zh: '显示机构资金流入/流出排行，散户动向对比，发现聪明钱信号', en: 'Shows institution inflow/outflow ranking, retail flow comparison, Smart Money signals' },

      // Price Ranking
      priceRanking: { zh: '涨跌幅排行', en: 'Price Ranking' },
      priceRankingDesc: { zh: '涨跌幅排行榜', en: 'Gainers/losers ranking' },
      priceRankingNote: { zh: '显示涨幅/跌幅排行，结合资金流和持仓变化分析趋势强度', en: 'Shows top gainers/losers, combined with fund flow and OI for trend analysis' },
      priceRankingMulti: { zh: '多周期', en: 'Multi-period' },

      // Common settings
      duration: { zh: '周期', en: 'Duration' },
      limit: { zh: '数量', en: 'Limit' },

      // Tips
      aiCanCalculate: { zh: '💡 提示：AI 可自行计算这些指标，开启可减少 AI 计算量', en: '💡 Tip: AI can calculate these, enabling reduces AI workload' },

      // System defense watchdog
      trailingPanel: { zh: '系统防守引擎', en: 'System Defense Engine' },
      trailingPanelDesc: { zh: '不经过 AI：后台机器狗直接监听数据流并触发强制平仓', en: 'No AI approval: backend watchdog listens to market data and force-closes positions' },
      enableFractalDefense: { zh: '开启 2B 假突破防守', en: 'Enable 2B false-break defense' },
      enableFractalDefenseDesc: { zh: '15m 二次刺破前高/前低但实体收回，判定诱多/诱空，立即强平', en: '15m second sweep through prior high/low with rejection close triggers an immediate forced exit' },
      enableEMA20GapDefense: { zh: '开启 EMA20 引力缺口防守', en: 'Enable EMA20 gravity-gap defense' },
      enableEMA20GapDefenseDesc: { zh: '当整根 K 线完全脱离 EMA20，判定趋势引力崩塌，立即强平', en: 'Force-close when a full candle detaches from EMA20 and trend gravity collapses' },
      enable3BarTrailing: { zh: '开启 3K线动量追踪', en: 'Enable 3-bar momentum trailing' },
      enable3BarTrailingDesc: { zh: '以最近 3 根 K 线极值为系统止损线，破位即落袋', en: 'Use the last 3 candles extreme as a system stop and exit on break' },
      enableStagedTakeProfit: { zh: '允许分批止盈', en: 'Allow staged take profit' },
      enableStagedTakeProfitDesc: { zh: '关闭后 AI 只能使用单一止盈价全仓平仓，不能使用 take_profit_stages', en: 'When off, AI may only use single take_profit for full close; no take_profit_stages' },

      // NofxOS Data Provider
      nofxosTitle: { zh: 'NofxOS 量化数据源', en: 'NofxOS Data Provider' },
      nofxosDesc: { zh: '专业加密货币量化数据服务', en: 'Professional crypto quant data service' },
      nofxosFeatures: { zh: 'AI500 · OI排行 · 资金流向 · 涨跌榜', en: 'AI500 · OI Ranking · Fund Flow · Price Ranking' },
      viewApiDocs: { zh: 'API 文档', en: 'API Docs' },
      apiKey: { zh: 'API Key', en: 'API Key' },
      apiKeyPlaceholder: { zh: '输入 NofxOS API Key', en: 'Enter NofxOS API Key' },
      fillDefault: { zh: '填入默认', en: 'Fill Default' },
      connected: { zh: '已配置', en: 'Configured' },
      notConfigured: { zh: '未配置', en: 'Not Configured' },
      nofxosDataSources: { zh: 'NofxOS 数据源', en: 'NofxOS Data Sources' },
    }
    return translations[key]?.[language] || key
  }

  // 获取当前选中的时间周期
  const selectedTimeframes = config.klines.selected_timeframes || [config.klines.primary_timeframe]

  // 切换时间周期选择
  const toggleTimeframe = (tf: string) => {
    if (disabled) return
    const current = [...selectedTimeframes]
    const index = current.indexOf(tf)

    if (index >= 0) {
      if (current.length > 1) {
        current.splice(index, 1)
        const newPrimary = tf === config.klines.primary_timeframe ? current[0] : config.klines.primary_timeframe
        onChange({
          ...config,
          klines: {
            ...config.klines,
            selected_timeframes: current,
            primary_timeframe: newPrimary,
            enable_multi_timeframe: current.length > 1,
          },
        })
      }
    } else {
      current.push(tf)
      onChange({
        ...config,
        klines: {
          ...config.klines,
          selected_timeframes: current,
          enable_multi_timeframe: current.length > 1,
        },
      })
    }
  }

  // 设置主时间周期
  const setPrimaryTimeframe = (tf: string) => {
    if (disabled) return
    onChange({
      ...config,
      klines: {
        ...config.klines,
        primary_timeframe: tf,
      },
    })
  }

  const categoryColors: Record<string, string> = {
    scalp: '#F6465D',
    intraday: '#F0B90B',
    swing: '#0ECB81',
    position: '#60a5fa',
  }

  // Ensure enable_raw_klines is always true
  const ensureRawKlines = () => {
    if (!config.enable_raw_klines) {
      onChange({ ...config, enable_raw_klines: true })
    }
  }

  // Call on mount if needed
  if (config.enable_raw_klines === undefined || config.enable_raw_klines === false) {
    ensureRawKlines()
  }

  // Check if any NofxOS feature is enabled
  const hasNofxosEnabled = config.enable_quant_data || config.enable_oi_ranking || config.enable_netflow_ranking || config.enable_price_ranking
  const hasApiKey = !!config.nofxos_api_key

  return (
    <div className="space-y-5">
      {/* ============================================ */}
      {/* NofxOS Data Provider - Top Configuration    */}
      {/* ============================================ */}
      <div
        className="rounded-lg overflow-hidden relative"
        style={{
          background: 'linear-gradient(135deg, rgba(99, 102, 241, 0.08) 0%, rgba(168, 85, 247, 0.08) 50%, rgba(236, 72, 153, 0.08) 100%)',
          border: '1px solid rgba(139, 92, 246, 0.3)',
        }}
      >
        {/* Decorative gradient line at top */}
        <div
          className="absolute top-0 left-0 right-0 h-[2px]"
          style={{ background: 'linear-gradient(90deg, #6366f1, #a855f7, #ec4899)' }}
        />

        <div className="p-4">
          {/* Header Row */}
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2">
              <div
                className="w-8 h-8 rounded-lg flex items-center justify-center"
                style={{ background: 'linear-gradient(135deg, #6366f1, #a855f7)' }}
              >
                <Zap className="w-4 h-4 text-white" />
              </div>
              <div>
                <h3 className="text-sm font-semibold" style={{ color: '#EAECEF' }}>
                  {t('nofxosTitle')}
                </h3>
                <span className="text-[10px]" style={{ color: '#848E9C' }}>
                  {t('nofxosFeatures')}
                </span>
              </div>
            </div>

            {/* Status & API Docs */}
            <div className="flex items-center gap-2">
              {hasApiKey ? (
                <span className="flex items-center gap-1 text-[10px] px-2 py-1 rounded-full" style={{ background: 'rgba(14, 203, 129, 0.15)', color: '#0ECB81' }}>
                  <Check className="w-3 h-3" />
                  {t('connected')}
                </span>
              ) : (
                <span className="flex items-center gap-1 text-[10px] px-2 py-1 rounded-full" style={{ background: 'rgba(246, 70, 93, 0.15)', color: '#F6465D' }}>
                  <AlertCircle className="w-3 h-3" />
                  {t('notConfigured')}
                </span>
              )}
              <a
                href="https://nofxos.ai/api-docs"
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-1 text-[10px] px-2 py-1 rounded-full transition-all hover:scale-[1.02]"
                style={{
                  background: 'rgba(139, 92, 246, 0.2)',
                  color: '#a855f7',
                }}
              >
                <ExternalLink className="w-3 h-3" />
                {t('viewApiDocs')}
              </a>
            </div>
          </div>

          {/* API Key Input */}
          <div className="flex items-center gap-2">
            <div className="flex-1 relative">
              <Key className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4" style={{ color: '#848E9C' }} />
              <input
                type="text"
                value={config.nofxos_api_key || ''}
                onChange={(e) => !disabled && onChange({ ...config, nofxos_api_key: e.target.value })}
                disabled={disabled}
                placeholder={t('apiKeyPlaceholder')}
                className="w-full pl-9 pr-3 py-2 rounded-lg text-sm font-mono"
                style={{
                  background: 'rgba(30, 35, 41, 0.8)',
                  border: hasApiKey ? '1px solid rgba(14, 203, 129, 0.3)' : '1px solid rgba(139, 92, 246, 0.3)',
                  color: '#EAECEF',
                }}
              />
            </div>
            {!disabled && !config.nofxos_api_key && (
              <button
                type="button"
                onClick={() => onChange({ ...config, nofxos_api_key: DEFAULT_NOFXOS_API_KEY })}
                className="px-3 py-2 rounded-lg text-xs font-medium transition-all hover:scale-[1.02]"
                style={{
                  background: 'linear-gradient(135deg, #6366f1, #a855f7)',
                  color: '#fff',
                }}
              >
                {t('fillDefault')}
              </button>
            )}
          </div>

          {/* NofxOS Data Sources Grid */}
          <div className="mt-4">
            <div className="text-[10px] font-medium mb-2" style={{ color: '#848E9C' }}>
              {t('nofxosDataSources')}
            </div>
            <div className="grid grid-cols-2 gap-2">
              {/* Quant Data */}
              <div
                className="p-2.5 rounded-lg transition-all cursor-pointer"
                style={{
                  background: config.enable_quant_data ? 'rgba(96, 165, 250, 0.1)' : 'rgba(30, 35, 41, 0.5)',
                  border: config.enable_quant_data ? '1px solid rgba(96, 165, 250, 0.3)' : '1px solid rgba(43, 49, 57, 0.5)',
                  opacity: disabled ? 0.5 : 1,
                }}
                onClick={() => !disabled && onChange({ ...config, enable_quant_data: !config.enable_quant_data })}
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 rounded-full" style={{ background: '#60a5fa' }} />
                    <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('quantData')}</span>
                  </div>
                  <input
                    type="checkbox"
                    checked={config.enable_quant_data || false}
                    onChange={(e) => { e.stopPropagation(); !disabled && onChange({ ...config, enable_quant_data: e.target.checked }) }}
                    disabled={disabled}
                    className="w-3.5 h-3.5 rounded accent-blue-500"
                  />
                </div>
                <p className="text-[10px] mt-1" style={{ color: '#5E6673' }}>{t('quantDataDesc')}</p>
                {config.enable_quant_data && (
                  <div className="flex gap-3 mt-2">
                    <label className="flex items-center gap-1.5 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={config.enable_quant_oi !== false}
                        onChange={(e) => { e.stopPropagation(); !disabled && onChange({ ...config, enable_quant_oi: e.target.checked }) }}
                        disabled={disabled}
                        className="w-3 h-3 rounded accent-blue-500"
                      />
                      <span className="text-[10px]" style={{ color: '#EAECEF' }}>OI</span>
                    </label>
                    <label className="flex items-center gap-1.5 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={config.enable_quant_netflow !== false}
                        onChange={(e) => { e.stopPropagation(); !disabled && onChange({ ...config, enable_quant_netflow: e.target.checked }) }}
                        disabled={disabled}
                        className="w-3 h-3 rounded accent-blue-500"
                      />
                      <span className="text-[10px]" style={{ color: '#EAECEF' }}>Netflow</span>
                    </label>
                  </div>
                )}
              </div>

              {/* OI Ranking */}
              <div
                className="p-2.5 rounded-lg transition-all cursor-pointer"
                style={{
                  background: config.enable_oi_ranking ? 'rgba(34, 197, 94, 0.1)' : 'rgba(30, 35, 41, 0.5)',
                  border: config.enable_oi_ranking ? '1px solid rgba(34, 197, 94, 0.3)' : '1px solid rgba(43, 49, 57, 0.5)',
                  opacity: disabled ? 0.5 : 1,
                }}
                onClick={() => !disabled && onChange({
                  ...config,
                  enable_oi_ranking: !config.enable_oi_ranking,
                  ...(!config.enable_oi_ranking && !config.oi_ranking_duration ? { oi_ranking_duration: '1h' } : {}),
                  ...(!config.enable_oi_ranking && !config.oi_ranking_limit ? { oi_ranking_limit: 10 } : {}),
                })}
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 rounded-full" style={{ background: '#22c55e' }} />
                    <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('oiRanking')}</span>
                  </div>
                  <input
                    type="checkbox"
                    checked={config.enable_oi_ranking || false}
                    onChange={(e) => { e.stopPropagation(); !disabled && onChange({
                      ...config,
                      enable_oi_ranking: e.target.checked,
                      ...(e.target.checked && !config.oi_ranking_duration ? { oi_ranking_duration: '1h' } : {}),
                      ...(e.target.checked && !config.oi_ranking_limit ? { oi_ranking_limit: 10 } : {}),
                    }) }}
                    disabled={disabled}
                    className="w-3.5 h-3.5 rounded accent-green-500"
                  />
                </div>
                <p className="text-[10px] mt-1" style={{ color: '#5E6673' }}>{t('oiRankingDesc')}</p>
                {config.enable_oi_ranking && (
                  <div className="flex gap-2 mt-2" onClick={(e) => e.stopPropagation()}>
                    <select
                      value={config.oi_ranking_duration || '1h'}
                      onChange={(e) => !disabled && onChange({ ...config, oi_ranking_duration: e.target.value })}
                      disabled={disabled}
                      className="flex-1 px-2 py-1 rounded text-[10px]"
                      style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                    >
                      <option value="1h">1h</option>
                      <option value="4h">4h</option>
                      <option value="24h">24h</option>
                    </select>
                    <select
                      value={config.oi_ranking_limit || 10}
                      onChange={(e) => !disabled && onChange({ ...config, oi_ranking_limit: parseInt(e.target.value) })}
                      disabled={disabled}
                      className="w-14 px-2 py-1 rounded text-[10px]"
                      style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                    >
                      {[5, 10, 15, 20].map(n => <option key={n} value={n}>{n}</option>)}
                    </select>
                  </div>
                )}
              </div>

              {/* NetFlow Ranking */}
              <div
                className="p-2.5 rounded-lg transition-all cursor-pointer"
                style={{
                  background: config.enable_netflow_ranking ? 'rgba(245, 158, 11, 0.1)' : 'rgba(30, 35, 41, 0.5)',
                  border: config.enable_netflow_ranking ? '1px solid rgba(245, 158, 11, 0.3)' : '1px solid rgba(43, 49, 57, 0.5)',
                  opacity: disabled ? 0.5 : 1,
                }}
                onClick={() => !disabled && onChange({
                  ...config,
                  enable_netflow_ranking: !config.enable_netflow_ranking,
                  ...(!config.enable_netflow_ranking && !config.netflow_ranking_duration ? { netflow_ranking_duration: '1h' } : {}),
                  ...(!config.enable_netflow_ranking && !config.netflow_ranking_limit ? { netflow_ranking_limit: 10 } : {}),
                })}
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 rounded-full" style={{ background: '#f59e0b' }} />
                    <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('netflowRanking')}</span>
                  </div>
                  <input
                    type="checkbox"
                    checked={config.enable_netflow_ranking || false}
                    onChange={(e) => { e.stopPropagation(); !disabled && onChange({
                      ...config,
                      enable_netflow_ranking: e.target.checked,
                      ...(e.target.checked && !config.netflow_ranking_duration ? { netflow_ranking_duration: '1h' } : {}),
                      ...(e.target.checked && !config.netflow_ranking_limit ? { netflow_ranking_limit: 10 } : {}),
                    }) }}
                    disabled={disabled}
                    className="w-3.5 h-3.5 rounded accent-amber-500"
                  />
                </div>
                <p className="text-[10px] mt-1" style={{ color: '#5E6673' }}>{t('netflowRankingDesc')}</p>
                {config.enable_netflow_ranking && (
                  <div className="flex gap-2 mt-2" onClick={(e) => e.stopPropagation()}>
                    <select
                      value={config.netflow_ranking_duration || '1h'}
                      onChange={(e) => !disabled && onChange({ ...config, netflow_ranking_duration: e.target.value })}
                      disabled={disabled}
                      className="flex-1 px-2 py-1 rounded text-[10px]"
                      style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                    >
                      <option value="1h">1h</option>
                      <option value="4h">4h</option>
                      <option value="24h">24h</option>
                    </select>
                    <select
                      value={config.netflow_ranking_limit || 10}
                      onChange={(e) => !disabled && onChange({ ...config, netflow_ranking_limit: parseInt(e.target.value) })}
                      disabled={disabled}
                      className="w-14 px-2 py-1 rounded text-[10px]"
                      style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                    >
                      {[5, 10, 15, 20].map(n => <option key={n} value={n}>{n}</option>)}
                    </select>
                  </div>
                )}
              </div>

              {/* Price Ranking */}
              <div
                className="p-2.5 rounded-lg transition-all cursor-pointer"
                style={{
                  background: config.enable_price_ranking ? 'rgba(236, 72, 153, 0.1)' : 'rgba(30, 35, 41, 0.5)',
                  border: config.enable_price_ranking ? '1px solid rgba(236, 72, 153, 0.3)' : '1px solid rgba(43, 49, 57, 0.5)',
                  opacity: disabled ? 0.5 : 1,
                }}
                onClick={() => !disabled && onChange({
                  ...config,
                  enable_price_ranking: !config.enable_price_ranking,
                  ...(!config.enable_price_ranking && !config.price_ranking_duration ? { price_ranking_duration: '1h,4h,24h' } : {}),
                  ...(!config.enable_price_ranking && !config.price_ranking_limit ? { price_ranking_limit: 10 } : {}),
                })}
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 rounded-full" style={{ background: '#ec4899' }} />
                    <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('priceRanking')}</span>
                  </div>
                  <input
                    type="checkbox"
                    checked={config.enable_price_ranking || false}
                    onChange={(e) => { e.stopPropagation(); !disabled && onChange({
                      ...config,
                      enable_price_ranking: e.target.checked,
                      ...(e.target.checked && !config.price_ranking_duration ? { price_ranking_duration: '1h,4h,24h' } : {}),
                      ...(e.target.checked && !config.price_ranking_limit ? { price_ranking_limit: 10 } : {}),
                    }) }}
                    disabled={disabled}
                    className="w-3.5 h-3.5 rounded accent-pink-500"
                  />
                </div>
                <p className="text-[10px] mt-1" style={{ color: '#5E6673' }}>{t('priceRankingDesc')}</p>
                {config.enable_price_ranking && (
                  <div className="flex gap-2 mt-2" onClick={(e) => e.stopPropagation()}>
                    <select
                      value={config.price_ranking_duration || '1h,4h,24h'}
                      onChange={(e) => !disabled && onChange({ ...config, price_ranking_duration: e.target.value })}
                      disabled={disabled}
                      className="flex-1 px-2 py-1 rounded text-[10px]"
                      style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                    >
                      <option value="1h">1h</option>
                      <option value="4h">4h</option>
                      <option value="24h">24h</option>
                      <option value="1h,4h,24h">{t('priceRankingMulti')}</option>
                    </select>
                    <select
                      value={config.price_ranking_limit || 10}
                      onChange={(e) => !disabled && onChange({ ...config, price_ranking_limit: parseInt(e.target.value) })}
                      disabled={disabled}
                      className="w-14 px-2 py-1 rounded text-[10px]"
                      style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                    >
                      {[5, 10, 15, 20].map(n => <option key={n} value={n}>{n}</option>)}
                    </select>
                  </div>
                )}
              </div>
            </div>

            {/* Warning if features enabled but no API key */}
            {hasNofxosEnabled && !hasApiKey && (
              <div className="flex items-center gap-2 mt-3 p-2 rounded-lg" style={{ background: 'rgba(246, 70, 93, 0.1)', border: '1px solid rgba(246, 70, 93, 0.2)' }}>
                <AlertCircle className="w-4 h-4 flex-shrink-0" style={{ color: '#F6465D' }} />
                <span className="text-[10px]" style={{ color: '#F6465D' }}>
                  {language === 'zh' ? '请配置 API Key 以启用 NofxOS 数据源' : 'Please configure API Key to enable NofxOS data sources'}
                </span>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* ============================================ */}
      {/* Section 1: Market Data (Required)           */}
      {/* ============================================ */}
      <div className="rounded-lg overflow-hidden" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
        <div className="px-3 py-2 flex items-center gap-2" style={{ background: '#1E2329', borderBottom: '1px solid #2B3139' }}>
          <BarChart2 className="w-4 h-4" style={{ color: '#F0B90B' }} />
          <span className="text-sm font-medium" style={{ color: '#EAECEF' }}>{t('marketData')}</span>
          <span className="text-xs" style={{ color: '#848E9C' }}>- {t('marketDataDesc')}</span>
        </div>

        <div className="p-3 space-y-4">
          {/* Raw Klines - Required, Always On */}
          <div className="flex items-center justify-between p-3 rounded-lg" style={{ background: 'rgba(240, 185, 11, 0.08)', border: '1px solid rgba(240, 185, 11, 0.2)' }}>
            <div className="flex items-center gap-3">
              <div className="w-8 h-8 rounded-lg flex items-center justify-center" style={{ background: 'rgba(240, 185, 11, 0.15)' }}>
                <TrendingUp className="w-4 h-4" style={{ color: '#F0B90B' }} />
              </div>
              <div>
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium" style={{ color: '#EAECEF' }}>{t('rawKlines')}</span>
                  <span className="px-1.5 py-0.5 rounded text-[10px] font-medium flex items-center gap-1" style={{ background: 'rgba(240, 185, 11, 0.2)', color: '#F0B90B' }}>
                    <Lock className="w-2.5 h-2.5" />
                    {t('required')}
                  </span>
                </div>
                <p className="text-xs mt-0.5" style={{ color: '#848E9C' }}>{t('rawKlinesDesc')}</p>
              </div>
            </div>
            <input
              type="checkbox"
              checked={true}
              disabled={true}
              className="w-5 h-5 rounded accent-yellow-500 cursor-not-allowed"
            />
          </div>

          {/* Timeframe Selection */}
          <div>
            <div className="flex items-center gap-2 mb-2">
              <Clock className="w-3.5 h-3.5" style={{ color: '#848E9C' }} />
              <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('timeframes')}</span>
            </div>
            <p className="text-[10px] mb-2" style={{ color: '#5E6673' }}>{t('timeframesDesc')}</p>

            {/* Timeframe Grid */}
            <div className="space-y-1.5">
              {(['scalp', 'intraday', 'swing', 'position'] as const).map((category) => {
                const categoryTfs = allTimeframes.filter((tf) => tf.category === category)
                return (
                  <div key={category} className="flex items-center gap-2">
                    <span className="text-[10px] w-10 flex-shrink-0" style={{ color: categoryColors[category] }}>
                      {t(category)}
                    </span>
                    <div className="flex flex-wrap gap-1">
                      {categoryTfs.map((tf) => {
                        const isSelected = selectedTimeframes.includes(tf.value)
                        const isPrimary = config.klines.primary_timeframe === tf.value
                        return (
                          <button
                            key={tf.value}
                            onClick={() => toggleTimeframe(tf.value)}
                            onDoubleClick={() => setPrimaryTimeframe(tf.value)}
                            disabled={disabled}
                            className={`px-2 py-1 rounded text-xs font-medium transition-all ${
                              isSelected ? '' : 'opacity-40 hover:opacity-70'
                            }`}
                            style={{
                              background: isSelected ? `${categoryColors[category]}15` : 'transparent',
                              border: `1px solid ${isSelected ? categoryColors[category] : '#2B3139'}`,
                              color: isSelected ? categoryColors[category] : '#848E9C',
                              boxShadow: isPrimary ? `0 0 0 2px ${categoryColors[category]}` : undefined,
                            }}
                            title={isPrimary ? `${tf.label} (Primary)` : tf.label}
                          >
                            {tf.label}
                            {isPrimary && <span className="ml-0.5 text-[8px]">★</span>}
                          </button>
                        )
                      })}
                    </div>
                  </div>
                )
              })}
            </div>

            {/* 各周期 K 线数量 (TimeframeCounts) */}
            {selectedTimeframes.length > 0 && (
              <div className="mt-3 pt-2" style={{ borderTop: '1px solid #2B3139' }}>
                <p className="text-[10px] font-medium mb-1.5" style={{ color: '#EAECEF' }}>{t('timeframeKlineCounts')}</p>
                <p className="text-[10px] mb-2" style={{ color: '#5E6673' }}>{t('timeframeKlineCountsDesc')}</p>
                <div className="flex flex-wrap gap-2">
                  {selectedTimeframes.map((tf) => {
                    const label = allTimeframes.find((t) => t.value === tf)?.label ?? tf
                    const value = config.klines.timeframe_counts?.[tf] ?? config.klines.primary_count ?? 30
                    return (
                      <div key={tf} className="flex items-center gap-1">
                        <span className="text-[10px] w-8" style={{ color: '#848E9C' }}>{label}:</span>
                        <input
                          type="number"
                          value={value}
                          onChange={(e) => {
                            if (disabled) return
                            const v = parseInt(e.target.value, 10)
                            const raw = { ...(config.klines.timeframe_counts || {}), [tf]: Number.isNaN(v) || v <= 0 ? undefined : v }
                            if (raw[tf] === undefined) delete raw[tf]
                            const next: Record<string, number> = {}
                            for (const [k, val] of Object.entries(raw)) {
                              if (typeof val === 'number') next[k] = val
                            }
                            onChange({
                              ...config,
                              klines: { ...config.klines, timeframe_counts: Object.keys(next).length ? next : undefined },
                            })
                          }}
                          disabled={disabled}
                          min={10}
                          max={500}
                          className="w-14 px-1.5 py-0.5 rounded text-xs text-center"
                          style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                        />
                      </div>
                    )
                  })}
                </div>
              </div>
            )}

            {/* 辅助周期选择 (Auxiliary Timeframe) */}
            <div className="mt-3 pt-2" style={{ borderTop: '1px solid #2B3139' }}>
              <div className="flex items-center gap-2 mb-2">
                <p className="text-[10px] font-medium" style={{ color: '#EAECEF' }}>{t('auxiliaryTimeframe')}</p>
                <span className="text-[10px] px-1.5 py-0.5 rounded" style={{ background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81' }}>{t('auxiliaryTimeframeTag')}</span>
              </div>
              <p className="text-[10px] mb-2" style={{ color: '#5E6673' }}>{t('auxiliaryTimeframeDesc')}</p>
              <div className="flex flex-wrap gap-1.5">
                <button
                  onClick={() => {
                    if (disabled) return
                    onChange({
                      ...config,
                      klines: { ...config.klines, auxiliary_timeframe: undefined },
                    })
                  }}
                  disabled={disabled}
                  className={`px-2 py-1 rounded text-xs font-medium transition-all ${
                    !config.klines.auxiliary_timeframe ? '' : 'opacity-40 hover:opacity-70'
                  }`}
                  style={{
                    background: !config.klines.auxiliary_timeframe ? 'rgba(14, 203, 129, 0.15)' : 'transparent',
                    border: `1px solid ${!config.klines.auxiliary_timeframe ? '#0ECB81' : '#2B3139'}`,
                    color: !config.klines.auxiliary_timeframe ? '#0ECB81' : '#848E9C',
                  }}
                  title={t('auxiliaryTimeframeNone')}
                >
                  {t('none')}
                </button>
                {allTimeframes.map((tf) => {
                  const isSelected = config.klines.auxiliary_timeframe === tf.value
                  const categoryColor = categoryColors[tf.category]
                  return (
                    <button
                      key={tf.value}
                      onClick={() => {
                        if (disabled) return
                        onChange({
                          ...config,
                          klines: { ...config.klines, auxiliary_timeframe: tf.value },
                        })
                      }}
                      disabled={disabled}
                      className={`px-2 py-1 rounded text-xs font-medium transition-all ${
                        isSelected ? '' : 'opacity-40 hover:opacity-70'
                      }`}
                      style={{
                        background: isSelected ? `${categoryColor}15` : 'transparent',
                        border: `1px solid ${isSelected ? categoryColor : '#2B3139'}`,
                        color: isSelected ? categoryColor : '#848E9C',
                      }}
                    >
                      {tf.label}
                    </button>
                  )
                })}
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* ============================================ */}
      {/* Section 2: Technical Indicators (Optional)  */}
      {/* ============================================ */}
      <div className="rounded-lg overflow-hidden" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
        <div className="px-3 py-2 flex items-center gap-2" style={{ background: '#1E2329', borderBottom: '1px solid #2B3139' }}>
          <Activity className="w-4 h-4" style={{ color: '#0ECB81' }} />
          <span className="text-sm font-medium" style={{ color: '#EAECEF' }}>{t('technicalIndicators')}</span>
          <span className="text-xs" style={{ color: '#848E9C' }}>- {t('technicalIndicatorsDesc')}</span>
        </div>

        <div className="p-3">
          {/* Tip */}
          <div className="flex items-start gap-2 mb-3 p-2 rounded" style={{ background: 'rgba(14, 203, 129, 0.05)' }}>
            <Info className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" style={{ color: '#0ECB81' }} />
            <p className="text-[10px]" style={{ color: '#848E9C' }}>{t('aiCanCalculate')}</p>
          </div>

          {/* Indicator Grid */}
          <div className="grid grid-cols-2 gap-2">
            {[
              { key: 'enable_ema', label: 'ema', desc: 'emaDesc', color: '#F0B90B', periodKey: 'ema_periods', defaultPeriods: '20,50' },
              { key: 'enable_macd', label: 'macd', desc: 'macdDesc', color: '#a855f7' },
              { key: 'enable_rsi', label: 'rsi', desc: 'rsiDesc', color: '#F6465D', periodKey: 'rsi_periods', defaultPeriods: '7,14' },
              { key: 'enable_atr', label: 'atr', desc: 'atrDesc', color: '#60a5fa', periodKey: 'atr_periods', defaultPeriods: '14' },
              { key: 'enable_adx', label: 'adx', desc: 'adxDesc', color: '#A855F7', periodKey: 'adx_periods', defaultPeriods: '14' },
              { key: 'enable_boll', label: 'boll', desc: 'bollDesc', color: '#ec4899', periodKey: 'boll_periods', defaultPeriods: '20' },
              { key: 'enable_bias', label: 'bias', desc: 'biasDesc', color: '#14b8a6', periodKey: 'bias_periods', defaultPeriods: '6,12,24' },
              { key: 'enable_fibonacci', label: 'fibonacci', desc: 'fibonacciDesc', color: '#f59e0b' },
              { key: 'enable_czsc', label: 'czsc', desc: 'czscDesc', color: '#8b5cf6' },
            ].map(({ key, label, desc, color, periodKey, defaultPeriods }) => (
              <div
                key={key}
                className="p-2.5 rounded-lg transition-all"
                style={{
                  background: config[key as keyof IndicatorConfig] ? `${color}08` : 'transparent',
                  border: `1px solid ${config[key as keyof IndicatorConfig] ? `${color}30` : '#2B3139'}`,
                }}
              >
                <div className="flex items-center justify-between mb-1">
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 rounded-full" style={{ background: color }} />
                    <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t(label)}</span>
                  </div>
                  <input
                    type="checkbox"
                    checked={config[key as keyof IndicatorConfig] as boolean || false}
                    onChange={(e) => !disabled && onChange({ ...config, [key]: e.target.checked })}
                    disabled={disabled}
                    className="w-4 h-4 rounded accent-yellow-500"
                  />
                </div>
                <p className="text-[10px] mb-1.5" style={{ color: '#5E6673' }}>{t(desc)}</p>
                {periodKey && config[key as keyof IndicatorConfig] && (
                  <input
                    type="text"
                    value={(config[periodKey as keyof IndicatorConfig] as number[])?.join(',') || defaultPeriods}
                    onChange={(e) => {
                      if (disabled) return
                      const periods = e.target.value
                        .split(',')
                        .map((s) => parseInt(s.trim()))
                        .filter((n) => !isNaN(n) && n > 0)
                      onChange({ ...config, [periodKey]: periods })
                    }}
                    disabled={disabled}
                    placeholder={defaultPeriods}
                    className="w-full px-2 py-1 rounded text-[10px] text-center"
                    style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                  />
                )}
              </div>
            ))}
          </div>

          {/* 缠论 CZSC 服务 URL：仅在勾选 CZSC 时展示，允许覆盖默认本地地址 */}
          {config.enable_czsc && (
            <div className="mt-3 space-y-1.5">
              <span className="text-[10px]" style={{ color: '#848E9C' }}>
                {language === 'zh'
                  ? '缠论服务 URL（留空则使用默认 http://127.0.0.1:8765）'
                  : 'CZSC service URL (leave empty for http://127.0.0.1:8765)'}
              </span>
              <input
                type="text"
                value={config.czsc_service_url || ''}
                placeholder="http://127.0.0.1:8765"
                onChange={(e) => {
                  if (disabled) return
                  onChange({ ...config, czsc_service_url: e.target.value })
                }}
                disabled={disabled}
                className="w-full px-2 py-1 rounded text-[10px] font-mono"
                style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
              />
            </div>
          )}
        </div>
      </div>

      {/* ============================================ */}
      {/* Section 3: Market Sentiment                 */}
      {/* ============================================ */}
      <div className="rounded-lg overflow-hidden" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
        <div className="px-3 py-2 flex items-center gap-2" style={{ background: '#1E2329', borderBottom: '1px solid #2B3139' }}>
          <TrendingUp className="w-4 h-4" style={{ color: '#22c55e' }} />
          <span className="text-sm font-medium" style={{ color: '#EAECEF' }}>{t('marketSentiment')}</span>
          <span className="text-xs" style={{ color: '#848E9C' }}>- {t('marketSentimentDesc')}</span>
        </div>

        <div className="p-3">
          <div className="grid grid-cols-3 gap-2">
            {[
              { key: 'enable_volume', label: 'volume', desc: 'volumeDesc', color: '#c084fc' },
              { key: 'enable_vol_mult', label: 'volMult', desc: 'volMultDesc', color: '#22d3ee' },
              { key: 'enable_oi', label: 'oi', desc: 'oiDesc', color: '#34d399' },
              { key: 'enable_funding_rate', label: 'fundingRate', desc: 'fundingRateDesc', color: '#fbbf24' },
              { key: 'enable_liquidation', label: 'liquidation', desc: 'liquidationDesc', color: '#fb923c' },
              { key: 'enable_volume_poc', label: 'volumePOC', desc: 'volumePOCDesc', color: '#38bdf8' },
              { key: 'enable_order_book_depth', label: 'orderBookDepth', desc: 'orderBookDepthDesc', color: '#4ade80' },
            ].map(({ key, label, desc, color }) => (
              <div
                key={key}
                className="p-2.5 rounded-lg transition-all"
                style={{
                  background: config[key as keyof IndicatorConfig] ? `${color}08` : 'transparent',
                  border: `1px solid ${config[key as keyof IndicatorConfig] ? `${color}30` : '#2B3139'}`,
                }}
              >
                <div className="flex items-center justify-between mb-1">
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 rounded-full" style={{ background: color }} />
                    <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t(label)}</span>
                  </div>
                  <input
                    type="checkbox"
                    checked={config[key as keyof IndicatorConfig] as boolean || false}
                    onChange={(e) => !disabled && onChange({ ...config, [key]: e.target.checked })}
                    disabled={disabled}
                    className="w-4 h-4 rounded accent-yellow-500"
                  />
                </div>
                <p className="text-[10px] mb-1.5" style={{ color: '#5E6673' }}>{t(desc)}</p>
                {/* 放量参数：前 N 根 K 线 + 成交量参考基准（小时） */}
                {key === 'enable_vol_mult' && config.enable_vol_mult && (
                  <div className="space-y-1.5 mt-1">
                    <div className="flex items-center gap-1">
                      <span className="text-[10px]" style={{ color: '#848E9C' }}>
                        {language === 'zh' ? 'N 条均值' : 'Bars'}
                      </span>
                      <input
                        type="number"
                        min={1}
                        max={100}
                        value={String(config.vol_mult_bars ?? 5)}
                        onChange={(e) => {
                          if (disabled) return
                          const n = parseInt(e.target.value.trim(), 10)
                          if (!isNaN(n) && n >= 1 && n <= 100) {
                            onChange({ ...config, vol_mult_bars: n })
                          }
                        }}
                        disabled={disabled}
                        className="flex-1 px-2 py-1 rounded text-[10px] text-center"
                        style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                      />
                    </div>
                    <div className="flex items-center gap-1">
                      <span className="text-[10px]" style={{ color: '#848E9C' }}>
                        {t('volumeBaselineHours')}
                      </span>
                      <input
                        type="number"
                        min={1}
                        max={72}
                        value={String(config.volume_baseline_hours ?? 4)}
                        onChange={(e) => {
                          if (disabled) return
                          const n = parseInt(e.target.value.trim(), 10)
                          if (!isNaN(n) && n >= 1 && n <= 72) {
                            onChange({ ...config, volume_baseline_hours: n })
                          }
                        }}
                        disabled={disabled}
                        className="flex-1 px-2 py-1 rounded text-[10px] text-center"
                        style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                      />
                    </div>
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* ============================================ */}
      {/* Section: 动态追踪止盈/止损 (硬风控)           */}
      {/* ============================================ */}
      <div className="rounded-lg overflow-hidden" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
        <div className="px-3 py-2 flex items-center gap-2" style={{ background: '#1E2329', borderBottom: '1px solid #2B3139' }}>
          <AlertCircle className="w-4 h-4" style={{ color: '#f59e0b' }} />
          <span className="text-sm font-medium" style={{ color: '#EAECEF' }}>{t('trailingPanel')}</span>
          <span className="text-xs" style={{ color: '#848E9C' }}>- {t('trailingPanelDesc')}</span>
        </div>
        <div className="p-3 space-y-3">
          <div className="flex items-center justify-between p-2.5 rounded-lg" style={{ background: config.enable_fractal_defense ? 'rgba(239, 68, 68, 0.08)' : 'transparent', border: config.enable_fractal_defense ? '1px solid rgba(239, 68, 68, 0.3)' : '1px solid #2B3139' }}>
            <div className="flex items-center gap-2">
              <div className="w-2 h-2 rounded-full" style={{ background: '#ef4444' }} />
              <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('enableFractalDefense')}</span>
            </div>
            <input
              type="checkbox"
              checked={config.enable_fractal_defense || false}
              onChange={(e) => !disabled && onChange({ ...config, enable_fractal_defense: e.target.checked })}
              disabled={disabled}
              className="w-4 h-4 rounded accent-red-500"
            />
          </div>
          <p className="text-[10px] mb-1.5" style={{ color: '#5E6673' }}>{t('enableFractalDefenseDesc')}</p>
          <div className="flex items-center justify-between p-2.5 rounded-lg" style={{ background: config.enable_ema20_gap_defense ? 'rgba(245, 158, 11, 0.08)' : 'transparent', border: config.enable_ema20_gap_defense ? '1px solid rgba(245, 158, 11, 0.3)' : '1px solid #2B3139' }}>
            <div className="flex items-center gap-2">
              <div className="w-2 h-2 rounded-full" style={{ background: '#f59e0b' }} />
              <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('enableEMA20GapDefense')}</span>
            </div>
            <input
              type="checkbox"
              checked={config.enable_ema20_gap_defense || false}
              onChange={(e) => !disabled && onChange({ ...config, enable_ema20_gap_defense: e.target.checked })}
              disabled={disabled}
              className="w-4 h-4 rounded accent-amber-500"
            />
          </div>
          <p className="text-[10px] mb-1.5" style={{ color: '#5E6673' }}>{t('enableEMA20GapDefenseDesc')}</p>
          <div className="flex items-center justify-between p-2.5 rounded-lg" style={{ background: config.enable_3bar_trailing ? 'rgba(34, 197, 94, 0.08)' : 'transparent', border: config.enable_3bar_trailing ? '1px solid rgba(34, 197, 94, 0.3)' : '1px solid #2B3139' }}>
            <div className="flex items-center gap-2">
              <div className="w-2 h-2 rounded-full" style={{ background: '#22c55e' }} />
              <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('enable3BarTrailing')}</span>
            </div>
            <input
              type="checkbox"
              checked={config.enable_3bar_trailing || false}
              onChange={(e) => !disabled && onChange({ ...config, enable_3bar_trailing: e.target.checked })}
              disabled={disabled}
              className="w-4 h-4 rounded accent-green-500"
            />
          </div>
          <p className="text-[10px] mb-1.5" style={{ color: '#5E6673' }}>{t('enable3BarTrailingDesc')}</p>
          <div className="flex items-center justify-between p-2.5 rounded-lg" style={{ background: config.enable_staged_take_profit !== false ? 'rgba(34, 197, 94, 0.08)' : 'transparent', border: config.enable_staged_take_profit !== false ? '1px solid rgba(34, 197, 94, 0.3)' : '1px solid #2B3139' }}>
            <div className="flex items-center gap-2">
              <div className="w-2 h-2 rounded-full" style={{ background: '#22c55e' }} />
              <span className="text-xs font-medium" style={{ color: '#EAECEF' }}>{t('enableStagedTakeProfit')}</span>
            </div>
            <input
              type="checkbox"
              checked={config.enable_staged_take_profit !== false}
              onChange={(e) => !disabled && onChange({ ...config, enable_staged_take_profit: e.target.checked })}
              disabled={disabled}
              className="w-4 h-4 rounded accent-green-500"
            />
          </div>
          <p className="text-[10px] mb-1.5" style={{ color: '#5E6673' }}>{t('enableStagedTakeProfitDesc')}</p>
        </div>
      </div>
    </div>
  )
}
