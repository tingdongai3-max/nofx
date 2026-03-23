package market

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
)

// GetWeightSignature returns the signature for the current adaptive state.
func GetWeightSignature(traderID, sector, symbol string) string {
	return GetAdaptiveWeightState(traderID, sector, symbol).GetWeightSignature()
}

// GetWeightSignature produces a stable fingerprint of all weight inputs that
// influence back-cast score recalculation.
func (state AdaptiveWeightState) GetWeightSignature() string {
	var builder strings.Builder

	for _, name := range adaptiveVisibleFactorOrder {
		factor := adaptiveFactorStateByName(state.Factors, name)
		fmt.Fprintf(&builder, "vf:%s:w=%s:ic=%s;", name, signatureFloat(factor.FinalWeight), signatureFloat(factor.FinalIC))
	}
	for _, name := range adaptiveHiddenFactorOrder {
		factor := adaptiveFactorStateByName(state.HiddenFactors, name)
		fmt.Fprintf(&builder, "hf:%s:w=%s:ic=%s;", name, signatureFloat(factor.FinalWeight), signatureFloat(factor.FinalIC))
	}
	for _, name := range adaptiveNestedWeightSignatureOrder(state.NestedWeights) {
		fmt.Fprintf(&builder, "nw:%s:w=%s;", name, signatureFloat(state.NestedWeights[name]))
	}

	digest := md5.Sum([]byte(builder.String()))
	return hex.EncodeToString(digest[:])
}

func adaptiveNestedWeightSignatureOrder(weights map[string]float64) []string {
	keys := make(map[string]struct{}, len(weights))
	for key := range defaultNestedAdaptiveWeights() {
		keys[key] = struct{}{}
	}
	for key := range weights {
		keys[key] = struct{}{}
	}

	order := make([]string, 0, len(keys))
	for key := range keys {
		order = append(order, key)
	}
	sort.Strings(order)
	return order
}

func signatureFloat(value float64) string {
	switch {
	case math.IsNaN(value):
		return "nan"
	case math.IsInf(value, 1):
		return "+inf"
	case math.IsInf(value, -1):
		return "-inf"
	default:
		return fmt.Sprintf("%.6f", value)
	}
}
