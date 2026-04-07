import type { RuntimeCapabilityPreview, TruthSnapshotMetadata } from './runtime_capability'
import type { PositionAggregatePreview } from './runtime_capability'
import type { OrderRegistrySummary } from './order_registry'
import type { ProtectionConsistencyPreview } from './protection'
import type { ScaleOutPlanSummary, ScaleInPlanSummary } from './protection_adjustment'

export interface ValidationMismatchReason {
  category: string
  reason: string
  field?: string
  symbol?: string
  expected?: string
  actual?: string
}

export interface TruthSnapshotSummaryPreview extends TruthSnapshotMetadata {
  trader_id: string
  selected_symbol?: string
  exchange: 'binance_usdm'
  mode: 'one_way'
  position_aggregate?: PositionAggregatePreview | null
  order_summary?: OrderRegistrySummary | null
  protection_summary?: ProtectionConsistencyPreview | null
  scale_out_summary?: ScaleOutPlanSummary | null
  scale_in_summary?: ScaleInPlanSummary | null
  generated_at: string
}

export interface ReplayEventPreview {
  index: number
  event_type: string
  label?: string
  event_source?: string
  event_time: string
  payload_json: string
}

export interface ReplayPreview extends TruthSnapshotMetadata {
  fixture_id: string
  fixture_version: string
  replay_mode: string
  event_count: number
  trader_id: string
  selected_symbol?: string
  event_sequence: ReplayEventPreview[]
  truth_snapshot?: TruthSnapshotSummaryPreview | null
  capability_preview?: RuntimeCapabilityPreview | null
  has_mismatch: boolean
  mismatch_reasons: ValidationMismatchReason[]
  generated_at: string
}

export interface RestorePreview extends TruthSnapshotMetadata {
  trader_id: string
  selected_symbol?: string
  restore_scope: string
  exchange: 'binance_usdm'
  mode: 'one_way'
  before_snapshot?: TruthSnapshotSummaryPreview | null
  after_snapshot?: TruthSnapshotSummaryPreview | null
  capability_preview?: RuntimeCapabilityPreview | null
  has_mismatch: boolean
  mismatch_reasons: ValidationMismatchReason[]
  generated_at: string
}

export interface ConflictMatrixPreview extends TruthSnapshotMetadata {
  case_id: string
  trader_id: string
  selected_symbol?: string
  event_sequence: string[]
  initial_snapshot?: TruthSnapshotSummaryPreview | null
  final_snapshot?: TruthSnapshotSummaryPreview | null
  capability_preview?: RuntimeCapabilityPreview | null
  has_mismatch: boolean
  mismatch_reasons: ValidationMismatchReason[]
  generated_at: string
}

export interface MigrationRegressionPreview extends TruthSnapshotMetadata {
  fixture_id: string
  phase: string
  fixture_version: string
  validation_trader_id?: string
  selected_symbol?: string
  truth_snapshot?: TruthSnapshotSummaryPreview | null
  capability_preview?: RuntimeCapabilityPreview | null
  has_mismatch: boolean
  mismatch_reasons: ValidationMismatchReason[]
  generated_at: string
}
