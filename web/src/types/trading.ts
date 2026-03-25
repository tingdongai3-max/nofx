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
  hidden_factors: AdaptiveFactorState[]
  nested_weights: Record<string, number>
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
  execution_mode?: string
  real_backtest?: RealBacktestDecisionMeta
}

export interface RealBacktestDimension {
  bin_start: number
  bin_label?: string
  trade_count: number
  expected_value: number
  median_expected_value: number
  profit_factor: number
  expected_value_long: number
  expected_value_short: number
  median_expected_value_long: number
  median_expected_value_short: number
  profit_factor_long: number
  profit_factor_short: number
  smoothed?: boolean
  smoothed_by?: string
}

export interface RealBacktestDecisionMeta {
  signal: string
  logic_score: number
  sector?: string
  position_size_usd?: number
  bin_center: number
  smoothing_half_width?: number
  hold_duration_seconds?: number
  final_entry_ev?: number
  attribution_weights?: Record<string, number>
  mahalanobis_distance?: number
  mahalanobis_threshold?: number
  mahalanobis_resonant?: boolean
  feature_vector_focus?: string
  feature_vector_deviation?: number
  entry_allowed?: boolean
  entry_block_reason?: string
  global?: RealBacktestDimension
  sector_bin?: RealBacktestDimension
  symbol?: RealBacktestDimension
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

export interface CandidateMarketItem {
  symbol: string
  sector?: string
  current_price: number
  logic_score: number
  bias: 'LONG' | 'SHORT' | 'WAIT' | string
  expected_ev: number
  _debug_bin_stats?: {
    bin_start: number
    trade_count: number
    ev_long: number
    profit_factor_long: number
    ev_short: number
    profit_factor_short: number
  }
}

export interface CandidateSnapshotResponse {
  trader_id: string
  trader_name: string
  updated_at: string
  score_engine: string
  candidates: CandidateMarketItem[]
}

export interface RealBacktestMonitorPosition {
  trader_id: string
  trader_name: string
  symbol: string
  side: string
  sector?: string
  signal?: string
  entry_price: number
  mark_price: number
  quantity: number
  leverage: number
  margin_used: number
  unrealized_pnl: number
  unrealized_pnl_pct: number
  logic_score: number
  bin_center: number
  smoothing_half_width: number
  entry_time: number
  close_at: number
  countdown_seconds: number
  global_ev: number
  sector_ev: number
  symbol_ev: number
  global_median_ev: number
  sector_median_ev: number
  symbol_median_ev: number
  resonance_strength: number
  mahalanobis_distance?: number
  mahalanobis_threshold?: number
  mahalanobis_resonant?: boolean
  feature_vector_focus?: string
  feature_vector_deviation?: number
  entry_allowed?: boolean
  entry_block_reason?: string
  global_bin?: RealBacktestDimension
  sector_bin?: RealBacktestDimension
  symbol_bin?: RealBacktestDimension
}

export interface RealBacktestLogEntry {
  trader_id: string
  trader_name: string
  symbol: string
  side: string
  action: string
  signal?: string
  price: number
  quantity: number
  logic_score: number
  realized_pnl?: number
  success: boolean
  auto_closed: boolean
  message?: string
  timestamp: number
  global_ev?: number
  sector_ev?: number
  symbol_ev?: number
  final_entry_ev?: number
  attribution_weights?: Record<string, number>
  mahalanobis_distance?: number
  mahalanobis_threshold?: number
  mahalanobis_resonant?: boolean
  feature_vector_focus?: string
  feature_vector_deviation?: number
  entry_allowed?: boolean
  entry_block_reason?: string
  execution_mode?: string
}

export interface RealFireAttributionDimension {
  name: string
  correlation: number
  real_weight: number
  shadow_weight: number
  weight_delta: number
  entry_average_ev: number
  hold_average_ev: number
  average_retention: number
  closed_trade_count: number
}

export interface RealFireAttribution {
  sample_count: number
  dimensions: RealFireAttributionDimension[]
  dominant_dimension?: string
  shadow_dominant_dimension?: string
}

export interface RealBacktestMonitorResponse {
  armed: boolean
  mode: string
  daemon_running: boolean
  total_equity: number
  position_count: number
  log_count: number
  positions: RealBacktestMonitorPosition[]
  logs: RealBacktestLogEntry[]
  attribution?: RealFireAttribution
  updated_at: number
}
