// Dynamic protection preview types.
// These types model the Binance-only move-stop / break-even / segmented trailing truth layer and its read-only preview responses.

import type { CapabilityReason, TruthSnapshotMetadata } from './runtime_capability'
import type { OrderRegistrySummary } from './order_registry'
import type { ProtectionGroupPreview, ProtectionPositionAggregatePreview, ProtectionConsistencyPreview, ProtectionStateMismatchReason } from './protection'

// TrailingRulePreview is the system-owned rule row used to drive dynamic protection moves.
// It is a rule-layer truth row, not an exchange order mirror.
export interface TrailingRulePreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // One-way truth-layer side.
  linked_position_key: string // System-owned position correlation key.
  trailing_rule_id: string // System-generated trailing rule identifier.
  rule_mode: 'break_even' | 'step_trailing' // Rule policy label.
  activation_type: 'profit_r_multiple' | 'profit_pct' | 'price_level' | string // Activation type.
  activation_value: number // Activation threshold value.
  step_trigger_type: 'profit_r_multiple' | 'profit_pct' | 'price_level' | string // Step trigger type.
  step_trigger_value: number // Step trigger threshold value.
  step_move_type: 'price_offset' | 'price_pct' | 'price_level' | string // Step move type.
  step_move_value: number // Step move step value.
  max_move_count: number // Maximum allowed stop-loss moves.
  status: 'draft' | 'armed' | 'paused' | 'invalid' | 'closed' | string // Rule truth status.
  source: string // Truth source label.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// ProtectionAdjustmentEventLogPreview is the append-only evidence row for dynamic protection changes.
// It is raw evidence, not a reconciled snapshot.
export interface ProtectionAdjustmentEventLogPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope attached to the event.
  protection_group_id: string // System-generated protection group identifier.
  protection_revision: number // System-owned protection revision snapshot.
  event_type: string // Raw event type from the adjustment manager or reconciler.
  old_stop_loss_price: number // Previous stop-loss trigger price before the change.
  new_stop_loss_price: number // New stop-loss trigger price after the change.
  old_take_profit_price: number // Previous take-profit trigger price before the change.
  new_take_profit_price: number // New take-profit trigger price after the change.
  reason: string // Human-readable reason for the protection adjustment.
  payload_json: string // Raw JSON payload preserved for evidence/debugging.
  event_source: string // Source label.
  event_time: string // Raw event timestamp.
  created_at: string // Persistence timestamp.
}

// ProtectionAdjustmentGuardPreview is the execution-facing result for dynamic protection gating.
// It is a guard result, not a truth-layer snapshot.
export interface ProtectionAdjustmentGuardPreview {
  allowed: boolean // Runtime permit emitted by the guard.
  blocked_reasons: CapabilityReason[] // Machine-readable explanations for every blocked branch.
  can_arm_break_even: boolean // True when break-even can be armed right now.
  can_arm_trailing: boolean // True when segmented trailing can be armed right now.
  can_move_stop_loss: boolean // True when the current stop-loss may be moved right now.
  remaining_move_budget: number // Remaining stop-loss move budget under the current rule.
  current_stop_loss_price: number // System-derived current stop-loss price.
  initial_stop_loss_price: number // System-derived initial stop-loss price.
  protection_revision: number // System-owned protection revision snapshot.
  protection_group_status: string // Current protection lifecycle status.
  protection_consistency: string // Protection consistency enum.
  protection_adjustment_consistency: string // Dynamic-protection consistency enum.
  protection_adjustment_status: string // Dynamic protection status label.
  has_pending_cancel_replace: boolean // Runtime truth flag for a pending cancel/replace flow.
  trailing_rule_id: string // System-owned trailing-rule identifier.
}

