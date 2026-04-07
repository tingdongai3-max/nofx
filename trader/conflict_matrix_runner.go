package trader

import (
	"fmt"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ConflictMatrixCase defines one phase-7 conflict validation scenario.
// It is validation-only and never enters the live execution path.
type ConflictMatrixCase struct {
	CaseID              string                      // Stable validation case identifier.
	TraderID            string                      // Trader scope used by the case.
	UserID              string                      // User scope used for capability preview rebuilding.
	SelectedSymbol      string                      // Symbol scope used by the case.
	ExecutionMode       string                      // Capability preview execution mode for the case.
	UserStreamReady     bool                        // User-stream readiness used during validation.
	AllowOrderPlacement bool                        // Runtime order-placement gate used during validation.
	EventSequence       []string                    // Human-readable event sequence summary.
	Run                 func(st *store.Store) error // Validation-only mutation sequence that drives the case.
}

// ConflictMatrixResult is the output of one long-chain conflict validation case.
// It is validation output only and does not change runtime permissions.
type ConflictMatrixResult struct {
	TruthSnapshotMetadata
	CaseID            string                     `json:"case_id"`                      // Stable validation case identifier.
	TraderID          string                     `json:"trader_id"`                    // System-owned trader key targeted by the case.
	SelectedSymbol    string                     `json:"selected_symbol,omitempty"`    // Selected symbol scope used by the case.
	EventSequence     []string                   `json:"event_sequence"`               // Human-readable validation event sequence.
	InitialSnapshot   *TruthSnapshotSummary      `json:"initial_snapshot,omitempty"`   // Truth snapshot before the case events run.
	FinalSnapshot     *TruthSnapshotSummary      `json:"final_snapshot,omitempty"`     // Truth snapshot after the case events run.
	CapabilityPreview *RuntimeCapabilityPreview  `json:"capability_preview,omitempty"` // Runtime capability result derived after the case.
	HasMismatch       bool                       `json:"has_mismatch"`                 // Validation flag showing that the case ended in inconsistency.
	MismatchReasons   []ValidationMismatchReason `json:"mismatch_reasons"`             // Machine-readable conflict mismatch explanations.
	GeneratedAt       time.Time                  `json:"generated_at"`                 // Validation result generation timestamp.
}

// ConflictMatrixRunner executes long-chain truth-layer conflict scenarios.
// It is validation-only and must not be wired into live trading execution.
type ConflictMatrixRunner struct {
	store *store.Store
}

// NewConflictMatrixRunner creates a conflict matrix runner.
func NewConflictMatrixRunner(st *store.Store) *ConflictMatrixRunner {
	return &ConflictMatrixRunner{store: st}
}

// RunCase executes one conflict-matrix scenario and returns the before/after truth snapshots.
func (r *ConflictMatrixRunner) RunCase(caseDef *ConflictMatrixCase) (*ConflictMatrixResult, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("conflict matrix runner store is not configured")
	}
	if caseDef == nil {
		return nil, fmt.Errorf("conflict matrix case is required")
	}
	if caseDef.CaseID == "" {
		return nil, fmt.Errorf("conflict matrix case ID is required")
	}
	if caseDef.TraderID == "" {
		return nil, fmt.Errorf("conflict matrix trader ID is required")
	}

	logger.Infof("conflict matrix case started: case_id=%s trader=%s symbol=%s", caseDef.CaseID, caseDef.TraderID, caseDef.SelectedSymbol)

	initialSnapshot, err := BuildTruthSnapshotSummary(r.store, caseDef.TraderID, caseDef.SelectedSymbol, caseDef.UserStreamReady)
	if err != nil {
		return nil, err
	}

	if caseDef.Run != nil {
		if err := caseDef.Run(r.store); err != nil {
			return nil, err
		}
	}

	finalSnapshot, err := BuildTruthSnapshotSummary(r.store, caseDef.TraderID, caseDef.SelectedSymbol, caseDef.UserStreamReady)
	if err != nil {
		return nil, err
	}

	orderPreview, err := BuildRuntimeOrderPreview(r.store, caseDef.TraderID, caseDef.SelectedSymbol, caseDef.UserStreamReady)
	if err != nil {
		return nil, err
	}
	scaleOutPreview, err := BuildRuntimeScaleOutPreview(r.store, caseDef.TraderID, caseDef.SelectedSymbol, caseDef.UserStreamReady)
	if err != nil {
		return nil, err
	}
	scaleInPreview, err := BuildRuntimeScaleInPreview(r.store, caseDef.TraderID, caseDef.SelectedSymbol, caseDef.UserStreamReady, 0)
	if err != nil {
		return nil, err
	}
	capabilityPreview, err := BuildRuntimeCapabilityPreview(
		r.store,
		caseDef.UserID,
		caseDef.TraderID,
		caseDef.SelectedSymbol,
		caseDef.ExecutionMode,
		nil,
		nil,
		orderPreview,
		scaleOutPreview,
		scaleInPreview,
		caseDef.AllowOrderPlacement,
		nil,
	)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	metadata := buildTruthSnapshotMetadata(now, "", false, "")
	initialSnapshot.TruthSnapshotMetadata = metadata
	finalSnapshot.TruthSnapshotMetadata = metadata
	capabilityPreview.TruthSnapshotMetadata = metadata

	mismatches := diffTruthSnapshots(initialSnapshot, finalSnapshot)
	mismatches = append(mismatches, collectValidationMismatchesFromRuntime(orderPreview, scaleOutPreview, scaleInPreview, capabilityPreview)...)
	if len(mismatches) > 0 {
		logger.Infof("conflict matrix mismatch found: case_id=%s trader=%s symbol=%s mismatch_count=%d", caseDef.CaseID, caseDef.TraderID, finalSnapshot.SelectedSymbol, len(mismatches))
	}

	return &ConflictMatrixResult{
		TruthSnapshotMetadata: metadata,
		CaseID:                caseDef.CaseID,
		TraderID:              caseDef.TraderID,
		SelectedSymbol:        finalSnapshot.SelectedSymbol,
		EventSequence:         append([]string(nil), caseDef.EventSequence...),
		InitialSnapshot:       initialSnapshot,
		FinalSnapshot:         finalSnapshot,
		CapabilityPreview:     capabilityPreview,
		HasMismatch:           len(mismatches) > 0,
		MismatchReasons:       mismatches,
		GeneratedAt:           now,
	}, nil
}
