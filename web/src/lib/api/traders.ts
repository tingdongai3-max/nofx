import type {
  TraderInfo,
  TraderConfigData,
  CreateTraderRequest,
  RuntimeCapabilityPreview,
  OrderReconcilePreview,
  ProtectionReconcilePreview,
  ProtectionAdjustmentPreview,
  ScaleOutReconcilePreview,
  ScaleInReconcilePreview,
  ReplayPreview,
  RestorePreview,
} from '../../types'
import { API_BASE, httpClient } from './helpers'
import { ApiError } from '../httpClient'

function throwApiError(
  message: string,
  errorKey?: string,
  errorParams?: Record<string, string>,
  statusCode?: number
): never {
  throw new ApiError(message, errorKey, errorParams, statusCode)
}

export const traderApi = {
  async getTraders(): Promise<TraderInfo[]> {
    const result = await httpClient.get<TraderInfo[]>(`${API_BASE}/my-traders`)
    if (!result.success) throw new Error('Failed to fetch trader list')
    return Array.isArray(result.data) ? result.data : []
  },

  async getPublicTraders(): Promise<any[]> {
    const result = await httpClient.get<any[]>(`${API_BASE}/traders`)
    if (!result.success) throw new Error('Failed to fetch public trader list')
    return result.data!
  },

  async createTrader(request: CreateTraderRequest): Promise<TraderInfo> {
    const result = await httpClient.post<TraderInfo>(
      `${API_BASE}/traders`,
      request
    )
    if (!result.success) {
      throwApiError(
        result.message || 'Failed to create trader',
        result.errorKey,
        result.errorParams,
        result.statusCode
      )
    }
    return result.data!
  },

  async deleteTrader(traderId: string): Promise<void> {
    const result = await httpClient.delete(`${API_BASE}/traders/${traderId}`)
    if (!result.success) throw new Error('Failed to delete trader')
  },

  async startTrader(traderId: string): Promise<void> {
    const result = await httpClient.post(
      `${API_BASE}/traders/${traderId}/start`
    )
    if (!result.success) {
      throwApiError(
        result.message || 'Failed to start trader',
        result.errorKey,
        result.errorParams,
        result.statusCode
      )
    }
  },

  async stopTrader(traderId: string): Promise<void> {
    const result = await httpClient.post(`${API_BASE}/traders/${traderId}/stop`)
    if (!result.success) throw new Error('Failed to stop trader')
  },

  async toggleCompetition(traderId: string, showInCompetition: boolean): Promise<void> {
    const result = await httpClient.put(
      `${API_BASE}/traders/${traderId}/competition`,
      { show_in_competition: showInCompetition }
    )
    if (!result.success) throw new Error('Failed to update competition visibility')
  },

  async closePosition(traderId: string, symbol: string, side: string): Promise<{ message: string }> {
    const result = await httpClient.post<{ message: string }>(
      `${API_BASE}/traders/${traderId}/close-position`,
      { symbol, side }
    )
    if (!result.success) throw new Error('Failed to close position')
    return result.data!
  },

  async updateTraderPrompt(
    traderId: string,
    customPrompt: string
  ): Promise<void> {
    const result = await httpClient.put(
      `${API_BASE}/traders/${traderId}/prompt`,
      { custom_prompt: customPrompt }
    )
    if (!result.success) throw new Error('Failed to update custom prompt')
  },

  async getTraderConfig(traderId: string): Promise<TraderConfigData> {
    const result = await httpClient.get<TraderConfigData>(
      `${API_BASE}/traders/${traderId}/config`
    )
    if (!result.success) throw new Error('Failed to fetch trader config')
    return result.data!
  },

  async updateTrader(
    traderId: string,
    request: CreateTraderRequest
  ): Promise<TraderInfo> {
    const result = await httpClient.put<TraderInfo>(
      `${API_BASE}/traders/${traderId}`,
      request
    )
    if (!result.success) {
      throwApiError(
        result.message || 'Failed to update trader',
        result.errorKey,
        result.errorParams,
        result.statusCode
      )
    }
    return result.data!
  },

  async getRuntimeCapabilityPreview(
    traderId: string,
    symbol?: string,
    silent: boolean = true
  ): Promise<RuntimeCapabilityPreview> {
    const params = new URLSearchParams({ trader_id: traderId })
    if (symbol) {
      params.append('symbol', symbol)
    }

    const result = await httpClient.request<RuntimeCapabilityPreview>(
      `${API_BASE}/runtime/capabilities/preview?${params.toString()}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch runtime capability preview')
    return result.data!
  },

  async getRuntimeReplayPreview(
    fixtureId: string,
    mode?: string,
    silent: boolean = true
  ): Promise<ReplayPreview> {
    const params = new URLSearchParams({ fixture_id: fixtureId })
    if (mode) {
      params.append('mode', mode)
    }

    const result = await httpClient.request<ReplayPreview>(
      `${API_BASE}/runtime/replay/preview?${params.toString()}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch runtime replay preview')
    return result.data!
  },

  async getRuntimeRestorePreview(
    traderId: string,
    symbol?: string,
    silent: boolean = true
  ): Promise<RestorePreview> {
    const params = new URLSearchParams({ trader_id: traderId })
    if (symbol) {
      params.append('symbol', symbol)
    }

    const result = await httpClient.request<RestorePreview>(
      `${API_BASE}/runtime/restore/preview?${params.toString()}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch runtime restore preview')
    return result.data!
  },

  async getRuntimeOrderPreview(
    traderId: string,
    symbol?: string,
    silent: boolean = true
  ): Promise<OrderReconcilePreview> {
    const params = new URLSearchParams({ trader_id: traderId })
    if (symbol) {
      params.append('symbol', symbol)
    }

    const result = await httpClient.request<OrderReconcilePreview>(
      `${API_BASE}/runtime/orders/preview?${params.toString()}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch runtime order preview')
    return result.data!
  },

  async getRuntimeProtectionPreview(
    traderId: string,
    symbol?: string,
    silent: boolean = true
  ): Promise<ProtectionReconcilePreview> {
    const params = new URLSearchParams({ trader_id: traderId })
    if (symbol) {
      params.append('symbol', symbol)
    }

    const result = await httpClient.request<ProtectionReconcilePreview>(
      `${API_BASE}/runtime/protection/preview?${params.toString()}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch runtime protection preview')
    return result.data!
  },

  async getRuntimeProtectionAdjustmentPreview(
    traderId: string,
    symbol?: string,
    silent: boolean = true
  ): Promise<ProtectionAdjustmentPreview> {
    const params = new URLSearchParams({ trader_id: traderId })
    if (symbol) {
      params.append('symbol', symbol)
    }

    const result = await httpClient.request<ProtectionAdjustmentPreview>(
      `${API_BASE}/runtime/protection-adjustment/preview?${params.toString()}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch runtime protection adjustment preview')
    return result.data!
  },

  async getRuntimeScaleOutPreview(
    traderId: string,
    symbol?: string,
    silent: boolean = true
  ): Promise<ScaleOutReconcilePreview> {
    const params = new URLSearchParams({ trader_id: traderId })
    if (symbol) {
      params.append('symbol', symbol)
    }

    const result = await httpClient.request<ScaleOutReconcilePreview>(
      `${API_BASE}/runtime/scale-out/preview?${params.toString()}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch runtime scale-out preview')
    return result.data!
  },

  async getRuntimeScaleInPreview(
    traderId: string,
    symbol?: string,
    silent: boolean = true
  ): Promise<ScaleInReconcilePreview> {
    const params = new URLSearchParams({ trader_id: traderId })
    if (symbol) {
      params.append('symbol', symbol)
    }

    const result = await httpClient.request<ScaleInReconcilePreview>(
      `${API_BASE}/runtime/scale-in/preview?${params.toString()}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch runtime scale-in preview')
    return result.data!
  },
}
