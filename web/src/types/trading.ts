export interface SystemStatus {
  trader_id: string
  trader_name: string
  ai_model: string
  is_running: boolean
  start_time: string
  runtime_minutes: number
  call_count: number
  initial_balance: number
  scan_interval: string
  stop_until: string
  last_reset_time: string
  ai_provider: string
  strategy_type?: 'ai_trading' | 'grid_trading'
  grid_symbol?: string
}

export interface AccountInfo {
  total_equity: number
  wallet_balance: number
  unrealized_profit: number // 未实现盈亏（交易所API官方值）
  available_balance: number
  total_pnl: number
  total_pnl_pct: number
  initial_balance: number
  daily_pnl: number
  position_count: number
  margin_used: number
  margin_used_pct: number
}

export interface FactorTelemetry {
  timestamp: number
  price: number
  heat_score: number
  trading_sub: number
  quant_sub: number
}

export interface ShadowSnapshot {
  id: number
  trader_id: string
  decision_time: number
  symbol: string
  sector: string
  action_taken: number
  price_t0: number
  heat_score: number
  trading_sub: number
  quant_sub: number
  market_factor: number
  trend_factor: number
  volume_spike_factor: number
  quant_factor: number
  social_factor: number
  onchain_factor: number
  vol_utilization: number
  funding_rate: number
  source_summary: string
  filled: boolean
  price_t1: number
  return_pct: number
  filled_at: number
  created_at: number
  updated_at: number
}

export interface AdaptiveFactorState {
  name: string
  ic: number
  sector_ic: number
  coin_ic: number
  final_ic: number
  default_weight: number
  empirical_weight: number
  final_weight: number
}

export interface AdaptiveWeightState {
  trader_id?: string
  symbol?: string
  sector?: string
  sample_count: number
  sector_sample_count: number
  coin_sample_count: number
  alpha: number
  sample_target: number
  blend_default: number
  blend_adaptive: number
  factors: AdaptiveFactorState[]
  updated_at: number
}

export interface Position {
  symbol: string
  side: string
  entry_price: number
  mark_price: number
  quantity: number
  leverage: number
  unrealized_pnl: number
  unrealized_pnl_pct: number
  liquidation_price: number
  margin_used: number
  telemetry?: FactorTelemetry[]
}

export interface DecisionAction {
  action: string
  symbol: string
  quantity: number
  leverage: number
  price: number
  stop_loss?: number // Stop loss price
  take_profit?: number // Take profit price
  confidence?: number // AI confidence (0-100)
  reasoning?: string // Brief reasoning
  order_id: number
  timestamp: string
  success: boolean
  error?: string
}

export interface AccountSnapshot {
  total_balance: number
  available_balance: number
  total_unrealized_profit: number
  position_count: number
  margin_used_pct: number
}

export interface DecisionRecord {
  timestamp: string
  cycle_number: number
  system_prompt: string
  input_prompt: string
  cot_trace: string
  decision_json: string
  account_state: AccountSnapshot
  positions: any[]
  candidate_coins: string[]
  decisions: DecisionAction[]
  execution_log: string[]
  success: boolean
  error_message?: string
}

export interface Statistics {
  total_cycles: number
  successful_cycles: number
  failed_cycles: number
  total_open_positions: number
  total_close_positions: number
}

// AI Trading相关类型
export interface TraderInfo {
  trader_id: string
  trader_name: string
  ai_model: string
  exchange_id?: string
  is_running?: boolean
  show_in_competition?: boolean
  strategy_id?: string
  strategy_name?: string
  custom_prompt?: string
  use_ai500?: boolean
  use_oi_top?: boolean
  system_prompt_template?: string
}

// Competition related types
export interface CompetitionTraderData {
  trader_id: string
  trader_name: string
  ai_model: string
  exchange: string
  total_equity: number
  total_pnl: number
  total_pnl_pct: number
  position_count: number
  margin_used_pct: number
  is_running: boolean
}

export interface CompetitionData {
  traders: CompetitionTraderData[]
  count: number
}

// Trader Configuration Data for View Modal
export interface TraderConfigData {
  trader_id?: string
  trader_name: string
  ai_model: string
  exchange_id: string
  strategy_id?: string // 策略ID
  strategy_name?: string // 策略名称
  is_cross_margin: boolean
  show_in_competition: boolean // 是否在竞技场显示
  scan_interval_minutes: number
  initial_balance: number
  is_running: boolean
  // 以下为旧版字段（向后兼容）
  btc_eth_leverage?: number
  altcoin_leverage?: number
  trading_symbols?: string
  custom_prompt?: string
  override_base_prompt?: boolean
  system_prompt_template?: string
  use_ai500?: boolean
  use_oi_top?: boolean
}

