package api

import (
	"math"
	"net/http"
	"strings"

	"nofx/market"

	"github.com/gin-gonic/gin"
)

// ScreenerFilterCondition 单条过滤条件
type ScreenerFilterCondition struct {
	Timeframe string  `json:"timeframe"` // 5m, 15m, 1h
	Indicator string  `json:"indicator"` // rsi, rsi_7, adx, bias, bias_20, vol_mult, realtime_rolling_volmult, emabias, boll_pct, atr_pct, macd
	Operator  string  `json:"operator"`  // >, <, >=, <=, ==, between
	Value     float64 `json:"value"`
	Value2    float64 `json:"value2,omitempty"` // 仅 between 时使用
}

// ScreenerFilterRequest POST /api/screener/filter 请求体
type ScreenerFilterRequest struct {
	Filters []ScreenerFilterCondition `json:"filters"`
}

// ScreenerFilterResponse 返回匹配的币种及最新指标快照（纯内存，禁止 HTTP 拉 K 线）
type ScreenerFilterResponse struct {
	Symbols []market.CoinQuantState `json:"symbols"`
	Count   int                     `json:"count"`
}

func (s *Server) handleScreenerFilter(c *gin.Context) {
	var req ScreenerFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request body")
		return
	}

	cache := market.GetScreenerCache()
	snap := cache.Snapshot()

	// 遍历内存缓存，应用所有条件
	var result []market.CoinQuantState
	for _, state := range snap {
		cpy := state.Copy()
		match := true
		for _, f := range req.Filters {
			tf := normalizeTF(f.Timeframe)
			snapTF, ok := cpy.ByTF[tf]
			if !ok {
				match = false
				break
			}
			v := getIndicatorValue(snapTF, f.Indicator)
			if !applyOperator(f.Operator, v, f.Value, f.Value2) {
				match = false
				break
			}
		}
		if match {
			result = append(result, cpy)
		}
	}

	c.JSON(http.StatusOK, ScreenerFilterResponse{
		Symbols: result,
		Count:   len(result),
	})
}

func normalizeTF(tf string) string {
	tf = strings.TrimSpace(strings.ToLower(tf))
	if tf == "1h" {
		return "1h"
	}
	if tf == "15m" {
		return "15m"
	}
	return "5m"
}

func getIndicatorValue(snap market.IndicatorSnapshot, name string) float64 {
	name = strings.TrimSpace(strings.ToLower(name))
	switch name {
	case "rsi", "rsi_7":
		return snap.RSI
	case "adx":
		return snap.ADX
	case "bias", "bias_20":
		return snap.Bias
	case "vol_mult", "realtime_rolling_volmult":
		if snap.RealtimeRollingVolMult != 0 {
			return snap.RealtimeRollingVolMult
		}
		return snap.VolMult
	case "emabias":
		return snap.EMABias
	case "boll_pct":
		return snap.BollPct
	case "atr_pct":
		return snap.ATRPct
	case "macd":
		return snap.MACD
	default:
		return 0
	}
}

func applyOperator(op string, v, a, b float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	op = strings.TrimSpace(strings.ToLower(op))
	switch op {
	case ">":
		return v > a
	case "<":
		return v < a
	case ">=":
		return v >= a
	case "<=":
		return v <= a
	case "==", "=":
		return v == a
	case "between":
		return v >= a && v <= b
	default:
		return false
	}
}
