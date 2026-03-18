import { useEffect, useMemo, useState } from 'react'

const BINANCE_FUTURES_STREAM = 'wss://fstream.binance.com/stream'

function normalizeSymbol(symbol: string): string {
  return symbol.replace(/[^a-zA-Z0-9]/g, '').toUpperCase()
}

export function useBinanceTicker(symbols: string[]) {
  const [realtimePrices, setRealtimePrices] = useState<Record<string, number>>(
    {}
  )

  const normalizedSymbols = useMemo(
    () =>
      Array.from(new Set(symbols.map(normalizeSymbol).filter(Boolean))).sort(),
    [symbols]
  )

  useEffect(() => {
    if (normalizedSymbols.length === 0) {
      setRealtimePrices({})
      return
    }

    let ws: WebSocket | null = null
    let reconnectTimer: number | null = null
    let closedByEffect = false

    const connect = () => {
      const streams = normalizedSymbols
        .map((symbol) => `${symbol.toLowerCase()}@ticker`)
        .join('/')

      ws = new WebSocket(`${BINANCE_FUTURES_STREAM}?streams=${streams}`)

      ws.onmessage = (event) => {
        try {
          const payload = JSON.parse(event.data) as {
            data?: {
              s?: string
              c?: string
              p?: string
            }
          }
          const symbol = payload.data?.s ? normalizeSymbol(payload.data.s) : ''
          const rawPrice = payload.data?.c ?? payload.data?.p
          const nextPrice = rawPrice ? Number(rawPrice) : NaN
          if (!symbol || !Number.isFinite(nextPrice)) return

          setRealtimePrices((prev) => {
            if (prev[symbol] === nextPrice) return prev
            return { ...prev, [symbol]: nextPrice }
          })
        } catch {
          return
        }
      }

      ws.onclose = () => {
        if (closedByEffect) return
        reconnectTimer = window.setTimeout(connect, 2000)
      }

      ws.onerror = () => {
        ws?.close()
      }
    }

    connect()

    return () => {
      closedByEffect = true
      if (reconnectTimer !== null) {
        window.clearTimeout(reconnectTimer)
      }
      ws?.close()
    }
  }, [normalizedSymbols])

  return { realtimePrices }
}
