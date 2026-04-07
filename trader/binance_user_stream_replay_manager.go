package trader

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
	tradertypes "nofx/trader/types"
)

// ReplayEventPreview is the read-only event summary returned by the replay preview API.
// It is replay-state evidence, not a live execution command.
type ReplayEventPreview struct {
	Index       int       `json:"index"`                  // Replay sequence index after mode expansion.
	EventType   string    `json:"event_type"`             // Raw replay event type.
	Label       string    `json:"label,omitempty"`        // Human-readable fixture label.
	EventSource string    `json:"event_source,omitempty"` // Replay event source label.
	EventTime   time.Time `json:"event_time"`             // Event timestamp preserved from the fixture payload when available.
	PayloadJSON string    `json:"payload_json"`           // Raw event payload preserved for audit/debugging.
}

// ReplayPreview is the validation replay result returned by the phase-7 replay bus.
// It is validation output only and does not grant live execution permission.
type ReplayPreview struct {
	TruthSnapshotMetadata
	FixtureID         string                     `json:"fixture_id"`                   // Stable replay fixture identifier.
	FixtureVersion    string                     `json:"fixture_version"`              // Replay fixture contract version.
	ReplayMode        string                     `json:"replay_mode"`                  // Validation replay mode: sequential, out_of_order, duplicate, delayed.
	EventCount        int                        `json:"event_count"`                  // Expanded replay event count after mode transformation.
	TraderID          string                     `json:"trader_id"`                    // System-owned trader key targeted by the replay.
	SelectedSymbol    string                     `json:"selected_symbol,omitempty"`    // Selected symbol scope used to build the replay snapshot.
	EventSequence     []ReplayEventPreview       `json:"event_sequence"`               // Replay event sequence preserved for audit/debugging.
	TruthSnapshot     *TruthSnapshotSummary      `json:"truth_snapshot,omitempty"`     // Current truth snapshot after replay completion.
	CapabilityPreview *RuntimeCapabilityPreview  `json:"capability_preview,omitempty"` // Runtime capability result derived after replay completion.
	HasMismatch       bool                       `json:"has_mismatch"`                 // Validation flag showing that replay output is inconsistent.
	MismatchReasons   []ValidationMismatchReason `json:"mismatch_reasons"`             // Machine-readable replay mismatch explanations.
	GeneratedAt       time.Time                  `json:"generated_at"`                 // Replay preview generation timestamp.
}

type replayFixturePayload struct {
	TraderID            string                      `json:"trader_id"`
	SelectedSymbol      string                      `json:"selected_symbol"`
	ExecutionMode       string                      `json:"execution_mode"`
	AllowOrderPlacement bool                        `json:"allow_order_placement"`
	UserStreamReady     bool                        `json:"user_stream_ready"`
	ExchangeOpenOrders  []tradertypes.OpenOrder     `json:"exchange_open_orders"`
	Events              []replayFixtureEventPayload `json:"events"`
}

type replayFixtureEventPayload struct {
	EventType   string          `json:"event_type"`
	EventSource string          `json:"event_source"`
	Label       string          `json:"label"`
	Payload     json.RawMessage `json:"payload"`
	EventTime   time.Time       `json:"event_time"`
}

type staticReplayOpenOrderReader struct {
	orders map[string][]tradertypes.OpenOrder
}

func (r staticReplayOpenOrderReader) GetOpenOrders(symbol string) ([]tradertypes.OpenOrder, error) {
	return append([]tradertypes.OpenOrder(nil), r.orders[strings.ToUpper(strings.TrimSpace(symbol))]...), nil
}

// BinanceUserStreamReplayManager replays recorded Binance USDⓈ-M user-stream events into the existing truth layers.
// It is validation-only and must not be used as a live execution path.
type BinanceUserStreamReplayManager struct {
	store *store.Store
}

// NewBinanceUserStreamReplayManager creates a replay manager.
func NewBinanceUserStreamReplayManager(st *store.Store) *BinanceUserStreamReplayManager {
	return &BinanceUserStreamReplayManager{store: st}
}