// ScaleOutPlanSummary is the compact scale-out summary attached to dynamic protection previews.
// It is a derived truth summary, not a raw exchange record.
export interface ScaleOutPlanSummary {
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // Truth-layer side scope.
  linked_position_key: string // System-owned position correlation key.
  scale_out_plan_id: string // System-generated scale-out plan identifier.
  plan_mode: string // Plan-layer policy label.
  scale_out_status: string // Current plan truth status.
  has_scale_out_plan: boolean // Truth-layer flag showing whether an active plan exists.
  total_planned_qty: number // System-derived total quantity planned by the active plan.
  pending_scale_out_qty: number // System-derived working-order reserve for active scale-out levels.
  remaining_scale_out_qty: number // System-derived total quantity still outstanding across the active plan.
  executed_scale_out_qty: number // System-derived cumulative executed quantity.
  has_working_orders: boolean // Truth-layer flag that at least one scale-out level is still working.
  working_level_count: number // System-derived count of working levels.
  active_level_count: number // System-derived count of non-terminal levels.
  completed_level_count: number // System-derived count of terminal levels.
  consistency_status: string // Scale-out consistency label.
  has_state_mismatch: boolean // Reconcile flag showing the truth layers disagree.
  mismatch_reasons: ProtectionStateMismatchReason[] // Machine-readable mismatch explanation list.
  last_synced_at: string // Latest sync timestamp across the scale-out scope.
  execution_eligible: boolean // Runtime readiness flag; not a direct execution permit.
}

// ScaleInPlanSummary is the compact scale-in summary attached to dynamic protection previews.
// It is a derived truth summary, not a raw exchange record.
export interface ScaleInPlanSummary {
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // Truth-layer side scope.
  linked_position_key: string // System-owned position correlation key.
  scale_in_plan_id: string // System-generated scale-in plan identifier.
  plan_mode: string // Plan-layer policy label.
  scale_in_status: string // Current plan truth status.
  has_scale_in_plan: boolean // Truth-layer flag showing whether an active plan exists.
  total_planned_qty: number // System-derived total quantity planned by the active plan.
  pending_scale_in_qty: number // System-derived working-order reserve for active add levels.
  remaining_scale_in_qty: number // System-derived total quantity still outstanding across the active plan.
  executed_scale_in_qty: number // System-derived cumulative executed quantity.
  max_scale_in_count: number // Strategy-layer cap for how many add attempts are allowed.
  current_scale_in_count: number // System-derived count of executed add attempts.
  has_working_orders: boolean // Truth-layer flag that at least one scale-in level is still working.
  working_level_count: number // System-derived count of working levels.
  active_level_count: number // System-derived count of non-terminal levels.
  completed_level_count: number // System-derived count of terminal levels.
  consistency_status: string // Scale-in consistency label.
  has_state_mismatch: boolean // Reconcile flag showing the truth layers disagree.
  mismatch_reasons: ProtectionStateMismatchReason[] // Machine-readable mismatch explanation list.
  last_synced_at: string // Latest sync timestamp across the scale-in scope.
  execution_eligible: boolean // Runtime readiness flag; not a direct execution permit.
}

