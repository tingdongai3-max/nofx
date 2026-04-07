# Phase 7 Live Readiness and Restore

This phase does not add a new trading capability.

It adds the validation and recovery layer that proves the phase 1-6 truth model can survive real replay, restart, migration, and conflict conditions without contradicting itself.

The target is simple:

- replay recorded exchange events through the existing truth layers
- rebuild the truth layers after restart and compare the result
- run long-chain conflict cases against the same truth layers
- upgrade old SQLite fixtures to the current schema and validate integrity
- expose the validation result through read-only APIs and dashboard panels

## Why Phase 7 does not add new behavior

By the end of phase 6 the system can:

- keep order truth
- keep protection truth
- keep partial reduce truth
- keep add-position truth
- keep dynamic protection revision truth

What it still has to prove is that these truth layers stay coherent under dirty runtime conditions:

- delayed or duplicated user-stream events
- restart and restore
- overlapping size-change and protection-change flows
- old SQLite files migrating across schema revisions
- frontend panels rendering the same state the APIs expose

Phase 7 exists to turn "the features run" into "the truth model is auditable and restart-safe".

The validation fixtures used by replay and migration are stored in a separate validation database, not in the live trading database. The default path is `data/phase7_validation_fixtures.db`, and test runs can override it with `PHASE7_VALIDATION_DB_PATH`.

## Files changed in this phase

### Replay bus and validation fixtures

- [`store/replay_fixture.go`](/root/nofx-src/store/replay_fixture.go)
- [`store/schema_migration_fixture.go`](/root/nofx-src/store/schema_migration_fixture.go)
- [`trader/binance_user_stream_replay_manager.go`](/root/nofx-src/trader/binance_user_stream_replay_manager.go)
- [`trader/phase7_truth_snapshot.go`](/root/nofx-src/trader/phase7_truth_snapshot.go)

### Restore and conflict validation

- [`trader/truth_layer_restore_manager.go`](/root/nofx-src/trader/truth_layer_restore_manager.go)
- [`trader/conflict_matrix_runner.go`](/root/nofx-src/trader/conflict_matrix_runner.go)
- [`trader/schema_migration_regression_runner.go`](/root/nofx-src/trader/schema_migration_regression_runner.go)
- [`trader/phase7_live_readiness_test.go`](/root/nofx-src/trader/phase7_live_readiness_test.go)

### Validation preview APIs

- [`api/handler_replay_preview.go`](/root/nofx-src/api/handler_replay_preview.go)
- [`api/handler_restore_preview.go`](/root/nofx-src/api/handler_restore_preview.go)
- [`api/server.go`](/root/nofx-src/api/server.go)

### Frontend debug panels and tests

