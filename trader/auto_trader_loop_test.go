package trader

import (
	"testing"
	"time"
)

func TestCalculateNextAlignmentFrom(t *testing.T) {
	base := time.Date(2026, 3, 21, 19, 3, 0, 0, time.UTC)

	tests := []struct {
		name         string
		now          time.Time
		interval     time.Duration
		wantNextTick time.Time
		wantDelay    time.Duration
	}{
		{
			name:         "restart between boundaries aligns to next wall clock slot",
			now:          base,
			interval:     5 * time.Minute,
			wantNextTick: time.Date(2026, 3, 21, 19, 5, 0, 0, time.UTC),
			wantDelay:    2 * time.Minute,
		},
		{
			name:         "exact boundary waits for next interval",
			now:          time.Date(2026, 3, 21, 19, 5, 0, 0, time.UTC),
			interval:     5 * time.Minute,
			wantNextTick: time.Date(2026, 3, 21, 19, 10, 0, 0, time.UTC),
			wantDelay:    5 * time.Minute,
		},
		{
			name:         "after boundary rolls to following slot",
			now:          time.Date(2026, 3, 21, 19, 5, 6, 0, time.UTC),
			interval:     5 * time.Minute,
			wantNextTick: time.Date(2026, 3, 21, 19, 10, 0, 0, time.UTC),
			wantDelay:    4*time.Minute + 54*time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDelay, gotNextTick := calculateNextAlignmentFrom(tt.now, tt.interval)
			if !gotNextTick.Equal(tt.wantNextTick) {
				t.Fatalf("next tick mismatch: got %s want %s", gotNextTick, tt.wantNextTick)
			}
			if gotDelay != tt.wantDelay {
				t.Fatalf("delay mismatch: got %v want %v", gotDelay, tt.wantDelay)
			}
		})
	}
}
