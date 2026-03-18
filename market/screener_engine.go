package market

import (
	"math"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/provider/nofxos"
)

const (
	ScreenerPoolMaxSize   = 150
	ScreenerPoolRefresh   = 5 * time.Minute
	ScreenerRSIPeriod     = 7
	ScreenerADXPeriod     = 14
	ScreenerBiasPeriod    = 20
	ScreenerVolMultBars   = 5
	ScreenerDefaultExchange = "binance"
)

// CoinQuantState 单币种多周期量化状态（线程安全）
type CoinQuantState struct {
	Symbol    string                       `json:"symbol"`
	Price     float64                      `json:"price"`
	ByTF      map[string]IndicatorSnapshot `json:"by_tf"` // "5m", "15m", "1h"
	UpdatedAt int64                        `json:"updated_at"`
	mu        sync.RWMutex
}

// Copy 返回当前状态的只读副本（用于 API 响应）
func (s *CoinQuantState) Copy() CoinQuantState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byTF := make(map[string]IndicatorSnapshot, len(s.ByTF))
	for k, v := range s.ByTF {
		byTF[k] = v
	}
	return CoinQuantState{
		Symbol:    s.Symbol,
		Price:     s.Price,
		ByTF:      byTF,
		UpdatedAt: s.UpdatedAt,
	}
}

// ScreenerCache 全量量化缓存：map[symbol]*CoinQuantState，仅内存遍历，禁止在 API 内拉 K 线
type ScreenerCache struct {
	mu   sync.RWMutex
	data map[string]*CoinQuantState
}

var (
	globalScreenerCache   = &ScreenerCache{data: make(map[string]*CoinQuantState)}
	globalScreenerNofxos  *nofxos.Client
	screenerPoolRefreshAt time.Time
	screenerPoolMu        sync.Mutex
)

// SetScreenerNofxosClient 设置 NOFXOS 客户端（用于构建标的池），可为 nil 则使用 DefaultClient
func SetScreenerNofxosClient(c *nofxos.Client) {
	if c == nil {
		globalScreenerNofxos = nofxos.DefaultClient()
	} else {
		globalScreenerNofxos = c
	}
}

// buildSymbolPool 从 ai500 + oi_top + oi_low 去重，取 top ScreenerPoolMaxSize
func buildSymbolPool() []string {
	client := globalScreenerNofxos
	if client == nil {
		client = nofxos.DefaultClient()
	}
	seen := make(map[string]bool)
	var list []string

	// AI500 取前 100
	ai500, err := client.GetTopRatedCoins(100)
	if err != nil {
		logger.Infof("⚠️ Screener AI500: %v", err)
	} else {
		for _, s := range ai500 {
			s = Normalize(s)
			if !seen[s] {
				seen[s] = true
				list = append(list, s)
			}
		}
	}

	// OI Top
	oiTop, err := client.GetOITopPositions()
	if err != nil {
		logger.Infof("⚠️ Screener OI Top: %v", err)
	} else {
		for _, p := range oiTop {
			s := Normalize(nofxos.NormalizeSymbol(p.Symbol))
			if !seen[s] {
				seen[s] = true
				list = append(list, s)
			}
		}
	}

	// OI Low
	oiLow, err := client.GetOILowPositions()
	if err != nil {
		logger.Infof("⚠️ Screener OI Low: %v", err)
	} else {
		for _, p := range oiLow {
			s := Normalize(nofxos.NormalizeSymbol(p.Symbol))
			if !seen[s] {
				seen[s] = true
				list = append(list, s)
			}
		}
	}

	if len(list) > ScreenerPoolMaxSize {
		list = list[:ScreenerPoolMaxSize]
	}
	return list
}

// ensureScreenerPool 按需刷新标的池并确保所有 symbol 的 5m/15m/1h 流已订阅
func ensureScreenerPool() {
	screenerPoolMu.Lock()
	defer screenerPoolMu.Unlock()
	if time.Since(screenerPoolRefreshAt) < ScreenerPoolRefresh {
		return
	}
	screenerPoolRefreshAt = time.Now()
	pool := buildSymbolPool()
	for _, symbol := range pool {
		for _, tf := range []string{"5m", "15m", "1h"} {
			_ = ensureKlineStream(symbol, tf, ScreenerDefaultExchange)
		}
	}
	logger.Infof("✓ Screener pool refreshed: %d symbols", len(pool))
}

