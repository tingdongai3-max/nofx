# Phase 5 Same-Symbol Add-Position

This phase adds the minimum Binance-only same-symbol add-position closed loop on top of the order, protection, scale-out, and runtime truth layers.

The goal is not to make the system "better at predicting price". The goal is to let the system answer, after an add-position fill:

- how much position is now left
- what the new average entry price is
- what protection is still attached
- how much quantity is still reserved
- whether add-position can stay open for the current cycle

## Why this phase comes before move-stop-loss or trailing work

- Add-position is the first feature that changes the live cost basis of an already open position.
- Once cost basis changes, `PositionAggregate`, `OrderRegistry`, and `ProtectionGroup` all need to agree on the new total size and coverage quantity.
- Move-stop-loss and trailing logic should reuse that truth later, but they should not be added before the add-position truth path is stable.

## File layout

### Add-position truth layer

- [`store/scale_in_plan.go`](/root/nofx-src/store/scale_in_plan.go)
- [`store/scale_in_plan_level.go`](/root/nofx-src/store/scale_in_plan_level.go)
- [`store/scale_in_event_log.go`](/root/nofx-src/store/scale_in_event_log.go)
- [`store/position_aggregate.go`](/root/nofx-src/store/position_aggregate.go)
- [`store/position_aggregate_builder.go`](/root/nofx-src/store/position_aggregate_builder.go)

### Add-position managers and reconcilers

- [`trader/scale_in_manager.go`](/root/nofx-src/trader/scale_in_manager.go)
- [`trader/scale_in_state_reconciler.go`](/root/nofx-src/trader/scale_in_state_reconciler.go)
- [`trader/scale_in_risk_guard.go`](/root/nofx-src/trader/scale_in_risk_guard.go)
- [`trader/protection_rebalance_manager.go`](/root/nofx-src/trader/protection_rebalance_manager.go)
- [`trader/auto_trader_scale_in.go`](/root/nofx-src/trader/auto_trader_scale_in.go)

### Read-only previews

- [`api/handler_scale_in_preview.go`](/root/nofx-src/api/handler_scale_in_preview.go)
- [`web/src/components/trader/ScaleInPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ScaleInPreviewPanel.tsx)
- [`web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx)
- [`web/src/components/trader/ProtectionPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ProtectionPreviewPanel.tsx)
- [`web/src/components/trader/ScaleOutPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ScaleOutPreviewPanel.tsx)

## Data contracts

### `ScaleInPlan`

This is the plan-layer truth row.

- `TraderID`, `Symbol`, `Side`, `LinkedPositionKey`: plan scope.
- `ScaleInPlanID`: system-generated plan identifier.
- `PlanMode`: current phase only allows `fixed_qty` or `fixed_ratio`.
- `Status`: `draft`, `armed`, `partially_filled`, `completed`, or `invalid`.
- `TotalPlannedQty`, `RemainingPlannedQty`, `ExecutedQty`: system-derived plan totals.
- `MaxScaleInCount`, `CurrentScaleInCount`: strategy limit and derived executed add count.
- `Source`: truth source label.
- `CreatedAt` / `UpdatedAt`: persistence timestamps.

### `ScaleInPlanLevel`

This is the level-layer truth row.

- `LevelIndex`: order within the plan.
- `TargetType`: current phase only allows `market` or `limit_price`.
- `TargetPrice`: target price for limit levels.
- `PlannedQty`, `ExecutedQty`, `RemainingQty`: level quantities.
- `LinkedOrderIntentID`, `LinkedExchangeOrderID`: correlation keys to the order registry.
- `Status`: level truth status.

### `ScaleInEventLog`

This is the append-only evidence chain.

- It records plan creation.
- It records level attachment.
- It records fills, cancellations, completion, and invalidation.
- It exists so the system can explain why the add-position plan changed state.

### `PositionAggregate`

The position truth layer now reads scale-in state directly.

- `HasScaleInPlan`
- `ScaleInPlanID`
- `ScaleInStatus`
- `PendingAddQty`
- `ExecutedScaleInQty`
- `RemainingScaleInQty`
- `ScaleInCount`
- `AvgEntryPrice`
- `TotalNotional`

These fields are derived state, not exchange raw fields.

### `OrderRegistry` and `ProtectionGroup`

- Add-position orders are written into `OrderRegistry` with `OrderRole = entry`.
- `PendingAddQty` is the reserved quantity for working entry orders and active scale-in levels.
- After add-position fills, `ProtectedQuantity` must be rebased to the new total position size.
- Stop-loss and take-profit prices are not moved in this phase.

## Quantity semantics

The following quantities are related, but they are not the same thing.

