import type {
  SystemStatus,
  AccountInfo,
  Position,
  DecisionRecord,
  Statistics,
  CompetitionData,
  PositionHistoryResponse,
  ShadowSnapshot,
  AdaptiveWeightState,
} from '../../types'
import { API_BASE, httpClient } from './helpers'

export const dataApi = {
  async getStatus(traderId?: string): Promise<SystemStatus> {
    const url = traderId
      ? `${API_BASE}/status?trader_id=${traderId}`
      : `${API_BASE}/status`
    const result = await httpClient.get<SystemStatus>(url)
    if (!result.success) throw new Error('Failed to fetch system status')
    return result.data!
  },

  async getAccount(traderId?: string): Promise<AccountInfo> {
    const url = traderId
      ? `${API_BASE}/account?trader_id=${traderId}`
      : `${API_BASE}/account`
    const result = await httpClient.get<AccountInfo>(url)
    if (!result.success) throw new Error('Failed to fetch account info')
    return result.data!
  },

  async getPositions(traderId?: string): Promise<Position[]> {
    const url = traderId
      ? `${API_BASE}/positions?trader_id=${traderId}`
      : `${API_BASE}/positions`
    const result = await httpClient.get<Position[]>(url)
    if (!result.success) throw new Error('Failed to fetch positions')
    return result.data!
  },

  async getDecisions(traderId?: string): Promise<DecisionRecord[]> {
    const url = traderId
      ? `${API_BASE}/decisions?trader_id=${traderId}`
      : `${API_BASE}/decisions`
    const result = await httpClient.get<DecisionRecord[]>(url)
    if (!result.success) throw new Error('Failed to fetch decision logs')
    return result.data!
  },

  async getLatestDecisions(
    traderId?: string,
    limit: number = 5
  ): Promise<DecisionRecord[]> {
    const params = new URLSearchParams()
    if (traderId) {
      params.append('trader_id', traderId)
    }
    params.append('limit', limit.toString())

    const result = await httpClient.get<DecisionRecord[]>(
      `${API_BASE}/decisions/latest?${params}`
    )
    if (!result.success) throw new Error('Failed to fetch latest decisions')
    return result.data!
  },

  async getStatistics(traderId?: string): Promise<Statistics> {
    const url = traderId
      ? `${API_BASE}/statistics?trader_id=${traderId}`
      : `${API_BASE}/statistics`
    const result = await httpClient.get<Statistics>(url)
    if (!result.success) throw new Error('Failed to fetch statistics')
    return result.data!
  },

  async getEquityHistory(traderId?: string): Promise<any[]> {
    const url = traderId
      ? `${API_BASE}/equity-history?trader_id=${traderId}`
      : `${API_BASE}/equity-history`
    const result = await httpClient.get<any[]>(url)
    if (!result.success) throw new Error('Failed to fetch equity history')
    return result.data!
  },

  async getEquityHistoryBatch(traderIds: string[], hours?: number): Promise<any> {
    const result = await httpClient.post<any>(
      `${API_BASE}/equity-history-batch`,
      { trader_ids: traderIds, hours: hours || 0 }
    )
    if (!result.success) throw new Error('Failed to fetch batch equity history')
    return result.data!
  },

  async getTopTraders(): Promise<any[]> {
    const result = await httpClient.get<any[]>(`${API_BASE}/top-traders`)
    if (!result.success) throw new Error('Failed to fetch top traders')
    return result.data!
  },

  async getPublicTraderConfig(traderId: string): Promise<any> {
    const result = await httpClient.get<any>(
      `${API_BASE}/trader/${traderId}/config`
    )
    if (!result.success) throw new Error('Failed to fetch public trader config')
    return result.data!
  },

  async getCompetition(): Promise<CompetitionData> {
    const result = await httpClient.get<CompetitionData>(
      `${API_BASE}/competition`
    )
    if (!result.success) throw new Error('Failed to fetch competition data')
    return result.data!
  },

  async getPositionHistory(traderId: string, limit: number = 100): Promise<PositionHistoryResponse> {
    const result = await httpClient.get<PositionHistoryResponse>(
      `${API_BASE}/positions/history?trader_id=${traderId}&limit=${limit}`
    )
    if (!result.success) throw new Error('Failed to fetch position history')
    return result.data!
  },

  async getShadowSnapshots(traderId: string, limit: number = 200): Promise<ShadowSnapshot[]> {
    const result = await httpClient.get<ShadowSnapshot[]>(
      `${API_BASE}/shadow-snapshots?trader_id=${encodeURIComponent(traderId)}&limit=${limit}`
    )
    if (!result.success) throw new Error('Failed to fetch shadow snapshots')
    return Array.isArray(result.data) ? result.data : []
  },

  async getAdaptiveWeights(
    traderId: string,
    symbol?: string,
    sector?: string
  ): Promise<AdaptiveWeightState> {
    const params = new URLSearchParams()
    params.append('trader_id', traderId)
    if (symbol) {
      params.append('symbol', symbol)
    }
    if (sector) {
      params.append('sector', sector)
    }

    const result = await httpClient.get<AdaptiveWeightState>(
      `${API_BASE}/adaptive-weights?${params.toString()}`
    )
    if (!result.success) throw new Error('Failed to fetch adaptive weights')
    return (
      result.data || {
        sample_count: 0,
        sector_sample_count: 0,
        coin_sample_count: 0,
        alpha: 0,
        sample_target: 30,
        blend_default: 1,
        blend_adaptive: 0,
        factors: [],
        updated_at: 0,
      }
    )
  },
}
