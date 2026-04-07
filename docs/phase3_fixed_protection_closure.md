# Phase 3 Fixed Protection Closure

This phase adds the smallest usable Binance-only fixed stop-loss / take-profit closure on top of the order truth layer.

## Why this phase exists

Fixed protection must be available before any future partial TP, add-position, move-stop-loss, or trailing work can be added safely.

The system needs one answer to all of these questions first:

- Did the protection group get created?
- Which order belongs to stop loss and which belongs to take profit?
- Which leg filled?
- Which sibling leg still needs to be canceled?
- Is the protection state consistent enough to allow another action?

Without a protection truth layer, later features would have to guess from a mix of order rows, position rows, and prompt text.

## Files changed in this phase

### Protection truth layer

- [`store/protection_group.go`](/root/nofx-src/store/protection_group.go)
- [`store/protection_event_log.go`](/root/nofx-src/store/protection_event_log.go)
- [`store/protection_group_builder.go`](/root/nofx-src/store/protection_group_builder.go)
- [`store/position_aggregate.go`](/root/nofx-src/store/position_aggregate.go)
- [`store/position_aggregate_builder.go`](/root/nofx-src/store/position_aggregate_builder.go)

### Service / coordinator layer

- [`trader/fixed_protection_manager.go`](/root/nofx-src/trader/fixed_protection_manager.go)
- [`trader/protection_state_reconciler.go`](/root/nofx-src/trader/protection_state_reconciler.go)
- [`trader/auto_trader_protection.go`](/root/nofx-src/trader/auto_trader_protection.go)
- [`trader/runtime_capability_resolver.go`](/root/nofx-src/trader/runtime_capability_resolver.go)

### Read-only previews

- [`api/handler_protection_preview.go`](/root/nofx-src/api/handler_protection_preview.go)
- [`web/src/components/trader/ProtectionPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ProtectionPreviewPanel.tsx)
- [`web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx)

### Tests

- [`store/phase3_protection_test.go`](/root/nofx-src/store/phase3_protection_test.go)
- [`trader/phase3_protection_test.go`](/root/nofx-src/trader/phase3_protection_test.go)

### Docs updated by this phase

- [`docs/phase1_position_capability_foundation.md`](/root/nofx-src/docs/phase1_position_capability_foundation.md)
- [`docs/phase2_order_registry_and_reconcile.md`](/root/nofx-src/docs/phase2_order_registry_and_reconcile.md)

## Data contracts

### `ProtectionGroup`

This is the protection truth row, not an exchange order mirror.

- `TraderID`, `Symbol`, `Side`: protection scope.
- `LinkedPositionKey`: system-owned key linking protection state to the current position.
- `ProtectionGroupID`: system-generated correlation id for the fixed protection pair.
- `ProtectionMode`: current phase only allows `fixed`.
- `StopLossOrderIntentID`, `TakeProfitOrderIntentID`: local intent ids for the two legs.
- `StopLossExchangeOrderID`, `TakeProfitExchangeOrderID`: exchange ids when available.
- `StopLossTriggerPrice`, `TakeProfitTriggerPrice`: system-derived fixed protection prices.
- `ProtectedQuantity`: system-derived protected size.
- `Status`: `pending_attach`, `armed`, `partial_invalid`, `closed`, or `cancel_pending`.
- `Source`: truth source label.
- `LastSyncedAt`: latest sync timestamp.

### `ProtectionEventLog`

This is the append-only evidence chain for protection state changes.

- It records protection group creation.
- It records leg attachment and leg completion.
- It records sibling cancel requests.
- It records group close / invalid transitions.

### `PositionAggregate` protection fields

These fields are derived state, not direct exchange mirrors:

- `HasProtection`
- `ProtectionMode`
- `ProtectedQuantity`
- `ProtectionGroupID`
- `StopLossArmed`
- `TakeProfitArmed`

They are used for preview and runtime clipping only.

### `ProtectionReconcilePreview`

