# Phase 6 Protection Adjustment and Trailing

This phase adds the minimum Binance-only dynamic protection closure on top of the fixed protection truth layer.

The goal is not to make the system more adaptive in an abstract sense. The goal is to let the system answer, after protection moves:

- what the initial stop was
- what the current stop is
- which protection revision is active
- whether break-even or segmented trailing is armed
- whether a cancel/replace is still in progress
- whether the current cycle is allowed to move protection again

## Why this phase comes before advanced continuous trailing

- Break-even is the first protection mutation that must reuse the same stop-loss identity after the initial fixed protection is already live.
- Segmented trailing is the smallest safe step after break-even because it still uses a bounded move rule and a bounded move budget.
- Continuous or adaptive trailing would require a richer move policy, more frequent evaluation, and more exchange-specific behavior. That would be a second feature, not this phase.

## Files changed in this phase

### Dynamic protection truth layer

- [`store/protection_group.go`](/root/nofx-src/store/protection_group.go)
- [`store/trailing_rule.go`](/root/nofx-src/store/trailing_rule.go)
- [`store/protection_adjustment_event_log.go`](/root/nofx-src/store/protection_adjustment_event_log.go)
- [`store/protection_adjustment_builder.go`](/root/nofx-src/store/protection_adjustment_builder.go)
- [`store/position_aggregate.go`](/root/nofx-src/store/position_aggregate.go)
- [`store/position_aggregate_builder.go`](/root/nofx-src/store/position_aggregate_builder.go)

### Dynamic protection managers and reconcilers

- [`trader/protection_adjustment_manager.go`](/root/nofx-src/trader/protection_adjustment_manager.go)
- [`trader/protection_cancel_replace_reconciler.go`](/root/nofx-src/trader/protection_cancel_replace_reconciler.go)
- [`trader/protection_adjustment_guard.go`](/root/nofx-src/trader/protection_adjustment_guard.go)
- [`trader/auto_trader_protection_adjustment.go`](/root/nofx-src/trader/auto_trader_protection_adjustment.go)

### Read-only previews and runtime clipping

