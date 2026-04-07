package trader

import (
	"fmt"
	"time"

	"nofx/logger"
	"nofx/store"
)

// RestorePreview is the phase-7 cold-start validation result returned by the restore preview API.
// It is validation output and does not open new trading capabilities.
type RestorePreview struct {
	TruthSnapshotMetadata
	TraderID          string                     `json:"trader_id"`                    // System-owned trader key targeted by restore validation.
	SelectedSymbol    string                     `json:"selected_symbol,omitempty"`    // Selected symbol scope used for restore validation.
	RestoreScope      string                     `json:"restore_scope"`                // Human-readable restore scope identifier.
	Exchange          string                     `json:"exchange"`                     // Fixed validation scope: binance_usdm.
	Mode              string                     `json:"mode"`                         // Fixed validation scope: one_way.
	BeforeSnapshot    *TruthSnapshotSummary      `json:"before_snapshot,omitempty"`    // Truth snapshot before the restore rebuild.
	AfterSnapshot     *TruthSnapshotSummary      `json:"after_snapshot,omitempty"`     // Truth snapshot after the restore rebuild.
	CapabilityPreview *RuntimeCapabilityPreview  `json:"capability_preview,omitempty"` // Runtime capability result derived after restore.
	HasMismatch       bool                       `json:"has_mismatch"`                 // Validation flag showing that restore changed the truth layer unexpectedly.
	MismatchReasons   []ValidationMismatchReason `json:"mismatch_reasons"`             // Machine-readable restore mismatch explanations.
	GeneratedAt       time.Time                  `json:"generated_at"`                 // Restore preview generation timestamp.
}

// TruthLayerRestoreManager validates that a trader's truth layers can be rebuilt after restart without contradiction.
// It is validation-only and does not add new trading behavior.
type TruthLayerRestoreManager struct {
	store *store.Store
}

// NewTruthLayerRestoreManager creates a restore validation manager.
func NewTruthLayerRestoreManager(st *store.Store) *TruthLayerRestoreManager {
	return &TruthLayerRestoreManager{store: st}
}

// RestoreTraderTruth rebuilds the trader truth layers and compares snapshots before and after the rebuild.
func (m *TruthLayerRestoreManager) RestoreTraderTruth(userID, traderID, selectedSymbol, executionMode string, userStreamReady, allowOrderPlacement bool) (*RestorePreview, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("truth layer restore manager store is not configured")
	}
	if traderID == "" {
		return nil, fmt.Errorf("trader ID is required")
	}

	logger.Infof("truth layer restore started: trader=%s symbol=%s execution_mode=%s user_stream_ready=%v", traderID, selectedSymbol, executionMode, userStreamReady)

	beforeSnapshot, err := BuildTruthSnapshotSummary(m.store, traderID, selectedSymbol, userStreamReady)
	if err != nil {
		return nil, err
	}

	if _, err := store.NewPositionAggregateBuilder(m.store).BuildForTrader(traderID, nil, nil); err != nil {
		return nil, err
	}

	orderPreview, err := BuildRuntimeOrderPreview(m.store, traderID, selectedSymbol, userStreamReady)
	if err != nil {
		return nil, err
	}
	scaleOutPreview, err := NewScaleOutStateReconciler(m.store, nil).SyncTraderScaleOutState(traderID, selectedSymbol, nil, userStreamReady)
	if err != nil {
		return nil, err
	}
	scaleInPreview, err := NewScaleInStateReconciler(m.store, nil).SyncTraderScaleInState(traderID, selectedSymbol, nil, userStreamReady)
	if err != nil {
		return nil, err
	}

	capabilityPreview, err := BuildRuntimeCapabilityPreview(
		m.store,
		userID,
		traderID,
		selectedSymbol,
		executionMode,
		nil,
		nil,
		orderPreview,
		scaleOutPreview,
		scaleInPreview,
		allowOrderPlacement,
		nil,
	)
	if err != nil {
		return nil, err
	}

	afterSnapshot, err := BuildTruthSnapshotSummary(m.store, traderID, selectedSymbol, userStreamReady)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	metadata := buildTruthSnapshotMetadata(now, "", true, "")
	beforeSnapshot.TruthSnapshotMetadata = metadata
	afterSnapshot.TruthSnapshotMetadata = metadata
	capabilityPreview.TruthSnapshotMetadata = metadata
	mismatches := diffTruthSnapshots(beforeSnapshot, afterSnapshot)

	quantityStatus := "unknown"
	quantityReasons := 0
	if afterSnapshot.QuantityAudit != nil {
		quantityStatus = afterSnapshot.QuantityAudit.Status
		quantityReasons = len(afterSnapshot.QuantityAudit.Reasons)
	}
	logger.Infof("truth layer restore completed: trader=%s symbol=%s mismatch_count=%d quantity_status=%s quantity_reasons=%d", traderID, afterSnapshot.SelectedSymbol, len(mismatches), quantityStatus, quantityReasons)

	return &RestorePreview{
		TruthSnapshotMetadata: metadata,
		TraderID:              traderID,
		SelectedSymbol:        afterSnapshot.SelectedSymbol,
		RestoreScope:          fmt.Sprintf("trader:%s symbol:%s", traderID, afterSnapshot.SelectedSymbol),
		Exchange:              "binance_usdm",
		Mode:                  "one_way",
		BeforeSnapshot:        beforeSnapshot,
		AfterSnapshot:         afterSnapshot,
		CapabilityPreview:     capabilityPreview,
		HasMismatch:           len(mismatches) > 0,
		MismatchReasons:       mismatches,
		GeneratedAt:           now,
	}, nil
}
