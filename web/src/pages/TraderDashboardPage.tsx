import { useEffect, useState, useRef } from 'react'
import { mutate } from 'swr'
import { api } from '../lib/api'
import { ChartTabs } from '../components/ChartTabs'
import { DecisionCard } from '../components/DecisionCard'
import { PositionHistory } from '../components/PositionHistory'
import { PunkAvatar, getTraderAvatar } from '../components/PunkAvatar'
import { confirmToast, notify } from '../lib/notify'
import { formatPrice, formatQuantity } from '../utils/format'
import { t, type Language } from '../i18n/translations'
import { LogOut, Loader2, Eye, EyeOff, Copy, Check } from 'lucide-react'
import { DeepVoidBackground } from '../components/DeepVoidBackground'
import { GridRiskPanel } from '../components/strategy/GridRiskPanel'
import type {
    SystemStatus,
    AccountInfo,
    Position,
    DecisionRecord,
    Statistics,
    TraderInfo,
    Exchange,
    IndicatorAnalysisResult,
    IndicatorDimensionAverages,
} from '../types'

// --- Helper Functions ---

// 获取友好的AI模型名称
function getModelDisplayName(modelId: string): string {
    switch (modelId.toLowerCase()) {
        case 'deepseek':
            return 'DeepSeek'
        case 'qwen':
            return 'Qwen'
        case 'claude':
            return 'Claude'
        default:
            return modelId.toUpperCase()
    }
}

// Helper function to get exchange display name from exchange ID (UUID)
function getExchangeDisplayNameFromList(
    exchangeId: string | undefined,
    exchanges: Exchange[] | undefined
): string {
    if (!exchangeId) return 'Unknown'
    const exchange = exchanges?.find((e) => e.id === exchangeId)
    if (!exchange) return exchangeId.substring(0, 8).toUpperCase() + '...'
    const typeName = exchange.exchange_type?.toUpperCase() || exchange.name
    return exchange.account_name
        ? `${typeName} - ${exchange.account_name}`
        : typeName
}

// Helper function to get exchange type from exchange ID (UUID) - for kline charts
function getExchangeTypeFromList(
    exchangeId: string | undefined,
    exchanges: Exchange[] | undefined
): string {
    if (!exchangeId) return 'binance'
    const exchange = exchanges?.find((e) => e.id === exchangeId)
    if (!exchange) return 'binance' // Default to binance for charts
    return exchange.exchange_type?.toLowerCase() || 'binance'
}

// Helper function to check if exchange is a perp-dex type (wallet-based)
function isPerpDexExchange(exchangeType: string | undefined): boolean {
    if (!exchangeType) return false
    const perpDexTypes = ['hyperliquid', 'lighter', 'aster']
    return perpDexTypes.includes(exchangeType.toLowerCase())
}

// Helper function to get wallet address for perp-dex exchanges
function getWalletAddress(exchange: Exchange | undefined): string | undefined {
    if (!exchange) return undefined
    const type = exchange.exchange_type?.toLowerCase()
    switch (type) {
        case 'hyperliquid':
            return exchange.hyperliquidWalletAddr
        case 'lighter':
            return exchange.lighterWalletAddr
        case 'aster':
            return exchange.asterSigner
        default:
            return undefined
    }
}

// Helper function to truncate wallet address for display
function truncateAddress(address: string, startLen = 6, endLen = 4): string {
    if (address.length <= startLen + endLen + 3) return address
    return `${address.slice(0, startLen)}...${address.slice(-endLen)}`
}

// --- Components ---

interface TraderDashboardPageProps {
    selectedTrader?: TraderInfo
    traders?: TraderInfo[]
    tradersError?: Error
    selectedTraderId?: string
    onTraderSelect: (traderId: string) => void
    onNavigateToTraders: () => void
    status?: SystemStatus
    account?: AccountInfo
    positions?: Position[]
    decisions?: DecisionRecord[]
    decisionsLimit: number
    onDecisionsLimitChange: (limit: number) => void
    stats?: Statistics
    lastUpdate: string
    language: Language
    exchanges?: Exchange[]
}

