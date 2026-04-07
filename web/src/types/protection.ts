import type { TruthSnapshotMetadata } from './runtime_capability'

// Protection truth preview types.
// These types model the Binance-only protection group truth layer and its read-only preview responses.

// ProtectionStateMismatchReason is a machine-readable protection reconcile explanation.
// It is debug output from the truth layer, not a raw exchange event.
export interface ProtectionStateMismatchReason {
  category: string // Mismatch bucket used by the backend.
  reason: string // Human-readable explanation.
  symbol?: string // Symbol scope for the mismatch.
  protection_group_id?: string // Protection group involved in the mismatch.
  order_role?: string // Registry role involved in the mismatch.
  local_intent_id?: string // Local intent id when available.
  exchange_order_id?: string // Exchange order id when available.
  client_order_id?: string // Exchange client id when available.
}

// ProtectionGroupPreview is the system-owned protection truth row returned by the backend.
// It is not an exchange order mirror; it represents the current fixed-protection snapshot.
export interface ProtectionGroupPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // One-way side scope.
  linked_position_key: string // System-owned position correlation key.
  protection_group_id: string // System-generated protection group identifier.
  protection_mode: string // Protection policy label; phase 3 only allows fixed.
  stop_loss_order_intent_id: string // System-local stop-loss intent id.
  take_profit_order_intent_id: string // System-local take-profit intent id.
  stop_loss_exchange_order_id: string // Exchange stop-loss order id when available.
  take_profit_exchange_order_id: string // Exchange take-profit order id when available.
  stop_loss_initial_trigger_price: number // Initial stop-loss trigger price before any movement.
  stop_loss_current_trigger_price: number // Current stop-loss trigger price after any movement.
  take_profit_initial_trigger_price: number // Initial take-profit trigger price before any movement.
  take_profit_current_trigger_price: number // Current take-profit trigger price after any movement.
  stop_loss_trigger_price: number // System-derived stop-loss trigger price.
  take_profit_trigger_price: number // System-derived take-profit trigger price.
  break_even_armed: boolean // Dynamic protection flag showing break-even is armed.
  trailing_armed: boolean // Dynamic protection flag showing segmented trailing is active.
  trailing_rule_id: string // System-owned trailing rule identifier.
  trailing_anchor_price: number // System-derived anchor price used by the latest trailing move.
  trailing_move_count: number // System-derived count of completed trailing moves.
  protection_revision: number // System-owned protection revision counter.
  last_protection_action: string // Last protection mutation action label.
  last_protection_action_at: string // Last protection mutation timestamp.
  protected_quantity: number // System-derived protected quantity.
  status: string // Protection group status.
  source: string // Truth source label.
  last_synced_at: string // Last exchange or reconciliation sync timestamp.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// ProtectionEventLogPreview is the append-only protection evidence row returned by the backend.
// It is raw evidence, not a reconciled protection snapshot.
export interface ProtectionEventLogPreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope attached to the event.
  protection_group_id: string // System-generated protection group identifier.
  event_type: string // Raw event type from manager/reconciler/bootstrap.
  payload_json: string // Raw JSON payload preserved for evidence/debugging.
  event_source: string // Source label.
  event_time: string // Raw event timestamp.
  created_at: string // Persistence timestamp.
}

// ProtectionPositionAggregatePreview is the system-owned position truth snapshot as seen by the protection layer.
// It is derived state, not a raw exchange mirror.
export interface ProtectionPositionAggregatePreview {
  id: number // System persistence row id.
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // One-way side scope.
  total_qty: number // System-derived net quantity.
  available_qty: number // System-derived quantity available for reduce/close.
  pending_add_qty: number // System-derived pending add quantity.
  pending_reduce_qty: number // System-derived pending reduce quantity.
  avg_entry_price: number // System-derived weighted entry price.
  realized_pnl: number // System-derived realized PnL.
  unrealized_pnl: number // System-derived unrealized PnL.
  peak_pnl_pct: number // System-derived peak PnL percent.
  has_protection: boolean // Protection truth flag.
  protection_mode: string // Protection policy label.
  protected_quantity: number // System-derived active protected quantity.
  protection_group_id: string // Current protection group identifier.
  stop_loss_armed: boolean // Stop-loss working flag.
  take_profit_armed: boolean // Take-profit working flag.
  protection_state_json: string // System-generated protection state JSON.
  scale_plan_state_json: string // System-generated scale plan JSON.
  execution_eligible: boolean // Runtime eligibility flag.
  last_reconciled_at: string // Last truth rebuild timestamp.
  created_at: string // Persistence timestamp.
  updated_at: string // Persistence timestamp.
}

