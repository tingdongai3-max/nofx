# Phase 2 Order Registry and Reconcile

This phase builds the Binance-only order truth layer that sits underneath later protection and scaling features.

## Why we need the registry first

Fixed TP/SL, partial reduce, add-position, and trailing protection all depend on a single answer to these questions:

- What orders do we believe exist?
- Which ones are still working?
- How much has actually filled?
- What quantity is still reserved by pending orders?

Without that layer, `PositionAggregate` can only guess from positions and decisions. That is not enough for safe execution.

## File layout

### Order truth layer

- [`store/order_registry.go`](/root/nofx-src/store/order_registry.go)
- [`store/order_event_log.go`](/root/nofx-src/store/order_event_log.go)
- [`store/order_registry_builder.go`](/root/nofx-src/store/order_registry_builder.go)

### Reconcile coordinator

- [`trader/binance_order_reconcile_manager.go`](/root/nofx-src/trader/binance_order_reconcile_manager.go)
- [`trader/order_state_reconciler.go`](/root/nofx-src/trader/order_state_reconciler.go)

### Read-only previews

- [`api/handler_order_registry_preview.go`](/root/nofx-src/api/handler_order_registry_preview.go)
- [`api/handler_capability_preview.go`](/root/nofx-src/api/handler_capability_preview.go)
- [`web/src/components/trader/OrderRegistryPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/OrderRegistryPreviewPanel.tsx)
- [`web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/RuntimeCapabilityPreviewPanel.tsx)

## Data contracts

### `OrderRegistry`

This is the system-owned order truth row, not the exchange raw order table.

- `TraderID`, `Symbol`, `Side`: system truth scope for one-way Binance futures.
- `OrderRole`: system-derived role such as `entry`, `reduce`, `stop_loss`, `take_profit`, `trailing_protection`, `cancel_replace`.
- `LocalIntentID`, `LinkedGroupID`, `LinkedPositionKey`: system correlation fields.
- `ExchangeOrderID`, `ClientOrderID`, `OrderType`, `TimeInForce`: exchange-origin metadata when available.
- `OrigQty`, `ExecutedQty`, `RemainingQty`: system-tracked quantities.
- `Status`, `IsWorking`, `LastExchangeUpdateAt`: reconciliation state.

### `OrderEventLog`

This is the append-only evidence chain.

- Every user-stream order event gets a row.
- Every bootstrap/recovery event gets a row.
- The log is used to explain why local state and exchange state diverged.

### `OrderRegistrySummary`

This is the derived rollup used by `PositionAggregate`.

- `PendingAddQty`: working entry quantity reserved by open orders.
- `PendingReduceQty`: working reduce/protection quantity reserved by open orders.
- `ProtectionCoverageQty`: protected quantity covered by stop/take-profit/trailing rows.
- `HasWorkingOrders`, `HasPendingCancelReplace`, `ExecutionEligible`: derived flags only.

## State flow

### 1. User stream event arrives

`trader/binance_order_reconcile_manager.go` parses Binance `ORDER_TRADE_UPDATE`, writes the raw event to `order_event_logs`, and updates `order_registry` through `order_registry_builder.go`.

### 2. Registry row is updated

The builder is the only place that computes:

- `ExecutedQty`
- `RemainingQty`
- `IsWorking`
- `Status`

That keeps remaining-quantity math in one place.

### 3. Reconcile runs

`trader/order_state_reconciler.go` compares local registry rows with the exchange open-order snapshot.

It flags:

- a local working order that no longer exists on the exchange
- an exchange working order that is not tracked locally
- a partially filled row with inconsistent remaining quantity
- pending cancel-replace flows
- user-stream not ready

### 4. Position aggregate refreshes

`store/position_aggregate_builder.go` now reads pending add/reduce coverage from the registry summary instead of guessing from decision records alone.

## Recovery logic

If the registry is empty, the reconciler can bootstrap from legacy `trader_orders` rows so the new truth layer can be rebuilt after restart.

The recovery path is explicit and lives in the builder/reconciler layer, not in a random handler or startup hook.

## What this phase still does not do

- Partial take-profit execution is still not implemented.
- Add-position execution is still not implemented.
- Move-stop-loss execution is still not implemented.
- Trailing protection execution is still not implemented.
- Hedge mode is not supported in this phase.
- Multi-exchange abstraction is not introduced here.

## What the UI/API can now show

The preview endpoints can now answer:

- what the registry believes exists
- what the recent evidence chain looks like
- whether the registry and exchange disagree
- why runtime capability is blocked by order state

## Why this is the right foundation

Later features can use the same registry rows to:

- reserve quantity for partial reduce
- reserve quantity for add/scaling logic
- compute protection coverage
- decide when a trailing or cancel-replace action is safe to expose

Until then, the system should only expose read-only state and should not pretend those execution tools already exist.

## Phase 4 dependency note

Phase 4 keeps using this same registry layer for partial reduce / batch take-profit.

That means:

- reduce orders are still registry rows
- scale-out plan levels map to `OrderRole = reduce`
- filled and canceled scale-out legs still land in the append-only event log
- the registry summary now feeds `PendingReduceQty` and `ProtectionCoverageQty` into `PositionAggregate`

The key rule is unchanged: there should still be only one registry truth layer. Phase 4 reuses it instead of creating a second order model for partial reduce.

## Phase 3 dependency note

Phase 3 uses this same registry layer to attach fixed protection orders and to track sibling cancel flows.

That means:

- protection legs are still order truth rows
- protection evidence still lands in `order_event_logs`
- `PositionAggregate` now reads protection state through the protection group summary instead of guessing from decisions alone
- capability clipping can block or expose `set_protection` based on registry consistency