export function TraderDashboardPage({
    selectedTrader,
    status,
    account,
    positions,
    decisions,
    decisionsLimit,
    onDecisionsLimitChange,
    lastUpdate,
    language,
    traders,
    tradersError,
    selectedTraderId,
    onTraderSelect,
    onNavigateToTraders,
    exchanges,
}: TraderDashboardPageProps) {
    const [closingPosition, setClosingPosition] = useState<string | null>(null)
    const [selectedChartSymbol, setSelectedChartSymbol] = useState<string | undefined>(undefined)
    const [chartUpdateKey, setChartUpdateKey] = useState<number>(0)
    const [exportPeriod, setExportPeriod] = useState<'last_24h' | 'last_7d' | 'last_30d'>('last_7d')
    const [exporting, setExporting] = useState(false)
    const chartSectionRef = useRef<HTMLDivElement>(null)
    const [showWalletAddress, setShowWalletAddress] = useState<boolean>(false)
    const [copiedAddress, setCopiedAddress] = useState<boolean>(false)

    // Current positions pagination
    const [positionsPageSize, setPositionsPageSize] = useState<number>(20)
    const [positionsCurrentPage, setPositionsCurrentPage] = useState<number>(1)

    // Indicator analysis controls
    const [indicatorTimeframe, setIndicatorTimeframe] = useState<string>('5m')
    const [indicatorRSIPeriod, setIndicatorRSIPeriod] = useState<number>(14)
    const [indicatorEMAPeriod, setIndicatorEMAPeriod] = useState<number>(20)
    const [indicatorVolMultBars, setIndicatorVolMultBars] = useState<number>(5)
    const [indicatorAnalysis, setIndicatorAnalysis] = useState<IndicatorAnalysisResult | null>(null)
    const [indicatorLoading, setIndicatorLoading] = useState<boolean>(false)
    const [indicatorError, setIndicatorError] = useState<string | null>(null)
    type IndicatorSideFilter = 'all' | 'long' | 'short'
    const [indicatorSideFilter, setIndicatorSideFilter] = useState<IndicatorSideFilter>('all')

    // Calculate paginated positions
    const totalPositions = positions?.length || 0
    const totalPositionPages = Math.ceil(totalPositions / positionsPageSize)
    const paginatedPositions = positions?.slice(
        (positionsCurrentPage - 1) * positionsPageSize,
        positionsCurrentPage * positionsPageSize
    ) || []

    // Fetch indicator analysis when trader / params change
    useEffect(() => {
        if (!selectedTraderId) {
            setIndicatorAnalysis(null)
            return
        }
        let aborted = false
        const fetchAnalysis = async () => {
            setIndicatorLoading(true)
            setIndicatorError(null)
            try {
                const params = new URLSearchParams({
                    trader_id: selectedTraderId,
                    timeframe: indicatorTimeframe,
                    rsi_period: String(indicatorRSIPeriod),
                    ema_period: String(indicatorEMAPeriod),
                    vol_mult_bars: String(indicatorVolMultBars),
                })
                const token = typeof localStorage !== 'undefined' ? localStorage.getItem('auth_token') : null
                const resp = await fetch(`/api/statistics/indicator-analysis?${params.toString()}`, {
                    headers: token ? { Authorization: `Bearer ${token}` } : {},
                })
                const data = (await resp.json()) as IndicatorAnalysisResult & { error?: string; code?: string; details?: string }
                if (!resp.ok) {
                    const msg = data?.error && data?.details ? `${data.error}: ${data.details}` : data?.error || `HTTP ${resp.status}`
                    throw new Error(msg)
                }
                if (!aborted) {
                    setIndicatorAnalysis(data)
                }
            } catch (err: any) {
                if (!aborted) {
                    setIndicatorError(err?.message || 'Failed to load indicator analysis')
                    setIndicatorAnalysis(null)
                }
            } finally {
                if (!aborted) {
                    setIndicatorLoading(false)
                }
            }
        }
        fetchAnalysis()
        return () => {
            aborted = true
        }
    }, [selectedTraderId, indicatorTimeframe, indicatorRSIPeriod, indicatorEMAPeriod, indicatorVolMultBars])

    // Reset page when positions change
    useEffect(() => {
        setPositionsCurrentPage(1)
    }, [selectedTraderId, positionsPageSize])

    // Auto-set chart symbol for grid trading
    useEffect(() => {
        if (status?.strategy_type === 'grid_trading' && status?.grid_symbol) {
            setSelectedChartSymbol(status.grid_symbol)
        }
    }, [status?.strategy_type, status?.grid_symbol])

    // Get current exchange info for perp-dex wallet display
    const currentExchange = exchanges?.find(
        (e) => e.id === selectedTrader?.exchange_id
    )
    const walletAddress = getWalletAddress(currentExchange)
    const isPerpDex = isPerpDexExchange(currentExchange?.exchange_type)

    // Copy wallet address to clipboard
    const handleCopyAddress = async () => {
        if (!walletAddress) return
        try {
            await navigator.clipboard.writeText(walletAddress)
            setCopiedAddress(true)
            setTimeout(() => setCopiedAddress(false), 2000)
        } catch (err) {
            console.error('Failed to copy address:', err)
        }
    }

    // Export AI decisions (machine-readable JSON)
    const handleExportDecisions = async () => {
        if (!selectedTraderId) return
        setExporting(true)
        try {
            const data = await api.getDecisionsExport(selectedTraderId, {
                period: exportPeriod,
                includePrompts: false,
            })
            const blob = new Blob([JSON.stringify(data, null, 2)], {
                type: 'application/json',
            })
            const url = URL.createObjectURL(blob)
            const a = document.createElement('a')
            a.href = url
            a.download = `decisions-${selectedTraderId}-${data.from.slice(0, 10)}.json`
            a.click()
            URL.revokeObjectURL(url)
            notify.success(
                language === 'zh'
                    ? `已导出 ${data.count} 条决策`
                    : `Exported ${data.count} decisions`
            )
        } catch (e) {
            const msg = (e as Error)?.message
            notify.error(language === 'zh' ? '导出失败' : 'Export failed', msg ? { description: msg } : undefined)
        } finally {
            setExporting(false)
        }
    }

    // Handle symbol click from Decision Card
    const handleSymbolClick = (symbol: string) => {
        // Set the selected symbol
        setSelectedChartSymbol(symbol)
        // Scroll to chart section
        setTimeout(() => {
            chartSectionRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
        }, 100)
    }

    // 平仓操作
    const handleClosePosition = async (symbol: string, side: string) => {
        if (!selectedTraderId) return

        const confirmMsg =
            language === 'zh'
                ? `确定要平仓 ${symbol} ${side === 'LONG' ? '多仓' : '空仓'} 吗？`
                : `Are you sure you want to close ${symbol} ${side === 'LONG' ? 'LONG' : 'SHORT'} position?`

        const confirmed = await confirmToast(confirmMsg, {
            title: language === 'zh' ? '确认平仓' : 'Confirm Close',
            okText: language === 'zh' ? '确认' : 'Confirm',
            cancelText: language === 'zh' ? '取消' : 'Cancel',
        })

        if (!confirmed) return

        setClosingPosition(symbol)
        try {
            await api.closePosition(selectedTraderId, symbol, side)
            notify.success(
                language === 'zh' ? '平仓成功' : 'Position closed successfully'
            )
            // 使用 SWR mutate 刷新数据而非重新加载页面
            await Promise.all([
                mutate(`positions-${selectedTraderId}`),
                mutate(`account-${selectedTraderId}`),
            ])
        } catch (err: unknown) {
            const errorMsg =
                err instanceof Error
                    ? err.message
                    : language === 'zh'
                        ? '平仓失败'
                        : 'Failed to close position'
            notify.error(errorMsg)
        } finally {
            setClosingPosition(null)
        }
    }

    // If API failed with error, show empty state (likely backend not running)
    if (tradersError) {
        return (
            <div className="flex items-center justify-center min-h-[60vh] relative z-10">
                <div className="text-center max-w-md mx-auto px-6">
                    <div
                        className="w-24 h-24 mx-auto mb-6 rounded-full flex items-center justify-center nofx-glass"
                        style={{
                            background: 'rgba(240, 185, 11, 0.1)',
                            borderColor: 'rgba(240, 185, 11, 0.3)',
                        }}
                    >
                        <svg
                            className="w-12 h-12 text-nofx-gold"
                            fill="none"
                            viewBox="0 0 24 24"
                            stroke="currentColor"
                        >
                            <path
                                strokeLinecap="round"
                                strokeLinejoin="round"
                                strokeWidth={2}
                                d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
                            />
                        </svg>
                    </div>
                    <h2 className="text-2xl font-bold mb-3 text-nofx-text-main">
                        {language === 'zh' ? '无法连接到服务器' : 'Connection Failed'}
                    </h2>
                    <p className="text-base mb-6 text-nofx-text-muted">
                        {language === 'zh'
                            ? '请确认后端服务已启动。'
                            : 'Please check if the backend service is running.'}
                    </p>
                    <button
                        onClick={() => window.location.reload()}
                        className="px-6 py-3 rounded-lg font-semibold transition-all hover:scale-105 active:scale-95 nofx-glass border border-nofx-gold/30 text-nofx-gold hover:bg-nofx-gold/10"
                    >
                        {language === 'zh' ? '重试' : 'Retry'}
                    </button>
                </div>
            </div>
        )
    }

    // If traders is loaded and empty, show empty state
    if (traders && traders.length === 0) {
        return (
            <div className="flex items-center justify-center min-h-[60vh] relative z-10">
                <div className="text-center max-w-md mx-auto px-6">
                    <div
                        className="w-24 h-24 mx-auto mb-6 rounded-full flex items-center justify-center nofx-glass"
                        style={{
                            background: 'rgba(240, 185, 11, 0.1)',
                            borderColor: 'rgba(240, 185, 11, 0.3)',
                        }}
                    >
                        <svg
                            className="w-12 h-12 text-nofx-gold"
                            fill="none"
                            viewBox="0 0 24 24"
                            stroke="currentColor"
                        >
                            <path
                                strokeLinecap="round"
                                strokeLinejoin="round"
                                strokeWidth={2}
                                d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"
                            />
                        </svg>
                    </div>
                    <h2 className="text-2xl font-bold mb-3 text-nofx-text-main">
                        {t('dashboardEmptyTitle', language)}
                    </h2>
                    <p className="text-base mb-6 text-nofx-text-muted">
                        {t('dashboardEmptyDescription', language)}
                    </p>
                    <button
                        onClick={onNavigateToTraders}
                        className="px-6 py-3 rounded-lg font-semibold transition-all hover:scale-105 active:scale-95 nofx-glass border border-nofx-gold/30 text-nofx-gold hover:bg-nofx-gold/10"
                    >
                        {t('goToTradersPage', language)}
                    </button>
                </div>
            </div>
        )
    }

    // If traders is still loading or selectedTrader is not ready, show skeleton
    if (!selectedTrader) {
        return (
            <div className="space-y-6 relative z-10">
                <div className="nofx-glass p-6 animate-pulse">
                    <div className="h-8 w-48 mb-3 bg-nofx-bg/50 rounded"></div>
                    <div className="flex gap-4">
                        <div className="h-4 w-32 bg-nofx-bg/50 rounded"></div>
                        <div className="h-4 w-24 bg-nofx-bg/50 rounded"></div>
                        <div className="h-4 w-28 bg-nofx-bg/50 rounded"></div>
                    </div>
                </div>
                <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
                    {[1, 2, 3, 4].map((i) => (
                        <div key={i} className="nofx-glass p-5 animate-pulse">
                            <div className="h-4 w-24 mb-3 bg-nofx-bg/50 rounded"></div>
                            <div className="h-8 w-32 bg-nofx-bg/50 rounded"></div>
                        </div>
                    ))}
                </div>
                <div className="nofx-glass p-6 animate-pulse">
                    <div className="h-6 w-40 mb-4 bg-nofx-bg/50 rounded"></div>
                    <div className="h-64 w-full bg-nofx-bg/50 rounded"></div>
                </div>
            </div>
        )
    }

    return (
        <DeepVoidBackground className="min-h-screen pb-12" disableAnimation>
            <div className="w-full px-4 md:px-8 relative z-10 pt-6">
                {/* Trader Header */}
                <div
                    className="mb-6 rounded-lg p-6 animate-scale-in nofx-glass group"
                    style={{
                        background: 'linear-gradient(135deg, rgba(15, 23, 42, 0.6) 0%, rgba(15, 23, 42, 0.4) 100%)',
                    }}
                >
                    <div className="flex items-start justify-between mb-4">
                        <h2 className="text-2xl font-bold flex items-center gap-4 text-nofx-text-main">
                            <div className="relative">
                                <PunkAvatar
                                    seed={getTraderAvatar(
                                        selectedTrader.trader_id,
                                        selectedTrader.trader_name
                                    )}
                                    size={56}
                                    className="rounded-xl border-2 border-nofx-gold/30 shadow-[0_0_15px_rgba(240,185,11,0.2)]"
                                />
                                <div className="absolute -bottom-1 -right-1 w-4 h-4 bg-nofx-green rounded-full border-2 border-[#0B0E11] shadow-[0_0_8px_rgba(14,203,129,0.8)] animate-pulse" />
                            </div>
                            <div className="flex flex-col">
                                <span className="text-3xl tracking-tight text-nofx-text font-semibold">
                                    {selectedTrader.trader_name}
                                </span>
                                <span className="text-xs font-mono text-nofx-text-muted opacity-60 flex items-center gap-2">
                                    <div className="w-1.5 h-1.5 bg-nofx-gold rounded-full" />
                                    ID: {selectedTrader.trader_id.slice(0, 8)}...
                                </span>
                            </div>
                        </h2>

                        <div className="flex items-center gap-4">
                            {/* Trader Selector */}
                            {traders && traders.length > 0 && (
                                <div className="flex items-center gap-2 nofx-glass px-1 py-1 rounded-lg border border-white/5">
                                    <select
                                        value={selectedTraderId}
                                        onChange={(e) => onTraderSelect(e.target.value)}
                                        className="bg-transparent text-sm font-medium cursor-pointer transition-colors text-nofx-text-main focus:outline-none px-2 py-1"
                                    >
                                        {traders.map((trader) => (
                                            <option key={trader.trader_id} value={trader.trader_id} className="bg-[#0B0E11]">
                                                {trader.trader_name}
                                            </option>
                                        ))}
                                    </select>
                                </div>
                            )}

                            {/* Wallet Address Display for Perp-DEX */}
                            {exchanges && isPerpDex && (
                                <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg nofx-glass border border-nofx-gold/20">
                                    {walletAddress ? (
                                        <>
                                            <span className="text-xs font-mono text-nofx-gold">
                                                {showWalletAddress
                                                    ? walletAddress
                                                    : truncateAddress(walletAddress)}
                                            </span>
                                            <button
                                                type="button"
                                                onClick={() => setShowWalletAddress(!showWalletAddress)}
                                                className="p-1 rounded hover:bg-white/10 transition-colors"
                                                title={
                                                    showWalletAddress
                                                        ? language === 'zh'
                                                            ? '隐藏地址'
                                                            : 'Hide address'
                                                        : language === 'zh'
                                                            ? '显示完整地址'
                                                            : 'Show full address'
                                                }
                                            >
                                                {showWalletAddress ? (
                                                    <EyeOff className="w-3.5 h-3.5 text-nofx-text-muted" />
                                                ) : (
                                                    <Eye className="w-3.5 h-3.5 text-nofx-text-muted" />
                                                )}
                                            </button>
                                            <button
                                                type="button"
                                                onClick={handleCopyAddress}
                                                className="p-1 rounded hover:bg-white/10 transition-colors"
                                                title={language === 'zh' ? '复制地址' : 'Copy address'}
                                            >
                                                {copiedAddress ? (
                                                    <Check className="w-3.5 h-3.5 text-nofx-green" />
                                                ) : (
                                                    <Copy className="w-3.5 h-3.5 text-nofx-text-muted" />
                                                )}
                                            </button>
                                        </>
                                    ) : (
                                        <span className="text-xs text-nofx-text-muted">
                                            {language === 'zh' ? '未配置地址' : 'No address configured'}
                                        </span>
                                    )}
                                </div>
                            )}
                        </div>
                    </div>
                    <div className="flex items-center gap-6 text-sm flex-wrap text-nofx-text-muted font-mono pl-2">
                        <span className="flex items-center gap-2">
                            <span className="opacity-60">AI Model:</span>
                            <span
                                className="font-bold px-2 py-0.5 rounded text-xs tracking-wide"
                                style={{
                                    background: selectedTrader.ai_model.includes('qwen') ? 'rgba(192, 132, 252, 0.15)' : 'rgba(96, 165, 250, 0.15)',
                                    color: selectedTrader.ai_model.includes('qwen') ? '#c084fc' : '#60a5fa',
                                    border: `1px solid ${selectedTrader.ai_model.includes('qwen') ? '#c084fc' : '#60a5fa'}40`
                                }}
                            >
                                {getModelDisplayName(
                                    selectedTrader.ai_model.split('_').pop() ||
                                    selectedTrader.ai_model
                                )}
                            </span>
                        </span>
                        <span className="w-px h-3 bg-white/10 hidden md:block" />
                        <span className="flex items-center gap-2">
                            <span className="opacity-60">Exchange:</span>
                            <span className="text-nofx-text-main font-semibold">
                                {getExchangeDisplayNameFromList(
                                    selectedTrader.exchange_id,
                                    exchanges
                                )}
                            </span>
                        </span>
                        <span className="w-px h-3 bg-white/10 hidden md:block" />
                        <span className="flex items-center gap-2">
                            <span className="opacity-60">Strategy:</span>
                            <span className="text-nofx-gold font-semibold tracking-wide">
                                {selectedTrader.strategy_name || 'No Strategy'}
                            </span>
                        </span>
                        {status && (
                            <div className="hidden md:contents">
                                <span className="w-px h-3 bg-white/10" />
                                <span>Cycles: <span className="text-nofx-text-main">{status.call_count}</span></span>
                                <span className="w-px h-3 bg-white/10" />
                                <span>Runtime: <span className="text-nofx-text-main">{status.runtime_minutes} min</span></span>
                            </div>
                        )}
                    </div>
                </div>

                {/* Debug Info */}
                {account && (
                    <div className="mb-4 px-3 py-1.5 rounded bg-black/40 border border-white/5 text-[10px] font-mono text-nofx-text-muted flex justify-between items-center opacity-60 hover:opacity-100 transition-opacity">
                        <span>SYSTEM_STATUS::ONLINE</span>
                        <div className="flex gap-4">
                            <span>LAST_UPDATE::{lastUpdate}</span>
                            <span>EQ::{account?.total_equity?.toFixed(2)}</span>
                            <span>PNL::{account?.total_pnl?.toFixed(2)}</span>
                        </div>
                    </div>
                )}

                {/* Account Overview */}
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
                    <StatCard
                        title={t('totalEquity', language)}
                        value={`${account?.total_equity?.toFixed(2) || '0.00'}`}
                        unit="USDT"
                        change={account?.total_pnl_pct || 0}
                        positive={(account?.total_pnl ?? 0) > 0}
                        icon="💰"
                    />
                    <StatCard
                        title={t('availableBalance', language)}
                        value={`${account?.available_balance?.toFixed(2) || '0.00'}`}
                        unit="USDT"
                        subtitle={`${account?.available_balance && account?.total_equity ? ((account.available_balance / account.total_equity) * 100).toFixed(1) : '0.0'}% ${t('free', language)}`}
                        icon="💳"
                    />
                    <StatCard
                        title={t('totalPnL', language)}
                        value={`${account?.total_pnl !== undefined && account.total_pnl >= 0 ? '+' : ''}${account?.total_pnl?.toFixed(2) || '0.00'}`}
                        unit="USDT"
                        change={account?.total_pnl_pct || 0}
                        positive={(account?.total_pnl ?? 0) >= 0}
                        icon="📈"
                    />
                    <StatCard
                        title={t('positions', language)}
                        value={`${account?.position_count || 0}`}
                        unit="ACTIVE"
                        subtitle={`${t('margin', language)}: ${account?.margin_used_pct?.toFixed(1) || '0.0'}%`}
                        icon="📊"
                    />
                </div>

                {/* Grid Risk Panel - Only show for grid trading strategy */}
                {status?.strategy_type === 'grid_trading' && selectedTraderId && (
                    <div className="mb-8 animate-slide-in" style={{ animationDelay: '0.05s' }}>
                        <GridRiskPanel
                            traderId={selectedTraderId}
                            language={language}
                            refreshInterval={5000}
                        />
                    </div>
                )}

                {/* Main Content Area */}
                <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
                    {/* Left Column: Charts + Positions */}
                    <div className="space-y-6">
                        {/* Chart Tabs (Equity / K-line) */}
                        <div
                            ref={chartSectionRef}
                            className="chart-container animate-slide-in scroll-mt-32 backdrop-blur-sm"
                            style={{ animationDelay: '0.1s' }}
                        >
                            <ChartTabs
                                traderId={selectedTrader.trader_id}
                                selectedSymbol={selectedChartSymbol}
                                updateKey={chartUpdateKey}
                                exchangeId={getExchangeTypeFromList(
                                    selectedTrader.exchange_id,
                                    exchanges
                                )}
                            />
                        </div>

                        {/* Current Positions */}
                        <div
                            className="nofx-glass p-6 animate-slide-in relative overflow-hidden group"
                            style={{ animationDelay: '0.15s' }}
                        >
                            <div className="absolute top-0 right-0 p-3 opacity-10 group-hover:opacity-20 transition-opacity">
                                <div className="w-24 h-24 rounded-full bg-blue-500 blur-3xl" />
                            </div>
                            <div className="flex items-center justify-between mb-5 relative z-10">
                                <h2 className="text-lg font-bold flex items-center gap-2 text-nofx-text-main uppercase tracking-wide">
                                    <span className="text-blue-500">◈</span> {t('currentPositions', language)}
                                </h2>
                                {positions && positions.length > 0 && (
                                    <div className="text-xs px-2 py-1 rounded bg-nofx-gold/10 text-nofx-gold border border-nofx-gold/20 font-mono shadow-[0_0_10px_rgba(240,185,11,0.1)]">
                                        {positions.length} {t('active', language)}
                                    </div>
                                )}
                            </div>
                            {positions && positions.length > 0 ? (
                                <div>
                                    <div className="overflow-x-auto">
                                        <table className="w-full text-xs">
                                            <thead className="text-left border-b border-white/5">
                                                <tr>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-left">{t('symbol', language)}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-center">{t('side', language)}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-center">{language === 'zh' ? '操作' : 'Action'}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-right hidden md:table-cell" title={t('entryPrice', language)}>{language === 'zh' ? '入场价' : 'Entry'}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-right hidden md:table-cell" title={t('markPrice', language)}>{language === 'zh' ? '标记价' : 'Mark'}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-right" title={t('quantity', language)}>{language === 'zh' ? '数量' : 'Qty'}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-right hidden md:table-cell" title={t('positionValue', language)}>{language === 'zh' ? '价值' : 'Value'}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-center hidden md:table-cell" title={t('leverage', language)}>{language === 'zh' ? '杠杆' : 'Lev.'}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-right" title={t('unrealizedPnL', language)}>{language === 'zh' ? '未实现盈亏' : 'uPnL'}</th>
                                                    <th className="px-1 pb-3 font-semibold text-nofx-text-muted whitespace-nowrap text-right hidden md:table-cell" title={t('liqPrice', language)}>{language === 'zh' ? '强平价' : 'Liq.'}</th>
                                                </tr>
                                            </thead>
                                            <tbody>
                                                {paginatedPositions.map((pos, i) => {
                                                    const entryPrice = Number(pos.entry_price) || 0
                                                    const markPrice = Number(pos.mark_price) || 0
                                                    const quantity = Number(pos.quantity) || 0
                                                    const unrealizedPnl = Number(pos.unrealized_pnl) || 0
                                                    const leverage = Number(pos.leverage) || 0
                                                    const liquidationPrice = Number(pos.liquidation_price) || 0
                                                    const side = (pos.side ?? 'long').toString().toLowerCase()
                                                    const symbol = (pos.symbol ?? '').toString()
                                                    return (
                                                    <tr
                                                        key={`${symbol}-${side}-${i}`}
                                                        className="border-b border-white/5 last:border-0 transition-all hover:bg-white/5 cursor-pointer group/row"
                                                        onClick={() => {
                                                            setSelectedChartSymbol(symbol)
                                                            setChartUpdateKey(Date.now())
                                                            if (chartSectionRef.current) {
                                                                chartSectionRef.current.scrollIntoView({
                                                                    behavior: 'smooth',
                                                                    block: 'start',
                                                                })
                                                            }
                                                        }}
                                                    >
                                                        <td className="px-1 py-3 font-mono font-semibold whitespace-nowrap text-left text-nofx-text-main group-hover/row:text-white transition-colors">
                                                            <span className="inline-flex items-center gap-1.5">
                                                                {symbol}
                                                                {pos.source === 'dry_run' && (
                                                                    <span
                                                                        className="px-1.5 py-0.5 rounded text-[10px] font-bold uppercase tracking-wider"
                                                                        style={{ background: 'rgba(245, 158, 11, 0.2)', color: '#f59e0b' }}
                                                                        title={language === 'zh' ? '模拟盘持仓' : 'Paper trading'}
                                                                    >
                                                                        {language === 'zh' ? '模拟盘' : 'Paper'}
                                                                    </span>
                                                                )}
                                                            </span>
                                                        </td>
                                                        <td className="px-1 py-3 whitespace-nowrap text-center">
                                                            <span
                                                                className={`px-1.5 py-0.5 rounded text-[10px] font-bold uppercase tracking-wider ${side === 'long' ? 'bg-nofx-green/10 text-nofx-green shadow-[0_0_8px_rgba(14,203,129,0.2)]' : 'bg-nofx-red/10 text-nofx-red shadow-[0_0_8px_rgba(246,70,93,0.2)]'}`}
                                                            >
                                                                {t(side === 'long' ? 'long' : 'short', language)}
                                                            </span>
                                                        </td>
                                                        <td className="px-1 py-3 whitespace-nowrap text-center">
                                                            <button
                                                                type="button"
                                                                onClick={(e) => {
                                                                    e.stopPropagation()
                                                                    handleClosePosition(symbol, side.toUpperCase())
                                                                }}
                                                                disabled={closingPosition === symbol}
                                                                className="inline-flex items-center gap-1 px-2 py-1 rounded text-[10px] font-semibold transition-all hover:scale-105 disabled:opacity-50 disabled:cursor-not-allowed mx-auto bg-nofx-red/10 text-nofx-red border border-nofx-red/30 hover:bg-nofx-red/20"
                                                                title={language === 'zh' ? '平仓' : 'Close Position'}
                                                            >
                                                                {closingPosition === symbol ? (
                                                                    <Loader2 className="w-3 h-3 animate-spin" />
                                                                ) : (
                                                                    <LogOut className="w-3 h-3" />
                                                                )}
                                                                {language === 'zh' ? '平仓' : 'Close'}
                                                            </button>
                                                        </td>
                                                        <td className="px-1 py-3 font-mono whitespace-nowrap text-right text-nofx-text-main hidden md:table-cell">{formatPrice(entryPrice)}</td>
                                                        <td className="px-1 py-3 font-mono whitespace-nowrap text-right text-nofx-text-main hidden md:table-cell">{formatPrice(markPrice)}</td>
                                                        <td className="px-1 py-3 font-mono whitespace-nowrap text-right text-nofx-text-main">{formatQuantity(quantity)}</td>
                                                        <td className="px-1 py-3 font-mono font-bold whitespace-nowrap text-right text-nofx-text-main hidden md:table-cell">{(quantity * markPrice).toFixed(2)}</td>
                                                        <td className="px-1 py-3 font-mono whitespace-nowrap text-center text-nofx-gold hidden md:table-cell">{leverage}x</td>
                                                        <td className="px-1 py-3 font-mono whitespace-nowrap text-right">
                                                            <span
                                                                className={`font-bold ${unrealizedPnl >= 0 ? 'text-nofx-green shadow-nofx-green' : 'text-nofx-red shadow-nofx-red'}`}
                                                                style={{ textShadow: unrealizedPnl >= 0 ? '0 0 10px rgba(14,203,129,0.3)' : '0 0 10px rgba(246,70,93,0.3)' }}
                                                            >
                                                                {unrealizedPnl >= 0 ? '+' : ''}
                                                                {unrealizedPnl.toFixed(2)}
                                                            </span>
                                                        </td>
                                                        <td className="px-1 py-3 font-mono whitespace-nowrap text-right text-nofx-text-muted hidden md:table-cell">{formatPrice(liquidationPrice)}</td>
                                                    </tr>
                                                    )
                                                })}
                                            </tbody>
                                        </table>
                                    </div>
                                    {/* Pagination footer */}
                                    {totalPositions > 10 && (
                                        <div className="flex flex-wrap items-center justify-between gap-3 pt-4 mt-4 text-xs border-t border-white/5 text-nofx-text-muted">
                                            <span>
                                                {language === 'zh'
                                                    ? `显示 ${paginatedPositions.length} / ${totalPositions} 个持仓`
                                                    : `Showing ${paginatedPositions.length} of ${totalPositions} positions`}
                                            </span>
                                            <div className="flex items-center gap-3">
                                                <div className="flex items-center gap-2">
                                                    <span>{language === 'zh' ? '每页' : 'Per page'}:</span>
                                                    <select
                                                        value={positionsPageSize}
                                                        onChange={(e) => setPositionsPageSize(Number(e.target.value))}
                                                        className="bg-black/40 border border-white/10 rounded px-2 py-1 text-xs text-nofx-text-main focus:outline-none focus:border-nofx-gold/50 transition-colors"
                                                    >
                                                        <option value={20}>20</option>
                                                        <option value={50}>50</option>
                                                        <option value={100}>100</option>
                                                    </select>
                                                </div>
                                                {totalPositionPages > 1 && (
                                                    <div className="flex items-center gap-1">
                                                        {['«', '‹', `${positionsCurrentPage} / ${totalPositionPages}`, '›', '»'].map((label, idx) => {
                                                            const isText = idx === 2;
                                                            const isFirst = idx === 0;
                                                            const isPrev = idx === 1;
                                                            const isNext = idx === 3;
                                                            const isLast = idx === 4;
                                                            if (isText) return <span key={idx} className="px-3 text-nofx-text-main">{label}</span>;

                                                            let onClick = () => { };
                                                            let disabled = false;

                                                            if (isFirst) { onClick = () => setPositionsCurrentPage(1); disabled = positionsCurrentPage === 1; }
                                                            if (isPrev) { onClick = () => setPositionsCurrentPage(p => Math.max(1, p - 1)); disabled = positionsCurrentPage === 1; }
                                                            if (isNext) { onClick = () => setPositionsCurrentPage(p => Math.min(totalPositionPages, p + 1)); disabled = positionsCurrentPage === totalPositionPages; }
                                                            if (isLast) { onClick = () => setPositionsCurrentPage(totalPositionPages); disabled = positionsCurrentPage === totalPositionPages; }

                                                            return (
                                                                <button
                                                                    key={idx}
                                                                    onClick={onClick}
                                                                    disabled={disabled}
                                                                    className={`px-2 py-1 rounded transition-colors ${disabled ? 'opacity-30 cursor-not-allowed' : 'hover:bg-white/10 text-nofx-text-main bg-white/5'}`}
                                                                >
                                                                    {label}
                                                                </button>
                                                            )
                                                        })}
                                                    </div>
                                                )}
                                            </div>
                                        </div>
                                    )}
                                </div>
                            ) : (
                                <div className="text-center py-16 text-nofx-text-muted opacity-60">
                                    <div className="text-6xl mb-4 opacity-50 grayscale">📊</div>
                                    <div className="text-lg font-semibold mb-2">{t('noPositions', language)}</div>
                                    <div className="text-sm">{t('noActivePositions', language)}</div>
                                </div>
                            )}
                        </div>
                    </div>

                    {/* Right Column: Recent Decisions */}
                    <div
                        className="nofx-glass p-6 animate-slide-in h-fit lg:sticky lg:top-24 lg:max-h-[calc(100vh-120px)] flex flex-col"
                        style={{ animationDelay: '0.2s' }}
                    >
                        {/* Header */}
                        <div className="flex items-center gap-3 mb-5 pb-4 border-b border-white/5 shrink-0">
                            <div
                                className="w-10 h-10 rounded-xl flex items-center justify-center text-xl shadow-[0_4px_14px_rgba(99,102,241,0.4)]"
                                style={{
                                    background: 'linear-gradient(135deg, #6366F1 0%, #8B5CF6 100%)',
                                }}
                            >
                                🧠
                            </div>
                            <div className="flex-1">
                                <h2 className="text-xl font-bold text-nofx-text-main">
                                    {t('recentDecisions', language)}
                                </h2>
                                {decisions && decisions.length > 0 && (
                                    <div className="text-xs text-nofx-text-muted">
                                        {t('lastCycles', language, { count: decisions.length })}
                                    </div>
                                )}
                            </div>
                            {/* Limit Selector */}
                            <select
                                value={decisionsLimit}
                                onChange={(e) => onDecisionsLimitChange(Number(e.target.value))}
                                className="px-3 py-1.5 rounded-lg text-sm font-medium cursor-pointer transition-all bg-black/40 text-nofx-text-main border border-white/10 hover:border-nofx-accent focus:outline-none"
                            >
                                <option value={5}>5</option>
                                <option value={10}>10</option>
                                <option value={20}>20</option>
                                <option value={50}>50</option>
                                <option value={100}>100</option>
                            </select>
                            {/* Export period + Export button */}
                            <select
                                value={exportPeriod}
                                onChange={(e) =>
                                    setExportPeriod(
                                        e.target.value as 'last_24h' | 'last_7d' | 'last_30d'
                                    )
                                }
                                className="px-3 py-1.5 rounded-lg text-sm font-medium cursor-pointer transition-all bg-black/40 text-nofx-text-muted border border-white/10 hover:border-white/20 focus:outline-none"
                                title={language === 'zh' ? '导出时间范围' : 'Export time range'}
                            >
                                <option value="last_24h">
                                    {language === 'zh' ? '最近24小时' : 'Last 24h'}
                                </option>
                                <option value="last_7d">
                                    {language === 'zh' ? '最近7天' : 'Last 7 days'}
                                </option>
                                <option value="last_30d">
                                    {language === 'zh' ? '最近30天' : 'Last 30 days'}
                                </option>
                            </select>
                            <button
                                type="button"
                                onClick={handleExportDecisions}
                                disabled={!selectedTraderId || exporting}
                                className="px-3 py-1.5 rounded-lg text-sm font-medium transition-all bg-black/40 text-nofx-accent border border-nofx-accent/50 hover:bg-nofx-accent/10 disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1.5"
                                title={language === 'zh' ? '导出 AI 决策分析（机器可读 JSON）' : 'Export AI decisions (machine-readable JSON)'}
                            >
                                {exporting ? (
                                    <Loader2 className="w-4 h-4 animate-spin" />
                                ) : (
                                    <>
                                        <span className="text-base">📥</span>
                                        {language === 'zh' ? '导出决策' : 'Export'}
                                    </>
                                )}
                            </button>
                        </div>

                        {/* Decisions List - Scrollable */}
                        <div
                            className="space-y-4 overflow-y-auto pr-2 custom-scrollbar"
                            style={{ maxHeight: 'calc(100vh - 280px)' }}
                        >
                            {decisions && decisions.length > 0 ? (
                                decisions.map((decision, i) => (
                                    <DecisionCard key={i} decision={decision} language={language} onSymbolClick={handleSymbolClick} />
                                ))
                            ) : (
                                <div className="py-16 text-center text-nofx-text-muted opacity-60">
                                    <div className="text-6xl mb-4 opacity-30 grayscale">🧠</div>
                                    <div className="text-lg font-semibold mb-2 text-nofx-text-main">
                                        {t('noDecisionsYet', language)}
                                    </div>
                                    <div className="text-sm">
                                        {t('aiDecisionsWillAppear', language)}
                                    </div>
                                </div>
                            )}
                        </div>
                    </div>
                </div>

                {/* Indicator Analysis + Position History Section: single column, indicator on top then history */}
                {selectedTraderId && (
                    <div className="flex flex-col gap-6 animate-slide-in w-full" style={{ animationDelay: '0.25s' }}>
                        {/* Trading Indicator Analysis Panel — full width */}
                        <div className="nofx-glass p-6 w-full">
                            <div className="flex items-center justify-between mb-4">
                                <div className="flex items-center gap-2">
                                    <span className="text-2xl">📊</span>
                                    <div>
                                        <h2 className="text-lg font-bold text-nofx-text-main">
                                            {language === 'zh' ? '交易指标分析' : 'Trading Indicator Analysis'}
                                        </h2>
                                        <p className="text-xs text-nofx-text-muted">
                                            {language === 'zh'
                                                ? '基于历史平仓交易，在开仓/平仓时刻回溯关键指标均值（盈利 vs 亏损）'
                                                : 'Backtest key indicators at entry/exit for winning vs losing trades.'}
                                        </p>
                                    </div>
                                </div>
                                <div className="flex flex-col items-end gap-2">
                                    {/* Timeframe selector */}
                                    <div className="flex items-center gap-2">
                                        <span className="text-xs text-nofx-text-muted">
                                            {language === 'zh' ? '周期' : 'Timeframe'}
                                        </span>
                                        <select
                                            value={indicatorTimeframe}
                                            onChange={(e) => setIndicatorTimeframe(e.target.value)}
                                            className="px-2 py-1 rounded-md text-xs bg-black/40 border border-white/10 text-nofx-text-main focus:outline-none focus:border-nofx-accent/60"
                                        >
                                            {['1m', '5m', '15m', '1h', '1d'].map((tf) => (
                                                <option key={tf} value={tf}>
                                                    {tf}
                                                </option>
                                            ))}
                                        </select>
                                    </div>
                                    {/* Quick parameter buttons */}
                                    <div className="flex flex-wrap gap-2 justify-end">
                                        <button
                                            type="button"
                                            onClick={() => setIndicatorRSIPeriod(7)}
                                            className={`px-2 py-0.5 rounded-full text-[10px] border ${indicatorRSIPeriod === 7 ? 'bg-nofx-accent/20 border-nofx-accent text-nofx-accent' : 'border-white/10 text-nofx-text-muted hover:border-nofx-accent/50'}`}
                                        >
                                            RSI-7
                                        </button>
                                        <button
                                            type="button"
                                            onClick={() => setIndicatorRSIPeriod(14)}
                                            className={`px-2 py-0.5 rounded-full text-[10px] border ${indicatorRSIPeriod === 14 ? 'bg-nofx-accent/20 border-nofx-accent text-nofx-accent' : 'border-white/10 text-nofx-text-muted hover:border-nofx-accent/50'}`}
                                        >
                                            RSI-14
                                        </button>
                                        <button
                                            type="button"
                                            onClick={() => setIndicatorEMAPeriod(20)}
                                            className={`px-2 py-0.5 rounded-full text-[10px] border ${indicatorEMAPeriod === 20 ? 'bg-nofx-gold/20 border-nofx-gold text-nofx-gold' : 'border-white/10 text-nofx-text-muted hover:border-nofx-gold/50'}`}
                                        >
                                            EMA-20
                                        </button>
                                        <button
                                            type="button"
                                            onClick={() => setIndicatorEMAPeriod(50)}
                                            className={`px-2 py-0.5 rounded-full text-[10px] border ${indicatorEMAPeriod === 50 ? 'bg-nofx-gold/20 border-nofx-gold text-nofx-gold' : 'border-white/10 text-nofx-text-muted hover:border-nofx-gold/50'}`}
                                        >
                                            EMA-50
                                        </button>
                                        {/* 放量：前 N 根 K 线 */}
                                        <button
                                            type="button"
                                            onClick={() => setIndicatorVolMultBars(5)}
                                            className={`px-2 py-0.5 rounded-full text-[10px] border ${indicatorVolMultBars === 5 ? 'bg-emerald-500/20 border-emerald-500 text-emerald-400' : 'border-white/10 text-nofx-text-muted hover:border-emerald-500/50'}`}
                                        >
                                            {language === 'zh' ? '放量-5' : 'Vol×5'}
                                        </button>
                                        <button
                                            type="button"
                                            onClick={() => setIndicatorVolMultBars(10)}
                                            className={`px-2 py-0.5 rounded-full text-[10px] border ${indicatorVolMultBars === 10 ? 'bg-emerald-500/20 border-emerald-500 text-emerald-400' : 'border-white/10 text-nofx-text-muted hover:border-emerald-500/50'}`}
                                        >
                                            {language === 'zh' ? '放量-10' : 'Vol×10'}
                                        </button>
                                        <button
                                            type="button"
                                            onClick={() => setIndicatorVolMultBars(20)}
                                            className={`px-2 py-0.5 rounded-full text-[10px] border ${indicatorVolMultBars === 20 ? 'bg-emerald-500/20 border-emerald-500 text-emerald-400' : 'border-white/10 text-nofx-text-muted hover:border-emerald-500/50'}`}
                                        >
                                            {language === 'zh' ? '放量-20' : 'Vol×20'}
                                        </button>
                                    </div>
                                </div>
                            </div>

                            <div className="border-t border-white/5 pt-3 mt-2">
                                {indicatorLoading && (
                                    <div className="text-xs text-nofx-text-muted flex items-center gap-2">
                                        <Loader2 className="w-3 h-3 animate-spin" />
                                        {language === 'zh' ? '加载指标分析中...' : 'Loading indicator analysis...'}
                                    </div>
                                )}
                                {indicatorError && !indicatorLoading && (
                                    <div className="text-xs text-nofx-red">
                                        {language === 'zh' ? '指标分析加载失败：' : 'Failed to load indicator analysis: '}
                                        {indicatorError}
                                    </div>
                                )}
                                {indicatorAnalysis && indicatorAnalysis.trade_count > 0 && !indicatorLoading && !indicatorError && (
                                    <>
                                        {/* Side filter tabs: 全部 / 做多 / 做空 */}
                                        <div className="flex gap-1 mb-4 p-1 rounded-lg bg-black/30 border border-white/10">
                                            {(
                                                [
                                                    { id: 'all' as const, zh: '全部', en: 'All' },
                                                    { id: 'long' as const, zh: '做多', en: 'Long' },
                                                    { id: 'short' as const, zh: '做空', en: 'Short' },
                                                ] as const
                                            ).map(({ id, zh, en }) => (
                                                <button
                                                    key={id}
                                                    type="button"
                                                    onClick={() => setIndicatorSideFilter(id)}
                                                    className={`flex-1 py-1.5 px-3 rounded-md text-xs font-medium transition-all duration-200 ${
                                                        indicatorSideFilter === id
                                                            ? 'bg-nofx-accent/30 text-nofx-accent border border-nofx-accent/50'
                                                            : 'text-nofx-text-muted hover:text-nofx-text-main hover:bg-white/5 border border-transparent'
                                                    }`}
                                                >
                                                    {language === 'zh' ? zh : en}
                                                </button>
                                            ))}
                                        </div>

                                        {/* 指标卡片：每行一张 W-full，三列 标题|开仓|平仓，盈利/亏损上下堆叠+横向柱状图 */}
                                        {(() => {
                                            const indicators =
                                                indicatorSideFilter === 'long'
                                                    ? indicatorAnalysis.indicators_long
                                                    : indicatorSideFilter === 'short'
                                                      ? indicatorAnalysis.indicators_short
                                                      : indicatorAnalysis.indicators_all
                                            const rsiPeriod = indicatorAnalysis.rsi_period ?? 14
                                            const emaPeriod = indicatorAnalysis.ema_period ?? 20
                                            const volMultBars = indicatorAnalysis.vol_mult_bars ?? 5
                                            const config: { key: string; label: string; unit: string; signed?: boolean; volMult?: boolean }[] = [
                                                { key: 'rsi', label: `RSI (${rsiPeriod})`, unit: '' },
                                                { key: 'emabias', label: language === 'zh' ? `EMA 偏离 (${emaPeriod})` : `EMA Bias (${emaPeriod})`, unit: '%', signed: true },
                                                { key: 'boll_pct', label: language === 'zh' ? 'BOLL 带内 (20)' : 'BOLL Band % (20)', unit: '%' },
                                                { key: 'atr_pct', label: 'ATR % (14)', unit: '%' },
                                                { key: 'macd', label: language === 'zh' ? 'MACD (柱)' : 'MACD (Histogram)', unit: '', signed: true },
                                                { key: 'adx', label: 'ADX (14)', unit: '' },
                                                { key: 'bias', label: language === 'zh' ? '乖离率 (Bias)' : 'Bias', unit: '%', signed: true },
                                                { key: 'vol_mult', label: language === 'zh' ? `放量 (前${volMultBars}根)` : `Vol Mult (${volMultBars})`, unit: 'x', volMult: true },
                                                { key: 'obv', label: language === 'zh' ? 'OBV (量价累积)' : 'OBV', unit: '', signed: true },
                                                { key: 'poc_deviation_pct', label: language === 'zh' ? 'POC 偏离度' : 'POC Deviation', unit: '%', signed: true },
                                                { key: 'liq_long_short_ratio', label: language === 'zh' ? '爆仓热度(多/空)' : 'Liq Heat (L/S)', unit: 'x' },
                                            ]
                                            const formatBiasSuffix = (val: number | undefined) =>
                                                val == null || val === 0 ? '' : val < 0 ? (language === 'zh' ? ' (EMA下)' : ' (Below)') : (language === 'zh' ? ' (EMA上)' : ' (Above)')
                                            const hasAny = (d: IndicatorDimensionAverages) =>
                                                (d?.profit_entry_count ?? 0) + (d?.profit_exit_count ?? 0) + (d?.loss_entry_count ?? 0) + (d?.loss_exit_count ?? 0) > 0

                                            // Debug: 核对后端返回的字段名与中位数（仅开发环境，Vite 使用 import.meta.env）
                                            if (typeof window !== 'undefined' && import.meta.env.DEV) {
                                                console.log('Indicator Data:', indicatorAnalysis)
                                            }

                                            // 单行：Avg | Med 数值 + 下方横向柱状图，进度条上可选白线标中位数位置
                                            const BarRow = ({
                                                avgValue,
                                                medianValue,
                                                unit,
                                                isWin,
                                                barPct,
                                                medianBarPct,
                                                biasSuffix,
                                            }: {
                                                avgValue: number | undefined
                                                medianValue: number | undefined
                                                unit: string
                                                isWin: boolean
                                                barPct: number
                                                medianBarPct: number
                                                biasSuffix?: string
                                            }) => {
                                                const hasVal = avgValue != null && !Number.isNaN(avgValue)
                                                // 中位数：包括 0（如 MACD 刚好为 0）也要显示 Med: 0.00
                                                const hasMed = typeof medianValue === 'number' && Number.isFinite(medianValue)
                                                const safeBarPct = Number.isFinite(barPct) ? Math.min(100, Math.max(0, barPct)) : 0
                                                const safeMedPct = Number.isFinite(medianBarPct) ? Math.min(100, Math.max(0, medianBarPct)) : NaN
                                                const showMedLine = hasMed && !Number.isNaN(safeMedPct) && safeMedPct >= 0 && safeMedPct <= 100
                                                return (
                                                    <div className="min-w-0">
                                                        <div className="font-mono text-lg font-bold flex flex-wrap items-baseline gap-x-3 gap-y-1 mb-1.5" style={{ color: '#EAECEF' }}>
                                                            {hasVal ? (
                                                                <>
                                                                    <span>Avg: {avgValue!.toFixed(2)}{unit && <span className="text-sm opacity-60">{unit}</span>}</span>
                                                                    {hasMed && (
                                                                        <span className="text-sm text-white/40 font-normal">Med: {medianValue!.toFixed(2)}{unit && <span className="opacity-60">{unit}</span>}</span>
                                                                    )}
                                                                    {biasSuffix && <span className="text-[10px]" style={{ color: isWin ? '#00ffad' : '#ff3b30', opacity: 0.9 }}>{biasSuffix}</span>}
                                                                </>
                                                            ) : (
                                                                <span className="opacity-60">–</span>
                                                            )}
                                                        </div>
                                                        <div className="h-2 w-full rounded-full overflow-hidden bg-white/10 relative">
                                                            {hasVal && safeBarPct > 0 ? (
                                                                <>
                                                                    <div
                                                                        className="h-full rounded-full transition-all duration-300 absolute inset-y-0 left-0"
                                                                        style={{
                                                                            width: `${safeBarPct}%`,
                                                                            backgroundColor: isWin ? 'rgba(0, 255, 173, 0.4)' : 'rgba(255, 59, 48, 0.4)',
                                                                        }}
                                                                    />
                                                                    {showMedLine && (
                                                                        <div
                                                                            className="absolute top-0 bottom-0 w-0.5 bg-white/70 rounded-full pointer-events-none"
                                                                            style={{ left: `${safeMedPct}%`, transform: 'translateX(-50%)' }}
                                                                            title={`Median: ${medianValue!.toFixed(2)}`}
                                                                        />
                                                                    )}
                                                                </>
                                                            ) : (
                                                                <div className="h-full w-full rounded-full border border-dashed border-[#2B3139]/80 bg-transparent absolute inset-0" style={{ boxSizing: 'border-box' }} />
                                                            )}
                                                        </div>
                                                    </div>
                                                )
                                            }

                                            return (
                                                <div key={indicatorSideFilter} className="grid grid-cols-1 gap-4 animate-fade-in w-full">
                                                    {config
                                                        .filter((item) => {
                                                            const d = indicators[item.key]
                                                            return d && hasAny(d)
                                                        })
                                                        .map((row) => {
                                                            const data = indicators[row.key]!
                                                            const entryWin = data.profit_entry_count > 0 ? data.profit_entry_avg : undefined
                                                            const entryWinMed = data.profit_entry_count > 0 ? data.profit_entry_median : undefined
                                                            const entryLoss = data.loss_entry_count > 0 ? data.loss_entry_avg : undefined
                                                            const entryLossMed = data.loss_entry_count > 0 ? data.loss_entry_median : undefined
                                                            const exitWin = data.profit_exit_count > 0 ? data.profit_exit_avg : undefined
                                                            const exitWinMed = data.profit_exit_count > 0 ? data.profit_exit_median : undefined
                                                            const exitLoss = data.loss_exit_count > 0 ? data.loss_exit_avg : undefined
                                                            const exitLossMed = data.loss_exit_count > 0 ? data.loss_exit_median : undefined
                                                            const allVals = [entryWin, entryLoss, exitWin, exitLoss].filter((v): v is number => v != null && !Number.isNaN(v))
                                                            // 进度条比例：左对齐 0 起。Bias/EMABias/MACD 用正负区间映射；VolMult 上限 max(5, 样本最大)
                                                            let getBarPct: (val: number | undefined) => number
                                                            if (row.signed) {
                                                                const min = allVals.length ? Math.min(...allVals, -1.5) : -1.5
                                                                const max = allVals.length ? Math.max(...allVals, 1.5) : 1.5
                                                                const range = max - min || 1
                                                                getBarPct = (val) => (val == null || Number.isNaN(val) ? 0 : ((val - min) / range) * 100)
                                                            } else if (row.volMult) {
                                                                const cap = allVals.length ? Math.max(5, ...allVals) : 5
                                                                getBarPct = (val) => (val == null || Number.isNaN(val) ? 0 : (Math.max(0, val) / cap) * 100)
                                                            } else {
                                                                const cap = allVals.length ? Math.max(1, ...allVals) : 1
                                                                getBarPct = (val) => (val == null || Number.isNaN(val) ? 0 : (Math.max(0, val) / cap) * 100)
                                                            }

                                                            const entryWinPct = getBarPct(entryWin)
                                                            const entryWinMedPct = getBarPct(entryWinMed)
                                                            const entryLossPct = getBarPct(entryLoss)
                                                            const entryLossMedPct = getBarPct(entryLossMed)
                                                            const exitWinPct = getBarPct(exitWin)
                                                            const exitWinMedPct = getBarPct(exitWinMed)
                                                            const exitLossPct = getBarPct(exitLoss)
                                                            const exitLossMedPct = getBarPct(exitLossMed)

                                                            return (
                                                                <div
                                                                    key={row.key}
                                                                    className="rounded-xl p-5 border transition-all duration-200 bg-[#161A1E] border-[#2B3139] w-full grid grid-cols-1 md:grid-cols-[1fr_2fr_2fr] gap-5 md:gap-6 items-stretch min-w-0"
                                                                >
                                                                    {/* 标题区：指标名+周期 text-base，居中 */}
                                                                    <div className="flex items-center justify-center md:justify-center text-center">
                                                                        <span className="text-base font-medium" style={{ color: '#848E9C' }}>
                                                                            {row.label}
                                                                        </span>
                                                                    </div>
                                                                    {/* 开仓区：盈利上、亏损下，各带横向柱状图 */}
                                                                    <div className="min-w-0 flex flex-col gap-5">
                                                                        <div className="text-xs mb-0.5" style={{ color: '#848E9C' }}>
                                                                            {language === 'zh' ? '开仓' : 'Entry'}
                                                                        </div>
                                                                        <BarRow avgValue={entryWin} medianValue={entryWinMed} unit={row.unit} isWin={true} barPct={entryWinPct} medianBarPct={entryWinMedPct} biasSuffix={row.key === 'bias' ? formatBiasSuffix(entryWin) : undefined} />
                                                                        <BarRow avgValue={entryLoss} medianValue={entryLossMed} unit={row.unit} isWin={false} barPct={entryLossPct} medianBarPct={entryLossMedPct} biasSuffix={row.key === 'bias' ? formatBiasSuffix(entryLoss) : undefined} />
                                                                    </div>
                                                                    {/* 平仓区：盈利上、亏损下，各带横向柱状图 */}
                                                                    <div className="min-w-0 flex flex-col gap-5">
                                                                        <div className="text-xs mb-0.5" style={{ color: '#848E9C' }}>
                                                                            {language === 'zh' ? '平仓' : 'Exit'}
                                                                        </div>
                                                                        <BarRow avgValue={exitWin} medianValue={exitWinMed} unit={row.unit} isWin={true} barPct={exitWinPct} medianBarPct={exitWinMedPct} biasSuffix={row.key === 'bias' ? formatBiasSuffix(exitWin) : undefined} />
                                                                        <BarRow avgValue={exitLoss} medianValue={exitLossMed} unit={row.unit} isWin={false} barPct={exitLossPct} medianBarPct={exitLossMedPct} biasSuffix={row.key === 'bias' ? formatBiasSuffix(exitLoss) : undefined} />
                                                                    </div>
                                                                </div>
                                                            )
                                                        })}
                                                </div>
                                            )
                                        })()}
                                        <div className="mt-3 text-[10px] text-nofx-text-muted">
                                            {language === 'zh'
                                                ? `样本笔数：${indicatorAnalysis.trade_count}（仅统计已有足够历史 K 线的交易）`
                                                : `Sample trades: ${indicatorAnalysis.trade_count} (only trades with sufficient kline history are included).`}
                                        </div>
                                    </>
                                )}
                                {!indicatorLoading && !indicatorError && (!indicatorAnalysis || indicatorAnalysis.trade_count === 0) && (
                                    <div className="text-xs text-nofx-text-muted">
                                        {language === 'zh'
                                            ? '暂无可用的历史平仓交易用于指标分析。'
                                            : 'No sufficient closed trades available for indicator analysis yet.'}
                                    </div>
                                )}
                            </div>
                        </div>

                        {/* Position History Panel — full width below indicator */}
                        <div className="nofx-glass p-6 w-full">
                            <div className="flex items-center justify-between mb-5">
                                <h2 className="text-xl font-bold flex items-center gap-2 text-nofx-text-main">
                                    <span className="text-2xl">📜</span>
                                    {t('positionHistory.title', language)}
                                </h2>
                            </div>
                            <PositionHistory traderId={selectedTraderId} />
                        </div>
                    </div>
                )}
            </div>
        </DeepVoidBackground>
    )
}

