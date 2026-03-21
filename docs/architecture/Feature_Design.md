# Feature Design: Static-to-Dynamic Indicator Migration

**Status:** Implemented
**Last Updated:** 2026-03-21

## Goal

Remove all hardcoded indicator periods from the market analysis pipeline so backend calculations, prompt rendering, and Strategy Studio configuration stay in sync.

## Problem

The old implementation embedded fixed fields such as:

- `CurrentEMA20`
- `CurrentRSI7`
- `EMA20Values`
- `RSI14Values`
- `ATR14`

This created three mismatches:

1. Frontend could configure periods that backend did not actually calculate.
2. Prompt text could still mention legacy labels such as `EMA20` even when the user selected different periods.
3. Preview prompt and live trading data could diverge.

## New Contract

Indicators are now represented as dynamic maps:

```go
type IndicatorResult struct {
    EMAs  map[int]float64
    RSIs  map[int]float64
    ATRs  map[int]float64
    Bolls map[int]BollResult
    MACD  float64
}
```

Per-timeframe series use:

```go
type IndicatorSeries struct {
    EMAs  map[int][]float64
    RSIs  map[int][]float64
    ATRs  map[int]float64
    Bolls map[int]BollSeries
    MACD  []float64
}
```

## Execution Flow

1. Strategy config enters `kernel/engine_analysis.go`.
2. The full `config.Indicators` object is passed into `market.GetWithTimeframes`.
3. `market/data_klines.go` sanitizes the configured period arrays.
4. Indicator loops dynamically compute only the requested periods.
5. `kernel/engine_prompt.go` formats prompt content by iterating over the configured maps.

## Compatibility Rules

- Non-positive or duplicate periods are ignored safely.
- Empty arrays skip indicator calculation instead of causing panic.
- Grid trading keeps its own explicit indicator set when it needs fixed operational periods.

## Verification Target

For a config with:

```json
{
  "enable_ema": true,
  "ema_periods": [10, 30]
}
```

The generated AI input must contain:

- `current_ema10`
- `current_ema30`
- `EMA10`
- `EMA30`

And must not contain:

- `current_ema20`
- `EMA20`

## Deployment Guard

- `bin/go build` must pass
- `make dev` must pass
- `ss -tlnp | grep -E '8080|3000'` must show both ports
- `curl -I http://localhost:3000` must return `200 OK`

## Restart Sequence Protocol

The development guardian now follows a stricter restart contract for the backend:

1. Wait: inspect recent backend/frontend logs before restart.
2. Kill: stop every existing `nofx` process.
3. Double Check: wait until port `8080` is fully released before launching a new process.
4. Verify PID: after launch, confirm the new PID is still alive after the early bind window.
5. Verify Port: only treat startup as successful when the new process remains alive and `8080` is listening.

This avoids the old race where an old backend instance still held `8080`, the new instance died with `bind: address already in use`, and the script falsely treated the old listener as a successful restart.

## Donchian Box Factor Engine

The Donchian box is now treated as a trend filter factor driven by dynamic strategy periods.

### Calculation Rule

For each configured period `N`:

- `Upper` = highest `High` across the trailing `N` klines
- `Lower` = lowest `Low` across the trailing `N` klines
- `Mid` = `(Upper + Lower) / 2`

This is a pure high-low window model and does not depend on moving averages.

### Prompt Contract

The AI receives both raw box levels and semantic state:

- `Donchian72_Upper`
- `Donchian72_Lower`
- `Donchian72_Mid`
- `Donchian72_State`

State is derived from the current price versus the box:

- Near `Upper`: `Price is testing the Donchian Resistance`
- Above `Upper`: `Breakout detected above the Donchian Resistance`
- Near `Lower`: `Price is testing the Donchian Support`
- Below `Lower`: `Breakdown detected below the Donchian Support`

Configured periods are sanitized dynamically, and periods above `primary_count` are ignored on the frontend before request submission.

## Adaptive Market Data Window

The system now splits indicator warmup from prompt context length.

### Memory Factor

- `MemoryFactor = 2.5`

This is used for recursive indicators so the final reading converges with less than 0.1% drift in normal conditions.

### Warmup Multipliers

- EMA: `ceil(period * 2.5)`
- RSI: `ceil(period * 2.5)`
- ATR: `ceil(period * 2.5)`
- MACD: `ceil(26 * 2.5)` → `65`
- BOLL: `period`
- Donchian: `period`

### Fetch Rule

Let:

- `userCount` = prompt-visible candle count
- `maxWarmup` = largest warmup requirement across enabled indicators

Then:

- if no warmup is needed: `fetchCount = userCount`
- otherwise: `fetchCount = userCount + maxWarmup`

This allows the backend to fetch enough history for convergence while still trimming the prompt window back to the exact user-configured length.
