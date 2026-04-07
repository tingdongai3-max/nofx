// Scale-in truth preview types.
// These types model the Binance-only same-symbol add-position truth layer and its read-only preview responses.

import type { CapabilityReason, PositionAggregatePreview, TruthSnapshotMetadata } from './runtime_capability'
import type { OrderRegistrySummary } from './order_registry'
import type { ProtectionStateMismatchReason } from './protection'

// ScaleInPlanMode is the plan-layer authorization label.
// It is a persisted truth field, not an execution command.
export type ScaleInPlanMode = 'fixed_qty' | 'fixed_ratio'

// ScaleInPlanStatus is the plan-layer truth state.
// It is a persisted truth field, not a direct execution permit.
export type ScaleInPlanStatus = 'draft' | 'armed' | 'partially_filled' | 'completed' | 'invalid'

// ScaleInLevelStatus is the level-layer truth state.
// It is a persisted truth field, not a direct execution permit.
export type ScaleInLevelStatus = 'draft' | 'armed' | 'partially_filled' | 'filled' | 'cancelled' | 'invalid'

// ScaleInTargetType is the level target type for the current phase.
// It is a plan-layer configuration field, not a raw exchange order type.
export type ScaleInTargetType = 'market' | 'limit_price'

// ScaleInConsistencyStatus is the compact scale-in consistency enum.
// It is a truth-layer diagnostic value used by previews and capability clipping.
export type ScaleInConsistencyStatus = 'consistent' | 'mismatch' | 'pending' | 'unknown'

// ScaleInStateMismatchReason is a machine-readable scale-in reconcile explanation.
// It is debug output from the truth layer, not a raw exchange event.
export interface ScaleInStateMismatchReason {
  category: string // Mismatch bucket used by the backend.
  reason: string // Human-readable explanation.
  symbol?: string // Symbol scope for the mismatch.
  scale_in_plan_id?: string // Scale-in plan involved in the mismatch.
  level_index?: number // Scale-in level involved in the mismatch.
  linked_position_key?: string // System-owned position correlation key.
  linked_order_intent_id?: string // Local intent id when available.
  exchange_order_id?: string // Exchange order id when available.
  client_order_id?: string // Exchange client id when available.
}

// ScaleInPlanPreview is the system-owned scale-in plan truth row returned by the backend.
// It is not a raw exchange order mirror; it represents the current plan snapshot.
export interface ScaleInPlanPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // One-way truth-layer side: LONG or SHORT.
  linked_position_key: string // System-owned position correlation key.
  scale_in_plan_id: string // System-generated scale-in plan identifier.
  plan_mode: ScaleInPlanMode // Plan-layer policy label.
  status: ScaleInPlanStatus // Current plan truth status.
  total_planned_qty: number // System-derived total planned quantity.
  remaining_planned_qty: number // System-derived quantity still reserved by the plan.
  executed_qty: number // System-derived cumulative executed quantity.
  max_scale_in_count: number // Strategy-layer add-attempt ceiling.
  current_scale_in_count: number // System-derived executed add-attempt count.
  source: string // Truth source label.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// ScaleInPlanLevelPreview is the system-owned scale-in plan level truth row returned by the backend.
// It is a level-layer snapshot, not a raw exchange order mirror.
export interface ScaleInPlanLevelPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // One-way truth-layer side: LONG or SHORT.
  linked_position_key: string // System-owned position correlation key.
  scale_in_plan_id: string // System-generated plan identifier.
  level_index: number // System-owned level index within the plan.
  target_type: ScaleInTargetType // Target type for this level.
  target_price: number // System-derived target price for the level.
  planned_qty: number // System-derived quantity planned for the level.
  executed_qty: number // System-derived cumulative executed quantity for the level.
  remaining_qty: number // System-derived remaining quantity for the level.
  linked_order_intent_id: string // System-local order intent id used for correlation.
  linked_exchange_order_id: string // Exchange raw order id when available.
  status: ScaleInLevelStatus // Current level truth status.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// ScaleInEventLogPreview is the append-only scale-in evidence row returned by the backend.
// It is raw evidence, not a reconciled plan snapshot.
export interface ScaleInEventLogPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope attached to the event.
  scale_in_plan_id: string // System-generated scale-in plan identifier.
  level_index: number // Level index involved in the event when available.
  event_type: string // Raw event type from the manager/reconciler/bootstrap.
  payload_json: string // Raw JSON payload preserved for evidence/debugging.
  event_source: string // Source label.
  event_time: string // Raw event timestamp.
  created_at: string // Persistence timestamp.
}

// ScaleInRiskPreview is the execution-facing result produced by the add-position risk gate.
// It is a guard result, not a truth-layer snapshot.
export interface ScaleInRiskPreview {
  allowed: boolean // Runtime permit emitted by the guard.
  blocked_reasons: CapabilityReason[] // Machine-readable explanations for every blocked branch.
  max_scale_in_count: number // Strategy-layer add-attempt ceiling.
  current_scale_in_count: number // Current add-attempt count from the scale-in truth layer.
  risk_budget_remaining: number // Remaining notional budget under the profile max-risk limit.
  current_position_risk_pct: number // Current notional as a percent of account equity when equity is known.
  current_position_notional: number // Current notional derived from the position aggregate.
  proposed_add_notional: number // Proposed add notional used by the guard.
}