// ProtectionAdjustmentPreview is the full read-only dynamic protection truth preview returned by the backend.
// It combines the protection truth layer, trailing rule truth layer, evidence chain, and guard result.
export interface ProtectionAdjustmentPreview extends TruthSnapshotMetadata {
  trader_id: string // System-owned trader identifier.
  selected_symbol?: string // Selected symbol scope used for the preview.
  exchange: 'binance_usdm' // Fixed phase-6 exchange scope.
  mode: 'one_way' // Fixed phase-6 mode scope.
  protection_group?: ProtectionGroupPreview | null // Current protection truth row.
  protection_summary?: ProtectionConsistencyPreview | null // Current protection truth rollup.
  trailing_rule?: TrailingRulePreview | null // Current dynamic trailing rule truth row.
  protection_event_logs: ProtectionAdjustmentEventLogPreview[] // Append-only raw dynamic-protection evidence chain.
  order_registry_summary?: OrderRegistrySummary | null // System-derived order truth summary for the selected scope.
  scale_out_summary?: ScaleOutPlanSummary | null // System-derived scale-out truth summary used for conflict detection.
  scale_in_summary?: ScaleInPlanSummary | null // System-derived scale-in truth summary used for conflict detection.
  position_aggregate?: ProtectionPositionAggregatePreview | null // System-owned position truth snapshot for the selected scope.
  has_protection: boolean // System-derived flag that an active protection group exists.
  protection_mode: string // Current protection policy label.
  protection_group_status: string // Current protection truth status.
  protection_consistency: string // Protection consistency enum: consistent, mismatch, pending, or unknown.
  protection_adjustment_consistency: string // Dynamic-protection consistency enum: consistent, mismatch, pending, or unknown.
  has_state_mismatch: boolean // Reconcile result flag; true means truth layers disagree.
  mismatch_reasons: ProtectionStateMismatchReason[] // Machine-readable mismatch explanation list.
  has_working_orders: boolean // Truth-layer flag derived from linked working protection rows.
  has_pending_cancel_replace: boolean // Truth-layer flag showing that the linked stop-loss leg is in cancel/replace.
  stop_loss_armed: boolean // System-derived stop-loss leg working flag.
  take_profit_armed: boolean // System-derived take-profit leg working flag.
  break_even_armed: boolean // System-derived flag showing the protection has moved to break-even.
  trailing_armed: boolean // System-derived flag showing that segmented trailing is active.
  trailing_rule_id: string // System-owned trailing rule identifier.
  trailing_anchor_price: number // System-derived anchor price used by the latest trailing move.
  trailing_move_count: number // System-derived count of completed trailing moves and break-even moves.
  protection_revision: number // System-owned protection revision counter.
  last_protection_action: string // Last protection mutation action label.
  last_protection_action_at: string // Last protection mutation timestamp.
  initial_stop_loss_price: number // Initial stop-loss trigger price before any movement.
  current_stop_loss_price: number // Current stop-loss trigger price after any movement.
  initial_take_profit_price: number // Initial take-profit trigger price before any movement.
  current_take_profit_price: number // Current take-profit trigger price after any movement.
  current_market_price: number // Best-effort current market price used for trigger evaluation.
  current_pnl_pct: number // Best-effort current unrealized PnL percent used for trigger evaluation.
  remaining_move_budget: number // Remaining stop-loss move budget under the current trailing rule.
  protection_adjustment_block_reasons: CapabilityReason[] // Machine-readable dynamic-protection block reasons.
  guard_assessment?: ProtectionAdjustmentGuardPreview | null // Execution-facing guard result used for debug/UI review.
  can_arm_break_even: boolean // Guard-derived flag showing break-even may be armed now.
  can_arm_trailing: boolean // Guard-derived flag showing trailing may be armed now.
  can_move_stop_loss: boolean // Guard-derived flag showing the current stop-loss may be moved now.
  protection_adjustment_status: string // Guard-derived dynamic protection status label.
  has_scale_out_plan: boolean // Truth-layer flag showing whether a scale-out plan exists.
  scale_out_status: string // Current scale-out plan truth status.
  scale_out_consistency: string // Scale-out consistency enum: consistent, mismatch, pending, or unknown.
  has_scale_out_mismatch: boolean // Reconcile flag showing that the scale-out truth layer disagrees.
  pending_scale_out_qty: number // Truth-layer working-order reserve for active scale-out levels.
  remaining_scale_out_qty: number // Truth-layer total quantity still outstanding across the current scale-out plan.
  executed_scale_out_qty: number // Truth-layer cumulative executed quantity across the current scale-out plan.
  has_scale_in_plan: boolean // Truth-layer flag showing whether a scale-in plan exists.
  scale_in_status: string // Current scale-in plan truth status.
  scale_in_consistency: string // Scale-in consistency enum: consistent, mismatch, pending, or unknown.
  has_scale_in_mismatch: boolean // Reconcile flag showing that the scale-in truth layer disagrees.
  pending_scale_in_qty: number // Truth-layer working-order reserve for active scale-in levels.
  remaining_scale_in_qty: number // Truth-layer total quantity still outstanding across the current scale-in plan.
  executed_scale_in_qty: number // Truth-layer cumulative executed quantity across the current scale-in plan.
  user_stream_ready: boolean // Runtime readiness flag for the Binance user-stream bridge.
  generated_at: string // Response generation timestamp.
}
