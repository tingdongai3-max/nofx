package kernel

import (
	"strings"
	"testing"

	"nofx/market"
	"nofx/store"
)

func TestFormatIndicatorSummaryIncludesDonchianFields(t *testing.T) {
	indicators := store.IndicatorConfig{
		EnableDonchianBox: true,
		DonchianPeriods:   []int{72, 500},
	}

	result := market.IndicatorResult{
		Donchians: map[int]market.DonchianResult{
			72: {
				Upper: 110,
				Lower: 90,
				Mid:   100,
			},
			500: {
				Upper: 140,
				Lower: 80,
				Mid:   110,
			},
		},
	}

	lines := formatIndicatorSummary(indicators, 111, result)
	text := strings.Join(lines, "\n")

	mustContain := []string{
		"Donchian72_Upper = 110.000",
		"Donchian72_Lower = 90.000",
		"Donchian500_Lower = 80.000",
		"Donchian72_State = Price is currently above the upper bound",
	}

	for _, expected := range mustContain {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected prompt summary to contain %q, got:\n%s", expected, text)
		}
	}
}

func TestDescribeDonchianState(t *testing.T) {
	box := market.DonchianResult{Upper: 110, Lower: 90, Mid: 100}

	cases := []struct {
		name         string
		currentPrice float64
		expected     string
	}{
		{name: "above upper", currentPrice: 111, expected: "Price is currently above the upper bound"},
		{name: "near upper", currentPrice: 109.7, expected: "Price is within 2% range of the upper bound"},
		{name: "near lower", currentPrice: 90.3, expected: "Price is within 2% range of the lower bound"},
		{name: "inside box", currentPrice: 100, expected: "Price is currently between the upper and lower bounds"},
		{name: "below lower", currentPrice: 89, expected: "Price is currently below the lower bound"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeDonchianState(tc.currentPrice, box)
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}
