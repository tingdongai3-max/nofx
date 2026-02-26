package market

import (
	"fmt"
	"math"
)

// IndicatorSnapshot aggregates key indicator values at a specific timestamp.
// Price-based indicators are normalized to relative/percentage form for cross-symbol comparison.
type IndicatorSnapshot struct {
	Close   float64 `json:"close"`
	RSI     float64 `json:"rsi"`
	MACD    float64 `json:"macd"` // histogram (oscillator, unchanged)
	ADX     float64 `json:"adx"`
	EMABias float64 `json:"emabias"`  // (Close - EMA) / EMA * 100
	BollPct float64 `json:"boll_pct"` // (Close - Lower) / (Upper - Lower), 0..1 band position
	ATRPct  float64 `json:"atr_pct"`  // ATR / Close * 100
	Bias    float64 `json:"bias"`
	VolMult float64 `json:"vol_mult"` // Volume multiplier vs previous 5 bars
}

// ComputeIndicatorSnapshot computes a snapshot of indicators on the last kline of the slice.
// The caller is responsible for passing a kline window that covers the required lookback.
// rsiPeriod / emaPeriod / macdFast / macdSlow / macdSignal are configurable; other periods follow
// standard defaults:
// - ATR / ADX: 14
// - BOLL: 20, 2σ
// - Bias: (Price - MA(N)) / MA(N) * 100, where N = emaPeriod (for analysis purposes)
func ComputeIndicatorSnapshot(klines []Kline, rsiPeriod, emaPeriod, macdFast, macdSlow, macdSignal int) IndicatorSnapshot {
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

	// VolMult: 当前K线Volume / 前5根K线Volume的平均值
	if len(klines) > 5 {
		sum := 0.0
		for i := len(klines) - 6; i < len(klines)-1; i++ {
			if i >= 0 {
				sum += klines[i].Volume
			}
		}
		avg := sum / 5.0
		if avg > 0 && !math.IsNaN(avg) && !math.IsInf(avg, 0) {
			snap.VolMult = last.Volume / avg
		}
	}

	return snap
}