func (c *ScreenerCache) getOrCreate(symbol string) *CoinQuantState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.data[symbol]; ok {
		return s
	}
	s := &CoinQuantState{
		Symbol: symbol,
		ByTF:   make(map[string]IndicatorSnapshot),
	}
	c.data[symbol] = s
	return s
}

func (c *ScreenerCache) updateFromKlines(symbol, interval string, klines []Kline) {
	if len(klines) == 0 {
		return
	}
	nowMs := time.Now().UTC().UnixMilli()
	tfMs := timeframeMsFromString(interval)
	snap := ComputeIndicatorSnapshot(klines, ScreenerRSIPeriod, ScreenerBiasPeriod, 12, 26, 9, ScreenerVolMultBars, nowMs, tfMs)
	state := c.getOrCreate(symbol)
	state.mu.Lock()
	state.ByTF[interval] = snap
	state.Price = snap.Close
	state.UpdatedAt = time.Now().UTC().UnixMilli()
	state.mu.Unlock()
}

// Snapshot 返回当前缓存快照（只读），用于 filter API 遍历
func (c *ScreenerCache) Snapshot() map[string]*CoinQuantState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]*CoinQuantState, len(c.data))
	for k, v := range c.data {
		out[k] = v
	}
	return out
}

// GetScreenerCache 返回全局 Screener 缓存（只读遍历）
func GetScreenerCache() *ScreenerCache {
	return globalScreenerCache
}

// QuantFilterCondition 与 store.QuantFilterCondition 一致，供策略引擎调用
type QuantFilterCondition struct {
	Timeframe string
	Indicator string
	Operator  string
	Value     float64
	Value2    float64
}

// FilterSymbolsByConditions 根据条件在内存缓存中筛选符号，返回通过的 symbol 列表（供策略引擎「量化优选」）
func FilterSymbolsByConditions(conditions []QuantFilterCondition) []string {
	if len(conditions) == 0 {
		return nil
	}
	cache := GetScreenerCache()
	snap := cache.Snapshot()
	var out []string
	for sym, state := range snap {
		cpy := state.Copy()
		match := true
		for _, f := range conditions {
			tf := strings.TrimSpace(strings.ToLower(f.Timeframe))
			if tf == "" {
				tf = "5m"
			}
			snapTF, ok := cpy.ByTF[tf]
			if !ok {
				match = false
				break
			}
			v := getIndicatorValueInternal(snapTF, f.Indicator)
			if !applyOperatorInternal(f.Operator, v, f.Value, f.Value2) {
				match = false
				break
			}
		}
		if match {
			out = append(out, sym)
		}
	}
	return out
}

func getIndicatorValueInternal(snap IndicatorSnapshot, name string) float64 {
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
	case "volume_poc", "poc":
		return snap.VolumePOC
	case "poc_deviation_pct":
		if snap.VolumePOC > 0 && snap.Close > 0 {
			return (snap.Close - snap.VolumePOC) / snap.VolumePOC * 100
		}
		return 0
	case "long_liq_usd":
		return snap.LongLiq
	case "short_liq_usd":
		return snap.ShortLiq
	case "liq_long_short_ratio":
		if snap.ShortLiq > 0 {
			return snap.LongLiq / snap.ShortLiq
		}
		return 0
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

func applyOperatorInternal(op string, v, a, b float64) bool {
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

// RunScreenerEngine 启动流式量化引擎：刷新标的池、订阅 K 线更新并更新内存状态机
func RunScreenerEngine() {
	ensureScreenerPool()
	ch := SubscribeKlineUpdates()
	go func() {
		tick := time.NewTicker(ScreenerPoolRefresh)
		defer tick.Stop()
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				ensureScreenerPool()
				klines, ok := getRealtimeKlines(ev.Symbol, ev.Interval, ev.Exchange, 200)
				if !ok || len(klines) == 0 {
					continue
				}
				globalScreenerCache.updateFromKlines(Normalize(ev.Symbol), ev.Interval, klines)
			case <-tick.C:
				ensureScreenerPool()
			}
		}
	}()
	logger.Infof("✓ Screener engine started (pool refresh %v)", ScreenerPoolRefresh)
}
