# Phase 1 Position Capability Foundation

This phase builds the minimum trusted base for later execution features without enabling them yet.

## Why we build this first

- `PositionAggregate` gives the system one authoritative view of symbol-level position truth.
- `StrategyProfile` separates permission from execution, so capability flags are stored explicitly instead of being buried inside a prompt.
- `RuntimeCapabilityResolver` turns strategy authorization plus runtime state into the exact action set the AI may use in the current cycle.

This prevents the prompt from pretending that unsupported execution tools already exist.

## PositionAggregate field model

`PositionAggregate` is the system-owned truth record, not a raw exchange mirror.

- `TraderID`: system-owned trader key.
- `Symbol`: exchange-normalized symbol.
- `Side`: normalized one-way side, `LONG` or `SHORT`.
- `TotalQty`: total open quantity owned by the system.
- `AvailableQty`: quantity still available for immediate reduce or close.
- `PendingAddQty`: quantity reserved by pending add/open orders.
- `PendingReduceQty`: quantity reserved by pending reduce or protection orders.
- `AvgEntryPrice`: weighted entry price derived from position and fill data.
- `RealizedPnL`: realized profit and loss derived by the system.
- `UnrealizedPnL`: unrealized profit and loss from live snapshots or best-effort reconciliation.
- `PeakPnLPct`: peak PnL percent used by future trailing logic and drawdown logic.
- `ProtectionStateJSON`: system-generated JSON describing protection status.
- `ScalePlanStateJSON`: system-generated JSON describing scale-in or reduction planning state.
- `ExecutionEligible`: runtime eligibility flag used by the resolver, not a direct execution permit.
- `LastReconciledAt`: timestamp of the last truth rebuild.
- `CreatedAt` / `UpdatedAt`: persistence timestamps.

## StrategyProfile field model

`StrategyProfile` is the strategy-layer authorization model.

- `TraderID`: trader identity that owns the profile.
- `Exchange`: fixed to `binance_usdm` in Phase 1.
- `Mode`: fixed to `one_way` in Phase 1.
- `AllowAddPosition`: strategy authorization only.
- `AllowPartialTakeProfit`: strategy authorization only.
- `AllowMoveStopLoss`: strategy authorization only.
- `AllowTrailingStop`: strategy authorization only.
- `ProtectionMode`: strategy-level protection policy label, such as `fixed` or `managed`.
- `MaxScaleInCount`: upper bound for scale-in attempts.
- `MaxPositionRiskPct`: strategy risk budget.
- `DecisionStyle`: readable strategy style label.
- `ExecutionEnabled`: global execution authorization gate.
- `CreatedAt` / `UpdatedAt`: persistence timestamps.

## RuntimeCapabilityResolver input and output

### Inputs

- `StrategyProfile`
- `PositionAggregate`
- execution mode: `live`, `sim`, or `readonly`
- whether the current cycle allows order placement
- whether the selected symbol has pending orders
- whether the selected symbol already has protection orders
- whether the trader is in a legacy or audit-only state

### Outputs

- `AllowedActions`
- `BlockedActions`
- `BlockReasons`

The resolver is intentionally transparent. It must explain why an action was blocked instead of returning a black-box boolean.

## Why we do not open add / reduce / trailing in Phase 1

- Those actions are not fully implemented in the execution layer yet.
- Exposing them early would create a contract mismatch between the prompt, the UI, and the engine.
- Phase 1 must not let the AI believe it has tools that cannot actually execute.

## How this phase supports future work

- Partial TP can use `AvailableQty` and `PendingReduceQty`.
- Add position can use `PendingAddQty` and `MaxScaleInCount`.
- Moving stop-loss and trailing stop can use `PeakPnLPct` and `ProtectionStateJSON`.
- The resolver can later open or close specific tool names without changing the position truth model again.

## Phase 2 dependency note

After Phase 2, `PositionAggregate` no longer depends only on positions and decision records. It also consumes the order truth layer built from:

- `store/order_registry.go`
- `store/order_event_log.go`
- `store/order_registry_builder.go`
- `trader/order_state_reconciler.go`

From that point on, `RuntimeCapabilityResolver` also considers order consistency signals such as working orders, pending cancel-replace flows, and user-stream readiness before opening actions.

## Phase 3 dependency note

After Phase 3, `PositionAggregate` also reads the fixed protection truth layer through:

- `store/protection_group.go`
- `store/protection_event_log.go`
- `store/protection_group_builder.go`

That protection state feeds:

- protection coverage fields on `PositionAggregate`
- `set_protection` gating in `RuntimeCapabilityResolver`
- the read-only protection preview panel

The same phase also makes the order truth layer responsible for fixed stop-loss and take-profit legs, but it still does not expose partial TP, add-position, move-stop-loss, or trailing actions.

## Phase 4 dependency note

After Phase 4, `PositionAggregate` also reads the scale-out truth layer through:

- `store/scale_out_plan.go`
- `store/scale_out_plan_level.go`
- `store/scale_out_event_log.go`
- `trader/scale_out_state_reconciler.go`

That adds the first true partial-reduce model:

- the system can see remaining planned quantity
- the system can see reserved reduce quantity
- the system can rebalance protection quantity after a partial fill

`RuntimeCapabilityResolver` then starts opening `reduce_position` and `arm_partial_take_profit` only when the scale-out truth layer is consistent and the rest of the runtime gates are satisfied.

Phase 4 still does not expose add-position, move-stop-loss, or trailing actions.

## Phase 5 dependency note

After Phase 5, `PositionAggregate` also reads the scale-in truth layer through:

- `store/scale_in_plan.go`
- `store/scale_in_plan_level.go`
- `store/scale_in_event_log.go`
- `trader/scale_in_state_reconciler.go`
- `trader/scale_in_risk_guard.go`

That adds the first same-symbol add-position model:

- the system can see remaining add quantity
- the system can see reserved add quantity
- the system can see the current average entry price after add fills
- the system can see whether a live scale-out plan blocks add-position

`RuntimeCapabilityResolver` then starts opening `add_position` only when the scale-in truth layer is consistent, the risk guard allows it, and the rest of the runtime gates are satisfied.

Phase 5 still does not expose move-stop-loss or trailing actions.

## Phase 6 dependency note

After Phase 6, `PositionAggregate` also reads the dynamic protection truth layer through:

- `store/trailing_rule.go`
- `store/protection_adjustment_event_log.go`
- `store/protection_adjustment_builder.go`
- `trader/protection_cancel_replace_reconciler.go`

That adds the movable protection model:

- the system can see the initial stop-loss price
- the system can see the current stop-loss price
- the system can see whether break-even or segmented trailing is armed
- the system can see the current protection revision and move count

`RuntimeCapabilityResolver` then starts opening `move_stop_loss`, `set_break_even_stop`, and `set_trailing_protection` only when the protection truth layer, cancel/replace lifecycle, order truth layer, scale-in/scale-out conflicts, and runtime gates are all clean.

## Acceptance rule

If a future feature cannot be derived from `PositionAggregate`, `StrategyProfile`, and runtime state, it should not be exposed in the first phase.
