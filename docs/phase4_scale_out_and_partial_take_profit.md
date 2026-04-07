# Phase 4 Scale-Out and Partial Take-Profit

This phase adds the minimum Binance-only partial reduce / batch take-profit closed loop on top of the phase 1-3 truth layers.

The goal is not to make the system "smarter" or more profitable. The goal is to let the system answer, after a partial reduction:

- how much position is still left
- what protection is still attached
- how much quantity is still reserved
- which actions are still safe to expose

## Why this phase comes before add-position or trailing work

- Partial reduce is the first feature that forces the system to keep `PositionAggregate`, `OrderRegistry`, and `ProtectionGroup` in sync after one leg has already changed size.
- Add-position would introduce a second kind of size change and a second reservation path before the partial-reduce truth path is proven.
- Trailing stop-loss would depend on the same quantity truth, but it should reuse the same truth model only after the reduce path is stable.

## File layout

### Scale-out truth layer

- [`store/scale_out_plan.go`](/root/nofx-src/store/scale_out_plan.go)
- [`store/scale_out_plan_level.go`](/root/nofx-src/store/scale_out_plan_level.go)
- [`store/scale_out_event_log.go`](/root/nofx-src/store/scale_out_event_log.go)
- [`store/position_aggregate.go`](/root/nofx-src/store/position_aggregate.go)
- [`store/position_aggregate_builder.go`](/root/nofx-src/store/position_aggregate_builder.go)

### Scale-out managers and reconcilers

- [`trader/scale_out_manager.go`](/root/nofx-src/trader/scale_out_manager.go)
- [`trader/scale_out_state_reconciler.go`](/root/nofx-src/trader/scale_out_state_reconciler.go)
- [`trader/protection_rebalance_manager.go`](/root/nofx-src/trader/protection_rebalance_manager.go)
- [`trader/auto_trader_scale_out.go`](/root/nofx-src/trader/auto_trader_scale_out.go)

### Read-only previews

- [`api/handler_scale_out_preview.go`](/root/nofx-src/api/handler_scale_out_preview.go)
- [`web/src/components/trader/ScaleOutPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ScaleOutPreviewPanel.tsx)
- [`web/src/components/trader/OrderRegistryPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/OrderRegistryPreviewPanel.tsx)
- [`web/src/components/trader/ProtectionPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ProtectionPreviewPanel.tsx)
- [`web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx)

## Data contracts

### `ScaleOutPlan`

This is the plan-layer truth row.

- `TraderID`, `Symbol`, `Side`, `LinkedPositionKey`: plan scope.
- `ScaleOutPlanID`: system-generated plan identifier.
- `PlanMode`: current phase only allows `fixed_ratio`.
- `Status`: `draft`, `armed`, `partially_filled`, `completed`, or `invalid`.
- `TotalPlannedQty`, `RemainingPlannedQty`, `ExecutedQty`: system-derived plan totals.
- `Source`: truth source label.
- `CreatedAt` / `UpdatedAt`: persistence timestamps.

### `ScaleOutPlanLevel`

This is the level-layer truth row.

- `LevelIndex`: order within the plan.
- `TargetType`: current phase only allows `limit_price` or `reduce_market`.
- `TargetPrice`: target price for that level.
- `PlannedQty`, `ExecutedQty`, `RemainingQty`: level quantities.
- `LinkedOrderIntentID`, `LinkedExchangeOrderID`: correlation keys to the order registry.
- `Status`: level truth status.

### `ScaleOutEventLog`

This is the append-only evidence chain.

- It records plan creation.
- It records level attachment.
- It records fills, cancellations, completion, and invalidation.
- It exists so the system can explain why a partial-reduce plan changed state.

### `PositionAggregate`

The position truth layer now reads scale-out state directly.

- `HasScaleOutPlan`
- `ScaleOutPlanID`
- `ScaleOutStatus`
- `PendingScaleOutQty`
- `ExecutedScaleOutQty`
- `RemainingScaleOutQty`

These fields are derived state, not exchange raw fields.

### `OrderRegistry` and `ProtectionGroup`

- Reduce orders from the scale-out plan are written into `OrderRegistry` with `OrderRole = reduce`.
- Fixed protection quantities are rebalanced against the remaining position after partial fills.
- Protection prices are not moved in this phase; only the protected quantity changes.

## Quantity semantics

The following quantities are related, but they are not the same thing. Phase 5 must keep them separated.

- `pending_reduce_qty`: order-layer reduce reserve. This is the quantity currently occupied by working reduce-only rows on the symbol, including scale-out reduce legs and fixed protection rows. It is not the remaining position size.
- `pending_add_qty`: entry-layer reserve. It is separate from the reduce/protection quantities and must not be netted against them when phase 5 add-position work starts.
- `pending_scale_out_qty`: scale-out plan reserve. This is the quantity currently occupied by working levels inside the active scale-out plan. It is a subset of the active plan, not the whole plan.
- `remaining_scale_out_qty`: plan-layer outstanding quantity. This is the total quantity still pending across the whole scale-out plan, including working and not-yet-working levels.
- `protected_quantity`: protection-layer coverage target. This is the quantity the fixed protection group is meant to cover on the live remaining position. It is not an order reservation metric.

