import type {
  AIModel,
  Exchange,
  UpdateModelConfigRequest,
  UpdateExchangeConfigRequest,
  CreateExchangeRequest,
} from '../../types'
import { API_BASE, httpClient, CryptoService } from './helpers'

export interface UpdateRealBacktestConfigRequest {
  enabled?: boolean
  rb_max_margin_per_trade?: number
  rb_reserve_margin?: number
  rb_min_ev_threshold?: number
  rb_max_ev_threshold?: number
}

export interface UpdateRealBacktestConfigResponse {
  real_backtest_enabled: boolean
  rb_max_margin_per_trade: number
  rb_reserve_margin: number
  rb_min_ev_threshold: number
  rb_max_ev_threshold: number
}

export interface UpdateAdaptiveMemoryConfigRequest {
  adaptive_global_samples?: number
  adaptive_sector_samples?: number
  adaptive_symbol_samples?: number
}

export interface UpdateAdaptiveMemoryConfigResponse {
  adaptive_global_samples: number
  adaptive_sector_samples: number
  adaptive_symbol_samples: number
}

export interface UpdateResonanceGuardConfigRequest {
  adaptive_entry_floor?: number
  adaptive_entry_lambda?: number
}

export interface UpdateResonanceGuardConfigResponse {
  adaptive_entry_floor: number
  adaptive_entry_lambda: number
}