- [`api/handler_protection_adjustment_preview.go`](/root/nofx-src/api/handler_protection_adjustment_preview.go)
- [`trader/protection_adjustment_preview.go`](/root/nofx-src/trader/protection_adjustment_preview.go)
- [`trader/runtime_capability_resolver.go`](/root/nofx-src/trader/runtime_capability_resolver.go)
- [`web/src/components/trader/ProtectionAdjustmentPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ProtectionAdjustmentPreviewPanel.tsx)
- [`web/src/components/trader/ProtectionPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ProtectionPreviewPanel.tsx)
- [`web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx)

## Data contracts

### `ProtectionGroup`

This is the protection adjustment truth row, not an exchange raw order mirror.

- `ProtectionMode`: current policy label. Phase 6 allows `fixed`, `break_even`, and `trailing_segmented`.
- `StopLossInitialTriggerPrice`: initial stop-loss trigger before the first protection move.
- `StopLossCurrentTriggerPrice`: current stop-loss trigger after the latest move.
- `TakeProfitInitialTriggerPrice`: initial take-profit trigger before any move.
- `TakeProfitCurrentTriggerPrice`: current take-profit trigger after the latest move.
- `BreakEvenArmed`: whether the protection has reached break-even.
- `TrailingArmed`: whether segmented trailing is active.
- `TrailingRuleID`: trailing rule truth key.
- `TrailingAnchorPrice`: anchor price used by the latest trailing move.
- `TrailingLastMoveAt`: timestamp of the latest move.
- `TrailingMoveCount`: number of completed moves.
- `ProtectionRevision`: system-owned revision counter for the protection truth row.
- `LastProtectionAction`: last protection mutation label.
- `LastProtectionActionAt`: timestamp of the last protection mutation.

### `TrailingRule`

This is the rule-layer truth row that controls when protection can move.

- `RuleMode`: `break_even` or `step_trailing`.
- `ActivationType`: `profit_r_multiple`, `profit_pct`, or `price_level`.
- `ActivationValue`: threshold for arming break-even or trailing.
- `StepTriggerType`: trigger type for the next move.
- `StepTriggerValue`: threshold for the next move.
- `StepMoveType`: how the new stop price is calculated.
- `StepMoveValue`: move distance or target level.
- `MaxMoveCount`: bounded budget for moves.
- `Status`: rule lifecycle state.

### `ProtectionAdjustmentEventLog`

This is the append-only evidence chain for dynamic protection changes.

- It records break-even arming.
- It records stop-loss move requests.
- It records cancel/replace start and completion.
- It records segmented trailing triggers.
- It records failed protection moves.

### `PositionAggregate`

The position truth layer now reads dynamic protection state directly.

- `CurrentStopLossPrice`
- `InitialStopLossPrice`
- `BreakEvenArmed`
- `TrailingArmed`
- `TrailingRuleID`
- `ProtectionRevision`
- `LastProtectionMoveAt`
- `ProtectionMoveCount`

These are system-derived fields, not exchange raw fields.

## State flow

### 1. Trigger evaluation

`trader/protection_adjustment_guard.go` evaluates whether the current cycle may arm break-even, arm trailing, or move the stop-loss.

### 2. Plan construction

`trader/protection_adjustment_manager.go` chooses one dynamic protection action and derives the next stop price from the current truth layer.

### 3. Cancel/replace lifecycle

`trader/protection_cancel_replace_reconciler.go` performs the stop replacement as a cancel/replace sequence:

- mark the current stop as pending replacement
- cancel the old stop
- create the new stop
- write the new order registry row
- advance the protection revision

### 4. Truth-layer refresh

`store/protection_adjustment_builder.go` writes the new protection snapshot and the append-only event row in one transaction.

### 5. Position refresh

`store/position_aggregate_builder.go` reads the updated protection truth so the current stop, initial stop, move count, and revision stay in sync.

### 6. Runtime clipping refresh

`trader/runtime_capability_resolver.go` now decides whether these actions are open:

- `move_stop_loss`
- `set_break_even_stop`
- `set_trailing_protection`

Those actions are only open when the protection truth layer, order truth layer, and runtime gates all agree.

## Protection cancel/replace flow

- The old protection leg is identified from the order registry truth layer.
- The old stop is canceled first.
- The new stop is created with a new local intent and a new exchange order id when available.
- The order registry stores both the cancel/replace row and the new protection row.
- The protection truth row advances its revision exactly once.
- Repeated replay of the same completion event must not create a second replacement.

This is why the phase uses a reconciler instead of a simple delete-and-recreate flow.

## Conflict rules

- If a live scale-out plan exists, dynamic protection moves are blocked by default.
- If a live scale-in plan exists, dynamic protection moves are blocked by default.
- If a cancel/replace is already in progress, dynamic protection moves are blocked until the flow completes.
- If the order truth layer or protection truth layer is inconsistent, dynamic protection moves are blocked.
- If the current cycle is not live or the user stream is not ready, dynamic protection moves are blocked.

This keeps add, reduce, and protection-move logic from fighting over the same position truth.

## What remains unsupported

- multi-exchange dynamic protection
- hedge mode
- advanced continuous trailing
- adaptive machine-learned trailing policies
- prompt-level exposure of unsupported tools
- complex frontend rule editors

## What Phase 7 and later can reuse

- `store.ProtectionGroupSummary`
- `store.TrailingRule`
- `store.ProtectionAdjustmentBuilder.ApplyProtectionAdjustmentEvent(...)`
- `store.ProtectionAdjustmentEventLog`
- `trader.ProtectionAdjustmentPreview`
- `trader.ProtectionAdjustmentGuardRequest`
- `trader.ProtectionAdjustmentGuardAssessment`
- `trader.ProtectionCancelReplaceReconciler`
- `api/runtime/protection-adjustment/preview`
- `web/src/components/trader/ProtectionAdjustmentPreviewPanel.tsx`

## Changed-file contract summary

The contract this phase adds is simple:

- truth layer: store the current protection revision, the initial stop, the current stop, and the trailing rule
- event layer: append every protection move to an evidence log
- coordinator layer: move protection through cancel/replace, not through ad hoc field edits
- guard layer: explain why the move is allowed or blocked
- preview layer: expose the full state without changing execution behavior

## Phase 7 validation note

Phase 7 does not add a new protection move. It verifies that the phase-6 dynamic protection state can be replayed, restored, and audited without drifting.

The states that must remain recoverable are:

- `ProtectionRevision`
- `StopLossInitialTriggerPrice`
- `StopLossCurrentTriggerPrice`
- `BreakEvenArmed`
- `TrailingArmed`
- `TrailingRuleID`
- pending cancel/replace status

The high-risk validation targets are:

- replay of protection move events with duplicate or delayed delivery
- restore after a protection revision has already advanced
- conflict cases where size changes and protection moves overlap
- migration of older SQLite files into the dynamic protection schema

Phase 7 also makes the protection previews expose snapshot lineage so the operator can distinguish:

- normal runtime truth
- replay-derived truth
- restore-derived truth
- migration-regression truth
