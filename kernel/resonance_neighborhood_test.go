package kernel

import (
	"math"
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
)

func TestEvaluateNeighborhoodAttributionDetectsWolfPackRadiusZone(t *testing.T) {
	currentVector := mustNeighborhoodVector(t, 1.5)
	baseTime := time.Now().UTC().UnixMilli()

	rows := make([]*store.ShadowSnapshot, 0, 5)
	for i := 0; i < 5; i++ {
		rows = append(rows, neighborhoodSnapshotRow(uint(i+1), baseTime+int64(i), 1.50+float64(i)*0.02, -0.12, "[REBEL_SON]"))
	}

	audit := EvaluateNeighborhoodAttribution(currentVector, rows, DefaultNeighborhoodRadius)
	if audit.SampleCount != 5 {
		t.Fatalf("expected 5 radius neighbors, got %d", audit.SampleCount)
	}
	if audit.SparseZone {
		t.Fatalf("expected verified radius zone, got %+v", audit)
	}

	adjustment := EvaluateNeighborhoodRiskAdjustment(0.0030, audit)
	if !adjustment.Blocked {
		t.Fatalf("expected wolf pack hard block, got %+v", adjustment)
	}
	if adjustment.BlockReason != "[Wolf Pack Detected]" {
		t.Fatalf("expected wolf pack reason, got %q", adjustment.BlockReason)
	}
	if adjustment.RiskDiscountFactor != 0 || adjustment.RiskAdjustedEV != 0 {
		t.Fatalf("expected zeroed adjustment, got %+v", adjustment)
	}
}

func TestEvaluateNeighborhoodAttributionAppliesSparseZoneDiscount(t *testing.T) {
	currentVector := mustNeighborhoodVector(t, 1.5)
	rows := []*store.ShadowSnapshot{
		neighborhoodSnapshotRow(1, time.Now().UTC().UnixMilli()+4, 1.49, 0.03, ""),
		neighborhoodSnapshotRow(2, time.Now().UTC().UnixMilli()+3, 1.52, -0.01, ""),
		neighborhoodSnapshotRow(3, time.Now().UTC().UnixMilli()+2, 1.55, 0.02, ""),
		neighborhoodSnapshotRow(4, time.Now().UTC().UnixMilli()+1, 1.58, 0.01, ""),
	}

	audit := EvaluateNeighborhoodAttribution(currentVector, rows, DefaultNeighborhoodRadius)
	if !audit.SparseZone {
		t.Fatalf("expected sparse zone, got %+v", audit)
	}
	if !audit.ShadowMonitorRequired {
		t.Fatalf("expected sparse zone to request shadow monitoring, got %+v", audit)
	}

	adjustment := EvaluateNeighborhoodRiskAdjustment(0.0030, audit)
	if adjustment.Blocked {
		t.Fatalf("expected sparse zone discount, got %+v", adjustment)
	}
	if math.Abs(adjustment.RiskDiscountFactor-0.50) > 1e-9 {
		t.Fatalf("expected 50%% sparse discount, got %.6f", adjustment.RiskDiscountFactor)
	}
	if math.Abs(adjustment.RiskAdjustedEV-0.0015) > 1e-9 {
		t.Fatalf("expected adjusted EV 0.0015, got %.6f", adjustment.RiskAdjustedEV)
	}
}

func TestEvaluateNeighborhoodAttributionHighNoiseZoneDiscountsToTenPercent(t *testing.T) {
	currentVector := mustNeighborhoodVector(t, 1.5)
	rows := make([]*store.ShadowSnapshot, 0, 12)
	baseTime := time.Now().UTC().UnixMilli()

	for i := 0; i < 4; i++ {
		rows = append(rows, neighborhoodSnapshotRow(uint(i+1), baseTime+int64(i), 1.48+float64(i)*0.03, 0.05, ""))
	}
	for i := 0; i < 8; i++ {
		rows = append(rows, neighborhoodSnapshotRow(uint(i+5), baseTime+20+int64(i), 1.50+float64(i)*0.02, -0.04, "[REBEL_SON]"))
	}

	audit := EvaluateNeighborhoodAttribution(currentVector, rows, DefaultNeighborhoodRadius)
	if audit.SampleCount != 12 {
		t.Fatalf("expected 12 radius neighbors, got %d", audit.SampleCount)
	}
	if math.Abs(audit.LossRate-(8.0/12.0)) > 1e-9 {
		t.Fatalf("expected loss rate 8/12, got %.6f", audit.LossRate)
	}

	adjustment := EvaluateNeighborhoodRiskAdjustment(0.0035, audit)
	if adjustment.Blocked {
		t.Fatalf("expected discounted high-noise zone, got %+v", adjustment)
	}
	if adjustment.Label != "高噪诱多区" {
		t.Fatalf("expected high noise label, got %q", adjustment.Label)
	}
	if math.Abs(adjustment.RiskDiscountFactor-0.10) > 1e-9 {
		t.Fatalf("expected 10%% discount factor, got %.6f", adjustment.RiskDiscountFactor)
	}
	if math.Abs(adjustment.RiskAdjustedEV-0.00035) > 1e-9 {
		t.Fatalf("expected adjusted EV 0.00035, got %.8f", adjustment.RiskAdjustedEV)
	}
	if ClearsRiskAdjustedEntryFloor(adjustment.RiskAdjustedEV, 10) {
		t.Fatalf("expected 0.035%% adjusted EV to fail 0.10%% entry floor, got %+v", adjustment)
	}
}

func mustNeighborhoodVector(t *testing.T, marketScore float64) []float64 {
	t.Helper()

	vector, ok := BuildResonanceFeatureVectorFromMarketData(&market.Data{
		HeatScore: &market.HeatScoreData{
			RawFactorScores: map[string]float64{
				"market": marketScore,
			},
			RawFactorAvailable: map[string]bool{
				"market": true,
			},
		},
	})
	if !ok {
		t.Fatal("expected feature vector to build")
	}
	return vector
}

func neighborhoodSnapshotRow(id uint, decisionTime int64, marketScore, returnPct float64, tag string) *store.ShadowSnapshot {
	rawFactors := store.ShadowRawFactors{
		Scores: map[string]float64{
			"market": marketScore,
		},
		Available: map[string]bool{
			"market": true,
		},
	}.MarshalText()

	return &store.ShadowSnapshot{
		ID:           id,
		DecisionTime: decisionTime,
		Filled:       true,
		ReturnPct:    returnPct,
		RawFactors:   rawFactors,
		KnnAuditTag:  tag,
	}
}
