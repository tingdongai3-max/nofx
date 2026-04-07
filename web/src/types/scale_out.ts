// Scale-out truth preview types.
// These types model the Binance-only partial reduce / batch take-profit truth layer and its read-only preview responses.

import type { CapabilityReason, PositionAggregatePreview, TruthSnapshotMetadata } from './runtime_capability'
import type { OrderRegistrySummary } from './order_registry'
import type { ProtectionStateMismatchReason } from './protection'

// ScaleOutPlanMode is the plan-layer authorization label.
// It is a persisted truth field, not an execution command.
export type ScaleOutPlanMode = 'fixed_ratio'

// ScaleOutPlanStatus is the plan-layer truth state.
// It is a persisted truth field, not a direct execution permit.
export type ScaleOutPlanStatus = 'draft' | 'armed' | 'partially_filled' | 'completed' | 'invalid'

// ScaleOutLevelStatus is the level-layer truth state.
// It is a persisted truth field, not a direct execution permit.
export type ScaleOutLevelStatus = 'draft' | 'armed' | 'partially_filled' | 'filled' | 'cancelled' | 'invalid'

// ScaleOutTargetType is the level target type for the current phase.
// It is a plan-layer configuration field, not a raw exchange order type.
export type ScaleOutTargetType = 'limit_price' | 'reduce_market'

// ScaleOutConsistencyStatus is the compact scale-out consistency enum.
// It is a truth-layer diagnostic value used by previews and capability clipping.
export type ScaleOutConsistencyStatus = 'consistent' | 'mismatch' | 'pending' | 'unknown'

// ScaleOutPlanPreview is the system-owned scale-out plan truth row returned by the backend.
// It is not a raw exchange order mirror; it represents the current plan snapshot.
export interface ScaleOutPlanPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // One-way truth-layer side: LONG or SHORT.
  linked_position_key: string // System-owned position correlation key.
  scale_out_plan_id: string // System-generated scale-out plan identifier.
  plan_mode: ScaleOutPlanMode // Plan-layer policy label.
  status: ScaleOutPlanStatus // Current plan truth status.
  total_planned_qty: number // System-derived total planned quantity.
  remaining_planned_qty: number // System-derived quantity still reserved by the plan.
  executed_qty: number // System-derived cumulative executed quantity.
  source: string // Truth source label.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// ScaleOutPlanLevelPreview is the system-owned scale-out plan level truth row returned by the backend.
// It is a level-layer snapshot, not a raw exchange order mirror.
export interface ScaleOutPlanLevelPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // One-way truth-layer side: LONG or SHORT.
  linked_position_key: string // System-owned position correlation key.
  scale_out_plan_id: string // System-generated plan identifier.
  level_index: number // System-owned level index within the plan.
  target_type: ScaleOutTargetType // Target type for this level.
  target_price: number // System-derived target price for the level.
  planned_qty: number // System-derived quantity planned for the level.
  executed_qty: number // System-derived cumulative executed quantity for the level.
  remaining_qty: number // System-derived remaining quantity for the level.
  linked_order_intent_id: string // System-local order intent id used for correlation.
  linked_exchange_order_id: string // Exchange raw order id when available.
  status: ScaleOutLevelStatus // Current level truth status.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// ScaleOutEventLogPreview is the append-only scale-out evidence row returned by the backend.
// It is raw evidence, not a reconciled plan snapshot.
export interface ScaleOutEventLogPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope attached to the event.
  scale_out_plan_id: string // System-generated scale-out plan identifier.
  level_index: number // Level index involved in the event when available.
  event_type: string // Raw event type from the manager/reconciler/bootstrap.
  payload_json: string // Raw JSON payload preserved for evidence/debugging.
  event_source: string // Source label.
  event_time: string // Raw event timestamp.
  created_at: string // Persistence timestamp.
}

// ScaleOutStateMismatchReason is a machine-readable scale-out reconcile explanation.
// It is debug output from the truth layer, not a raw exchange event.
export interface ScaleOutStateMismatchReason {
  category: string // Mismatch bucket used by the backend.
  reason: string // Human-readable explanation.
  symbol?: string // Symbol scope for the mismatch.
  scale_out_plan_id?: string // Scale-out plan involved in the mismatch.
  level_index?: number // Scale-out level involved in the mismatch.
  linked_position_key?: string // System-owned position correlation key.
  linked_order_intent_id?: string // Local intent id when available.
  exchange_order_id?: string // Exchange order id when available.
  client_order_id?: string // Exchange client id when available.
}

