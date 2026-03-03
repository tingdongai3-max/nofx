package market

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// timeframeMsFromString returns period in ms for a timeframe string (e.g. "5m" -> 300000). 0 if unknown.
func timeframeMsFromString(tf string) int64 {
	dur, err := TFDuration(tf)
	if err != nil {
		return 0
	}
	return dur.Milliseconds()
}

// IndicatorSnapshot aggregates key indicator values at a specific timestamp.
// Price-based indicators are normalized to relative/percentage form for cross-symbol comparison.
type IndicatorSnapshot struct {
	Close                  float64 `json:"close"`
	RSI                    float64 `json:"rsi"`
	MACD                   float64 `json:"macd"`                     // histogram (oscillator, unchanged)
	ADX                    float64 `json:"adx"`
	EMABias                float64 `json:"emabias"`                  // (Close - EMA) / EMA * 100
	BollPct                float64 `json:"boll_pct"`                 // (Close - Lower) / (Upper - Lower), 0..1 band position
	ATRPct                 float64 `json:"atr_pct"`                  // ATR / Close * 100
	Bias                   float64 `json:"bias"`
	VolMult                float64 `json:"vol_mult"`                 // 兼容旧字段：等同于 realtime_rolling_volmult
	RealtimeRollingVolMult float64 `json:"realtime_rolling_volmult"` // 实时 5 分钟滚动放量：当前 5 分钟滚动成交量 / 近 24 小时平均每 5 分钟成交量
}

// ComputeIndicatorSnapshot computes a snapshot of indicators on the last kline of the slice.
// The caller is responsible for passing a kline window that covers the required lookback.
// rsiPeriod / emaPeriod / macdFast / macdSlow / macdSignal are configurable; other periods follow
// standard defaults:
// - ATR / ADX: 14
// - BOLL: 20, 2σ
// - Bias: (Price - MA(N)) / MA(N) * 100, where N = emaPeriod (for analysis purposes)
// - volMultBars: 放量指标前N根K线数量，当前成交量/前N根平均成交量，默认5
// nowMs: 当前时间 UTC 毫秒，用于未闭合 K 线 VolMult 智能动能继承；0 则用简单算法。
// timeframeMs: 单根 K 线周期毫秒（如 5m=300000），与 nowMs 同时非 0 时启用智能 VolMult。
func ComputeIndicatorSnapshot(klines []Kline, rsiPeriod, emaPeriod, macdFast, macdSlow, macdSignal, volMultBars int, nowMs, timeframeMs int64) IndicatorSnapshot {
	if len(klines) == 0 {
		return IndicatorSnapshot{}
	}

	if rsiPeriod <= 0 {
		rsiPeriod = 14
	}
	if emaPeriod <= 0 {
		emaPeriod = 20
	}
	if macdFast <= 0 {
		macdFast = 12
	}
	if macdSlow <= 0 {
		macdSlow = 26
	}
	if macdSignal <= 0 {
		macdSignal = 9
	}
	if volMultBars <= 0 {
		volMultBars = 5
	}

	opts := &IndicatorParams{
		EMAPeriods:  []int{emaPeriod},
		RSIPeriods:  []int{rsiPeriod},
		ATRPeriods:  []int{14},
		ADXPeriods:  []int{14},
		BOLLPeriods: []int{20},
		BIASPeriods: []int{emaPeriod},
		MACDFast:    macdFast,
		MACDSlow:    macdSlow,
		MACDSignal:  macdSignal,
	}

	dyn := fillDynamicIndicators(klines, opts)
	last := klines[len(klines)-1]
	close := last.Close

	snap := IndicatorSnapshot{Close: close}

	if v, ok := dyn[fmt.Sprintf("rsi_%d", rsiPeriod)]; ok {
		snap.RSI = v
	}
	if v, ok := dyn["macd_histogram"]; ok {
		snap.MACD = v
	}
	if v, ok := dyn["adx_14"]; ok {
		snap.ADX = v
	}
	if v, ok := dyn[fmt.Sprintf("bias_%d", emaPeriod)]; ok {
		snap.Bias = v
	}

	var ema, atr, bollUpper, bollLower float64
	if v, ok := dyn[fmt.Sprintf("ema_%d", emaPeriod)]; ok {
		ema = v
	}
	if v, ok := dyn["atr_14"]; ok {
		atr = v
	}
	if v, ok := dyn["boll_upper_20"]; ok {
		bollUpper = v
	}
	if v, ok := dyn["boll_lower_20"]; ok {
		bollLower = v
	}

	// EMABias: (Close - EMA) / EMA * 100
	if ema > 0 && !math.IsNaN(ema) && !math.IsInf(ema, 0) {
		snap.EMABias = (close - ema) / ema * 100
	}
	// BollPct: (Close - Lower) / (Upper - Lower), clamp to [0,1] for band position
	if bollUpper > bollLower && bollLower >= 0 {
		pct := (close - bollLower) / (bollUpper - bollLower)
		if !math.IsNaN(pct) && !math.IsInf(pct, 0) {
			if pct < 0 {
				pct = 0
			}
			if pct > 1 {
				pct = 1
			}
			snap.BollPct = pct * 100 // store as 0..100 for consistency with other %
		}
	}
	// ATRPct: ATR / Close * 100
	if close > 0 && atr > 0 && !math.IsNaN(atr) && !math.IsInf(atr, 0) {
		snap.ATRPct = atr / close * 100
	}

	// VolMult (realtime_rolling_volmult) 放量：基于连续 5 分钟滚动窗口的成交量动能，
	// 分子：过去 300 秒滚动成交量（RollingVolume）
	// 分母：近 24 小时（或可用历史）平均每 5 分钟成交量（AverageVolumePer5Min）
	if len(klines) >= 2 {
		rolling, avgPer5Min := computeRollingVolumeFromKlines(klines, nowMs, timeframeMs)
		if avgPer5Min > 0 && rolling > 0 {
			volMult := rolling / avgPer5Min
			if !math.IsNaN(volMult) && !math.IsInf(volMult, 0) {
				snap.RealtimeRollingVolMult = volMult
				snap.VolMult = volMult
			}
		} else {
			// Fail-safe：若 WS / 时间戳异常导致 rolling 计算失败，则退化为旧的 N-bar 平均算法。
			if n := volMultBars; n >= 1 && len(klines) > n {
				start := len(klines) - 1 - n
				if start < 0 {
					start = 0
				}
				sum := 0.0
				for i := start; i < len(klines)-1; i++ {
					sum += klines[i].Volume
				}
				closedCount := len(klines) - 1 - start
				if closedCount <= 0 {
					closedCount = 1
				}
				avgVol := sum / float64(closedCount)
				if avgVol > 0 && !math.IsNaN(avgVol) && !math.IsInf(avgVol, 0) {
					snap.RealtimeRollingVolMult = last.Volume / avgVol
					snap.VolMult = snap.RealtimeRollingVolMult
				}
			}
		}
	}

	return snap
}