export const configApi = {
  async getModelConfigs(): Promise<AIModel[]> {
    const result = await httpClient.get<AIModel[]>(`${API_BASE}/models`)
    if (!result.success) throw new Error('Failed to fetch model configs')
    return Array.isArray(result.data) ? result.data : []
  },

  async getSupportedModels(): Promise<AIModel[]> {
    const result = await httpClient.get<AIModel[]>(
      `${API_BASE}/supported-models`
    )
    if (!result.success) throw new Error('Failed to fetch supported models')
    return result.data!
  },

  async getPromptTemplates(): Promise<string[]> {
    const res = await fetch(`${API_BASE}/prompt-templates`)
    if (!res.ok) throw new Error('Failed to fetch prompt templates')
    const data = await res.json()
    if (Array.isArray(data.templates)) {
      return data.templates.map((item: { name: string }) => item.name)
    }
    return []
  },

  async updateModelConfigs(request: UpdateModelConfigRequest): Promise<void> {
    // Check if transport encryption is enabled
    const config = await CryptoService.fetchCryptoConfig()

    if (!config.transport_encryption) {
      // Transport encryption disabled, send plaintext
      const result = await httpClient.put(`${API_BASE}/models`, request)
      if (!result.success) throw new Error('Failed to update model configs')
      return
    }

    // Fetch RSA public key
    const publicKey = await CryptoService.fetchPublicKey()

    // Initialize crypto service
    await CryptoService.initialize(publicKey)

    // Get user info from localStorage
    const userId = localStorage.getItem('user_id') || ''
    const sessionId = sessionStorage.getItem('session_id') || ''

    // Encrypt sensitive data
    const encryptedPayload = await CryptoService.encryptSensitiveData(
      JSON.stringify(request),
      userId,
      sessionId
    )

    // Send encrypted data
    const result = await httpClient.put(`${API_BASE}/models`, encryptedPayload)
    if (!result.success) throw new Error('Failed to update model configs')
  },

  async getExchangeConfigs(): Promise<Exchange[]> {
    const result = await httpClient.get<Exchange[]>(`${API_BASE}/exchanges`)
    if (!result.success) throw new Error('Failed to fetch exchange configs')
    return result.data!
  },

  async getSupportedExchanges(): Promise<Exchange[]> {
    const result = await httpClient.get<Exchange[]>(
      `${API_BASE}/supported-exchanges`
    )
    if (!result.success) throw new Error('Failed to fetch supported exchanges')
    return result.data!
  },

  async updateExchangeConfigs(
    request: UpdateExchangeConfigRequest
  ): Promise<void> {
    const result = await httpClient.put(`${API_BASE}/exchanges`, request)
    if (!result.success) throw new Error('Failed to update exchange configs')
  },

  async createExchange(request: CreateExchangeRequest): Promise<{ id: string }> {
    const result = await httpClient.post<{ id: string }>(`${API_BASE}/exchanges`, request)
    if (!result.success) throw new Error('Failed to create exchange account')
    return result.data!
  },

  async createExchangeEncrypted(request: CreateExchangeRequest): Promise<{ id: string }> {
    // Check if transport encryption is enabled
    const config = await CryptoService.fetchCryptoConfig()

    if (!config.transport_encryption) {
      // Transport encryption disabled, send plaintext
      const result = await httpClient.post<{ id: string }>(`${API_BASE}/exchanges`, request)
      if (!result.success) throw new Error('Failed to create exchange account')
      return result.data!
    }

    // Fetch RSA public key
    const publicKey = await CryptoService.fetchPublicKey()

    // Initialize crypto service
    await CryptoService.initialize(publicKey)

    // Get user info
    const userId = localStorage.getItem('user_id') || ''
    const sessionId = sessionStorage.getItem('session_id') || ''

    // Encrypt sensitive data
    const encryptedPayload = await CryptoService.encryptSensitiveData(
      JSON.stringify(request),
      userId,
      sessionId
    )

    // Send encrypted data
    const result = await httpClient.post<{ id: string }>(
      `${API_BASE}/exchanges`,
      encryptedPayload
    )
    if (!result.success) throw new Error('Failed to create exchange account')
    return result.data!
  },

  async deleteExchange(exchangeId: string): Promise<void> {
    const result = await httpClient.delete(`${API_BASE}/exchanges/${exchangeId}`)
    if (!result.success) throw new Error('Failed to delete exchange account')
  },

  async updateExchangeConfigsEncrypted(
    request: UpdateExchangeConfigRequest
  ): Promise<void> {
    // Check if transport encryption is enabled
    const config = await CryptoService.fetchCryptoConfig()

    if (!config.transport_encryption) {
      // Transport encryption disabled, send plaintext
      const result = await httpClient.put(`${API_BASE}/exchanges`, request)
      if (!result.success) throw new Error('Failed to update exchange configs')
      return
    }

    // Fetch RSA public key
    const publicKey = await CryptoService.fetchPublicKey()

    // Initialize crypto service
    await CryptoService.initialize(publicKey)

    // Get user info from localStorage
    const userId = localStorage.getItem('user_id') || ''
    const sessionId = sessionStorage.getItem('session_id') || ''

    // Encrypt sensitive data
    const encryptedPayload = await CryptoService.encryptSensitiveData(
      JSON.stringify(request),
      userId,
      sessionId
    )

    // Send encrypted data
    const result = await httpClient.put(
      `${API_BASE}/exchanges`,
      encryptedPayload
    )
    if (!result.success) throw new Error('Failed to update exchange configs')
  },

  async getServerIP(): Promise<{
    public_ip: string
    message: string
  }> {
    const result = await httpClient.get<{
      public_ip: string
      message: string
    }>(`${API_BASE}/server-ip`)
    if (!result.success) throw new Error('Failed to fetch server IP')
    return result.data!
  },

  async updateRealBacktestConfig(
    request: UpdateRealBacktestConfigRequest
  ): Promise<UpdateRealBacktestConfigResponse> {
    const result = await httpClient.put<UpdateRealBacktestConfigResponse>(
      `${API_BASE}/config/real-backtest`,
      request
    )
    if (!result.success || !result.data) {
      throw new Error('Failed to update real backtest config')
    }
    return result.data
  },

  async updateRealBacktestEnabled(enabled: boolean): Promise<boolean> {
    const result = await configApi.updateRealBacktestConfig({ enabled })
    return Boolean(result.real_backtest_enabled)
  },

  async updateAdaptiveMemoryConfig(
    request: UpdateAdaptiveMemoryConfigRequest
  ): Promise<UpdateAdaptiveMemoryConfigResponse> {
    const result = await httpClient.put<UpdateAdaptiveMemoryConfigResponse>(
      `${API_BASE}/config/adaptive-memory`,
      request
    )
    if (!result.success || !result.data) {
      throw new Error('Failed to update adaptive memory config')
    }
    return result.data
  },

  async updateResonanceGuardConfig(
    request: UpdateResonanceGuardConfigRequest
  ): Promise<UpdateResonanceGuardConfigResponse> {
    const result = await httpClient.put<UpdateResonanceGuardConfigResponse>(
      `${API_BASE}/config/resonance-guard`,
      request
    )
    if (!result.success || !result.data) {
      throw new Error('Failed to update resonance guard config')
    }
    return result.data
  },
}
