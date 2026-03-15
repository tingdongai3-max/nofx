// Package ta 提供轻量级技术分析指标计算
// 主要用于宏观趋势判断（SuperTrend + ADX）
package ta

import (
	"math"

	"github.com/markcheno/go-talib"
)

// Kline K线数据（兼容 market.Kline）
type Kline struct {
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64
}

// MacroTrend 宏观趋势结构
type MacroTrend struct {
	Timeframe     string  `json:"timeframe"`      // 时间周期，如 "1h", "1d"
	Direction     string  `json:"direction"`      // 趋势方向：up 或 down
	ADXValue     float64 `json:"adx_value"`      // ADX 数值
	TrendStrength string  `json:"trend_strength"` // 趋势强度：strong 或 weak
	SuperTrend   float64 `json:"super_trend"`    // SuperTrend 线数值
}

// SuperTrendConfig SuperTrend 配置
type SuperTrendConfig struct {
	Period    int     // ATR 周期，默认 10
	Multiplier float64 // 倍数，默认 3.0
}

// DefaultSuperTrendConfig 默认配置
var DefaultSuperTrendConfig = SuperTrendConfig{
	Period:     10,
	Multiplier: 3.0,
}

// GetMacroTrend 计算宏观趋势
// 输入：大级别 K 线切片（如 1h 或 1d）
// 输出：MacroTrend 结构
func GetMacroTrend(klines []Kline, timeframe string, config ...SuperTrendConfig) MacroTrend {
	if len(klines) < 14 {
		return MacroTrend{
			Timeframe:     timeframe,
			Direction:     "unknown",
			ADXValue:      0,
			TrendStrength: "weak",
			SuperTrend:    0,
		}
	}

	cfg := DefaultSuperTrendConfig
	if len(config) > 0 {
		cfg = config[0]
	}

	// 提取收盘价、最高价、最低价序列
	closes := make([]float64, len(klines))
	highs := make([]float64, len(klines))
	lows := make([]float64, len(klines))

	for i, k := range klines {
		closes[i] = k.Close
		highs[i] = k.High
		lows[i] = k.Low
	}

	// 计算 ATR
	atr := talib.Atr(highs, lows, closes, cfg.Period)
	if len(atr) == 0 || math.IsNaN(atr[len(atr)-1]) {
		return MacroTrend{
			Timeframe:     timeframe,
			Direction:     "unknown",
			ADXValue:      0,
			TrendStrength: "weak",
			SuperTrend:    0,
		}
	}

	// 计算 SuperTrend
	// 使用前一根已闭合 K 线的 ATR（无未来函数）
	superTrend, direction := calculateSuperTrend(highs, lows, closes, atr, cfg.Period, cfg.Multiplier)

	// 计算 ADX
	adx := calculateADX(highs, lows, closes, 14)
	trendStrength := "weak"
	if adx >= 20 {
		trendStrength = "strong"
	}

	return MacroTrend{
		Timeframe:     timeframe,
		Direction:     direction,
		ADXValue:      adx,
		TrendStrength: trendStrength,
		SuperTrend:   superTrend,
	}
}

// calculateSuperTrend 计算 SuperTrend（简化版）
// 使用前一根已闭合 K 线的数据（无未来函数）
func calculateSuperTrend(highs, lows, closes []float64, atr []float64, period int, multiplier float64) (float64, string) {
	length := len(closes)
	if length < period+2 {
		return 0, "unknown"
	}

	// 使用前一根 K 线（已闭合）的 ATR
	atrValue := atr[length-2]
	if math.IsNaN(atrValue) || atrValue == 0 {
		atrValue = atr[length-1]
	}

	// 从第一根开始计算
	var direction string
	var upperBand, lowerBand float64

	for i := period; i < length-1; i++ {
		hl2 := (highs[i] + lows[i]) / 2
		currentUpper := hl2 + (multiplier * atrValue)
		currentLower := hl2 - (multiplier * atrValue)

		if i == period {
			// 初始化
			if closes[i] > currentUpper {
				direction = "up"
				upperBand = currentUpper
				lowerBand = currentLower
			} else {
				direction = "down"
				upperBand = currentUpper
				lowerBand = currentLower
			}
			continue
		}

		// 更新轨线
		if direction == "up" {
			upperBand = max(upperBand, currentUpper)
			if closes[i] < upperBand {
				upperBand = currentUpper
				lowerBand = currentLower
				direction = "down"
			} else {
				lowerBand = min(lowerBand, currentLower)
			}
		} else {
			lowerBand = min(lowerBand, currentLower)
			if closes[i] > lowerBand {
				upperBand = currentUpper
				lowerBand = currentLower
				direction = "up"
			} else {
				upperBand = max(upperBand, currentUpper)
			}
		}
	}

	// 计算当前的 SuperTrend 值
	currentHL2 := (highs[length-1] + lows[length-1]) / 2
	currentUpper := currentHL2 + (multiplier * atrValue)
	currentLower := currentHL2 - (multiplier * atrValue)

	var superTrend float64
	if direction == "up" {
		upperBand = max(upperBand, currentUpper)
		if closes[length-1] < upperBand {
			superTrend = upperBand
			direction = "down"
		} else {
			superTrend = currentLower
		}
	} else {
		lowerBand = min(lowerBand, currentLower)
		if closes[length-1] > lowerBand {
			superTrend = lowerBand
			direction = "up"
		} else {
			superTrend = currentUpper
		}
	}

	return superTrend, direction
}

// calculateADX 计算 ADX（平均趋向指数）
func calculateADX(highs, lows, closes []float64, period int) float64 {
	if len(highs) < period+1 {
		return 0
	}

	// 使用 go-talib 计算 ADX
	adx := talib.Adx(highs, lows, closes, period)
	if len(adx) == 0 {
		return 0
	}

	lastADX := adx[len(adx)-1]
	if math.IsNaN(lastADX) {
		return 0
	}
	return lastADX
}

// IsTrendStrong 判断趋势是否强劲
func IsTrendStrong(adx float64) bool {
	return adx >= 20
}

// GetTrendDirection 获取趋势方向
// 基于价格与 SuperTrend 的位置关系
func GetTrendDirection(currentPrice float64, superTrend float64) string {
	if superTrend == 0 {
		return "unknown"
	}
	if currentPrice > superTrend {
		return "up"
	}
	return "down"
}
