// Order truth preview types.
// These types model the Binance-only order registry, event evidence chain, and reconcile preview.

import type { ProtectionStateMismatchReason } from './protection'
import type { CapabilityReason, TruthSnapshotMetadata } from './runtime_capability'

// OrderRegistryEntry is the system-owned order truth row returned by the backend.
// It is not a raw exchange order mirror; the values are the reconciled truth layer used by previews.
export interface OrderRegistryEntry {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // One-way truth-layer side: LONG or SHORT.
  position_side_mode: string // Binance futures position mode; fixed to one_way in this scope.
  order_role: string // System-derived role: entry, reduce, stop_loss, take_profit, trailing_protection, cancel_replace.
  local_intent_id: string // System-local intent id used for recovery and correlation.
  linked_group_id: string // System-local group id for related orders.
  linked_position_key: string // System-local position key used by the truth layer.
  exchange_order_id: string // Exchange raw order id when available.
  client_order_id: string // Exchange raw client order id when available.
  order_type: string // Exchange raw order type.
  time_in_force: string // Exchange raw time-in-force.
  reduce_only: boolean // Exchange raw reduce-only flag.
  close_position: boolean // Exchange raw close-position flag.
  orig_qty: number // Exchange raw original quantity.
  executed_qty: number // System-tracked executed quantity.
  remaining_qty: number // System-derived remaining quantity.
  avg_price: number // Exchange raw average fill price when available.
  trigger_price: number // Exchange raw trigger price.
  activation_price: number // Exchange raw activation price.
  callback_rate: number // Exchange raw callback rate.
  status: string // Exchange/state status after reconciliation.
  source: string // Truth source label: user_stream, local_submit, exchange_snapshot, bootstrap.
  is_working: boolean // System-derived working-order flag.
  last_exchange_update_at: string // Latest exchange/user-stream timestamp.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// OrderEventLogPreview is the raw event evidence row returned by the backend.
// It is append-only evidence, not a reconciled state snapshot.
export interface OrderEventLogPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope attached to the event.
  local_intent_id: string // System-local intent id used for correlation.
  exchange_order_id: string // Exchange raw order id when available.
  event_type: string // Raw event type from user stream or bootstrap.
  event_source: string // Event source label.
  payload_json: string // Raw JSON payload preserved for evidence/debugging.
  event_time: string // Raw event timestamp.
  created_at: string // Persistence timestamp.
}

// OrderStateMismatchReason is a machine-readable reconcile explanation.
// It is not a raw exchange event and should only be used for preview/debugging.
export interface OrderStateMismatchReason {
  category: string // Mismatch bucket used by the backend.
  reason: string // Human-readable explanation.
  symbol?: string // Symbol scope for the mismatch.
  order_role?: string // Registry role involved in the mismatch.
  local_intent_id?: string // Local intent id when available.
  exchange_order_id?: string // Exchange order id when available.
  client_order_id?: string // Exchange client id when available.
}

// ScaleOutBlockReason is the read-only capability clipping reason derived from the scale-out truth layer.
// It is debug output for the UI, not an execution command.
export interface ScaleOutBlockReason {
  action: string // Runtime-clipped action name.
  category: string // Block classification used by the backend.
  reason: string // Human-readable explanation.
}

// OrderRegistrySummary is the system-derived rollup of registry rows for a symbol/side.
// It is the summary used by capability clipping and the order truth preview panel.
export interface OrderRegistrySummary {
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // Truth-layer side scope.
  has_working_orders: boolean // System-derived working-order flag.
  has_pending_cancel_replace: boolean // System-derived indicator for an active cancel_replace flow.
  pending_add_qty: number // System-derived quantity reserved by working entry orders.
  pending_reduce_qty: number // System-derived quantity reserved by working reduce/protection orders.
  protection_coverage_qty: number // System-derived protected quantity coverage.
  working_order_count: number // System-derived count of working rows.
  working_order_ids: string[] // Exchange order ids for the working rows.
  pending_order_ids: string[] // Client or local intent ids for pending evidence rows.
  last_exchange_update_at: string // Latest exchange/user-stream timestamp in the summary scope.
  execution_eligible: boolean // System-derived execution readiness flag; not a direct order permit.
}

// OrderReconcilePreview is the read-only order truth preview returned by the backend.
// It combines the registry truth layer, recent evidence, and reconcile status.
export interface OrderReconcilePreview extends TruthSnapshotMetadata {
  trader_id: string // System-owned trader identifier.
  selected_symbol?: string // Selected symbol used for summary and mismatch detection.
  exchange: 'binance_usdm' // Fixed phase-2 exchange scope.
  mode: 'one_way' // Fixed phase-2 mode scope.
  order_registry: OrderRegistryEntry[] // System-owned registry rows, not the raw exchange table.
  order_event_logs: OrderEventLogPreview[] // Append-only raw event evidence chain.
  has_protection: boolean // System-derived flag showing whether a protection group is present.
  protection_mode: string // System-derived protection policy label.
  protection_group_status: string // Current protection group truth status.
  protection_adjustment_status: string // Runtime dynamic-protection status label.
  protection_adjustment_consistency: string // Runtime dynamic-protection consistency label.
  protection_adjustment_block_reasons: CapabilityReason[] // Machine-readable dynamic-protection block reasons.
  protection_revision: number // Runtime protection revision snapshot.
  current_stop_loss_price: number // Runtime current stop-loss trigger price.
  initial_stop_loss_price: number // Runtime initial stop-loss trigger price.
  break_even_armed: boolean // Runtime dynamic-protection break-even flag.
  trailing_armed: boolean // Runtime dynamic-protection trailing flag.
  remaining_move_budget: number // Remaining dynamic-protection move budget.
  has_pending_cancel_replace: boolean // Runtime truth flag for a pending cancel/replace flow.
  stop_loss_armed: boolean // System-derived stop-loss leg working flag.
  take_profit_armed: boolean // System-derived take-profit leg working flag.
  protection_consistency: string // Debug label describing protection truth consistency.
  protection_block_reasons: ProtectionStateMismatchReason[] // Machine-readable protection mismatch explanation list.
  reconcile_status: string // Backend reconcile status label.
  has_working_orders: boolean // Truth-layer flag derived from registry rows.
  pending_add_qty: number // Truth-layer pending add quantity.
  pending_reduce_qty: number // Truth-layer pending reduce quantity.
  has_scale_out_plan: boolean // Truth-layer flag showing whether a scale-out plan exists.
  scale_out_status: string // Current scale-out plan truth status.
  scale_out_consistency: string // Scale-out consistency enum: consistent, mismatch, pending, or unknown.
  has_scale_out_mismatch: boolean // Truth-layer mismatch flag for the scale-out state.
  pending_scale_out_qty: number // Truth-layer quantity reserved by active scale-out levels.
  remaining_scale_out_qty: number // Truth-layer quantity still pending across the current plan.
  executed_scale_out_qty: number // Truth-layer cumulative executed quantity across the current plan.
  scale_out_block_reasons: ScaleOutBlockReason[] // Machine-readable scale-out block reasons for UI/debug.
  protection_rebalanced: boolean // Derived flag showing whether protection coverage matches the current position.
  has_state_mismatch: boolean // Reconcile result flag; true means registry and exchange disagree.
  mismatch_reasons: OrderStateMismatchReason[] // Machine-readable mismatch explanation list.
  user_stream_ready: boolean // Runtime readiness flag for the Binance user-stream bridge.
  generated_at: string // Response generation timestamp.
  local_summary?: OrderRegistrySummary | null // System-derived summary used by preview and capability clipping.
}
