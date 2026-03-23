# Feature Design: Spearman Rank IC Full-Stack Guard

## Scope

This deployment guard closes the gap between the Spearman Rank IC rollout and the actual live stack state.
It verifies that:

- the backend is listening on `:8080`
- the frontend is listening on `:3000`
- the frontend returns HTTP `200`
- adaptive caches are refreshed after deployment
- ShadowSnapshot migration completion is explicitly logged

## Mathematical Contract

The adaptive IC engine now uses:

- `RankData(data []float64) []float64`
- `EWMASpearmanCorrelation(x, y, halfLife)`
- `adaptiveCoinLimit = 500`
- `alpha = min(CoinSampleCount / 1250.0, 0.4)`

This keeps coin-specific adaptation faster while making IC estimates more robust to extreme return outliers.

## Deployment Guard

`market.FullStackHealthCheck()` performs:

1. `ss -tlnp` port inspection for `:8080` and `:3000`
2. `HEAD http://localhost:3000` and HTTP `200` validation
3. `store.RefreshCache()`
4. `store.NotifyMigration()`

If the guard fails, it still triggers cache refresh and migration notification before returning the failure.
The function is intentionally reentrant: every invocation logs entry, refreshes caches, emits migration completion, and then reports success or failure.

## Store Hooks

`store.RefreshCache()` is implemented as a hook fan-out so store-owned and cross-module runtime caches can be invalidated without creating import cycles.

Current registered hook:

- adaptive IC cache invalidation from `market/ic_engine.go`

`store.NotifyMigration()` emits:

- `ShadowSnapshot DB schema migration completed`

Each hook execution now logs timestamped status so repeated cache refreshes can be audited directly from runtime logs.

## Execution Path

The guard is wired into three runtime paths:

- CLI: `./nofx healthcheck`
- CLI: `./nofx full_regression`
- CLI: `./nofx monitor_spearman`
- HTTP: `GET /api/healthcheck`

`scripts/dev_guard.sh` now calls `./nofx healthcheck` after backend/frontend smoke tests.
If the guard fails, it attempts one automatic `make dev` restart via a retry guard.

## CI/CD Regression Flow

The dedicated workflow `.github/workflows/spearman-full-stack-guard.yml` performs:

1. frontend build
2. backend build
3. `./nofx full_regression`
4. temporary backend/frontend startup
5. `./nofx healthcheck`
6. `./nofx healthcheck` again to verify reentrant execution
7. `./nofx monitor_spearman`

Unlike older advisory-only jobs, this guard job is blocking and fails the pipeline immediately on any regression.

## Spearman Drift Monitoring

`market.MonitorSpearmanOutlierDrift()` evaluates deterministic canary datasets and logs:

- `INFO` when a series stays within the configured drift threshold
- `WARN` when `abs(IC) > threshold`

The default CLI dataset includes:

- a stable oscillating series
- an outlier-perturbed series with a large jump

This provides a lightweight deployment canary without depending on live market APIs.

## Outlier Robustness Evidence

The red-line test suite includes a synthetic sample:

- `100` monotonic baseline observations
- `1` injected `+500%` return outlier

The assertion verifies that the Spearman-based empirical weight drift is materially smaller than the Pearson-based drift.

Reference test:

- `market/ic_engine_test.go` `TestEWMASpearmanCorrelationResistsOutlierWeightDrift`

## Frontend Contract

The Data Lab panel now remains aligned to the new backend limits:

- `sample_target` fallback: `500`
- UI terminology: `Spearman Rank IC`

This prevents stale `30`/`2000` fallback values from causing misleading progress states during degraded API responses.