// Stat Card Component - Deep Void Style
function StatCard({
    title,
    value,
    unit,
    change,
    positive,
    subtitle,
    icon,
}: {
    title: string
    value: string
    unit?: string
    change?: number
    positive?: boolean
    subtitle?: string
    icon?: string
}) {
    return (
        <div className="group nofx-glass p-5 rounded-lg transition-all duration-300 hover:bg-white/5 hover:translate-y-[-2px] border border-white/5 hover:border-nofx-gold/20 relative overflow-hidden">
            <div className="absolute top-0 right-0 p-4 opacity-5 group-hover:opacity-10 transition-opacity text-4xl grayscale group-hover:grayscale-0">
                {icon}
            </div>
            <div className="text-xs mb-2 font-mono uppercase tracking-wider text-nofx-text-muted flex items-center gap-2">
                {title}
            </div>
            <div className="flex items-baseline gap-1 mb-1">
                <div className="text-2xl font-bold font-mono text-nofx-text-main tracking-tight group-hover:text-white transition-colors">
                    {value}
                </div>
                {unit && <span className="text-xs font-mono text-nofx-text-muted opacity-60">{unit}</span>}
            </div>

            {change !== undefined && (
                <div className="flex items-center gap-1">
                    <div
                        className={`text-sm mono font-bold flex items-center gap-1 ${positive ? 'text-nofx-green' : 'text-nofx-red'}`}
                    >
                        <span>{positive ? '▲' : '▼'}</span>
                        <span>{positive ? '+' : ''}{change.toFixed(2)}%</span>
                    </div>
                </div>
            )}
            {subtitle && (
                <div className="text-xs mt-2 mono text-nofx-text-muted opacity-80">
                    {subtitle}
                </div>
            )}
        </div>
    )
}
