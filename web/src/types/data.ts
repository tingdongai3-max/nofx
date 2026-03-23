export interface ScoreBinPerformance {
  bin_start: number
  bin_label: string
  trade_count: number
  ev_long: number
  median_ev_long: number
  profit_factor_long: number
  ev_short: number
  median_ev_short: number
  profit_factor_short: number
  smoothed?: boolean
  smoothed_by?: string
}