// Position History Types
export interface HistoricalPosition {
  id: number
  trader_id: string
  exchange_id: string
  exchange_type: string
  symbol: string
  side: string
  quantity: number
  entry_quantity: number
  entry_price: number
  entry_order_id: string
  entry_time: string
  exit_price: number
  exit_order_id: string
  exit_time: string
  realized_pnl: number
  fee: number
  leverage: number
  status: string
  close_reason: string
  telemetry?: FactorTelemetry[]
  created_at: string
  updated_at: string
}

// Matches Go TraderStats struct exactly
export interface TraderStats {
  total_trades: number
  win_trades: number
  loss_trades: number
  win_rate: number
  profit_factor: number
  sharpe_ratio: number
  total_pnl: number
  total_fee: number
  avg_win: number
  avg_loss: number
  max_drawdown_pct: number
}

// Matches Go SymbolStats struct exactly
export interface SymbolStats {
  symbol: string
  total_trades: number
  win_trades: number
  win_rate: number
  total_pnl: number
  avg_pnl: number
  avg_hold_mins: number
}

// Matches Go DirectionStats struct exactly
export interface DirectionStats {
  side: string
  trade_count: number
  win_rate: number
  total_pnl: number
  avg_pnl: number
}

export interface PositionHistoryResponse {
  positions: HistoricalPosition[]
  stats: TraderStats | null
  symbol_stats: SymbolStats[]
  direction_stats: DirectionStats[]
}

// Grid Risk Information for frontend display
export interface GridRiskInfo {
  // Leverage info
  current_leverage: number
  effective_leverage: number
  recommended_leverage: number

  // Position info
  current_position: number
  max_position: number
  position_percent: number

  // Liquidation info
  liquidation_price: number
  liquidation_distance: number

  // Market state
  regime_level: string

  // Box state
  short_box_upper: number
  short_box_lower: number
  mid_box_upper: number
  mid_box_lower: number
  long_box_upper: number
  long_box_lower: number
  current_price: number

  // Breakout state
  breakout_level: string
  breakout_direction: string
}

export interface CandidateDonchianBox {
  period: number
  upper: number
  lower: number
  mid: number
  state: string
}

export interface CandidateEmaSignals {
  state:
    | 'bullish_stack'
    | 'bearish_stack'
    | 'mixed'
    | 'single_ema'
    | 'unavailable'
  values?: Record<number, number>
  periods?: number[]
}

export interface CandidateTrendContext {
  timeframe: string
  rsi?: number
  macd?: number
  macd_state?: string
  ema_state?: CandidateEmaSignals['state'] | string
  donchian_state?: string
  donchian_period?: number
}

export interface CandidateMarketItem {
  symbol: string
  current_price: number
  timeframes: string[]
  donchian_boxes?: Record<string, CandidateDonchianBox>
  trend_contexts?: Record<string, CandidateTrendContext>
  ema_signals: CandidateEmaSignals
  open_interest?: {
    Latest: number
    Average: number
    sample_count?: number
    period?: string
  }
  orderbook?: {
    bid_total: number
    ask_total: number
    imbalance: number
  }
  dex_screener?: {
    chain_id?: string
    pair_address?: string
    pair_url?: string
    liquidity_usd: number
    volume_h1: number
    buy_txns_h1: number
    sell_txns_h1: number
    buy_ratio: number
    buy_sell_ratio: number
    cex_volume_h1: number
    onchain_to_cex_ratio: number
  }
  gecko_sentiment?: {
    coin_id?: string
    public_interest_score: number
    sentiment_votes_up_percentage: number
    using_private_key?: boolean
    cached_at?: string
  }
  heat_score?: {
    composite_score: number
    trading_score: number
    quant_score: number
    source_weights?: Record<string, number>
  }
  vol_utilization?: number
  vol_util_basis?: string
  funding_rate?: number
  ai500_score?: number | null
  sources?: string[]
  updated_at: string
}

export interface CandidateSnapshotResponse {
  trader_id: string
  trader_name: string
  updated_at: string
  candidates: CandidateMarketItem[]
}
