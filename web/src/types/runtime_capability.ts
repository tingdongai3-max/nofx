// Runtime capability preview types.
// These types model the backend's read-only capability clipping response.

import type { OrderStateMismatchReason } from './order_registry'
import type { ProtectionStateMismatchReason } from './protection'

// TruthSnapshotMetadata marks whether a preview comes from normal runtime, replay, restore, or migration validation.
// It is validation lineage only and does not change execution permissions.
export interface TruthSnapshotMetadata {
  truth_snapshot_version: string // Validation snapshot version for traceability.
  replayed_from_fixture?: string // Replay fixture id when the preview came from validation replay.
  restored_from_snapshot: boolean // True when the preview came from restore validation.
  migration_fixture_version?: string // Migration fixture version when the preview came from schema regression.
}

// StrategyProfile is the strategy-layer authorization snapshot returned by the backend.
// It is not the runtime decision result for the current cycle.
export interface StrategyProfile {
  trader_id: string // Strategy-layer trader identifier.
  exchange: 'binance_usdm' // Fixed Phase 1 scope: Binance USDⓈ-M Futures.
  mode: 'one_way' // Fixed Phase 1 position mode.
  allow_add_position: boolean // Strategy-layer authorization only.
  allow_partial_take_profit: boolean // Strategy-layer authorization only.
  allow_move_stop_loss: boolean // Strategy-layer authorization only.
  allow_trailing_stop: boolean // Strategy-layer authorization only.
  protection_mode: string // Strategy-layer protection policy label.
  max_scale_in_count: number // Strategy-layer scale-in ceiling.
  max_position_risk_pct: number // Strategy-layer risk budget percentage.
  decision_style: string // Human-readable decision style label.
  execution_enabled: boolean // Global execution authorization gate.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// PositionAggregatePreview is the system-owned position truth snapshot for a single symbol/side.
// It is derived from positions, fills, orders, and decisions, not copied from the exchange raw API.
export interface PositionAggregatePreview {
  trader_id: string // System-owned trader identifier.
  symbol: string // System-normalized symbol.
  side: string // Normalized one-way side.
  total_qty: number // System-derived open quantity.
  available_qty: number // System-derived quantity available for reduce/close.
  pending_add_qty: number // System-derived reserved add quantity.
  pending_reduce_qty: number // System-derived reserved reduce quantity.
  avg_entry_price: number // System-derived weighted entry price.
  realized_pnl: number // System-derived realized PnL.
  unrealized_pnl: number // System-derived unrealized PnL.
  peak_pnl_pct: number // System-derived peak PnL percent.
  has_protection: boolean // System-derived protection truth flag.
  protection_mode: string // System-derived protection policy label.
  protected_quantity: number // System-derived active protected quantity.
  protection_group_id: string // System-derived current protection group identifier.
  stop_loss_armed: boolean // System-derived stop-loss leg working flag.
  take_profit_armed: boolean // System-derived take-profit leg working flag.
  scale_out_plan_id: string // System-derived active scale-out plan identifier.
  has_scale_out_plan: boolean // System-derived flag from the scale-out truth layer.
  scale_out_status: string // System-derived scale-out plan status.
  pending_scale_out_qty: number // System-derived quantity reserved by the active scale-out plan.
  executed_scale_out_qty: number // System-derived executed quantity from the scale-out plan.
  remaining_scale_out_qty: number // System-derived remaining quantity reserved by the active scale-out plan.
  scale_in_plan_id: string // System-derived active scale-in plan identifier.
  has_scale_in_plan: boolean // System-derived flag from the scale-in truth layer.
  scale_in_status: string // System-derived scale-in plan status.
  executed_scale_in_qty: number // System-derived executed quantity from the scale-in plan.
  remaining_scale_in_qty: number // System-derived remaining quantity reserved by the active scale-in plan.
  scale_in_count: number // System-derived executed add-attempt count from the scale-in truth layer.
  protection_state_json: string // System-generated protection state JSON.
  scale_plan_state_json: string // System-generated scale plan JSON.
  execution_eligible: boolean // Runtime eligibility flag, not a direct order permit.
  last_reconciled_at: string // Last truth rebuild timestamp.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// CapabilityReason is the runtime clipping reason attached to a blocked action.
// It is debug output, not an authorization flag.
export interface CapabilityReason {
  action: string // Runtime-clipped action name.
  category: string // Block classification for debugging.
  reason: string // Human-readable block reason.
}

// RuntimeCapabilityPreview is the read-only backend response for runtime capability clipping.
// It combines the strategy profile, selected aggregate, and the allowed/blocked action set.
export interface RuntimeCapabilityPreview extends TruthSnapshotMetadata {
  trader_id: string // System-owned trader identifier for the preview.
  exchange: 'binance_usdm' // Fixed Phase 1 exchange scope.
  mode: 'one_way' // Fixed Phase 1 position mode.
  execution_mode: 'live' | 'sim' | 'readonly' | string // Runtime execution mode used for clipping.
  selected_symbol?: string // Selected symbol for the current preview.
  strategy_profile: StrategyProfile // Strategy-layer authorization snapshot.
  position_aggregate?: PositionAggregatePreview | null // System-owned aggregate truth for the selected symbol.
  position_aggregates: PositionAggregatePreview[] // System-owned aggregate list for debugging and UI review.
  allowed_actions: string[] // Runtime-clipped actions currently exposed to the AI/UI.
  blocked_actions: string[] // Runtime-clipped actions currently hidden or disabled.
  block_reasons: CapabilityReason[] // Debug reasons for each blocked action.
  has_pending_orders: boolean // Runtime state flag derived from selected-symbol pending orders.
  has_protection_orders: boolean // Runtime state flag derived from selected-symbol protection orders.
  has_protection: boolean // Runtime protection state flag derived from the protection truth layer.
  protection_group_status: string // Current protection group status used by the resolver.
  protection_adjustment_status: string // Runtime dynamic-protection status label from the protection-adjustment truth layer.
  protection_adjustment_consistency: string // Runtime dynamic-protection consistency enum from the truth layer.
  protection_adjustment_block_reasons: CapabilityReason[] // Dynamic-protection block reasons used by debug output.
  protection_revision: number // Runtime protection revision snapshot.
  current_stop_loss_price: number // Runtime current stop-loss price.
  initial_stop_loss_price: number // Runtime initial stop-loss price.
  break_even_armed: boolean // Runtime dynamic-protection break-even flag.
  trailing_armed: boolean // Runtime dynamic-protection trailing flag.
  remaining_move_budget: number // Remaining dynamic-protection move budget.
  can_arm_break_even: boolean // Guard flag showing break-even may be armed now.
  can_arm_trailing: boolean // Guard flag showing trailing may be armed now.
  can_move_stop_loss: boolean // Guard flag showing the stop-loss may be moved now.
  has_pending_cancel_replace: boolean // Runtime truth flag for a pending cancel/replace flow.
  stop_loss_armed: boolean // Runtime protection leg flag derived from the protection truth layer.
  take_profit_armed: boolean // Runtime protection leg flag derived from the protection truth layer.
  protection_consistency: string // Runtime protection consistency label returned by the truth layer.
  has_protection_mismatch: boolean // Runtime mismatch flag for the protection truth layer.
  protection_block_reasons: ProtectionStateMismatchReason[] // Machine-readable protection mismatch reasons for UI/debug.
  has_scale_out_plan: boolean // Runtime scale-out plan flag derived from the scale-out truth layer.
  scale_out_status: string // Runtime scale-out status used for capability clipping.
  scale_out_consistency: string // Runtime scale-out consistency label returned by the truth layer.
  has_scale_out_mismatch: boolean // Runtime mismatch flag for the scale-out truth layer.
  pending_scale_out_qty: number // System-derived quantity reserved by active working scale-out levels.
  remaining_scale_out_qty: number // System-derived quantity still pending across the current plan.
  executed_scale_out_qty: number // System-derived cumulative executed quantity across the current plan.
  scale_out_block_reasons: CapabilityReason[] // Machine-readable scale-out block reasons for UI/debug.
  protection_rebalanced: boolean // Derived flag showing whether protection coverage matches the current position.
  has_scale_in_plan: boolean // Runtime scale-in plan flag derived from the scale-in truth layer.
  scale_in_status: string // Runtime scale-in status used for capability clipping.
  scale_in_consistency: string // Runtime scale-in consistency label returned by the truth layer.
  has_scale_in_mismatch: boolean // Runtime mismatch flag for the scale-in truth layer.
  pending_scale_in_qty: number // System-derived quantity reserved by active working scale-in levels.
  remaining_scale_in_qty: number // System-derived quantity still pending across the current add plan.
  executed_scale_in_qty: number // System-derived cumulative executed quantity across the current add plan.
  scale_in_count: number // Strategy-limited add-attempt count used by the resolver.
  scale_in_block_reasons: CapabilityReason[] // Machine-readable scale-in block reasons for UI/debug.
  risk_budget_remaining: number // Remaining notional risk budget under the strategy profile.
  has_working_orders: boolean // Runtime truth-layer flag; true means the order registry still has working rows.
  has_state_mismatch: boolean // Runtime truth-layer flag; true means the registry and exchange disagree.
  pending_add_qty: number // System-derived quantity reserved by working entry orders.
  pending_reduce_qty: number // System-derived quantity reserved by working reduce/protection orders.
  mismatch_reasons: OrderStateMismatchReason[] // Reconcile reasons shown to the UI for blocking/debugging.
  user_stream_ready: boolean // Runtime readiness flag for the Binance user-stream bridge.
  allow_order_placement: boolean // Runtime execution gate for this cycle.
  legacy_or_audit_only: boolean // Runtime audit flag that blocks execution.
  generated_at: string // Response generation timestamp.
}