This is the read-only API/UI preview of the protection truth layer.

- It exposes the current protection group snapshot.
- It exposes the recent protection evidence chain.
- It exposes mismatch reasons and consistency state.
- It does not change execution behavior.

## State flow

### 1. Entry fill is confirmed

`trader/fixed_protection_manager.go` builds a fixed protection plan from:

- the current position aggregate
- the current strategy profile
- the latest decision record that carries fixed protection prices

This is the only place that computes the fixed protection prices for phase 3.

### 2. Protection group is created

The fixed protection manager writes a protection group row with status `pending_attach` and appends a protection event log row.

### 3. Stop loss and take profit orders are attached

The manager submits the two Binance algo orders, then writes both order rows into the order registry truth layer.

At this point the protection group moves to `armed`.

### 4. One leg fills

`trader/protection_state_reconciler.go` detects the filled leg, requests sibling cancellation, updates the sibling order row to `PENDING_CANCEL`, and moves the protection group to `cancel_pending` or `partial_invalid` when needed.

### 5. Position aggregate refreshes

`store/position_aggregate_builder.go` reads the protection group summary and the order registry summary to refresh:

- `HasProtection`
- `ProtectedQuantity`
- `ProtectionGroupID`
- `StopLossArmed`
- `TakeProfitArmed`
- pending order coverage

### 6. Runtime capability clipping updates

`trader/runtime_capability_resolver.go` uses the protection consistency state to decide whether `set_protection` is allowed.

It does not expose partial TP, add-position, move-stop-loss, or trailing actions in this phase.

## What is reused later

The next phases can reuse these interfaces without changing the underlying truth model again:

- `store.ProtectionGroupBuilder.ApplyProtectionEvent(...)`
- `store.OrderRegistryBuilder.ApplyOrderEvent(...)`
- `store.ProtectionGroupSummary`
- `store.OrderRegistrySummary`
- `trader.ProtectionReconcilePreview`
- `trader.RuntimeCapabilityRequest`
- `trader.RuntimeCapabilityPreview`

## Phase 4 dependency note

Phase 4 reuses the fixed protection truth layer and only changes the protected quantity when the remaining position changes after a partial reduce.

That means:

- protection groups still stay in the same truth table
- protection order rows still stay in the same order registry
- the rebalance step only updates quantity, not stop-loss or take-profit prices
- the protection truth layer becomes quantity-aware for partial reduce flows

This is the reason Phase 3 had to exist before Phase 4: the system needs one stable protection identity before it can safely rebalance that protection around a smaller remaining position.

## Phase 5 dependency note

Phase 5 reuses the same protection truth layer again, but this time the protected quantity must move up after a same-symbol add-position fill.

That means:

- the protection group identity stays the same
- the stop-loss and take-profit prices stay fixed
- only `ProtectedQuantity` is rebased to the new total position size
- `RuntimeCapabilityResolver` must continue to use protection consistency as a blocking signal when the protection truth layer is out of sync

The protection truth layer therefore remains the same model across Phase 3, Phase 4, and Phase 5. Only the quantity it covers changes.

## Phase 6 dependency note

Phase 6 upgrades the fixed protection truth row into the base of the movable protection model.

That means:

- the same protection group identity is reused
- the order registry still owns the working stop-loss and take-profit truth rows
- the protection adjustment builder appends every move to an evidence log
- the cancel/replace reconciler becomes the only place that changes the current stop-loss price
- the initial stop-loss price remains available so later break-even and trailing logic can explain how far the stop moved

Phase 6 does not create a second protection model. It extends the same protection group so future break-even and trailing states can be derived without inventing a new truth table.

## Known limits

- Binance USDⓈ-M Futures one-way mode only.
- Fixed protection only.
- No partial take-profit.
- No partial reduce.
- No add-position.
- No move-stop-loss.
- No trailing protection.
- No prompt-level exposure of future tools.
- The protection preview is read-only; it exists for verification and debugging.
