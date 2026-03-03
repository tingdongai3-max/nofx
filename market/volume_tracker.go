package market

import "time"

const (
	// rollingWindowSeconds defines the length of the real-time volume window.
	rollingWindowSeconds = 300 // 5 minutes
	// baselineWindowHours defines how far back we look when estimating the average 5-minute volume.
	baselineWindowHours = 24
)

// computeRollingVolumeFromKlines computes:
//   - rollingVol: total volume in the last rollingWindowSeconds
//   - avgVolPer5Min: baseline average 5-minute volume estimated from historical klines.
//
// It is intentionally independent of candle boundaries:
//   - The numerator always covers a continuous window of ~5 minutes (based on CloseTime).
//   - The denominator normalizes to "per 5 minutes" using actual elapsed time instead of fixed bar counts.
//
// Fail-safe:
//   - If we don't have enough history or timestamps are missing, it returns (0, 0) so the caller can
//     gracefully fall back to a simpler bar-based heuristic.
func computeRollingVolumeFromKlines(klines []Kline, nowMs, timeframeMs int64) (rollingVol, avgVolPer5Min float64) {
	n := len(klines)
	if n == 0 {
		return 0, 0
	}
	last := klines[n-1]

	// Infer timeframe when not provided (e.g. historical snapshot callers).
	if timeframeMs <= 0 && n >= 2 {
		tf := klines[n-1].CloseTime - klines[n-2].CloseTime
		if tf > 0 {
			timeframeMs = tf
		}
	}
	if timeframeMs <= 0 {
		return 0, 0
	}

	// Establish "now". For real-time WS callers this is wall-clock; for offline snapshot we
	// approximate with last.CloseTime.
	if nowMs <= 0 {
		nowMs = last.CloseTime
	}

	windowMs := int64(rollingWindowSeconds) * int64(time.Second/time.Millisecond)
	cutoff := nowMs - windowMs

	// 1) Numerator: rolling volume over the last windowMs, using kline CloseTime as the sample time.
	rolling := 0.0
	for i := n - 1; i >= 0; i-- {
		k := klines[i]
		// Skip obviously invalid timestamps
		if k.CloseTime == 0 {
			continue
		}
		if k.CloseTime < cutoff {
			break
		}
		rolling += k.Volume
	}

	// If we have essentially no coverage in the window, let caller fall back.
	if rolling <= 0 {
		return 0, 0
	}

	// 2) Denominator: average 5-minute volume over ~baselineWindowHours lookback.
	baselineWindowMs := int64(baselineWindowHours) * int64(time.Hour/time.Millisecond)
	baselineStart := last.CloseTime - baselineWindowMs

	var (
		baselineVol      float64
		firstTs, lastTs  int64
		baselineBarCount int
	)

	for i := n - 1; i >= 0; i-- {
		k := klines[i]
		if k.CloseTime == 0 {
			continue
		}
		if k.CloseTime < baselineStart {
			break
		}
		baselineVol += k.Volume
		baselineBarCount++
		if lastTs == 0 || k.CloseTime > lastTs {
			lastTs = k.CloseTime
		}
		if firstTs == 0 || k.CloseTime < firstTs {
			firstTs = k.CloseTime
		}
	}

	if baselineBarCount == 0 || baselineVol <= 0 {
		return 0, 0
	}

	// Prefer actual elapsed time as denominator when timestamps are sane.
	elapsedMs := lastTs - firstTs
	if elapsedMs <= 0 {
		// Fallback: approximate using bar count * timeframe
		elapsedMs = int64(baselineBarCount) * timeframeMs
	}
	if elapsedMs <= 0 {
		return 0, 0
	}

	avgVolPerSecond := baselineVol / (float64(elapsedMs) / 1000.0)
	if avgVolPerSecond <= 0 {
		return 0, 0
	}

	avgVolPer5Min = avgVolPerSecond * float64(rollingWindowSeconds)
	return rolling, avgVolPer5Min
}