// ScaleOutProtectionSummaryPreview is the protection summary as consumed by the scale-out preview.
// It is derived from the protection truth layer, not the raw exchange order table.
export interface ScaleOutProtectionSummaryPreview {
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
  consistency_status: ScaleOutConsistencyStatus // Protection consistency enum reused by scale-out preview.
  mismatch_reasons: ProtectionStateMismatchReason[] // Machine-readable mismatch explanation list.
  has_working_orders: boolean // Truth-layer flag derived from linked working protection rows.
  has_pending_cancel_replace: boolean // Truth-layer flag for any linked cancel_replace flow.
  stop_loss_trigger_price: number // Current stop-loss trigger price from the protection group.
  take_profit_trigger_price: number // Current take-profit trigger price from the protection group.
  last_synced_at: string // Latest sync timestamp across the protection scope.
  execution_eligible: boolean // Runtime readiness flag; not a direct execution permit.
}

// ScaleOutConsistencyPreview is the compact read-only scale-out consistency summary.
// It is used by the UI to show why a partial-reduce plan is open, blocked, or inconsistent.
export interface ScaleOutConsistencyPreview {
  trader_id: string // System-owned trader identifier.
  selected_symbol?: string // Selected symbol scope.
  exchange: 'binance_usdm' // Fixed phase-4 exchange scope.
  mode: 'one_way' // Fixed phase-4 mode scope.
  scale_out_plan?: ScaleOutPlanPreview | null // Current scale-out plan truth row.
  scale_out_plans: ScaleOutPlanPreview[] // Current scale-out plan snapshot rows for debug.
  scale_out_levels: ScaleOutPlanLevelPreview[] // Current scale-out level rows for debug.
  scale_out_event_logs: ScaleOutEventLogPreview[] // Append-only raw scale-out evidence chain.
  order_registry_summary?: OrderRegistrySummary | null // System-derived order truth summary for the selected scope.
  protection_summary?: ScaleOutProtectionSummaryPreview | null // System-derived protection summary for the selected scope.
  position_aggregate?: PositionAggregatePreview | null // System-owned position truth snapshot for the selected scope.
  protection_adjustment_status: string // Runtime dynamic-protection status label.
  protection_adjustment_consistency: string // Runtime dynamic-protection consistency label.
  protection_adjustment_block_reasons: CapabilityReason[] // Machine-readable dynamic-protection block reasons.
  protection_revision: number // Runtime protection revision snapshot.
  current_stop_loss_price: number // Runtime current stop-loss trigger price.
  initial_stop_loss_price: number // Runtime initial stop-loss trigger price.
  break_even_armed: boolean // Runtime dynamic-protection break-even flag.
  trailing_armed: boolean // Runtime dynamic-protection trailing flag.
  remaining_move_budget: number // Remaining dynamic-protection move budget.
  has_scale_out_plan: boolean // Truth-layer flag showing whether a scale-out plan exists.
  scale_out_status: string // Current scale-out plan truth status.
  scale_out_consistency: ScaleOutConsistencyStatus // Scale-out consistency enum returned by the truth layer.
  has_state_mismatch: boolean // Reconcile result flag; true means truth layers disagree.
  mismatch_reasons: ScaleOutStateMismatchReason[] // Machine-readable mismatch explanation list.
  has_working_orders: boolean // Truth-layer flag derived from linked working scale-out rows.
  pending_add_qty: number // Truth-layer pending add quantity from the order truth layer; separate from scale-out reserves.
  pending_scale_out_qty: number // Truth-layer quantity reserved by active working levels.
  remaining_scale_out_qty: number // Truth-layer quantity still pending across the current plan.
  executed_scale_out_qty: number // Truth-layer cumulative executed quantity across the plan.
  scale_out_block_reasons: CapabilityReason[] // Capability-style reasons used by the UI and resolver preview.
  protection_rebalanced: boolean // Derived flag showing the protection coverage matches the current position.
  has_pending_cancel_replace: boolean // Truth-layer flag for any linked cancel/replace flow.
  user_stream_ready: boolean // Runtime readiness flag for the Binance user-stream bridge.
}

// ScaleOutReconcilePreview is the full read-only scale-out truth preview returned by the backend.
// It combines the scale-out plan truth layer, linked order evidence, and reconcile status.
export interface ScaleOutReconcilePreview extends ScaleOutConsistencyPreview, TruthSnapshotMetadata {
  generated_at: string // Response generation timestamp.
}
