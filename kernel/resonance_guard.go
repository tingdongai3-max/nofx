package kernel

import "math"

// CalculateResonanceScore returns the raw weighted mean of the supplied factors.
// Structural penalties are applied separately so sharp peaks are not damped by
// the score composition itself. If a Mahalanobis distance is provided, a global
// 2.0 hard-cap zeroes the score before any composition math.
func CalculateResonanceScore(factors []float64, weights []float64, lambda float64, mahalanobisDistance ...float64) float64 {
	if len(mahalanobisDistance) > 0 {
		distance := mahalanobisDistance[0]
		if !math.IsNaN(distance) && !math.IsInf(distance, 0) && distance > DefaultMahalanobisThreshold {
			return 0
		}
	}
	if len(factors) == 0 {
		return 0
	}

	sanitizedFactors := make([]float64, 0, len(factors))
	for _, factor := range factors {
		sanitizedFactors = append(sanitizedFactors, sanitizeFinite(factor))
	}

	normalizedWeights := normalizeResonanceWeights(weights, len(sanitizedFactors))
	weightedMean := weightedAverage(sanitizedFactors, normalizedWeights)
	if weightedMean <= 0 {
		return 0
	}
	return weightedMean
}

// ValidateResonanceFactors returns whether the factors clear the adaptive floor.
// Negative values are rejected before the floor check.
func ValidateResonanceFactors(factors []float64, floor float64) (bool, string, float64) {
	if len(factors) == 0 {
		return false, "Logic Incoherence", 0
	}
	if math.IsNaN(floor) || math.IsInf(floor, 0) {
		floor = 0
	}

	minFactor := math.Inf(1)
	for _, raw := range factors {
		value := sanitizeFinite(raw)
		if value < minFactor {
			minFactor = value
		}
		if value < 0 {
			return false, "Negative Factor Violation", minFactor
		}
		if value < floor {
			return false, "Logic Incoherence", minFactor
		}
	}
	return true, "", minFactor
}

func normalizeResonanceWeights(weights []float64, factorCount int) []float64 {
	if factorCount <= 0 {
		return nil
	}

	normalized := make([]float64, factorCount)
	sum := 0.0
	for i := 0; i < factorCount; i++ {
		value := 1.0
		if i < len(weights) {
			value = sanitizeFinite(weights[i])
		}
		if value < 0 {
			value = 0
		}
		normalized[i] = value
		sum += value
	}
	if sum <= 0 {
		for i := range normalized {
			normalized[i] = 1.0 / float64(factorCount)
		}
		return normalized
	}

	for i := range normalized {
		normalized[i] /= sum
	}
	return normalized
}

func weightedAverage(values []float64, weights []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(weights) != len(values) {
		return meanFloat64(values)
	}

	total := 0.0
	for i := range values {
		total += values[i] * weights[i]
	}
	return total
}

func weightedVariance(values []float64, weights []float64, mean float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(weights) != len(values) {
		return populationVariance(values, mean)
	}

	sum := 0.0
	for i, value := range values {
		weight := weights[i]
		delta := value - mean
		sum += weight * delta * delta
	}
	return sum
}

func populationVariance(values []float64, mean float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, value := range values {
		delta := value - mean
		sum += delta * delta
	}
	return sum / float64(len(values))
}
