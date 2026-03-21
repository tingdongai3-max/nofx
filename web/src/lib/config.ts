export interface SystemConfig {
  initialized: boolean
  beta_mode?: boolean
}

export const BACKEND_SERVICE_OFFLINE = 'Backend Service Offline'

let configPromise: Promise<SystemConfig> | null = null
let cachedConfig: SystemConfig | null = null

async function parseJSONResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    if (res.status >= 500) {
      throw new Error(BACKEND_SERVICE_OFFLINE)
    }
    throw new Error(`Request failed with status ${res.status}`)
  }

  const contentType = res.headers.get('content-type') || ''
  if (!contentType.includes('application/json')) {
    throw new Error(BACKEND_SERVICE_OFFLINE)
  }

  const body = await res.text()
  if (!body.trim()) {
    throw new Error(BACKEND_SERVICE_OFFLINE)
  }

  try {
    return JSON.parse(body) as T
  } catch {
    throw new Error(BACKEND_SERVICE_OFFLINE)
  }
}

export function getSystemConfig(): Promise<SystemConfig> {
  if (cachedConfig) {
    return Promise.resolve(cachedConfig)
  }
  if (configPromise) {
    return configPromise
  }
  configPromise = fetch('/api/config')
    .then((res) => parseJSONResponse<SystemConfig>(res))
    .then((data: SystemConfig) => {
      cachedConfig = data
      return data
    })
    .catch((error) => {
      configPromise = null
      throw error
    })
  return configPromise
}

/** Call after first-time setup completes so next check reflects initialized=true */
export function invalidateSystemConfig() {
  cachedConfig = null
  configPromise = null
}