// RunFixture replays one stored fixture using the requested replay mode.
func (m *BinanceUserStreamReplayManager) RunFixture(fixtureID, replayMode string) (*ReplayPreview, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("binance user stream replay manager store is not configured")
	}
	fixtureID = strings.TrimSpace(fixtureID)
	if fixtureID == "" {
		return nil, fmt.Errorf("fixture ID is required")
	}

	fixtureStore, err := openPhase7ValidationFixtureStore()
	if err != nil {
		return nil, err
	}
	fixture, err := fixtureStore.ReplayFixture().GetByFixtureID(fixtureID)
	if err != nil {
		return nil, err
	}
	if fixture == nil {
		fixture, err = m.store.ReplayFixture().GetByFixtureID(fixtureID)
		if err != nil {
			return nil, err
		}
		if fixture == nil {
			return nil, fmt.Errorf("replay fixture %s not found", fixtureID)
		}
	}

	var payload replayFixturePayload
	if err := json.Unmarshal([]byte(fixture.PayloadJSON), &payload); err != nil {
		return nil, fmt.Errorf("failed to parse replay fixture payload: %w", err)
	}
	if payload.TraderID == "" {
		return nil, fmt.Errorf("replay fixture %s is missing trader_id", fixtureID)
	}

	replayMode = normalizeReplayMode(replayMode)
	expandedEvents := expandReplayEvents(payload.Events, replayMode)
	logger.Infof("user stream replay started: fixture_id=%s replay_mode=%s trader=%s symbol=%s event_count=%d", fixtureID, replayMode, payload.TraderID, strings.ToUpper(strings.TrimSpace(payload.SelectedSymbol)), len(expandedEvents))

	reconcileMgr := NewBinanceOrderReconcileManager(m.store)
	reconcileMgr.SetUserStreamReady(payload.UserStreamReady)
	eventSequence := make([]ReplayEventPreview, 0, len(expandedEvents))
	for index, event := range expandedEvents {
		switch strings.ToUpper(strings.TrimSpace(event.EventType)) {
		case "ORDER_TRADE_UPDATE":
			if _, err := reconcileMgr.HandleOrderTradeUpdate(payload.TraderID, event.Payload); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported replay event type %q", event.EventType)
		}

		eventSequence = append(eventSequence, ReplayEventPreview{
			Index:       index + 1,
			EventType:   event.EventType,
			Label:       strings.TrimSpace(event.Label),
			EventSource: chooseNonEmpty(strings.TrimSpace(event.EventSource), "replay_fixture"),
			EventTime:   chooseEventTime(event.EventTime),
			PayloadJSON: strings.TrimSpace(string(event.Payload)),
		})
	}

	if _, err := store.NewPositionAggregateBuilder(m.store).BuildForTrader(payload.TraderID, nil, nil); err != nil {
		return nil, err
	}

	reader := buildReplayOpenOrderReader(payload.ExchangeOpenOrders)
	orderPreview, err := NewOrderStateReconciler(m.store).ReconcileTraderOrders(payload.TraderID, payload.SelectedSymbol, reader, reconcileMgr.UserStreamReady())
	if err != nil {
		return nil, err
	}
	scaleOutPreview, err := NewScaleOutStateReconciler(m.store, nil).SyncTraderScaleOutState(payload.TraderID, payload.SelectedSymbol, nil, orderPreview.UserStreamReady)
	if err != nil {
		return nil, err
	}
	scaleInPreview, err := NewScaleInStateReconciler(m.store, nil).SyncTraderScaleInState(payload.TraderID, payload.SelectedSymbol, nil, orderPreview.UserStreamReady)
	if err != nil {
		return nil, err
	}

	traderRow, err := m.store.Trader().GetByID(payload.TraderID)
	if err != nil {
		return nil, err
	}
	capabilityPreview, err := BuildRuntimeCapabilityPreview(
		m.store,
		traderRow.UserID,
		payload.TraderID,
		payload.SelectedSymbol,
		chooseNonEmpty(strings.TrimSpace(payload.ExecutionMode), "readonly"),
		nil,
		nil,
		orderPreview,
		scaleOutPreview,
		scaleInPreview,
		payload.AllowOrderPlacement,
		nil,
	)
	if err != nil {
		return nil, err
	}

	snapshot, err := BuildTruthSnapshotSummary(m.store, payload.TraderID, payload.SelectedSymbol, orderPreview.UserStreamReady)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	metadata := buildTruthSnapshotMetadata(now, fixtureID, false, "")
	snapshot.TruthSnapshotMetadata = metadata
	capabilityPreview.TruthSnapshotMetadata = metadata
	mismatches := collectValidationMismatchesFromRuntime(orderPreview, scaleOutPreview, scaleInPreview, capabilityPreview)
	hasMismatch := len(mismatches) > 0

	quantityStatus := "unknown"
	quantityReasons := 0
	if snapshot.QuantityAudit != nil {
		quantityStatus = snapshot.QuantityAudit.Status
		quantityReasons = len(snapshot.QuantityAudit.Reasons)
	}
	logger.Infof("user stream replay completed: fixture_id=%s replay_mode=%s trader=%s symbol=%s event_count=%d mismatch_count=%d quantity_status=%s quantity_reasons=%d",
		fixtureID, replayMode, payload.TraderID, snapshot.SelectedSymbol, len(eventSequence), len(mismatches), quantityStatus, quantityReasons)

	return &ReplayPreview{
		TruthSnapshotMetadata: metadata,
		FixtureID:             fixture.FixtureID,
		FixtureVersion:        fixture.Version,
		ReplayMode:            replayMode,
		EventCount:            len(eventSequence),
		TraderID:              payload.TraderID,
		SelectedSymbol:        snapshot.SelectedSymbol,
		EventSequence:         eventSequence,
		TruthSnapshot:         snapshot,
		CapabilityPreview:     capabilityPreview,
		HasMismatch:           hasMismatch,
		MismatchReasons:       mismatches,
		GeneratedAt:           now,
	}, nil
}

func normalizeReplayMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "sequential":
		return "sequential"
	case "out_of_order", "duplicate", "delayed":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "sequential"
	}
}

func expandReplayEvents(events []replayFixtureEventPayload, replayMode string) []replayFixtureEventPayload {
	expanded := append([]replayFixtureEventPayload(nil), events...)
	switch replayMode {
	case "out_of_order":
		for left, right := 0, len(expanded)-1; left < right; left, right = left+1, right-1 {
			expanded[left], expanded[right] = expanded[right], expanded[left]
		}
	case "duplicate":
		duplicated := make([]replayFixtureEventPayload, 0, len(expanded)*2)
		for _, event := range expanded {
			duplicated = append(duplicated, event, event)
		}
		expanded = duplicated
	case "delayed":
		if len(expanded) > 1 {
			first := expanded[0]
			expanded = append(expanded[1:], first)
		}
	}
	return expanded
}

func buildReplayOpenOrderReader(openOrders []tradertypes.OpenOrder) openOrderReader {
	if len(openOrders) == 0 {
		return nil
	}
	grouped := make(map[string][]tradertypes.OpenOrder)
	for _, order := range openOrders {
		symbol := strings.ToUpper(strings.TrimSpace(order.Symbol))
		grouped[symbol] = append(grouped[symbol], order)
	}
	return staticReplayOpenOrderReader{orders: grouped}
}

func chooseEventTime(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now().UTC()
	}
	return ts.UTC()
}

func collectValidationMismatchesFromRuntime(orderPreview *OrderReconcilePreview, scaleOutPreview *ScaleOutReconcilePreview, scaleInPreview *ScaleInReconcilePreview, capabilityPreview *RuntimeCapabilityPreview) []ValidationMismatchReason {
	reasons := make([]ValidationMismatchReason, 0)

	appendReason := func(category, reason, field, symbol, expected, actual string) {
		reasons = append(reasons, ValidationMismatchReason{
			Category: category,
			Reason:   reason,
			Field:    field,
			Symbol:   symbol,
			Expected: expected,
			Actual:   actual,
		})
	}

	if orderPreview != nil {
		for _, mismatch := range orderPreview.MismatchReasons {
			appendReason("order_state", mismatch.Reason, mismatch.OrderRole, mismatch.Symbol, "", mismatch.ExchangeOrderID)
		}
	}
	if scaleOutPreview != nil {
		for _, mismatch := range scaleOutPreview.MismatchReasons {
			appendReason("scale_out", mismatch.Reason, mismatch.ScaleOutPlanID, mismatch.Symbol, "", mismatch.ExchangeOrderID)
		}
	}
	if scaleInPreview != nil {
		for _, mismatch := range scaleInPreview.MismatchReasons {
			appendReason("scale_in", mismatch.Reason, mismatch.ScaleInPlanID, mismatch.Symbol, "", mismatch.ExchangeOrderID)
		}
	}
	if capabilityPreview != nil {
		for _, mismatch := range capabilityPreview.ProtectionBlockReasons {
			appendReason("protection", mismatch.Reason, mismatch.ProtectionGroupID, mismatch.Symbol, "", mismatch.ExchangeOrderID)
		}
	}

	return reasons
}