// FetchAndSnapshotIndicators fetches klines around tsMs, computes IndicatorSnapshot, returns JSON string.
// Used by OrderSync async backfill for 指标分析 秒开.
// On error returns empty string (graceful degradation). rsiPeriod/emaPeriod/macdFast/macdSlow/macdSignal/volMultBars use defaults if <=0.
// 固化参数：RSI 7, EMA 20, BOLL 20（胜率优化），后端写死不依赖前端。
func FetchAndSnapshotIndicators(symbol string, tsMs int64, timeframe string, rsiPeriod, emaPeriod, macdFast, macdSlow, macdSignal, volMultBars int) string {
	if rsiPeriod <= 0 {
		rsiPeriod = 7
	}
	if emaPeriod <= 0 {
		emaPeriod = 20
	}
	if macdFast <= 0 {
		macdFast = 12
	}
	if macdSlow <= 0 {
		macdSlow = 26
	}
	if macdSignal <= 0 {
		macdSignal = 9
	}
	if volMultBars <= 0 {
		volMultBars = 5
	}
	if timeframe == "" {
		timeframe = "5m"
	}

	maxLookbackBars := rsiPeriod
	if emaPeriod > maxLookbackBars {
		maxLookbackBars = emaPeriod
	}
	if macdSlow+macdSignal > maxLookbackBars {
		maxLookbackBars = macdSlow + macdSignal
	}
	if volMultBars+1 > maxLookbackBars {
		maxLookbackBars = volMultBars + 1
	}
	if maxLookbackBars < 35 {
		maxLookbackBars = 35
	}

	tfDur, err := TFDuration(timeframe)
	if err != nil {
		return ""
	}
	buffer := tfDur * time.Duration(maxLookbackBars+5)
	start := time.UnixMilli(tsMs).Add(-buffer)
	end := time.UnixMilli(tsMs).Add(buffer)
	klines, err := GetKlinesRange(symbol, timeframe, start, end)
	if err != nil || len(klines) == 0 {
		return ""
	}

	tfMs := int64(tfDur / time.Millisecond)
	var slice []Kline
	for i := len(klines) - 1; i >= 0; i-- {
		k := klines[i]
		if tsMs >= k.OpenTime && tsMs < k.OpenTime+tfMs {
			slice = klines[:i+1]
			break
		}
	}
	if len(slice) == 0 {
		return ""
	}

	snap := ComputeIndicatorSnapshot(slice, rsiPeriod, emaPeriod, macdFast, macdSlow, macdSignal, volMultBars, tsMs, tfMs)
	raw, err := json.Marshal(snap)
	if err != nil {
		return ""
	}
	return string(raw)
}