- [`web/src/types/replay.ts`](/root/nofx-src/web/src/types/replay.ts)
- [`web/src/lib/api/traders.ts`](/root/nofx-src/web/src/lib/api/traders.ts)
- [`web/src/components/trader/ReplayPreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/ReplayPreviewPanel.tsx)
- [`web/src/components/trader/RestorePreviewPanel.tsx`](/root/nofx-src/web/src/components/trader/RestorePreviewPanel.tsx)
- [`web/src/components/trader/ReplayPreviewPanel.test.tsx`](/root/nofx-src/web/src/components/trader/ReplayPreviewPanel.test.tsx)
- [`web/src/components/trader/RestorePreviewPanel.test.tsx`](/root/nofx-src/web/src/components/trader/RestorePreviewPanel.test.tsx)
- [`web/src/pages/TraderDashboardPage.tsx`](/root/nofx-src/web/src/pages/TraderDashboardPage.tsx)
- [`web/src/pages/TraderDashboardPage.phase7.test.tsx`](/root/nofx-src/web/src/pages/TraderDashboardPage.phase7.test.tsx)

## Why each area changed

- `ReplayFixture` and `SchemaMigrationFixture` give validation a versioned, queryable input contract instead of hidden ad hoc test data.
- `BinanceUserStreamReplayManager` proves the existing order, protection, scale-out, scale-in, and capability layers can be driven by recorded exchange traffic.
- `TruthLayerRestoreManager` proves a restart rebuild does not silently mutate the truth model.
- `ConflictMatrixRunner` runs high-risk combined scenarios against the same truth layers that production uses.
- `SchemaMigrationRegressionRunner` proves old SQLite files can migrate forward without producing orphan truth rows or illegal enum/state values.
- Replay and restore preview APIs let operators inspect validation output without changing execution behavior.
- Replay and restore panels make the validation state visible on the dashboard.

## Data contracts

### `ReplayFixture`

This is a validation-only fixture row. It does not enter the trading execution path.

- `FixtureID`: stable fixture key used by replay preview.
- `FixtureType`: validation scope label such as `binance_user_stream` or `conflict_case`.
- `Version`: fixture payload contract version.
- `Source`: where the fixture came from.
- `PayloadJSON`: versioned replay payload.

### `SchemaMigrationFixture`

This is a validation-only SQLite upgrade fixture row.

- `FixtureID`: stable migration fixture key.
- `Phase`: source schema generation, such as `phase1_pre` or `phase5_pre`.
- `Version`: fixture payload contract version.
- `Source`: fixture provenance.
- `PayloadJSON`: schema SQL, seed SQL, and optional validation scope.

### `TruthSnapshotMetadata`

Every validation preview now carries lineage metadata.

- `truth_snapshot_version`: timestamp/version of the validation snapshot.
- `replayed_from_fixture`: non-empty only when the output came from a replay fixture.
- `restored_from_snapshot`: true only when the output came from restore validation.
- `migration_fixture_version`: non-empty only when the output came from a migration regression run.

This prevents replay, restore, migration, and live runtime outputs from becoming indistinguishable.

### `TruthSnapshotSummary`

This is the normalized validation summary used by replay, restore, conflict, and migration previews.

It includes:

- the current `PositionAggregate`
- the order summary
- the protection summary
- the scale-out summary
- the scale-in summary
- the quantity invariant audit

It is validation output only. It does not open or change any runtime capability by itself.

### Quantity invariant audit

The audit is the hard stop for the quantity truth layer. It checks that the following values stay internally consistent for one trader/symbol scope:

- `total_qty`
- `available_qty`
- `pending_reduce_qty`
- `pending_scale_out_qty`
- `remaining_scale_out_qty`
- `protected_quantity`
- `order_summary.pending_reduce_qty`
- `order_summary.protection_coverage_qty`

The allowed relationships are deliberately conservative:

- `available_qty + pending_reduce_qty` should equal `total_qty`
- `pending_reduce_qty` must cover the active `pending_scale_out_qty`
- `pending_scale_out_qty` must not exceed `remaining_scale_out_qty`
- `protected_quantity` must not exceed `total_qty`
- `order_summary.pending_reduce_qty` must cover the aggregate reduce reserve
- `order_summary.protection_coverage_qty` must cover `protected_quantity`

Any violation becomes a quantity mismatch reason in the replay, restore, or capability preview.

## Replay flow

### 1. Load a versioned replay fixture

`trader/binance_user_stream_replay_manager.go` loads a stored `ReplayFixture` row from the isolated validation database and parses the versioned payload.

### 2. Expand the event stream

The replay bus supports:

- `sequential`
- `out_of_order`
- `duplicate`
- `delayed`

The expanded event sequence is preserved in the preview response for audit.

### 3. Drive the existing truth layers

Replay reuses the existing Binance order reconcile entrypoint and then rebuilds:

- `OrderRegistry`
- `ProtectionGroup`
- `ScaleOutPlan`
- `ScaleInPlan`
- `PositionAggregate`
- `RuntimeCapabilityResolver`

### 4. Emit a truth snapshot

After replay, the system returns:

- the replay event sequence
- the resulting truth snapshot
- the capability result
- any mismatch reasons

## Restore flow

### 1. Capture the pre-restore truth snapshot

The restore manager records the current truth snapshot before any rebuild.

### 2. Rebuild truth layers from persisted state

It refreshes the trader's aggregate and preview state from the existing database rows and the current validation scope.

### 3. Capture the post-restore truth snapshot

The restore manager then captures the post-rebuild snapshot.

### 4. Diff the snapshots

Any unexpected change becomes a structured mismatch reason.

This is the core guarantee: restart must not silently rewrite truth.

## Conflict matrix coverage

The conflict runner exists to validate long-chain interactions, not isolated feature logic.

The initial matrix covers at least:

- active scale-out plus attempted scale-in
- active scale-in plus attempted protection adjustment
- pending protection cancel/replace plus attempted reduce
- partial fill plus break-even trigger
- restart plus delayed user-stream replay
- protection mismatch plus capability resolution

Each case returns:

- initial truth snapshot
- event sequence
- final truth snapshot
- capability result
- mismatch reasons

## SQLite migration regression strategy

Phase 7 migration regression uses versioned SQLite fixtures instead of assumptions about the current database.

The runner:

- creates a temporary SQLite file from the fixture SQL
- opens it through the current store and migrations
- rebuilds key truth layers
- validates integrity

The integrity checks cover:

- no orphan truth rows for the declared validation scope
- no illegal empty/unknown enum state where a stable enum is required
- key aggregate fields can be rebuilt
- capability preview can still be constructed

## Frontend E2E target

This phase does not add a complex browser console.

The frontend goal is narrower:

- replay preview panel renders fixture, event sequence, mismatches, and capability result
- restore preview panel renders before/after snapshots and diffs
- dashboard integration keeps both panels mounted in the main trader page

The panel tests and dashboard integration test are suitable for continuous regression because they validate:

- API contract shape
- panel rendering
- panel coexistence inside the trader dashboard

## Validation coverage added in this phase

### Backend

- real user-stream replay test
- cold-start restore line test
- long-chain conflict matrix test
- SQLite migration regression test

### Frontend

- replay panel rendering test
- restore panel rendering test
- trader dashboard integration test for replay + restore panels

## What Phase 7 still does not cover

- exchange-recorded websocket streams captured from every supported exchange condition
- full browser automation with screenshots and navigation across the whole app
- all possible multi-symbol or multi-trader conflict permutations
- every historical pre-phase database shape that may exist outside the tracked fixtures

Those gaps are now explicit, versioned, and auditable instead of hidden.

## What should enter continuous regression

The following validation paths are stable enough to run continuously:

- replay fixture regression
- restore snapshot regression
- conflict matrix regression
- SQLite migration regression
- replay/restore panel rendering regression
- dashboard integration regression for replay + restore panels

## Completion standard for this phase

Phase 7 is considered complete when:

- replay, restore, conflict, and migration validation paths exist
- validation outputs are visible via read-only APIs
- dashboard panels render those validation outputs
- logs clearly identify replay, restore, conflict, and migration runs
- validation lineage is attached to preview outputs
- the phase adds no new trading action and no new exchange/runtime capability
