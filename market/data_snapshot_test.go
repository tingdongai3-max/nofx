package market

import (
	"testing"
	"time"
)

func TestNormalizePriceSnapshotTime(t *testing.T) {
	now := time.Date(2026, 3, 23, 16, 3, 42, 987000000, time.UTC)
	got := NormalizePriceSnapshotTime(now)
	want := time.Date(2026, 3, 23, 16, 3, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("snapshot time mismatch: got %s want %s", got, want)
	}
}

func TestFindAlignedMinuteClosePrefersExactPreviousMinuteBar(t *testing.T) {
	snapshotTime := time.Date(2026, 3, 23, 16, 3, 0, 0, time.UTC)
	targetOpen := snapshotTime.Add(-1 * time.Minute).UnixMilli()
	klines := []Kline{
		{
			OpenTime:  snapshotTime.Add(-2 * time.Minute).UnixMilli(),
			CloseTime: snapshotTime.Add(-1*time.Minute).UnixMilli() - 1,
			Close:     99.5,
		},
		{
			OpenTime:  targetOpen,
			CloseTime: snapshotTime.UnixMilli() - 1,
			Close:     101.25,
		},
		{
			OpenTime:  snapshotTime.UnixMilli(),
			CloseTime: snapshotTime.Add(time.Minute).UnixMilli() - 1,
			Close:     102.75,
		},
	}

	got, err := findAlignedMinuteClose(klines, targetOpen, snapshotTime)
	if err != nil {
		t.Fatalf("findAlignedMinuteClose returned error: %v", err)
	}
	if got != 101.25 {
		t.Fatalf("expected previous closed 1m price 101.25, got %.2f", got)
	}
}

func TestFindAlignedMinuteCloseFallsBackToLatestClosedBarBeforeSnapshot(t *testing.T) {
	snapshotTime := time.Date(2026, 3, 23, 16, 3, 0, 0, time.UTC)
	klines := []Kline{
		{
			OpenTime:  snapshotTime.Add(-90 * time.Second).UnixMilli(),
			CloseTime: snapshotTime.Add(-30*time.Second).UnixMilli() - 1,
			Close:     88.8,
		},
	}

	got, err := findAlignedMinuteClose(klines, snapshotTime.Add(-1*time.Minute).UnixMilli(), snapshotTime)
	if err != nil {
		t.Fatalf("findAlignedMinuteClose fallback returned error: %v", err)
	}
	if got != 88.8 {
		t.Fatalf("expected fallback close 88.8, got %.2f", got)
	}
}
