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
		offset       time.Duration
		wantNextTick time.Time
		wantDelay    time.Duration
	}{
		{
			name:         "restart between boundaries aligns to next candle close",
			now:          base,
			interval:     5 * time.Minute,
			offset:       5 * time.Second,
			wantNextTick: time.Date(2026, 3, 21, 19, 5, 5, 0, time.UTC),
			wantDelay:    2*time.Minute + 5*time.Second,
		},
		{
			name:         "exact boundary waits for offset",
			now:          time.Date(2026, 3, 21, 19, 5, 0, 0, time.UTC),
			interval:     5 * time.Minute,
			offset:       5 * time.Second,
			wantNextTick: time.Date(2026, 3, 21, 19, 5, 5, 0, time.UTC),
			wantDelay:    5 * time.Second,
		},
		{
			name:         "after offset rolls to following boundary",
			now:          time.Date(2026, 3, 21, 19, 5, 6, 0, time.UTC),
			interval:     5 * time.Minute,
			offset:       5 * time.Second,
			wantNextTick: time.Date(2026, 3, 21, 19, 10, 5, 0, time.UTC),
			wantDelay:    4*time.Minute + 59*time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDelay, gotNextTick := calculateNextAlignmentFrom(tt.now, tt.interval, tt.offset)
			if !gotNextTick.Equal(tt.wantNextTick) {
				t.Fatalf("next tick mismatch: got %s want %s", gotNextTick, tt.wantNextTick)
			}
			if gotDelay != tt.wantDelay {
				t.Fatalf("delay mismatch: got %v want %v", gotDelay, tt.wantDelay)
			}
		})
	}
}
