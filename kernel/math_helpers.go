package kernel

import "math"

func sanitizeFinite(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func meanFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	total := 0.0
	for _, value := range values {
		total += sanitizeFinite(value)
	}
	return total / float64(len(values))
}
