package kernel

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"nofx/market"
	"nofx/store"
)

const (
	DefaultMahalanobisThreshold          = 2.0
	defaultResonanceMahalanobisThreshold = DefaultMahalanobisThreshold
	defaultResonanceMahalanobisAlpha     = 0.5
	defaultResonanceMahalanobisFloor     = 45.0
	defaultResonanceMahalanobisAnchor    = 100.0
	resonanceFeatureSampleLimit          = 200
	resonanceFeatureMinimumPositiveRows  = 20
	coreFeatureArchetypeCount            = 3
	coreFeatureArchetypeIterations       = 12
	promotedArchetypePrefix              = "Type_N"
)

var resonanceFeatureNames = []string{
	"market",
	"trend",
	"donchian_factor",
	"volume_spike",
	"mtf_resonance",
	"quant_oi",
	"quant_imbalance",
	"quant_netflow",
	"social_rank",
	"social_upvote",
	"onchain_ratio",
	"onchain_buy_ratio",
	"orderbook_imbalance",
	"vol_utilization",
	"funding_rate",
}

type FeatureArchetype struct {
	ID                  string
	Name                string
	Description         string
	FeatureNames        []string
	Mean                []float64
	InvCov              [][]float64
	SampleCount         int
	PositiveSampleCount int
	AverageReturnPct    float64
	MaxMarginFraction   float64
	EntryEVScale        float64
	Promoted            bool
	UpdatedAt           time.Time
}

type FeatureArchetypeLibrary struct {
	FeatureNames []string
	Archetypes   []*FeatureArchetype
	UpdatedAt    time.Time
}

type ResonanceFeatureCluster struct {
	FeatureNames        []string
	Mean                []float64
	InvCov              [][]float64
	SampleCount         int
	PositiveSampleCount int
	UpdatedAt           time.Time
}

type resonanceFeatureSource struct {
	values    map[string]float64
	available map[string]bool
}

type archetypeTrainingSample struct {
	vector    []float64
	returnPct float64
}

func BuildResonanceFeatureVectorFromMarketData(data *market.Data) ([]float64, bool) {
	source := resonanceFeatureSource{
		values:    make(map[string]float64),
		available: make(map[string]bool),
	}
	if data == nil {
		return nil, false
	}

	if data.HeatScore != nil {
		for key, value := range data.HeatScore.RawFactorScores {
			source.values[key] = sanitizeResonanceFeatureValue(value)
		}
		for key, value := range data.HeatScore.RawFactorAvailable {
			source.available[key] = value
		}
	}

	if isFiniteResonanceFeature(data.VolatilityUtilization) && data.VolatilityUtilization > 0 {
		source.values["vol_utilization"] = sanitizeResonanceFeatureValue(data.VolatilityUtilization * 100)
		source.available["vol_utilization"] = true
	}

	if isFiniteResonanceFeature(data.FundingRate) && data.FundingRate != 0 {
		source.values["funding_rate"] = sanitizeResonanceFeatureValue(data.FundingRate * 10000)
		source.available["funding_rate"] = true
	}

	if data.Orderbook != nil && isFiniteResonanceFeature(data.Orderbook.Imbalance) {
		source.values["orderbook_imbalance"] = sanitizeResonanceFeatureValue(data.Orderbook.Imbalance * 100)
		source.available["orderbook_imbalance"] = true
	}

	vector := buildResonanceFeatureVector(source)
	return vector, len(vector) > 0
}

func BuildResonanceFeatureVectorFromSnapshot(row *store.ShadowSnapshot) ([]float64, bool) {
	vector := buildResonanceFeatureVector(buildResonanceFeatureSourceFromSnapshot(row))
	return vector, len(vector) > 0
}

func BuildResonanceFeatureClusterFromSnapshots(rows []*store.ShadowSnapshot) *ResonanceFeatureCluster {
	if len(rows) == 0 {
		return nil
	}

	positiveRows := make([]*store.ShadowSnapshot, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Filled || row.ReturnPct <= 0 || excludesSnapshotFromCoreArchetypes(row) {
			continue
		}
		positiveRows = append(positiveRows, row)
	}
	if len(positiveRows) < resonanceFeatureMinimumPositiveRows {
		return nil
	}

	sort.SliceStable(positiveRows, func(i, j int) bool {
		if positiveRows[i].DecisionTime == positiveRows[j].DecisionTime {
			return positiveRows[i].ID > positiveRows[j].ID
		}
		return positiveRows[i].DecisionTime > positiveRows[j].DecisionTime
	})
	if len(positiveRows) > resonanceFeatureSampleLimit {
		positiveRows = positiveRows[:resonanceFeatureSampleLimit]
	}

	samples := make([][]float64, 0, len(positiveRows))
	for _, row := range positiveRows {
		vector, ok := BuildResonanceFeatureVectorFromSnapshot(row)
		if !ok {
			continue
		}
		samples = append(samples, vector)
	}
	cluster := buildResonanceFeatureCluster(resonanceFeatureNames, samples)
	if cluster == nil {
		return nil
	}
	cluster.SampleCount = len(rows)
	cluster.PositiveSampleCount = len(samples)
	return cluster
}

