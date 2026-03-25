package kernel

import (
	"math"
	"nofx/logger"
	"nofx/store"
	"time"
)

type EntryFloorOptimizationResult struct {
	SampleCount  int       `json:"sample_count"`
	Floor        float64   `json:"floor"`
	PeakFloor    int       `json:"peak_floor"`
	PeakScore    float64   `json:"peak_score"`
	PlateauStart int       `json:"plateau_start"`
	PlateauEnd   int       `json:"plateau_end"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// OptimizeEntryFloor recalculates the adaptive entry floor from filled shadow snapshots.
func OptimizeEntryFloor(st *store.Store) (EntryFloorOptimizationResult, error) {
	result := EntryFloorOptimizationResult{
		Floor:     defaultAdaptiveEntryFloorForOptimization(),
		UpdatedAt: time.Now().UTC(),
	}
	if st == nil || st.Shadow() == nil {
		return result, nil
	}

	currentConfig, err := st.GetResonanceGuardConfig()
	if err != nil {
		return result, err
	}
	result.Floor = currentConfig.AdaptiveEntryFloor

	rows, err := st.Shadow().ListPerformanceSnapshotsByTrader("", true)
	if err != nil {
		return result, err
	}
	if len(rows) == 0 {
		return result, nil
	}

	curve := make([]float64, 61)
	for threshold := 0; threshold <= 60; threshold++ {
		sum := 0.0
		count := 0
		cutoff := float64(threshold)
		for _, row := range rows {
			if row == nil || !isFiniteFloat(row.ReturnPct) {
				continue
			}
			if entryResonanceScore(row, currentConfig.AdaptiveEntryLambda) < cutoff {
				continue
			}
			sum += row.ReturnPct
			count++
		}
		if count == 0 {
			curve[threshold] = math.NaN()
			continue
		}
		curve[threshold] = sum / float64(count)
	}

	selection := selectEntryFloorFromCurve(curve)
	if selection.PeakIndex < 0 {
		return result, nil
	}

	currentConfig.AdaptiveEntryFloor = float64(selection.SelectedFloor)
	if err := st.SetResonanceGuardConfig(currentConfig); err != nil {
		return result, err
	}

	result.SampleCount = len(rows)
	result.Floor = float64(selection.SelectedFloor)
	result.PeakFloor = selection.PeakIndex
	result.PeakScore = selection.PeakScore
	result.PlateauStart = selection.PlateauStart
	result.PlateauEnd = selection.PlateauEnd
	result.UpdatedAt = time.Now().UTC()

	logger.Infof("V3_AUDIT_ENTRY_FLOOR: rows=%d peak=%d peak_score=%.4f plateau=[%d,%d] selected=%d",
		result.SampleCount,
		result.PeakFloor,
		result.PeakScore,
		result.PlateauStart,
		result.PlateauEnd,
		selection.SelectedFloor,
	)

	return result, nil
}

func defaultAdaptiveEntryFloorForOptimization() float64 {
	return 45.0
}

func entryResonanceScore(row *store.ShadowSnapshot, lambda float64) float64 {
	if row == nil {
		return 0
	}

	values := []float64{
		row.HeatScore,
		row.TradingSub,
		row.QuantSub,
		row.MarketFactor,
		row.TrendFactor,
		row.DonchianFactor,
		row.VolumeSpikeFactor,
		row.MTFResonanceFactor,
		row.QuantFactor,
		row.SocialFactor,
		row.OnChainFactor,
	}

	factors := make([]float64, 0, len(values))
	for _, value := range values {
		if isFiniteFloat(value) {
			factors = append(factors, value)
		}
	}
	if len(factors) == 0 {
		return 0
	}
	return CalculateResonanceScore(factors, nil, lambda)
}

type entryFloorSelection struct {
	PeakIndex     int
	PeakScore     float64
	PlateauStart  int
	PlateauEnd    int
	SelectedFloor int
}

func selectEntryFloorFromCurve(values []float64) entryFloorSelection {
	selection := entryFloorSelection{
		PeakIndex:     -1,
		PeakScore:     math.NaN(),
		PlateauStart:  -1,
		PlateauEnd:    -1,
		SelectedFloor: -1,
	}

	peakIndex, peakScore := highestFiniteValue(values)
	if peakIndex < 0 {
		return selection
	}

	plateauThreshold := peakScore - (math.Abs(peakScore) * 0.15)
	start, end := peakIndex, peakIndex
	for start-1 >= 0 && isFiniteFloat(values[start-1]) && values[start-1] >= plateauThreshold {
		start--
	}
	for end+1 < len(values) && isFiniteFloat(values[end+1]) && values[end+1] >= plateauThreshold {
		end++
	}

	selectedFloor := (start + end) / 2
	if selectedFloor < 0 {
		selectedFloor = 0
	}
	if selectedFloor > 60 {
		selectedFloor = 60
	}

	selection.PeakIndex = peakIndex
	selection.PeakScore = peakScore
	selection.PlateauStart = start
	selection.PlateauEnd = end
	selection.SelectedFloor = selectedFloor
	return selection
}

func highestFiniteValue(values []float64) (int, float64) {
	peakIndex := -1
	peakValue := math.Inf(-1)
	for index, value := range values {
		if !isFiniteFloat(value) {
			continue
		}
		if value > peakValue {
			peakValue = value
			peakIndex = index
		}
	}
	if peakIndex < 0 {
		return -1, math.NaN()
	}
	return peakIndex, peakValue
}

func isFiniteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
