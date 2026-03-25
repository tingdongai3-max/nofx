package kernel

import (
	"math"
	"sort"
	"strings"

	"nofx/store"
)

const (
	DefaultNeighborhoodRadius         = 0.8
	sparseNeighborhoodSampleFloor     = 5
	ratedNeighborhoodSampleFloor      = 3
	severeNeighborhoodLossReturnFloor = -0.03
	neighborhoodDistanceMetricEuclid  = "euclidean"
	neighborhoodDistanceMetricMahalan = "mahalanobis"
)

type NeighborhoodAuditResult struct {
	Radius                float64
	DistanceMetric        string
	SampleCount           int
	WinCount              int
	LossCount             int
	NeutralCount          int
	WinRate               float64
	LossRate              float64
	TopNeighborReturnPct  float64
	TopNeighborDistance   float64
	MostSimilarIsWinner   bool
	RebelAnchorCount      int
	SevereLossCount       int
	SparseZone            bool
	ShadowMonitorRequired bool
	Label                 string
}

func EvaluateNeighborhoodAttribution(currentVector []float64, rows []*store.ShadowSnapshot, radius float64) NeighborhoodAuditResult {
	return EvaluateNeighborhoodAttributionInArchetype(currentVector, rows, nil, nil, radius)
}

func EvaluateNeighborhoodAttributionInArchetype(currentVector []float64, rows []*store.ShadowSnapshot, library *FeatureArchetypeLibrary, selected *FeatureArchetype, radius float64) NeighborhoodAuditResult {
	if radius <= 0 {
		radius = DefaultNeighborhoodRadius
	}

	result := NeighborhoodAuditResult{
		Radius:         radius,
		DistanceMetric: neighborhoodDistanceMetricEuclid,
		Label:          "Sparse Zone",
		SparseZone:     true,
	}
	if len(currentVector) == 0 || len(rows) == 0 {
		result.ShadowMonitorRequired = true
		return result
	}

	type neighborSample struct {
		row        *store.ShadowSnapshot
		distance   float64
		returnPct  float64
		isRebel    bool
		decisionTs int64
	}

	samples := make([]neighborSample, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Filled || !isFiniteScore(row.ReturnPct) {
			continue
		}
		vector, ok := BuildResonanceFeatureVectorFromSnapshot(row)
		if !ok {
			continue
		}
		if selected != nil && library != nil && !belongsToArchetypeDomain(row, vector, library, selected) {
			continue
		}
		distance, metric, ok := neighborhoodDistance(currentVector, vector, selected)
		if !ok {
			continue
		}
		if distance > radius {
			continue
		}
		result.DistanceMetric = metric
		isRebel := isRebelSonSnapshot(row)
		if isRebel {
			result.RebelAnchorCount++
		}
		samples = append(samples, neighborSample{
			row:        row,
			distance:   distance,
			returnPct:  row.ReturnPct,
			isRebel:    isRebel,
			decisionTs: row.DecisionTime,
		})
	}

	if len(samples) == 0 {
		result.ShadowMonitorRequired = true
		return result
	}

	sort.SliceStable(samples, func(i, j int) bool {
		if samples[i].distance != samples[j].distance {
			return samples[i].distance < samples[j].distance
		}
		if samples[i].isRebel != samples[j].isRebel {
			return samples[i].isRebel
		}
		if samples[i].decisionTs != samples[j].decisionTs {
			return samples[i].decisionTs > samples[j].decisionTs
		}
		if samples[i].row != nil && samples[j].row != nil && samples[i].row.ID != samples[j].row.ID {
			return samples[i].row.ID > samples[j].row.ID
		}
		return false
	})

	result.SampleCount = len(samples)
	for _, sample := range samples {
		switch {
		case sample.returnPct > 0:
			result.WinCount++
		case sample.returnPct < 0 || sample.isRebel:
			result.LossCount++
		default:
			result.NeutralCount++
		}
		if sample.returnPct < severeNeighborhoodLossReturnFloor {
			result.SevereLossCount++
		}
	}
	if result.SampleCount >= ratedNeighborhoodSampleFloor {
		result.WinRate = float64(result.WinCount) / float64(result.SampleCount)
		result.LossRate = float64(result.LossCount) / float64(result.SampleCount)
	}

	top := samples[0]
	result.TopNeighborDistance = top.distance
	result.TopNeighborReturnPct = top.returnPct
	result.MostSimilarIsWinner = top.returnPct > 0

	if result.SampleCount < sparseNeighborhoodSampleFloor {
		result.ShadowMonitorRequired = true
		return result
	}

	result.SparseZone = false
	result.Label = "Radius Neighborhood"
	return result
}

func belongsToArchetypeDomain(row *store.ShadowSnapshot, vector []float64, library *FeatureArchetypeLibrary, selected *FeatureArchetype) bool {
	if selected == nil || library == nil || len(library.Archetypes) == 0 {
		return true
	}
	if row != nil {
		explicitLabel := strings.TrimSpace(row.ArchetypeLabel)
		if explicitLabel != "" {
			return strings.EqualFold(explicitLabel, strings.TrimSpace(selected.ID)) ||
				strings.EqualFold(explicitLabel, strings.TrimSpace(selected.Name))
		}
	}
	best := nearestArchetypeForVector(vector, library)
	if best == nil || best.Archetype == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(best.Archetype.ID), strings.TrimSpace(selected.ID))
}

func nearestArchetypeForVector(vector []float64, library *FeatureArchetypeLibrary) *FeatureArchetypeMatch {
	if len(vector) == 0 || library == nil || len(library.Archetypes) == 0 {
		return nil
	}
	match := &FeatureArchetypeMatch{}
	bestSet := false
	for _, archetype := range library.Archetypes {
		if archetype == nil {
			continue
		}
		distance, ok := archetype.Distance(vector)
		if !ok {
			continue
		}
		if !bestSet || distance < match.Distance {
			bestSet = true
			match.Distance = distance
			match.Archetype = archetype
		}
	}
	if !bestSet {
		return nil
	}
	return match
}

func neighborhoodDistance(currentVector, candidateVector []float64, _ *FeatureArchetype) (float64, string, bool) {
	distance, ok := euclideanDistance(currentVector, candidateVector)
	return distance, neighborhoodDistanceMetricEuclid, ok
}

func euclideanDistance(a, b []float64) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}

	sum := 0.0
	for i := range a {
		if math.IsNaN(a[i]) || math.IsInf(a[i], 0) || math.IsNaN(b[i]) || math.IsInf(b[i], 0) {
			return 0, false
		}
		diff := a[i] - b[i]
		sum += diff * diff
	}
	if sum < 0 {
		sum = 0
	}
	return math.Sqrt(sum), true
}

func isRebelSonSnapshot(row *store.ShadowSnapshot) bool {
	if row == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(row.KnnAuditTag), "[REBEL_SON]") {
		return true
	}
	return row.ReturnPct <= severeNeighborhoodLossReturnFloor
}
