package store

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func newPhase5Store(t *testing.T) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "phase5-store.sqlite")
	st, err := NewWithConfig(DBConfig{Type: DBTypeSQLite, Path: dbPath})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func TestPositionAggregateBuilder_ReadsScaleInState(t *testing.T) {
	st := newPhase5Store(t)
	traderID := "trader-phase5"
	symbol := "BTCUSDT"
	side := "LONG"
	positionKey := symbol + "|" + side
	now := time.Now().UTC()

	if err := st.Position().Create(&TraderPosition{
		TraderID:           traderID,
		ExchangeID:         "exchange-1",
		ExchangeType:       "binance",
		ExchangePositionID: "pos-1",
		Symbol:             symbol,
		Side:               side,
		Quantity:           1,
		EntryPrice:         65000,
		EntryTime:          now.UnixMilli(),
		CreatedAt:          now.UnixMilli(),
		UpdatedAt:          now.UnixMilli(),
	}); err != nil {
		t.Fatalf("seed position failed: %v", err)
	}

	plan, err := st.ScaleInPlanBuilder().ApplyScaleInEvent(&ScaleInPlanEventInput{
		TraderID:            traderID,
		Symbol:              symbol,
		Side:                side,
		LinkedPositionKey:   positionKey,
		ScaleInPlanID:       "si-phase5",
		PlanMode:            "fixed_qty",
		Status:              "armed",
		TotalPlannedQty:     0.2,
		RemainingPlannedQty: 0.2,
		ExecutedQty:         0,
		MaxScaleInCount:     2,
		CurrentScaleInCount: 0,
		Source:              "unit_test",
		EventType:           "SCALE_IN_PLAN_CREATED",
		EventSource:         "unit_test",
		PayloadJSON:         "{}",
		EventTime:           now,
		Levels: []*ScaleInPlanLevelInput{
			{
				TraderID:            traderID,
				Symbol:              symbol,
				Side:                side,
				LinkedPositionKey:   positionKey,
				ScaleInPlanID:       "si-phase5",
				LevelIndex:          1,
				TargetType:          "market",
				PlannedQty:          0.2,
				ExecutedQty:         0,
				RemainingQty:        0.2,
				LinkedOrderIntentID: "si-phase5-l1",
				Status:              "armed",
			},
		},
	})
	if err != nil {
		t.Fatalf("seed scale-in plan failed: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected scale-in plan")
	}

	aggregates, err := NewPositionAggregateBuilder(st).BuildForTrader(traderID, nil, nil)
	if err != nil {
		t.Fatalf("build aggregates failed: %v", err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("expected 1 aggregate, got %d", len(aggregates))
	}

	agg := aggregates[0]
	if !agg.HasScaleInPlan {
		t.Fatalf("expected aggregate to read scale-in plan state")
	}
	if agg.ScaleInPlanID != "si-phase5" {
		t.Fatalf("expected scale-in plan id si-phase5, got %s", agg.ScaleInPlanID)
	}
	if agg.ScaleInStatus != "armed" {
		t.Fatalf("expected armed scale-in status, got %s", agg.ScaleInStatus)
	}
	if diff := math.Abs(agg.PendingAddQty - 0.2); diff > 1e-9 {
		t.Fatalf("expected pending add qty 0.2, got %.12f", agg.PendingAddQty)
	}
	if diff := math.Abs(agg.RemainingScaleInQty - 0.2); diff > 1e-9 {
		t.Fatalf("expected remaining scale-in qty 0.2, got %.12f", agg.RemainingScaleInQty)
	}
	if agg.ScaleInCount != 0 {
		t.Fatalf("expected scale-in count 0, got %d", agg.ScaleInCount)
	}
	if !agg.ExecutionEligible {
		t.Fatalf("expected aggregate to remain execution eligible")
	}
	if agg.ScalePlanStateJSON == "" {
		t.Fatalf("expected scale plan state json to be populated")
	}
}