// ProtectionOrderRegistrySummaryPreview is the order registry summary as consumed by protection preview panels.
// It is derived from the order truth layer, not the raw exchange order table.
export interface ProtectionOrderRegistrySummaryPreview {
  trader_id: string // System-owned trader identifier.
  symbol: string // Truth-layer symbol scope.
  side: string // Truth-layer side scope.
  has_working_orders: boolean // Working-order flag.
  has_pending_cancel_replace: boolean // Cancel/replace activity flag.
  pending_add_qty: number // Pending add quantity.
  pending_reduce_qty: number // Pending reduce quantity.
  protection_coverage_qty: number // Protected quantity coverage.
  working_order_count: number // Count of working rows.
  working_order_ids: string[] // Exchange order ids for working rows.
  pending_order_ids: string[] // Client or local intent ids for pending evidence rows.
  last_exchange_update_at: string // Latest exchange/user-stream timestamp in scope.
  execution_eligible: boolean // Execution readiness flag.
}

// ProtectionConsistencyPreview is the compact read-only protection consistency summary.
// It is used by the UI to show why fixed protection is open, blocked, or inconsistent.
export interface ProtectionConsistencyPreview {
  trader_id: string // System-owned trader identifier.
  selected_symbol?: string // Selected symbol scope.
  exchange: 'binance_usdm' // Fixed phase-3 exchange scope.
  mode: 'one_way' // Fixed phase-3 mode scope.
  has_protection: boolean // Protection truth-layer flag.
  protection_mode: string // Protection policy label.
  protection_group_status: string // Current protection truth status.
  protection_adjustment_status: string // Runtime dynamic-protection status label.
  protection_adjustment_consistency: string // Runtime dynamic-protection consistency label.
  protection_adjustment_block_reasons: Array<{
    action: string
    category: string
    reason: string
  }> // Machine-readable dynamic-protection block reasons.
  protection_revision: number // Runtime protection revision snapshot.
  current_stop_loss_price: number // Runtime current stop-loss trigger price.
  initial_stop_loss_price: number // Runtime initial stop-loss trigger price.
  break_even_armed: boolean // Runtime dynamic-protection break-even flag.
  trailing_armed: boolean // Runtime dynamic-protection trailing flag.
  remaining_move_budget: number // Remaining dynamic-protection move budget.
  has_pending_cancel_replace: boolean // Runtime truth flag for a pending cancel/replace flow.
  stop_loss_armed: boolean // Stop-loss leg working flag.
  take_profit_armed: boolean // Take-profit leg working flag.
  protected_quantity: number // Protection-layer coverage target for the current live position.
  protection_consistency: string // Debug label describing protection consistency.
  has_state_mismatch: boolean // Reconcile result flag; true means truth layers disagree.
  mismatch_reasons: ProtectionStateMismatchReason[] // Machine-readable mismatch explanation list.
  has_working_orders: boolean // Working-order truth flag.
  pending_add_qty: number // Pending add quantity from the order truth layer.
  pending_reduce_qty: number // Pending reduce quantity from the order truth layer.
  has_scale_out_plan: boolean // Runtime scale-out plan flag derived from the scale-out truth layer.
  scale_out_status: string // Runtime scale-out status used for capability clipping.
  scale_out_consistency: string // Runtime scale-out consistency label returned by the truth layer.
  has_scale_out_mismatch: boolean // Runtime mismatch flag for the scale-out truth layer.
  pending_scale_out_qty: number // System-derived quantity reserved by active working scale-out levels.
  remaining_scale_out_qty: number // System-derived quantity still pending across the current plan.
  executed_scale_out_qty: number // System-derived cumulative executed quantity across the current plan.
  scale_out_block_reasons: Array<{
    action: string
    category: string
    reason: string
  }> // Machine-readable scale-out block reasons for UI/debug.
  protection_rebalanced: boolean // Derived flag showing whether protection coverage matches the current position.
  user_stream_ready: boolean // Runtime readiness flag for the Binance user-stream bridge.
}

// ProtectionReconcilePreview is the full read-only protection truth preview returned by the backend.
// It combines the protection group truth layer, linked order evidence, and reconcile status.
export interface ProtectionReconcilePreview extends ProtectionConsistencyPreview, TruthSnapshotMetadata {
  protection_group?: ProtectionGroupPreview | null // Current protection group truth row.
  protection_groups: ProtectionGroupPreview[] // Current protection snapshot rows for debug.
  protection_event_logs: ProtectionEventLogPreview[] // Append-only raw protection evidence chain.
  position_aggregate?: ProtectionPositionAggregatePreview | null // System-owned position truth snapshot for the selected scope.
  order_registry_summary?: ProtectionOrderRegistrySummaryPreview | null // System-derived order truth summary for the selected scope.
  generated_at: string // Response generation timestamp.
}