func BuildFeatureArchetypeLibraryFromSnapshots(rows []*store.ShadowSnapshot) *FeatureArchetypeLibrary {
	if len(rows) == 0 {
		return nil
	}

	coreSamples := collectCoreArchetypeSamples(rows)
	archetypes := make([]*FeatureArchetype, 0, coreFeatureArchetypeCount+4)
	if len(coreSamples) >= resonanceFeatureMinimumPositiveRows {
		archetypes = append(archetypes, buildCoreFeatureArchetypes(coreSamples)...)
	}
	archetypes = append(archetypes, buildPromotedFeatureArchetypes(rows)...)
	if len(archetypes) == 0 {
		return nil
	}

	sort.SliceStable(archetypes, func(i, j int) bool {
		return featureArchetypeSortKey(archetypes[i]) < featureArchetypeSortKey(archetypes[j])
	})

	return &FeatureArchetypeLibrary{
		FeatureNames: append([]string(nil), resonanceFeatureNames...),
		Archetypes:   archetypes,
		UpdatedAt:    time.Now().UTC(),
	}
}

func ApplyResonanceMahalanobisPenalty(baseScore float64, vector []float64, cluster *ResonanceFeatureCluster, alpha, threshold float64) (float64, float64, bool) {
	if cluster == nil || baseScore <= 0 {
		return baseScore, 0, false
	}

	if math.IsNaN(alpha) || math.IsInf(alpha, 0) || alpha <= 0 {
		alpha = defaultResonanceMahalanobisAlpha
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 {
		threshold = DefaultMahalanobisThreshold
	}

	allowed, distance, _ := ResonanceHardGateStatus(vector, cluster, threshold)
	if !allowed {
		return 0, distance, true
	}
	return baseScore, distance, false
}

func IsStatisticallyResonant(vector []float64, cluster *ResonanceFeatureCluster) bool {
	if cluster == nil {
		return false
	}
	allowed, _, _ := ResonanceHardGateStatus(vector, cluster, DefaultMahalanobisThreshold)
	return allowed
}

func ResonanceHardGateStatus(vector []float64, cluster *ResonanceFeatureCluster, threshold float64) (bool, float64, string) {
	if cluster == nil {
		return true, 0, ""
	}

	distance, ok := cluster.Distance(vector)
	if !ok {
		return true, 0, ""
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold <= 0 {
		threshold = DefaultMahalanobisThreshold
	}

	if resonanceVolatilityAccelerationBlocked(vector, cluster) {
		return false, distance, "Volatility acceleration > 100%"
	}
	if distance > threshold {
		return false, distance, fmt.Sprintf("D > %.1f", threshold)
	}
	return true, distance, ""
}

func IsResonanceVolatilityAccelerationBlocked(vector []float64, cluster *ResonanceFeatureCluster) bool {
	return resonanceVolatilityAccelerationBlocked(vector, cluster)
}

func BuildResonanceSnapshotFilter(rows []*store.ShadowSnapshot, floor, alpha, threshold float64) func(*store.ShadowSnapshot, float64) bool {
	cluster := BuildResonanceFeatureClusterFromSnapshots(rows)
	if cluster == nil {
		return nil
	}
	if math.IsNaN(alpha) || math.IsInf(alpha, 0) || alpha <= 0 {
		alpha = defaultResonanceMahalanobisAlpha
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 {
		threshold = DefaultMahalanobisThreshold
	}
	if math.IsNaN(floor) || math.IsInf(floor, 0) || floor < 0 {
		floor = defaultResonanceMahalanobisFloor
	}

	return func(row *store.ShadowSnapshot, score float64) bool {
		if row == nil || !isFiniteResonanceFeature(score) || score < floor {
			return false
		}
		vector, ok := BuildResonanceFeatureVectorFromSnapshot(row)
		if !ok {
			return false
		}
		final, distance, _ := ApplyResonanceMahalanobisPenalty(score, vector, cluster, alpha, threshold)
		return distance <= threshold && final >= floor
	}
}

func BuildResonanceFeatureClusterFromVectors(featureNames []string, samples [][]float64) *ResonanceFeatureCluster {
	return buildResonanceFeatureCluster(featureNames, samples)
}

func (a *FeatureArchetype) Distance(vector []float64) (float64, bool) {
	if a == nil {
		return 0, false
	}
	return mahalanobisDistanceWithInverseCovariance(vector, a.Mean, a.InvCov)
}

func (a *FeatureArchetype) Cluster() *ResonanceFeatureCluster {
	if a == nil {
		return nil
	}
	return &ResonanceFeatureCluster{
		FeatureNames:        append([]string(nil), a.FeatureNames...),
		Mean:                append([]float64(nil), a.Mean...),
		InvCov:              cloneSquareMatrix(a.InvCov),
		SampleCount:         a.SampleCount,
		PositiveSampleCount: a.PositiveSampleCount,
		UpdatedAt:           a.UpdatedAt,
	}
}

func (c *ResonanceFeatureCluster) Distance(vector []float64) (float64, bool) {
	if c == nil {
		return 0, false
	}
	return mahalanobisDistanceWithInverseCovariance(vector, c.Mean, c.InvCov)
}

func mahalanobisDistanceWithInverseCovariance(vector, mean []float64, invCov [][]float64) (float64, bool) {
	if len(mean) == 0 || len(invCov) == 0 || len(vector) != len(mean) {
		return 0, false
	}

	diff := make([]float64, len(vector))
	for i := range vector {
		if math.IsNaN(vector[i]) || math.IsInf(vector[i], 0) {
			return 0, false
		}
		diff[i] = vector[i] - mean[i]
	}

	quadratic := 0.0
	for i := range diff {
		row := invCov[i]
		if len(row) != len(diff) {
			return 0, false
		}
		for j := range diff {
			quadratic += diff[i] * row[j] * diff[j]
		}
	}
	if quadratic < 0 {
		quadratic = 0
	}
	return math.Sqrt(quadratic), true
}

func buildResonanceFeatureCluster(featureNames []string, samples [][]float64) *ResonanceFeatureCluster {
	return buildResonanceFeatureClusterWithMinimumSamples(featureNames, samples, resonanceFeatureMinimumPositiveRows)
}

func buildResonanceFeatureClusterWithMinimumSamples(featureNames []string, samples [][]float64, minimumSamples int) *ResonanceFeatureCluster {
	if len(featureNames) == 0 || len(samples) == 0 {
		return nil
	}
	if minimumSamples <= 0 {
		minimumSamples = 1
	}

	cleaned := make([][]float64, 0, len(samples))
	for _, sample := range samples {
		if len(sample) != len(featureNames) {
			continue
		}

		vector := make([]float64, len(sample))
		valid := false
		for i, value := range sample {
			if !isFiniteResonanceFeature(value) {
				vector = nil
				break
			}
			vector[i] = sanitizeResonanceFeatureValue(value)
			if vector[i] != 0 {
				valid = true
			}
		}
		if vector == nil || !valid {
			continue
		}
		cleaned = append(cleaned, vector)
	}
	if len(cleaned) < minimumSamples {
		return nil
	}
	if minimumSamples >= resonanceFeatureMinimumPositiveRows && len(cleaned) > resonanceFeatureSampleLimit {
		cleaned = cleaned[:resonanceFeatureSampleLimit]
	}

	mean := computeVectorMean(cleaned)
	covariance := computeVectorCovariance(cleaned, mean)
	if minimumSamples < resonanceFeatureMinimumPositiveRows {
		covariance = stabilizeCovarianceDiagonal(covariance, 1.0)
	}
	invCov, ok := invertRegularizedMatrix(covariance)
	if !ok {
		invCov = invertDiagonalCovariance(covariance)
	}
	if len(invCov) == 0 {
		return nil
	}

	return &ResonanceFeatureCluster{
		FeatureNames:        append([]string(nil), featureNames...),
		Mean:                mean,
		InvCov:              invCov,
		SampleCount:         len(cleaned),
		PositiveSampleCount: len(cleaned),
		UpdatedAt:           time.Now().UTC(),
	}
}

func resonanceVolatilityAccelerationBlocked(vector []float64, cluster *ResonanceFeatureCluster) bool {
	if cluster == nil || len(vector) == 0 || len(cluster.Mean) == 0 {
		return false
	}

	featureIndex := resonanceFeatureIndex(cluster.FeatureNames, "vol_utilization")
	if featureIndex < 0 || featureIndex >= len(vector) || featureIndex >= len(cluster.Mean) {
		return false
	}

	mean := cluster.Mean[featureIndex]
	current := vector[featureIndex]
	if mean <= 0 || current <= 0 {
		return false
	}
	return current >= mean*2
}

func resonanceFeatureIndex(names []string, target string) int {
	for i, name := range names {
		if name == target {
			return i
		}
	}
	return -1
}

func buildResonanceFeatureSourceFromSnapshot(row *store.ShadowSnapshot) resonanceFeatureSource {
	source := resonanceFeatureSource{
		values:    make(map[string]float64),
		available: make(map[string]bool),
	}
	if row == nil {
		return source
	}

	if raw, err := row.DecodedRawFactors(); err == nil {
		for key, value := range raw.Scores {
			source.values[key] = sanitizeResonanceFeatureValue(value)
		}
		for key, value := range raw.Available {
			source.available[key] = value
		}
	}

	if len(source.values) == 0 {
		source.values["market"] = sanitizeResonanceFeatureValue(row.MarketFactor)
		source.available["market"] = row.MarketFactor != 0
		source.values["trend"] = sanitizeResonanceFeatureValue(row.TrendFactor)
		source.available["trend"] = row.TrendFactor != 0
		source.values["donchian_factor"] = sanitizeResonanceFeatureValue(row.DonchianFactor)
		source.available["donchian_factor"] = row.DonchianFactor != 0
		source.values["volume_spike"] = sanitizeResonanceFeatureValue(row.VolumeSpikeFactor)
		source.available["volume_spike"] = row.VolumeSpikeFactor != 0
		source.values["mtf_resonance"] = sanitizeResonanceFeatureValue(row.MTFResonanceFactor)
		source.available["mtf_resonance"] = row.MTFResonanceFactor != 0
		source.values["quant_oi"] = sanitizeResonanceFeatureValue(row.QuantOIRaw)
		source.available["quant_oi"] = row.QuantOIRaw != 0
		source.values["quant_imbalance"] = sanitizeResonanceFeatureValue(row.QuantImbalanceRaw)
		source.available["quant_imbalance"] = row.QuantImbalanceRaw != 0
		source.values["quant_netflow"] = sanitizeResonanceFeatureValue(row.QuantNetflowRaw)
		source.available["quant_netflow"] = row.QuantNetflowRaw != 0
		source.values["social_rank"] = sanitizeResonanceFeatureValue(row.SocialRankRaw)
		source.available["social_rank"] = row.SocialRankRaw != 0
		source.values["social_upvote"] = sanitizeResonanceFeatureValue(row.SocialUpvoteRaw)
		source.available["social_upvote"] = row.SocialUpvoteRaw != 0
		source.values["onchain_ratio"] = sanitizeResonanceFeatureValue(row.OnChainRatioRaw)
		source.available["onchain_ratio"] = row.OnChainRatioRaw != 0
		source.values["onchain_buy_ratio"] = sanitizeResonanceFeatureValue(row.OnChainBuyRaw)
		source.available["onchain_buy_ratio"] = row.OnChainBuyRaw != 0
	}

	if _, ok := source.values["vol_utilization"]; !ok {
		source.values["vol_utilization"] = sanitizeResonanceFeatureValue(row.VolUtilization * 100)
	}
	if _, ok := source.available["vol_utilization"]; !ok {
		source.available["vol_utilization"] = row.VolUtilization > 0
	}

	if _, ok := source.values["funding_rate"]; !ok {
		source.values["funding_rate"] = sanitizeResonanceFeatureValue(row.FundingRate * 10000)
	}
	if _, ok := source.available["funding_rate"]; !ok {
		source.available["funding_rate"] = row.FundingRate != 0
	}

	if _, ok := source.values["orderbook_imbalance"]; !ok {
		source.values["orderbook_imbalance"] = sanitizeResonanceFeatureValue(row.QuantImbalanceRaw * 100)
	}
	if _, ok := source.available["orderbook_imbalance"]; !ok {
		source.available["orderbook_imbalance"] = row.QuantImbalanceRaw != 0
	}

	return source
}

func buildResonanceFeatureVector(source resonanceFeatureSource) []float64 {
	if len(source.values) == 0 {
		return nil
	}

	vector := make([]float64, len(resonanceFeatureNames))
	hasAvailable := false
	for i, name := range resonanceFeatureNames {
		value := 0.0
		if source.available != nil {
			if available, ok := source.available[name]; ok && !available {
				vector[i] = 0
				continue
			}
			if available, ok := source.available[name]; ok && available {
				hasAvailable = true
			}
		}
		if raw, ok := source.values[name]; ok && isFiniteResonanceFeature(raw) {
			value = raw
			if value != 0 {
				hasAvailable = true
			}
		}
		vector[i] = value
	}
	if !hasAvailable {
		return nil
	}
	return vector
}

func computeVectorMean(samples [][]float64) []float64 {
	if len(samples) == 0 {
		return nil
	}

	dim := len(samples[0])
	mean := make([]float64, dim)
	for _, sample := range samples {
		if len(sample) != dim {
			return nil
		}
		for i, value := range sample {
			mean[i] += value
		}
	}
	for i := range mean {
		mean[i] /= float64(len(samples))
	}
	return mean
}

func computeVectorCovariance(samples [][]float64, mean []float64) [][]float64 {
	if len(samples) == 0 || len(mean) == 0 {
		return nil
	}

	dim := len(mean)
	covariance := make([][]float64, dim)
	for i := range covariance {
		covariance[i] = make([]float64, dim)
	}

	denominator := float64(len(samples) - 1)
	if denominator <= 0 {
		denominator = 1
	}
	for _, sample := range samples {
		if len(sample) != dim {
			return nil
		}
		for i := 0; i < dim; i++ {
			deltaI := sample[i] - mean[i]
			for j := i; j < dim; j++ {
				deltaJ := sample[j] - mean[j]
				covariance[i][j] += deltaI * deltaJ
				if i != j {
					covariance[j][i] = covariance[i][j]
				}
			}
		}
	}
	for i := 0; i < dim; i++ {
		for j := 0; j < dim; j++ {
			covariance[i][j] /= denominator
		}
	}
	return covariance
}

func invertRegularizedMatrix(matrix [][]float64) ([][]float64, bool) {
	if len(matrix) == 0 || len(matrix) != len(matrix[0]) {
		return nil, false
	}

	regularized := cloneSquareMatrix(matrix)
	diagonalScale := matrixTrace(regularized)
	if diagonalScale <= 0 {
		diagonalScale = 1
	}
	epsilon := math.Max(1e-6, diagonalScale/float64(len(regularized))*1e-3)
	for i := range regularized {
		regularized[i][i] += epsilon
	}

	return invertSquareMatrix(regularized)
}

func invertDiagonalCovariance(matrix [][]float64) [][]float64 {
	if len(matrix) == 0 || len(matrix) != len(matrix[0]) {
		return nil
	}

	inv := make([][]float64, len(matrix))
	for i := range inv {
		inv[i] = make([]float64, len(matrix))
		variance := matrix[i][i]
		if variance <= 0 || math.IsNaN(variance) || math.IsInf(variance, 0) {
			variance = 1e-6
		}
		inv[i][i] = 1 / variance
	}
	return inv
}

func cloneSquareMatrix(matrix [][]float64) [][]float64 {
	if len(matrix) == 0 {
		return nil
	}

	cloned := make([][]float64, len(matrix))
	for i := range matrix {
		cloned[i] = append([]float64(nil), matrix[i]...)
	}
	return cloned
}

func matrixTrace(matrix [][]float64) float64 {
	if len(matrix) == 0 {
		return 0
	}

	trace := 0.0
	for i := range matrix {
		if i < len(matrix[i]) {
			trace += matrix[i][i]
		}
	}
	return trace
}

func stabilizeCovarianceDiagonal(matrix [][]float64, floor float64) [][]float64 {
	if len(matrix) == 0 || floor <= 0 {
		return matrix
	}

	stabilized := cloneSquareMatrix(matrix)
	for i := range stabilized {
		if i >= len(stabilized[i]) {
			continue
		}
		if stabilized[i][i] < floor || math.IsNaN(stabilized[i][i]) || math.IsInf(stabilized[i][i], 0) {
			stabilized[i][i] = floor
		}
	}
	return stabilized
}

func invertSquareMatrix(matrix [][]float64) ([][]float64, bool) {
	n := len(matrix)
	if n == 0 {
		return nil, false
	}

	augmented := make([][]float64, n)
	for i := 0; i < n; i++ {
		if len(matrix[i]) != n {
			return nil, false
		}
		augmented[i] = make([]float64, n*2)
		copy(augmented[i], matrix[i])
		augmented[i][n+i] = 1
	}

	for col := 0; col < n; col++ {
		pivotRow := col
		pivotValue := math.Abs(augmented[col][col])
		for row := col + 1; row < n; row++ {
			value := math.Abs(augmented[row][col])
			if value > pivotValue {
				pivotValue = value
				pivotRow = row
			}
		}
		if pivotValue < 1e-12 {
			return nil, false
		}
		if pivotRow != col {
			augmented[col], augmented[pivotRow] = augmented[pivotRow], augmented[col]
		}

		pivot := augmented[col][col]
		for j := 0; j < n*2; j++ {
			augmented[col][j] /= pivot
		}

		for row := 0; row < n; row++ {
			if row == col {
				continue
			}
			factor := augmented[row][col]
			if factor == 0 {
				continue
			}
			for j := 0; j < n*2; j++ {
				augmented[row][j] -= factor * augmented[col][j]
			}
		}
	}

	inverse := make([][]float64, n)
	for i := 0; i < n; i++ {
		inverse[i] = append([]float64(nil), augmented[i][n:]...)
	}
	return inverse, true
}

func sanitizeResonanceFeatureValue(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func isFiniteResonanceFeature(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func collectCoreArchetypeSamples(rows []*store.ShadowSnapshot) []archetypeTrainingSample {
	if len(rows) == 0 {
		return nil
	}

	positiveRows := make([]*store.ShadowSnapshot, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Filled || row.ReturnPct <= 0 || excludesSnapshotFromCoreArchetypes(row) {
			continue
		}
		positiveRows = append(positiveRows, row)
	}
	if len(positiveRows) == 0 {
		return nil
	}

	sort.SliceStable(positiveRows, func(i, j int) bool {
		if positiveRows[i].DecisionTime == positiveRows[j].DecisionTime {
			return positiveRows[i].ID > positiveRows[j].ID
		}
		return positiveRows[i].DecisionTime > positiveRows[j].DecisionTime
	})
	if len(positiveRows) > resonanceFeatureSampleLimit {
		positiveRows = positiveRows[:resonanceFeatureSampleLimit]
	}

	samples := make([]archetypeTrainingSample, 0, len(positiveRows))
	for _, row := range positiveRows {
		vector, ok := BuildResonanceFeatureVectorFromSnapshot(row)
		if !ok {
			continue
		}
		samples = append(samples, archetypeTrainingSample{
			vector:    vector,
			returnPct: row.ReturnPct,
		})
	}
	return samples
}

func buildCoreFeatureArchetypes(samples []archetypeTrainingSample) []*FeatureArchetype {
	if len(samples) < resonanceFeatureMinimumPositiveRows {
		return nil
	}

	vectors := make([][]float64, 0, len(samples))
	for _, sample := range samples {
		if len(sample.vector) != len(resonanceFeatureNames) {
			continue
		}
		vectors = append(vectors, sample.vector)
	}
	if len(vectors) < resonanceFeatureMinimumPositiveRows {
		return nil
	}

	normalized := normalizeKMeansSamples(vectors)
	seedIndices := selectFeatureArchetypeSeeds(vectors)
	assignments := runKMeansAssignments(normalized, seedIndices, coreFeatureArchetypeIterations)
	if len(assignments) != len(vectors) {
		return nil
	}

	clusterMembers := make([][]int, coreFeatureArchetypeCount)
	for sampleIndex, clusterIndex := range assignments {
		if clusterIndex < 0 || clusterIndex >= coreFeatureArchetypeCount {
			continue
		}
		clusterMembers[clusterIndex] = append(clusterMembers[clusterIndex], sampleIndex)
	}

	rawArchetypes := make([]*FeatureArchetype, 0, coreFeatureArchetypeCount)
	for _, members := range clusterMembers {
		if len(members) == 0 {
			continue
		}
		clusterVectors := make([][]float64, 0, len(members))
		totalReturn := 0.0
		for _, memberIndex := range members {
			clusterVectors = append(clusterVectors, samples[memberIndex].vector)
			totalReturn += samples[memberIndex].returnPct
		}
		cluster := buildResonanceFeatureClusterWithMinimumSamples(resonanceFeatureNames, clusterVectors, 1)
		if cluster == nil {
			continue
		}

		rawArchetypes = append(rawArchetypes, &FeatureArchetype{
			FeatureNames:        append([]string(nil), resonanceFeatureNames...),
			Mean:                append([]float64(nil), cluster.Mean...),
			InvCov:              cloneSquareMatrix(cluster.InvCov),
			SampleCount:         len(members),
			PositiveSampleCount: len(members),
			AverageReturnPct:    totalReturn / float64(len(members)),
			UpdatedAt:           time.Now().UTC(),
		})
	}
	if len(rawArchetypes) == 0 {
		return nil
	}

	assignFeatureArchetypeLabels(rawArchetypes)
	return rawArchetypes
}

func buildPromotedFeatureArchetypes(rows []*store.ShadowSnapshot) []*FeatureArchetype {
	if len(rows) == 0 {
		return nil
	}

	groupedRows := make(map[string][]*store.ShadowSnapshot)
	for _, row := range rows {
		if row == nil || !row.Filled || row.ReturnPct <= 0 || !row.IncubationPromoted {
			continue
		}
		label := strings.TrimSpace(row.ArchetypeLabel)
		if label == "" || isCoreFeatureArchetypeID(label) {
			continue
		}
		groupedRows[label] = append(groupedRows[label], row)
	}
	if len(groupedRows) == 0 {
		return nil
	}

	labels := make([]string, 0, len(groupedRows))
	for label := range groupedRows {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	archetypes := make([]*FeatureArchetype, 0, len(labels))
	for _, label := range labels {
		rowsForLabel := groupedRows[label]
		clusterVectors := make([][]float64, 0, len(rowsForLabel))
		totalReturn := 0.0
		for _, row := range rowsForLabel {
			vector, ok := BuildResonanceFeatureVectorFromSnapshot(row)
			if !ok {
				continue
			}
			clusterVectors = append(clusterVectors, vector)
			totalReturn += row.ReturnPct
		}
		cluster := buildResonanceFeatureClusterWithMinimumSamples(resonanceFeatureNames, clusterVectors, 1)
		if cluster == nil {
			continue
		}
		avgReturn := 0.0
		if len(clusterVectors) > 0 {
			avgReturn = totalReturn / float64(len(clusterVectors))
		}
		archetypes = append(archetypes, &FeatureArchetype{
			ID:                  label,
			Name:                label,
			Description:         "Promoted outlier cohort",
			FeatureNames:        append([]string(nil), resonanceFeatureNames...),
			Mean:                append([]float64(nil), cluster.Mean...),
			InvCov:              cloneSquareMatrix(cluster.InvCov),
			SampleCount:         len(clusterVectors),
			PositiveSampleCount: len(clusterVectors),
			AverageReturnPct:    avgReturn,
			MaxMarginFraction:   promotedArchetypeMaxMarginFraction(avgReturn),
			EntryEVScale:        1.0,
			Promoted:            true,
			UpdatedAt:           time.Now().UTC(),
		})
	}
	return archetypes
}

func normalizeKMeansSamples(samples [][]float64) [][]float64 {
	if len(samples) == 0 {
		return nil
	}

	dim := len(samples[0])
	means := make([]float64, dim)
	stddev := make([]float64, dim)
	for _, sample := range samples {
		if len(sample) != dim {
			return nil
		}
		for i, value := range sample {
			means[i] += value
		}
	}
	for i := range means {
		means[i] /= float64(len(samples))
	}
	for _, sample := range samples {
		for i, value := range sample {
			delta := value - means[i]
			stddev[i] += delta * delta
		}
	}
	for i := range stddev {
		stddev[i] = math.Sqrt(stddev[i] / float64(len(samples)))
		if stddev[i] <= 1e-9 || math.IsNaN(stddev[i]) || math.IsInf(stddev[i], 0) {
			stddev[i] = 1
		}
	}

	normalized := make([][]float64, len(samples))
	for i, sample := range samples {
		normalized[i] = make([]float64, dim)
		for j, value := range sample {
			normalized[i][j] = (value - means[j]) / stddev[j]
		}
	}
	return normalized
}

func selectFeatureArchetypeSeeds(samples [][]float64) []int {
	if len(samples) == 0 {
		return nil
	}

	selectUnique := func(scoreFn func([]float64) float64, used map[int]struct{}) int {
		bestIndex := -1
		bestScore := math.Inf(-1)
		for i, sample := range samples {
			if _, exists := used[i]; exists {
				continue
			}
			score := scoreFn(sample)
			if score > bestScore {
				bestScore = score
				bestIndex = i
			}
		}
		if bestIndex < 0 {
			for i := range samples {
				if _, exists := used[i]; exists {
					continue
				}
				bestIndex = i
				break
			}
		}
		if bestIndex >= 0 {
			used[bestIndex] = struct{}{}
		}
		return bestIndex
	}

	used := make(map[int]struct{}, coreFeatureArchetypeCount)
	meanReversionSeed := selectUnique(archetypeMeanReversionScore, used)
	momentumSeed := selectUnique(archetypeMomentumScore, used)
	oversoldSeed := selectUnique(archetypeOversoldScore, used)

	seeds := []int{meanReversionSeed, momentumSeed, oversoldSeed}
	for i, seed := range seeds {
		if seed >= 0 {
			continue
		}
		for sampleIndex := range samples {
			if _, exists := used[sampleIndex]; exists {
				continue
			}
			seeds[i] = sampleIndex
			used[sampleIndex] = struct{}{}
			break
		}
		if seeds[i] < 0 {
			seeds[i] = 0
		}
	}
	return seeds
}

func runKMeansAssignments(samples [][]float64, seedIndices []int, iterations int) []int {
	if len(samples) == 0 || len(seedIndices) == 0 {
		return nil
	}
	if iterations <= 0 {
		iterations = coreFeatureArchetypeIterations
	}

	centroids := make([][]float64, 0, len(seedIndices))
	for _, seedIndex := range seedIndices {
		if seedIndex < 0 || seedIndex >= len(samples) {
			continue
		}
		centroids = append(centroids, append([]float64(nil), samples[seedIndex]...))
	}
	if len(centroids) == 0 {
		return nil
	}

	assignments := make([]int, len(samples))
	for i := range assignments {
		assignments[i] = -1
	}

	for iteration := 0; iteration < iterations; iteration++ {
		changed := false
		for sampleIndex, sample := range samples {
			bestCluster := 0
			bestDistance := math.Inf(1)
			for clusterIndex, centroid := range centroids {
				distance, ok := euclideanDistance(sample, centroid)
				if !ok {
					continue
				}
				if distance < bestDistance {
					bestDistance = distance
					bestCluster = clusterIndex
				}
			}
			if assignments[sampleIndex] != bestCluster {
				assignments[sampleIndex] = bestCluster
				changed = true
			}
		}

		nextCentroids := make([][]float64, len(centroids))
		counts := make([]int, len(centroids))
		for clusterIndex := range nextCentroids {
			nextCentroids[clusterIndex] = make([]float64, len(centroids[clusterIndex]))
		}
		for sampleIndex, clusterIndex := range assignments {
			if clusterIndex < 0 || clusterIndex >= len(nextCentroids) {
				continue
			}
			counts[clusterIndex]++
			for dim, value := range samples[sampleIndex] {
				nextCentroids[clusterIndex][dim] += value
			}
		}
		for clusterIndex := range nextCentroids {
			if counts[clusterIndex] == 0 {
				nextCentroids[clusterIndex] = append([]float64(nil), centroids[clusterIndex]...)
				continue
			}
			for dim := range nextCentroids[clusterIndex] {
				nextCentroids[clusterIndex][dim] /= float64(counts[clusterIndex])
			}
		}
		centroids = nextCentroids
		if !changed {
			break
		}
	}

	return assignments
}

func assignFeatureArchetypeLabels(archetypes []*FeatureArchetype) {
	if len(archetypes) == 0 {
		return
	}

	remaining := make(map[int]struct{}, len(archetypes))
	for i := range archetypes {
		remaining[i] = struct{}{}
	}

	assign := func(targetID, targetName, targetDescription string, scoreFn func([]float64) float64) {
		bestIndex := -1
		bestScore := math.Inf(-1)
		for idx := range remaining {
			score := scoreFn(archetypes[idx].Mean)
			if score > bestScore {
				bestScore = score
				bestIndex = idx
			}
		}
		if bestIndex < 0 {
			return
		}
		archetypes[bestIndex].ID = targetID
		archetypes[bestIndex].Name = targetName
		archetypes[bestIndex].Description = targetDescription
		archetypes[bestIndex].MaxMarginFraction, archetypes[bestIndex].EntryEVScale = defaultArchetypeRiskParameters(targetID)
		delete(remaining, bestIndex)
	}

	assign("Type_B", "Momentum_Spike", "High slope, volume expansion, impulsive acceleration", archetypeMomentumScore)
	assign("Type_C", "Extreme_Oversold", "Deep negative deviation with low-volume stabilization", archetypeOversoldScore)
	assign("Type_A", "Mean_Reversion", "Low-volatility oscillation and pullback compression", archetypeMeanReversionScore)

	for idx := range remaining {
		archetypes[idx].ID = "Type_A"
		archetypes[idx].Name = "Mean_Reversion"
		archetypes[idx].Description = "Low-volatility oscillation and pullback compression"
		archetypes[idx].MaxMarginFraction, archetypes[idx].EntryEVScale = defaultArchetypeRiskParameters("Type_A")
	}
}

func archetypeMomentumScore(vector []float64) float64 {
	return featureValue(vector, "volume_spike")*1.3 +
		featureValue(vector, "trend")*1.1 +
		featureValue(vector, "donchian_factor")*0.9 +
		featureValue(vector, "quant_netflow")*0.5 +
		featureValue(vector, "vol_utilization")*0.2
}

func archetypeOversoldScore(vector []float64) float64 {
	return -featureValue(vector, "market")*1.0 -
		featureValue(vector, "trend")*0.8 -
		featureValue(vector, "donchian_factor")*0.8 -
		featureValue(vector, "quant_netflow")*0.4 -
		featureValue(vector, "onchain_buy_ratio")*0.2 -
		featureValue(vector, "vol_utilization")*0.15
}

func archetypeMeanReversionScore(vector []float64) float64 {
	return -math.Abs(featureValue(vector, "trend"))*0.9 -
		math.Abs(featureValue(vector, "donchian_factor"))*0.8 -
		math.Abs(featureValue(vector, "quant_netflow"))*0.4 -
		math.Abs(featureValue(vector, "volume_spike"))*0.5 -
		featureValue(vector, "vol_utilization")*0.5 +
		featureValue(vector, "mtf_resonance")*0.3
}

func featureValue(vector []float64, name string) float64 {
	index := resonanceFeatureIndex(resonanceFeatureNames, name)
	if index < 0 || index >= len(vector) {
		return 0
	}
	return vector[index]
}

func defaultArchetypeRiskParameters(archetypeID string) (float64, float64) {
	switch archetypeID {
	case "Type_B":
		return 1.0, 1.0
	case "Type_C":
		return 0.60, 1.0
	case "Type_A":
		fallthrough
	default:
		return 0.75, 1.0
	}
}

func promotedArchetypeMaxMarginFraction(avgReturn float64) float64 {
	scale := 0.50 + (avgReturn * 10)
	if scale < 0.40 {
		scale = 0.40
	}
	if scale > 0.85 {
		scale = 0.85
	}
	return scale
}

func featureArchetypeSortKey(archetype *FeatureArchetype) string {
	if archetype == nil {
		return "zzz"
	}
	switch archetype.ID {
	case "Type_A":
		return "001"
	case "Type_B":
		return "002"
	case "Type_C":
		return "003"
	default:
		return "100_" + strings.TrimSpace(archetype.ID)
	}
}

func excludesSnapshotFromCoreArchetypes(row *store.ShadowSnapshot) bool {
	if row == nil {
		return true
	}
	return row.Incubating && !row.IncubationPromoted
}

func isCoreFeatureArchetypeID(label string) bool {
	switch strings.TrimSpace(label) {
	case "Type_A", "Type_B", "Type_C":
		return true
	default:
		return false
	}
}