// ScaleInProtectionSummaryPreview is the protection summary as consumed by the scale-in preview.
// It is derived from the protection truth layer, not the raw exchange order table.
export interface ScaleInProtectionSummaryPreview {
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // Truth-layer side scope.
  linked_position_key: string // System-owned position correlation key.
  protection_group_id: string // System-generated protection group identifier.
  protection_mode: string // Protection policy label.
  protection_group_status: string // Current protection truth status.
  has_protection: boolean // Protection truth flag.
  stop_loss_armed: boolean // Stop-loss working flag.
  take_profit_armed: boolean // Take-profit working flag.
  protected_quantity: number // System-derived active protected quantity.
  has_state_mismatch: boolean // Reconcile result flag; true means truth layers disagree.
  consistency_status: ScaleInConsistencyStatus // Protection consistency enum reused by scale-in preview.
  mismatch_reasons: ProtectionStateMismatchReason[] // Machine-readable mismatch explanation list.
  has_working_orders: boolean // Truth-layer flag derived from linked working protection rows.
  has_pending_cancel_replace: boolean // Truth-layer flag for any linked cancel_replace flow.
  stop_loss_trigger_price: number // Current stop-loss trigger price from the protection group.
  take_profit_trigger_price: number // Current take-profit trigger price from the protection group.
  last_synced_at: string // Latest sync timestamp across the protection scope.
  execution_eligible: boolean // Runtime readiness flag; not a direct execution permit.
}

// ScaleInReconcilePreview is the full read-only scale-in truth preview returned by the backend.
// It combines the scale-in plan truth layer, linked order evidence, risk gate state, and reconcile status.
export interface ScaleInReconcilePreview extends TruthSnapshotMetadata {
  trader_id: string // System-owned trader identifier.
  selected_symbol?: string // Selected symbol scope.
  exchange: 'binance_usdm' // Fixed phase-5 exchange scope.
  mode: 'one_way' // Fixed phase-5 mode scope.
  scale_in_plan?: ScaleInPlanPreview | null // Current scale-in plan truth row.
  scale_in_plans: ScaleInPlanPreview[] // Current scale-in plan snapshot rows for debug.
  scale_in_levels: ScaleInPlanLevelPreview[] // Current scale-in level rows for debug.
  scale_in_event_logs: ScaleInEventLogPreview[] // Append-only raw scale-in evidence chain.
  order_registry_summary?: OrderRegistrySummary | null // System-derived order truth summary for the selected scope.
  protection_summary?: ScaleInProtectionSummaryPreview | null // System-derived protection summary for the selected scope.
  position_aggregate?: PositionAggregatePreview | null // System-owned position truth snapshot for the selected scope.
  risk_assessment?: ScaleInRiskPreview | null // Execution-facing risk guard result used by the preview panel.
  protection_adjustment_status: string // Runtime dynamic-protection status label.
  protection_adjustment_consistency: string // Runtime dynamic-protection consistency label.
  protection_adjustment_block_reasons: CapabilityReason[] // Machine-readable dynamic-protection block reasons.
  protection_revision: number // Runtime protection revision snapshot.
  current_stop_loss_price: number // Runtime current stop-loss trigger price.
  initial_stop_loss_price: number // Runtime initial stop-loss trigger price.
  break_even_armed: boolean // Runtime dynamic-protection break-even flag.
  trailing_armed: boolean // Runtime dynamic-protection trailing flag.
  remaining_move_budget: number // Remaining dynamic-protection move budget.
  has_scale_in_plan: boolean // Truth-layer flag showing whether a scale-in plan exists.
  scale_in_status: string // Current scale-in plan truth status.
  scale_in_consistency: ScaleInConsistencyStatus // Scale-in consistency enum returned by the truth layer.
  has_state_mismatch: boolean // Reconcile result flag; true means truth layers disagree.
  mismatch_reasons: ScaleInStateMismatchReason[] // Machine-readable mismatch explanation list.
  has_working_orders: boolean // Truth-layer flag derived from linked working scale-in rows.
  pending_add_qty: number // Truth-layer pending add quantity from the order registry summary.
  pending_scale_in_qty: number // Truth-layer working-order reserve for the active scale-in plan.
  remaining_scale_in_qty: number // Truth-layer total quantity still outstanding across the current plan.
  executed_scale_in_qty: number // Truth-layer cumulative executed quantity across the current plan.
  scale_in_count: number // Truth-layer executed add-attempt count from the plan snapshot.
  scale_in_block_reasons: CapabilityReason[] // Capability-style reasons used by the UI and resolver preview.
  risk_budget_remaining: number // Remaining notional risk budget under the strategy profile.
  has_pending_cancel_replace: boolean // Truth-layer flag for any linked cancel/replace flow.
  user_stream_ready: boolean // Runtime readiness flag for the Binance user-stream bridge.
  generated_at: string // Response generation timestamp.
}