The practical relationships are:

- `pending_scale_out_qty <= remaining_scale_out_qty`
- `pending_reduce_qty` can be greater than `pending_scale_out_qty` because it also includes fixed protection rows
- `protected_quantity` should track the live remaining position after confirmed fills, while the order registry keeps the reservation truth
- `available_qty` is derived centrally from the truth layers and must not be recomputed independently inside handlers or UI previews

For phase 5 add-position work, treat `pending_reduce_qty` as reserved reduce exposure, not free sellable size. Treat `remaining_scale_out_qty` as plan backlog, not already-working exposure. Treat `protected_quantity` as coverage, not a second order reservation counter.

Phase 5 also adds a conflict rule: when a live scale-out plan exists, add-position is blocked by default. That keeps `PendingReduceQty`, `PendingScaleOutQty`, `PendingAddQty`, and `ProtectedQuantity` from drifting independently while two size-changing plans are active.

## State flow

### 1. Scale-out plan is created

`trader/scale_out_manager.go` builds a fixed-ratio plan, creates one level per planned slice, and writes both the plan and level rows to the store.

### 2. Reduce orders are attached

Each level generates a reduce-only Binance order and a matching order registry row.

### 3. Partial fill is observed

`trader/scale_out_state_reconciler.go` reads the registry truth and advances the filled level and the plan state.

### 4. Remaining quantity is rebuilt

`store/position_aggregate_builder.go` now subtracts the reserved scale-out quantity from the available quantity so the system does not over-expose the remaining position.

### 5. Protection is rebalanced

`trader/protection_rebalance_manager.go` updates `ProtectedQuantity` to match the remaining position size.

Only quantity is changed here. Stop-loss and take-profit trigger prices stay fixed in this phase.

### 6. Runtime capability clipping updates

`trader/runtime_capability_resolver.go` can now open or block:

- `reduce_position`
- `arm_partial_take_profit`

Those actions remain conditional. They are still blocked when the order truth layer, protection truth layer, or user-stream state is inconsistent.

## Protection rebalance rules

- Recompute the protected quantity from the remaining position truth.
- Keep the existing fixed protection prices.
- Do not create a second protection model.
- Do not duplicate reserved quantity math in multiple handlers.
- Treat the rebalance as a quantity-only update that preserves the current protection group identity.

## What remains unsupported

- `add_position`
- `move_stop_loss`
- `set_trailing_protection`
- hedge mode
- multi-exchange abstraction
- prompt-level fake exposure of future tools
- interactive plan editing in the frontend

## What Phase 5 and Phase 6 can reuse

- `store.ScaleOutPlan`
- `store.ScaleOutPlanLevel`
- `store.ScaleOutEventLog`
- `store.OrderRegistry`
- `store.ProtectionGroup`
- `store.PositionAggregate`
- `trader.ScaleOutStateReconciler`
- `trader.ProtectionRebalanceManager`
- `trader.RuntimeCapabilityResolver`
- `api/runtime/scale-out/preview`
- `web/src/components/trader/ScaleOutPreviewPanel.tsx`

The later phases should extend this truth model instead of inventing a second one.

## Phase 6 dependency note

Phase 6 reuses the same scale-out truth layer to protect the movable protection flow from conflicting size changes.

That means:

- if a scale-out plan is still active, protection moves are blocked by default
- the current stop-loss can move only when the order truth layer and protection truth layer are clean
- the scale-out truth layer still owns the remaining reduce quantity and the protection layer only reuses it as a conflict signal
- later dynamic-protection code must not recompute remaining reduce quantity independently

This keeps `PendingReduceQty`, `PendingScaleOutQty`, `RemainingScaleOutQty`, and protection coverage from drifting apart while dynamic protection is running.

## Phase 5 dependency note

Phase 5 reuses the same aggregate and order truth layers to introduce same-symbol add-position. That means Phase 4 data remains the source of truth for:

- remaining reduce quantity
- protection coverage quantity
- whether an active scale-out plan blocks add-position

The scale-out truth model does not become a second add-position model. It stays the reduce-side truth layer and is only used as a blocking / consistency signal when add-position is active.

## Phase 7 validation note

Phase 7 does not extend scale-out behavior. It validates that the phase-4 reduce truth survives dirty runtime conditions.

The high-risk validation targets are:

- replayed user-stream fills against an already active scale-out plan
- restart rebuild of `ScaleOutPlan`, `ScaleOutPlanLevel`, and `PositionAggregate`
- conflict cases where scale-out and scale-in overlap
- migration regression from older SQLite files into the current truth-layer schema

The states that must remain recoverable are:

- `ScaleOutStatus`
- `PendingScaleOutQty`
- `RemainingScaleOutQty`
- `ExecutedScaleOutQty`
- protection coverage after partial reduce

Phase 7 also makes scale-out previews carry validation lineage metadata so the operator can see whether the output came from live runtime, replay validation, restore validation, or migration regression.
