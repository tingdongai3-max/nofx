package store

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newPhase4Store(t *testing.T) *Store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "phase4-store.sqlite")
	st, err := NewWithConfig(DBConfig{Type: DBTypeSQLite, Path: dbPath})
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return st
}

func TestPositionAggregateBuilder_ReadsScaleOutState(t *testing.T) {
	st := newPhase4Store(t)
	traderID := "trader-phase4"
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

	plan, err := st.ScaleOutPlanBuilder().ApplyScaleOutEvent(&ScaleOutPlanEventInput{
		TraderID:            traderID,
		Symbol:              symbol,
		Side:                side,
		LinkedPositionKey:   positionKey,
		ScaleOutPlanID:      "so-phase4",
		PlanMode:            "fixed_ratio",
		Status:              "armed",
		TotalPlannedQty:     1,
		RemainingPlannedQty: 0.8,
		ExecutedQty:         0.2,
		Source:              "unit_test",
		EventType:           "SCALE_OUT_PLAN_CREATED",
		EventSource:         "unit_test",
		PayloadJSON:         "{}",
		EventTime:           now,
		Levels: []*ScaleOutPlanLevelInput{
			{
				TraderID:           traderID,
				Symbol:             symbol,
				Side:               side,
				LinkedPositionKey:  positionKey,
				ScaleOutPlanID:     "so-phase4",
				LevelIndex:         1,
				TargetType:         "limit_price",
				TargetPrice:        66000,
				PlannedQty:         0.4,
				ExecutedQty:        0,
				RemainingQty:       0.4,
				LinkedOrderIntentID:"so-phase4-l1",
				Status:             "armed",
			},
			{
				TraderID:           traderID,
				Symbol:             symbol,
				Side:               side,
				LinkedPositionKey:  positionKey,
				ScaleOutPlanID:     "so-phase4",
				LevelIndex:         2,
				TargetType:         "reduce_market",
				TargetPrice:        67000,
				PlannedQty:         0.6,
				ExecutedQty:        0.2,
				RemainingQty:       0.4,
				LinkedOrderIntentID:"so-phase4-l2",
				Status:             "partially_filled",
			},
		},
	})
	if err != nil {
		t.Fatalf("seed scale-out plan failed: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected scale-out plan")
	}

	aggregates, err := NewPositionAggregateBuilder(st).BuildForTrader(traderID, nil, nil)
	if err != nil {
		t.Fatalf("build aggregates failed: %v", err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("expected 1 aggregate, got %d", len(aggregates))
	}

	agg := aggregates[0]
	if !agg.HasScaleOutPlan {
		t.Fatalf("expected aggregate to read scale-out plan state")
	}
	if agg.ScaleOutPlanID != "so-phase4" {
		t.Fatalf("expected scale-out plan id so-phase4, got %s", agg.ScaleOutPlanID)
	}
	if agg.ScaleOutStatus != "armed" {
		t.Fatalf("expected armed scale-out status, got %s", agg.ScaleOutStatus)
	}
	if diff := math.Abs(agg.PendingScaleOutQty - 0.8); diff > 1e-9 {
		t.Fatalf("expected pending scale-out qty 0.8, got %.12f", agg.PendingScaleOutQty)
	}
	if diff := math.Abs(agg.ExecutedScaleOutQty - 0.2); diff > 1e-9 {
		t.Fatalf("expected executed scale-out qty 0.2, got %.12f", agg.ExecutedScaleOutQty)
	}
	if diff := math.Abs(agg.RemainingScaleOutQty - 0.8); diff > 1e-9 {
		t.Fatalf("expected remaining scale-out qty 0.8, got %.12f", agg.RemainingScaleOutQty)
	}
	if diff := math.Abs(agg.AvailableQty - 0.2); diff > 1e-9 {
		t.Fatalf("expected available qty 0.2, got %.12f", agg.AvailableQty)
	}
	if !strings.Contains(agg.ScalePlanStateJSON, `"scale_out_status":"armed"`) {
		t.Fatalf("expected scale plan JSON to include armed status, got %s", agg.ScalePlanStateJSON)
	}
}