- `pending_add_qty`: order-layer reserve for working entry orders.
- `pending_scale_in_qty`: scale-in plan reserve for working levels inside the current active plan.
- `remaining_scale_in_qty`: plan-layer backlog across the whole active plan, including working and not-yet-working levels.
- `available_qty`: live position quantity after subtracting all reserved reduce or protection quantities.
- `protected_quantity`: protection-layer coverage target for the live remaining position.

The practical relationships are:

- `pending_scale_in_qty <= remaining_scale_in_qty`
- `pending_add_qty` must come from the order truth layer and the scale-in truth layer, not from repeated local math
- `protected_quantity` must match the current live position after confirmed fills
- `AvgEntryPrice` is the only trusted cost basis after add-position fills

For Phase 6 work, treat `pending_add_qty` as reserved entry exposure, not free capital. Treat `protected_quantity` as coverage, not a second reservation counter.

## State flow

### 1. Add-position plan is created

`trader/scale_in_manager.go` builds a fixed-quantity or fixed-ratio plan, creates one level per planned slice, and writes both the plan and level rows to the store.

### 2. Add orders are attached

Each level generates a Binance entry order and a matching order registry row.

### 3. Partial fill is observed

`trader/scale_in_state_reconciler.go` reads the registry truth and advances the filled level and the plan state.

### 4. Average entry price is rebuilt

`store/position_builder.go` and `store/position_aggregate_builder.go` keep the weighted entry price consistent after add fills.

### 5. Protection is rebased

`trader/protection_rebalance_manager.go` updates `ProtectedQuantity` to match the new total position size.

Only quantity is changed here. Stop-loss and take-profit trigger prices stay fixed in this phase.

### 6. Runtime capability clipping updates

`trader/runtime_capability_resolver.go` can now open or block:

- `add_position`

Those actions remain conditional. They are still blocked when the order truth layer, protection truth layer, scale-out truth layer, risk guard, or user-stream state is inconsistent.

## Add-position risk guard rules

- Require an existing open position on the same symbol and side.
- Require Binance USDⓈ-M Futures one-way mode.
- Require user-stream readiness and order-placement readiness.
- Block when an active scale-out plan exists.
- Block when another add-position plan is already active.
- Block when the current order truth layer, protection truth layer, or scale-out truth layer is inconsistent.
- Enforce `MaxScaleInCount` and `MaxPositionRiskPct`.

## Conflict rules with Phase 4 scale-out

- A live scale-out plan blocks add-position by default.
- The reason is simple: the system must not let two size-changing plans fight over the same remaining quantity at the same time.
- If a trader wants to scale in after a scale-out, the scale-out plan must reach a terminal state first.
- This rule keeps `PendingReduceQty`, `PendingScaleOutQty`, `PendingAddQty`, and `ProtectedQuantity` from drifting independently.

## What remains unsupported

- `move_stop_loss`
- `set_trailing_protection`
- hedge mode
- multi-exchange abstraction
- prompt-level fake exposure of future tools
- interactive plan editing in the frontend

## What Phase 6 can reuse

- `store.ScaleInPlan`
- `store.ScaleInPlanLevel`
- `store.ScaleInEventLog`
- `store.OrderRegistry`
- `store.ProtectionGroup`
- `store.PositionAggregate`
- `trader.ScaleInRiskGuard`
- `trader.ScaleInStateReconciler`
- `trader.ProtectionRebalanceManager`
- `trader.RuntimeCapabilityResolver`
- `api/runtime/scale-in/preview`
- `web/src/components/trader/ScaleInPreviewPanel.tsx`

The later phases should extend this truth model instead of inventing a second one.

## Phase 6 dependency note

Phase 6 reuses the same add-position truth layer to decide whether dynamic protection may move.

That means:

- if a scale-in plan is still active, protection moves are blocked by default
- the current average entry price from Phase 5 is the break-even reference for later protection moves
- the add-position truth layer remains the source of truth for cost basis, while Phase 6 only reads it
- later dynamic-protection code must not recompute average entry price independently

This keeps add-position, reduce-position, and protection-move state from drifting apart while a position is still changing size.

## Phase 7 validation note

Phase 7 does not add a new add-position rule. It proves that the phase-5 truth model is restart-safe and replay-safe.

The recoverable states that Phase 7 checks are:

- `AvgEntryPrice`
- `TotalNotional`
- `PendingAddQty`
- `RemainingScaleInQty`
- `ExecutedScaleInQty`
- `ScaleInCount`
- protection coverage after a same-symbol add fill

The high-risk conflict cases include:

- active scale-in plus dynamic protection adjustment
- active scale-out plus attempted scale-in
- restore after scale-in plan progress
- migration of older SQLite files into the current scale-in schema

Phase 7 treats the phase-5 truth layer as validation input, not as a place to invent a second average-price calculation path. The same aggregate fields must rebuild to the same answer after replay and restore.
