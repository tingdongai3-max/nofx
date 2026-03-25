package kernel

import "testing"

func TestSelectEntryFloorFromCurveKeepsSharpPeak(t *testing.T) {
	selection := selectEntryFloorFromCurve([]float64{20, 98, 30})

	if selection.PeakIndex != 1 {
		t.Fatalf("expected sharp peak at index 1, got %d", selection.PeakIndex)
	}
	if selection.PeakScore != 98 {
		t.Fatalf("expected sharp peak score 98, got %.2f", selection.PeakScore)
	}
	if selection.PlateauStart != 1 || selection.PlateauEnd != 1 {
		t.Fatalf("expected isolated peak plateau [1,1], got [%d,%d]", selection.PlateauStart, selection.PlateauEnd)
	}
	if selection.SelectedFloor != 1 {
		t.Fatalf("expected selected floor 1, got %d", selection.SelectedFloor)
	}
}
