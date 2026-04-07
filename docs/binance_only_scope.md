# Binance-Only Scope

This phase is intentionally scoped to `Binance USDⓈ-M Futures` in `one_way` mode.

## Why this scope exists

- The position truth model in Phase 1 is designed around a single execution contract: Binance futures positions, orders, fills, and protection orders.
- One-way mode keeps the system-wide position identity simple: one symbol maps to one net direction at a time.
- A single exchange and a single mode keep the capability resolver deterministic, debuggable, and safe to audit before adding more execution behaviors.

## What is supported now

- `exchange = binance_usdm`
- `mode = one_way`
- Read-only capability preview for the current trader
- Strategy capability authorization via `StrategyProfile`
- System-owned position truth via `PositionAggregate`
- Runtime capability clipping via `RuntimeCapabilityResolver`

## What is not supported in Phase 1

- Partial take profit execution
- Add-position execution
- Reduce-position execution
- Moving stop-loss execution
- Trailing stop execution
- Multi-exchange capability abstraction
- Hedge mode execution semantics
- Prompt-level fake exposure of unsupported tools

## Boundary for future expansion

- Additional exchanges can be added only after each exchange has a verified position truth model and order-state contract.
- Hedge mode must use a separate capability model because the meaning of `side`, `available_qty`, and protection state changes.
- Partial TP, add/reduce, and trailing stop should be added only after execution support exists in the order and position reconciliation layer.

## Operational rule

Phase 1 may describe future capabilities in docs and data models, but it must not expose those capabilities to the AI prompt or the execution toolset.
