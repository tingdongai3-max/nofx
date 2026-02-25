package market

import (
	"context"
	"fmt"
	"math"
	"nofx/logger"
	"nofx/provider/coinank/coinank_api"
	"nofx/provider/coinank/coinank_enum"
	"nofx/provider/hyperliquid"
	"sort"
	"strconv"
	"strings"
	"time"

	talib "github.com/markcheno/go-talib"
)

// Note: Kline data now uses free/open API (coinank_api.Kline) which doesn't require authentication

// getKlinesFromCoinAnk fetches kline data from CoinAnk API (replacement for WSMonitorCli)
func getKlinesFromCoinAnk(symbol, interval, exchange string, limit int) ([]Kline, error) {
	// Map interval string to coinank enum
	var coinankInterval coinank_enum.Interval
	switch interval {
	case "1m":
		coinankInterval = coinank_enum.Minute1
	case "3m":
		coinankInterval = coinank_enum.Minute3
	case "5m":
		coinankInterval = coinank_enum.Minute5
	case "15m":
		coinankInterval = coinank_enum.Minute15
	case "30m":
		coinankInterval = coinank_enum.Minute30
	case "1h":
		coinankInterval = coinank_enum.Hour1
	case "2h":
		coinankInterval = coinank_enum.Hour2
	case "4h":
		coinankInterval = coinank_enum.Hour4
	case "6h":
		coinankInterval = coinank_enum.Hour6
	case "8h":
		coinankInterval = coinank_enum.Hour8
	case "12h":
		coinankInterval = coinank_enum.Hour12
	case "1d":
		coinankInterval = coinank_enum.Day1
	case "3d":
		coinankInterval = coinank_enum.Day3
	case "1w":
		coinankInterval = coinank_enum.Week1
	default:
		return nil, fmt.Errorf("unsupported interval: %s", interval)
	}

	// Map exchange string to coinank enum
	var coinankExchange coinank_enum.Exchange
	switch strings.ToLower(exchange) {
	case "binance":
		coinankExchange = coinank_enum.Binance
	case "bybit":
		coinankExchange = coinank_enum.Bybit
	case "okx":
		coinankExchange = coinank_enum.Okex
	case "bitget":
		coinankExchange = coinank_enum.Bitget
	case "gate":
		coinankExchange = coinank_enum.Gate
	case "hyperliquid":
		coinankExchange = coinank_enum.Hyperliquid
	case "aster":
		coinankExchange = coinank_enum.Aster
	default:
		// Default to Binance for unknown exchanges
		coinankExchange = coinank_enum.Binance
	}

	// Prefer live WebSocket buffer first to achieve real-time, zero-HTTP quotes.
	ensureKlineStream(symbol, interval, exchange)
	if live, ok := getRealtimeKlines(symbol, interval, exchange, limit); ok && len(live) > 0 {
		return live, nil
	}

	// Fallback: call CoinAnk free/open HTTP API (no authentication required).
	// This path is mainly for historical backfill or when WS hasn't produced data yet.
	ctx := context.Background()
	ts := time.Now().UnixMilli()
	coinankKlines, err := coinank_api.Kline(ctx, symbol, coinankExchange, ts, coinank_enum.To, limit, coinankInterval)
	if err != nil {
		// If exchange-specific data fails, fallback to Binance
		if coinankExchange != coinank_enum.Binance {
			logger.Warnf("⚠️ CoinAnk %s data failed, falling back to Binance: %v", exchange, err)
			coinankKlines, err = coinank_api.Kline(ctx, symbol, coinank_enum.Binance, ts, coinank_enum.To, limit, coinankInterval)
			if err != nil {
				return nil, fmt.Errorf("CoinAnk API error (fallback): %w", err)
			}
		} else {
			return nil, fmt.Errorf("CoinAnk API error: %w", err)
		}
	}

	// Convert coinank kline format to market.Kline format
	klines := make([]Kline, len(coinankKlines))
	for i, ck := range coinankKlines {
		klines[i] = Kline{
			OpenTime:  ck.StartTime,
			Open:      ck.Open,
			High:      ck.High,
			Low:       ck.Low,
			Close:     ck.Close,
			Volume:    ck.Volume,
			CloseTime: ck.EndTime,
		}
	}

	return klines, nil
}

// getKlinesFromHyperliquid fetches kline data from Hyperliquid API for xyz dex assets
func getKlinesFromHyperliquid(symbol, interval string, limit int) ([]Kline, error) {
	// Remove xyz: prefix if present for the API call
	baseCoin := strings.TrimPrefix(symbol, "xyz:")

	// Map interval to Hyperliquid format
	hlInterval := hyperliquid.MapTimeframe(interval)

	// Create Hyperliquid client
	client := hyperliquid.NewClient()

	// Fetch candles
	ctx := context.Background()
	candles, err := client.GetCandles(ctx, baseCoin, hlInterval, limit)
	if err != nil {
		return nil, fmt.Errorf("Hyperliquid API error: %w", err)
	}

	// Convert to market.Kline format
	klines := make([]Kline, len(candles))
	for i, c := range candles {
		open, _ := strconv.ParseFloat(c.Open, 64)
		high, _ := strconv.ParseFloat(c.High, 64)
		low, _ := strconv.ParseFloat(c.Low, 64)
		closePrice, _ := strconv.ParseFloat(c.Close, 64)
		volume, _ := strconv.ParseFloat(c.Volume, 64)

		klines[i] = Kline{
			OpenTime:  c.OpenTime,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closePrice,
			Volume:    volume,
			CloseTime: c.CloseTime,
		}
	}

	return klines, nil
}

// Get retrieves market data for the specified token (uses Binance data by default).
// opts 为 nil 时使用默认指标参数并填充 DynamicIndicators。
func Get(symbol string, opts *IndicatorParams) (*Data, error) {
	return GetWithExchange(symbol, "binance", opts)
}

// GetWithExchange retrieves market data for the specified token using exchange-specific data.
// opts 为 nil 时使用默认指标参数。
func GetWithExchange(symbol, exchange string, opts *IndicatorParams) (*Data, error) {
	var klines3m, klines4h []Kline
	var err error
	// Normalize symbol
	symbol = Normalize(symbol)

	// Check if this is an xyz dex asset (use Hyperliquid API)
	isXyzAsset := IsXyzDexAsset(symbol)

	// For hyperliquid exchange, also use Hyperliquid API
	useHyperliquidAPI := isXyzAsset || strings.ToLower(exchange) == "hyperliquid"

	// Get 3-minute K-line data (or 5-minute for xyz assets as 3m may not be available)
	if useHyperliquidAPI {
		// Use Hyperliquid API for xyz dex assets (use 5m since 3m may not be available)
		klines3m, err = getKlinesFromHyperliquid(symbol, "5m", 100)
		if err != nil {
			return nil, fmt.Errorf("Failed to get 5-minute K-line from Hyperliquid: %v", err)
		}
	} else {
		// Use CoinAnk for regular crypto assets with exchange-specific data
		klines3m, err = getKlinesFromCoinAnk(symbol, "3m", exchange, 100)
		if err != nil {
			return nil, fmt.Errorf("Failed to get 3-minute K-line from CoinAnk (%s): %v", exchange, err)
		}
	}

	// Data staleness detection: Prevent DOGEUSDT-style price freeze issues
	if isStaleData(klines3m, symbol) {
		logger.Infof("⚠️  WARNING: %s detected stale data (consecutive price freeze), skipping symbol", symbol)
		return nil, fmt.Errorf("%s data is stale, possible cache failure", symbol)
	}

	// Get 4-hour K-line data
	if useHyperliquidAPI {
		klines4h, err = getKlinesFromHyperliquid(symbol, "4h", 100)
		if err != nil {
			return nil, fmt.Errorf("Failed to get 4-hour K-line from Hyperliquid: %v", err)
		}
	} else {
		klines4h, err = getKlinesFromCoinAnk(symbol, "4h", exchange, 100)
		if err != nil {
			return nil, fmt.Errorf("Failed to get 4-hour K-line from CoinAnk (%s): %v", exchange, err)
		}
	}

	// Check if data is empty
	if len(klines3m) == 0 {
		return nil, fmt.Errorf("3-minute K-line data is empty")
	}
	if len(klines4h) == 0 {
		return nil, fmt.Errorf("4-hour K-line data is empty")
	}

	currentPrice := klines3m[len(klines3m)-1].Close
	dynamicIndicators := fillDynamicIndicators(klines3m, opts)

	// Calculate price change percentage
	// 1-hour price change = price from 20 3-minute K-lines ago
	priceChange1h := 0.0
	if len(klines3m) >= 21 { // Need at least 21 K-lines (current + 20 previous)
		price1hAgo := klines3m[len(klines3m)-21].Close
		if price1hAgo > 0 {
			priceChange1h = ((currentPrice - price1hAgo) / price1hAgo) * 100
		}
	}

	// 4-hour price change = price from 1 4-hour K-line ago
	priceChange4h := 0.0
	if len(klines4h) >= 2 {
		price4hAgo := klines4h[len(klines4h)-2].Close
		if price4hAgo > 0 {
			priceChange4h = ((currentPrice - price4hAgo) / price4hAgo) * 100
		}
	}

	// Get OI data
	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		// OI failure doesn't affect overall result, use default values
		oiData = &OIData{Latest: 0, Average: 0}
	}

	fundingRate, _ := getFundingRate(symbol)
	intradayData := calculateIntradaySeries(klines3m)
	longerTermData := calculateLongerTermData(klines4h)
	fibonacci := fillFibonacci(klines3m)

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		DynamicIndicators: dynamicIndicators,
		Fibonacci:         fibonacci,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		IntradaySeries:    intradayData,
		LongerTermContext: longerTermData,
	}, nil
}

// DefaultCountForTimeframe 返回未配置时各周期默认 K 线数量（小周期多、大周期少，控制 Token），供调用方构建 counts 使用
func DefaultCountForTimeframe(tf string) int {
	switch tf {
	case "1m":
		return 200
	case "3m":
		return 150
	case "5m":
		return 100
	case "15m":
		return 50
	case "30m":
		return 40
	case "1h":
		return 30
	case "2h":
		return 24
	case "4h":
		return 20
	case "6h":
		return 16
	case "8h":
		return 14
	case "12h":
		return 12
	case "1d":
		return 10
	case "3d":
		return 8
	case "1w":
		return 6
	default:
		return 50
	}
}

// GetWithTimeframes retrieves market data for specified multiple timeframes (uses Binance by default).
// For exchange-specific data, use GetWithTimeframesWithExchange.
func GetWithTimeframes(symbol string, timeframes []string, primaryTimeframe string, counts map[string]int, opts *IndicatorParams) (*Data, error) {
	return GetWithTimeframesWithExchange(symbol, timeframes, primaryTimeframe, counts, opts, "binance")
}

// GetWithTimeframesWithExchange retrieves market data for specified multiple timeframes from the given exchange.
// exchange must match the trader's connected exchange (e.g. okx, binance) to avoid cross-exchange data pollution.
func GetWithTimeframesWithExchange(symbol string, timeframes []string, primaryTimeframe string, counts map[string]int, opts *IndicatorParams, exchange string) (*Data, error) {
	symbol = Normalize(symbol)
	if exchange == "" {
		exchange = "binance"
	}

	if len(timeframes) == 0 {
		return nil, fmt.Errorf("at least one timeframe is required")
	}

	if primaryTimeframe == "" {
		primaryTimeframe = timeframes[0]
	}

	hasPrimary := false
	for _, tf := range timeframes {
		if tf == primaryTimeframe {
			hasPrimary = true
			break
		}
	}
	if !hasPrimary {
		timeframes = append([]string{primaryTimeframe}, timeframes...)
	}

	timeframeData := make(map[string]*TimeframeSeriesData)
	var primaryKlines []Kline
	isXyzAsset := IsXyzDexAsset(symbol)

	for _, tf := range timeframes {
		limit := counts[tf]
		if limit <= 0 {
			limit = DefaultCountForTimeframe(tf)
		}

		var klines []Kline
		var err error
		if isXyzAsset {
			klines, err = getKlinesFromHyperliquid(symbol, tf, limit)
		} else {
			klines, err = getKlinesFromCoinAnk(symbol, tf, exchange, limit)
		}
		if err != nil {
			logger.Infof("⚠️ Failed to get %s %s K-line: %v", symbol, tf, err)
			continue
		}
		if len(klines) == 0 {
			logger.Infof("⚠️ %s %s K-line data is empty", symbol, tf)
			continue
		}

		if tf == primaryTimeframe {
			primaryKlines = klines
		}

		seriesData := calculateTimeframeSeries(klines, tf, limit)
		timeframeData[tf] = seriesData
	}

	// If primary timeframe data is empty, return error
	if len(primaryKlines) == 0 {
		return nil, fmt.Errorf("Primary timeframe %s K-line data is empty", primaryTimeframe)
	}

	// Data staleness detection
	if isStaleData(primaryKlines, symbol) {
		logger.Infof("⚠️  WARNING: %s detected stale data (consecutive price freeze), skipping symbol", symbol)
		return nil, fmt.Errorf("%s data is stale, possible cache failure", symbol)
	}

	currentPrice := primaryKlines[len(primaryKlines)-1].Close
	dynamicIndicators := fillDynamicIndicators(primaryKlines, opts)
	fibonacci := fillFibonacci(primaryKlines)

	priceChange1h := calculatePriceChangeByBars(primaryKlines, primaryTimeframe, 60)
	priceChange4h := calculatePriceChangeByBars(primaryKlines, primaryTimeframe, 240)

	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		oiData = &OIData{Latest: 0, Average: 0}
	}
	fundingRate, _ := getFundingRate(symbol)

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		DynamicIndicators: dynamicIndicators,
		Fibonacci:         fibonacci,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		TimeframeData:     timeframeData,
	}, nil
}

// calculateTimeframeSeries calculates series data for a single timeframe
func calculateTimeframeSeries(klines []Kline, timeframe string, count int) *TimeframeSeriesData {
	if count <= 0 {
		count = 10 // default
	}

	data := &TimeframeSeriesData{
		Timeframe:   timeframe,
		Klines:      make([]KlineBar, 0, count),
		MidPrices:   make([]float64, 0, count),
		EMA20Values: make([]float64, 0, count),
		EMA50Values: make([]float64, 0, count),
		MACDValues:  make([]float64, 0, count),
		RSI7Values:  make([]float64, 0, count),
		RSI14Values: make([]float64, 0, count),
		Volume:      make([]float64, 0, count),
		BOLLUpper:   make([]float64, 0, count),
		BOLLMiddle:  make([]float64, 0, count),
		BOLLLower:   make([]float64, 0, count),
	}

	// Get latest N data points based on count from config
	start := len(klines) - count
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		// Store full OHLCV kline data
		data.Klines = append(data.Klines, KlineBar{
			Time:   klines[i].OpenTime,
			Open:   klines[i].Open,
			High:   klines[i].High,
			Low:    klines[i].Low,
			Close:  klines[i].Close,
			Volume: klines[i].Volume,
		})

		// Keep MidPrices and Volume for backward compatibility
		data.MidPrices = append(data.MidPrices, klines[i].Close)
		data.Volume = append(data.Volume, klines[i].Volume)

		// Calculate EMA20 for each point
		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		// Calculate EMA50 for each point
		if i >= 49 {
			ema50 := calculateEMA(klines[:i+1], 50)
			data.EMA50Values = append(data.EMA50Values, ema50)
		}

		// Calculate MACD for each point
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		// Calculate RSI for each point
		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}

		// Calculate Bollinger Bands (period 20, std dev multiplier 2)
		if i >= 19 {
			upper, middle, lower := calculateBOLL(klines[:i+1], 20, 2.0)
			data.BOLLUpper = append(data.BOLLUpper, upper)
			data.BOLLMiddle = append(data.BOLLMiddle, middle)
			data.BOLLLower = append(data.BOLLLower, lower)
		}
	}

	// Calculate ATR14
	data.ATR14 = calculateATR(klines, 14)

	return data
}

// calculatePriceChangeByBars calculates how many K-lines to look back for price change based on timeframe
func calculatePriceChangeByBars(klines []Kline, timeframe string, targetMinutes int) float64 {
	if len(klines) < 2 {
		return 0
	}

	// Parse timeframe to minutes
	tfMinutes := parseTimeframeToMinutes(timeframe)
	if tfMinutes <= 0 {
		return 0
	}

	// Calculate how many K-lines to look back
	barsBack := targetMinutes / tfMinutes
	if barsBack < 1 {
		barsBack = 1
	}

	currentPrice := klines[len(klines)-1].Close
	idx := len(klines) - 1 - barsBack
	if idx < 0 {
		idx = 0
	}

	oldPrice := klines[idx].Close
	if oldPrice > 0 {
		return ((currentPrice - oldPrice) / oldPrice) * 100
	}
	return 0
}

// parseTimeframeToMinutes parses timeframe string to minutes
func parseTimeframeToMinutes(tf string) int {
	switch tf {
	case "1m":
		return 1
	case "3m":
		return 3
	case "5m":
		return 5
	case "15m":
		return 15
	case "30m":
		return 30
	case "1h":
		return 60
	case "2h":
		return 120
	case "4h":
		return 240
	case "6h":
		return 360
	case "8h":
		return 480
	case "12h":
		return 720
	case "1d":
		return 1440
	case "3d":
		return 4320
	case "1w":
		return 10080
	default:
		return 0
	}
}

// calculateEMA calculates EMA
func calculateEMA(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}
	closes := make([]float64, len(klines))
	for i, k := range klines {
		closes[i] = k.Close
	}
	out := talib.Ema(closes, period)
	if len(out) == 0 {
		return 0
	}
	return out[len(out)-1]
}

// calculateMACD calculates MACD
func calculateMACD(klines []Kline) float64 {
	if len(klines) < 26 {
		return 0
	}
	closes := make([]float64, len(klines))
	for i, k := range klines {
		closes[i] = k.Close
	}
	macd, _, _ := talib.Macd(closes, 12, 26, 9)
	if len(macd) == 0 {
		return 0
	}
	return macd[len(macd)-1]
}

// calculateRSI calculates RSI
func calculateRSI(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}
	closes := make([]float64, len(klines))
	for i, k := range klines {
		closes[i] = k.Close
	}
	out := talib.Rsi(closes, period)
	if len(out) == 0 {
		return 0
	}
	return out[len(out)-1]
}

// calculateATR calculates ATR
func calculateATR(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trs[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	// Calculate initial ATR
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	// Wilder smoothing
	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}

	return atr
}

// calculateBIAS 乖离率: (当前收盘 - N日均线) / N日均线 * 100. 使用 SMA(Close, N).
func calculateBIAS(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}
	closes := make([]float64, len(klines))
	for i, k := range klines {
		closes[i] = k.Close
	}
	last := len(closes) - 1
	sum := 0.0
	for i := last - period + 1; i <= last; i++ {
		sum += closes[i]
	}
	ma := sum / float64(period)
	if ma == 0 {
		return 0
	}
	return (closes[last] - ma) / ma * 100
}

// calculateBOLL calculates Bollinger Bands (upper, middle, lower)
// period: typically 20, multiplier: typically 2
func calculateBOLL(klines []Kline, period int, multiplier float64) (upper, middle, lower float64) {
	if len(klines) < period {
		return 0, 0, 0
	}
	closes := make([]float64, len(klines))
	for i, k := range klines {
		closes[i] = k.Close
	}
	up, mid, low := talib.BBands(closes, period, multiplier, multiplier, 0)
	if len(up) == 0 || len(mid) == 0 || len(low) == 0 {
		return 0, 0, 0
	}
	last := len(up) - 1
	return up[last], mid[last], low[last]
}

// defaultIndicatorParams 返回默认指标参数（当调用方未传时使用）
func defaultIndicatorParams() *IndicatorParams {
	return &IndicatorParams{
		EMAPeriods:  []int{20, 50},
		RSIPeriods:  []int{7, 14},
		ATRPeriods:  []int{14},
		ADXPeriods:  []int{14},
		BOLLPeriods: []int{20},
		BIASPeriods: []int{6, 12, 24},
		MACDFast:    12,
		MACDSlow:    26,
		MACDSignal:  9,
	}
}

// fillDynamicIndicators 根据 klines 和策略参数填充 DynamicIndicators 字典
func fillDynamicIndicators(klines []Kline, opts *IndicatorParams) map[string]float64 {
	if opts == nil {
		opts = defaultIndicatorParams()
	}
	out := make(map[string]float64)
	closes := make([]float64, len(klines))
	for i, k := range klines {
		closes[i] = k.Close
	}

	// EMA
	for _, p := range opts.EMAPeriods {
		if len(klines) >= p {
			out[fmt.Sprintf("ema_%d", p)] = calculateEMA(klines, p)
		}
	}
	// SMA (simple moving average: sum of last p closes / p)
	for _, p := range opts.SMAPeriods {
		if len(klines) >= p {
			sum := 0.0
			for i := len(closes) - p; i < len(closes); i++ {
				sum += closes[i]
			}
			out[fmt.Sprintf("sma_%d", p)] = sum / float64(p)
		}
	}
	// RSI
	for _, p := range opts.RSIPeriods {
		if len(klines) > p {
			rsi := talib.Rsi(closes, p)
			if len(rsi) > 0 {
				out[fmt.Sprintf("rsi_%d", p)] = rsi[len(rsi)-1]
			}
		}
	}
	// MACD (fast, slow, signal) - 当 fast/slow/sig 为正数时才计算，避免在未启用 MACD 时仍输出
	fast, slow, sig := opts.MACDFast, opts.MACDSlow, opts.MACDSignal
	if fast > 0 && slow > 0 && sig > 0 && len(klines) >= slow {
		macd, signal, hist := talib.Macd(closes, fast, slow, sig)
		if len(macd) > 0 {
			out["macd"] = macd[len(macd)-1]
		}
		if len(signal) > 0 {
			out["macd_signal"] = signal[len(signal)-1]
		}
		if len(hist) > 0 {
			out["macd_histogram"] = hist[len(hist)-1]
		}
	}
	// ATR
	for _, p := range opts.ATRPeriods {
		if len(klines) > p {
			out[fmt.Sprintf("atr_%d", p)] = calculateATR(klines, p)
		}
	}
	// ADX (Average Directional Index)
	highs := make([]float64, len(klines))
	lows := make([]float64, len(klines))
	for i, k := range klines {
		highs[i] = k.High
		lows[i] = k.Low
	}
	for _, p := range opts.ADXPeriods {
		if len(klines) > p+1 {
			adx := talib.Adx(highs, lows, closes, p)
			if len(adx) > 0 {
				out[fmt.Sprintf("adx_%d", p)] = adx[len(adx)-1]
			}
		}
	}
	// BOLL (last value only for scalar; series in TimeframeSeriesData)
	for _, p := range opts.BOLLPeriods {
		if len(klines) >= p {
			u, m, l := calculateBOLL(klines, p, 2)
			out[fmt.Sprintf("boll_upper_%d", p)] = u
			out[fmt.Sprintf("boll_middle_%d", p)] = m
			out[fmt.Sprintf("boll_lower_%d", p)] = l
		}
	}
	// BIAS 乖离率: (Close - MA(Close,N)) / MA(Close,N) * 100
	for _, p := range opts.BIASPeriods {
		if len(klines) >= p {
			out[fmt.Sprintf("bias_%d", p)] = calculateBIAS(klines, p)
		}
	}
	return out
}

// calculateIntradaySeries calculates intraday series data
func calculateIntradaySeries(klines []Kline) *IntradayData {
	data := &IntradayData{
		MidPrices:   make([]float64, 0, 10),
		EMA20Values: make([]float64, 0, 10),
		MACDValues:  make([]float64, 0, 10),
		RSI7Values:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
		Volume:      make([]float64, 0, 10),
	}

	// Get latest 10 data points
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)
		data.Volume = append(data.Volume, klines[i].Volume)

		// Calculate EMA20 for each point
		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		// Calculate MACD for each point
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		// Calculate RSI for each point
		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	// Calculate 3m ATR14
	data.ATR14 = calculateATR(klines, 14)

	return data
}

// calculateLongerTermData calculates longer-term data
func calculateLongerTermData(klines []Kline) *LongerTermData {
	data := &LongerTermData{
		MACDValues:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	// Calculate EMA
	data.EMA20 = calculateEMA(klines, 20)
	data.EMA50 = calculateEMA(klines, 50)

	// Calculate ATR
	data.ATR3 = calculateATR(klines, 3)
	data.ATR14 = calculateATR(klines, 14)

	// Calculate volume
	if len(klines) > 0 {
		data.CurrentVolume = klines[len(klines)-1].Volume
		// Calculate average volume
		sum := 0.0
		for _, k := range klines {
			sum += k.Volume
		}
		data.AverageVolume = sum / float64(len(klines))
	}

	// Calculate MACD and RSI series
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

// fillFibonacci 根据主周期 K 线区间计算最高、最低及五个斐波那契回撤/扩展位，供 AI 阻力/支撑参考
func fillFibonacci(klines []Kline) map[string]float64 {
	out := make(map[string]float64)
	if len(klines) == 0 {
		return out
	}
	high, low := klines[0].High, klines[0].Low
	for _, k := range klines[1:] {
		if k.High > high {
			high = k.High
		}
		if k.Low < low {
			low = k.Low
		}
	}
	out["high"] = high
	out["low"] = low
	diff := high - low
	if diff <= 0 {
		return out
	}
	// 回撤位：从 high 向下算
	out["0.236"] = high - 0.236*diff
	out["0.382"] = high - 0.382*diff
	out["0.5"] = high - 0.5*diff
	out["0.618"] = high - 0.618*diff
	out["0.786"] = high - 0.786*diff
	return out
}

// getOpenInterestData retrieves OI data
func getOpenInterestData(symbol string) (*OIData, error) {
	// Normalize to Binance-style symbol (e.g., BTCUSDT) for ticker snapshot lookup.
	norm := Normalize(symbol)
	// xyz: assets (stocks/forex/commodities) don't have Binance futures OI; return zeroed data.
	if IsXyzDexAsset(norm) {
		return &OIData{Latest: 0, Average: 0}, nil
	}

	// Remove any xyz: prefix that might remain after normalization.
	norm = strings.TrimPrefix(strings.ToUpper(norm), "XYZ:")

	snap, ok := getTickerSnapshot(norm)
	if !ok || snap == nil {
		// Graceful fallback: no snapshot yet, return zero data.
		return &OIData{Latest: 0, Average: 0}, nil
	}

	oi := snap.OIUSD
	if oi <= 0 {
		return &OIData{Latest: 0, Average: 0}, nil
	}

	return &OIData{
		Latest:  oi,
		Average: oi * 0.999, // Lightweight approximation of average
	}, nil
}

// getFundingRate retrieves funding rate using the global WebSocket ticker stream.
func getFundingRate(symbol string) (float64, error) {
	norm := Normalize(symbol)
	if IsXyzDexAsset(norm) {
		return 0, nil
	}
	norm = strings.TrimPrefix(strings.ToUpper(norm), "XYZ:")

	snap, ok := getTickerSnapshot(norm)
	if !ok || snap == nil {
		return 0, nil
	}
	return snap.FundingRate, nil
}

// Format formats and outputs market data (uses DynamicIndicators for AI prompt)
func Format(data *Data) string {
	var sb strings.Builder

	priceStr := formatPriceWithDynamicPrecision(data.CurrentPrice)
	sb.WriteString(fmt.Sprintf("current_price = %s", priceStr))
	if len(data.DynamicIndicators) > 0 {
		// 按 key 排序输出，便于阅读
		keys := make([]string, 0, len(data.DynamicIndicators))
		for k := range data.DynamicIndicators {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := data.DynamicIndicators[k]
			// 将 key 转为可读名，如 ema_200 -> current_ema200
			readable := strings.ReplaceAll(k, "_", "")
			sb.WriteString(fmt.Sprintf(", current_%s = %.3f", readable, v))
		}
	}
	sb.WriteString("\n\n")

	if len(data.Fibonacci) > 0 {
		sb.WriteString("Fibonacci levels (resistance/support from recent range): ")
		sb.WriteString(fmt.Sprintf("high = %s, low = %s",
			formatPriceWithDynamicPrecision(data.Fibonacci["high"]),
			formatPriceWithDynamicPrecision(data.Fibonacci["low"])))
		for _, k := range []string{"0.236", "0.382", "0.5", "0.618", "0.786"} {
			if v, ok := data.Fibonacci[k]; ok {
				sb.WriteString(fmt.Sprintf(", %s = %s", k, formatPriceWithDynamicPrecision(v)))
			}
		}
		sb.WriteString("\n\n")
	}

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest and funding rate for perps:\n\n",
		data.Symbol))

	if data.OpenInterest != nil {
		// Format OI data with dynamic precision
		oiLatestStr := formatPriceWithDynamicPrecision(data.OpenInterest.Latest)
		oiAverageStr := formatPriceWithDynamicPrecision(data.OpenInterest.Average)
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %s Average: %s\n\n",
			oiLatestStr, oiAverageStr))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))

	if data.IntradaySeries != nil {
		sb.WriteString("Intraday series (3‑minute intervals, oldest → latest):\n\n")

		if len(data.IntradaySeries.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
		}

		if len(data.IntradaySeries.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA indicators (20‑period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
		}

		if len(data.IntradaySeries.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
		}

		if len(data.IntradaySeries.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
		}

		if len(data.IntradaySeries.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
		}

		if len(data.IntradaySeries.Volume) > 0 {
			sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.IntradaySeries.Volume)))
		}

		sb.WriteString(fmt.Sprintf("3m ATR (14‑period): %.3f\n\n", data.IntradaySeries.ATR14))
	}

	if data.LongerTermContext != nil {
		sb.WriteString("Longer‑term context (4‑hour timeframe):\n\n")

		sb.WriteString(fmt.Sprintf("20‑Period EMA: %.3f vs. 50‑Period EMA: %.3f\n\n",
			data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))

		sb.WriteString(fmt.Sprintf("3‑Period ATR: %.3f vs. 14‑Period ATR: %.3f\n\n",
			data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))

		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
			data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))

		if len(data.LongerTermContext.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
		}

		if len(data.LongerTermContext.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
		}
	}

	// Multi-timeframe data (new)
	if len(data.TimeframeData) > 0 {
		// Output sorted by timeframe
		timeframeOrder := []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w"}
		for _, tf := range timeframeOrder {
			if tfData, ok := data.TimeframeData[tf]; ok {
				sb.WriteString(fmt.Sprintf("=== %s Timeframe ===\n\n", strings.ToUpper(tf)))
				formatTimeframeData(&sb, tfData)
			}
		}
	}

	return sb.String()
}

// formatTimeframeData formats data for a single timeframe
func formatTimeframeData(sb *strings.Builder, data *TimeframeSeriesData) {
	// Use OHLCV table format if kline data is available
	if len(data.Klines) > 0 {
		sb.WriteString("Time(UTC)      Open      High      Low       Close     Volume\n")
		for i, k := range data.Klines {
			t := time.Unix(k.Time/1000, 0).UTC()
			timeStr := t.Format("01-02 15:04")
			marker := ""
			if i == len(data.Klines)-1 {
				marker = "  <- current"
			}
			sb.WriteString(fmt.Sprintf("%-14s %-9.4f %-9.4f %-9.4f %-9.4f %-12.2f%s\n",
				timeStr, k.Open, k.High, k.Low, k.Close, k.Volume, marker))
		}
		sb.WriteString("\n")
	} else if len(data.MidPrices) > 0 {
		// Fallback to old format for backward compatibility
		sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.MidPrices)))
		if len(data.Volume) > 0 {
			sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.Volume)))
		}
	}

	// Technical indicators
	if len(data.EMA20Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA20: %s\n", formatFloatSlice(data.EMA20Values)))
	}

	if len(data.EMA50Values) > 0 {
		sb.WriteString(fmt.Sprintf("EMA50: %s\n", formatFloatSlice(data.EMA50Values)))
	}

	if len(data.MACDValues) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.MACDValues)))
	}

	if len(data.RSI7Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI7: %s\n", formatFloatSlice(data.RSI7Values)))
	}

	if len(data.RSI14Values) > 0 {
		sb.WriteString(fmt.Sprintf("RSI14: %s\n", formatFloatSlice(data.RSI14Values)))
	}

	if data.ATR14 > 0 {
		sb.WriteString(fmt.Sprintf("ATR14: %.4f\n", data.ATR14))
	}

	sb.WriteString("\n")
}

// formatPriceWithDynamicPrecision dynamically selects precision based on price range
// This perfectly supports all coins from ultra-low price meme coins (< 0.0001) to BTC/ETH
func formatPriceWithDynamicPrecision(price float64) string {
	switch {
	case price < 0.0001:
		// Ultra-low price meme coins: 1000SATS, 1000WHY, DOGS
		// 0.00002070 → "0.00002070" (8 decimal places)
		return fmt.Sprintf("%.8f", price)
	case price < 0.001:
		// Low price meme coins: NEIRO, HMSTR, HOT, NOT
		// 0.00015060 → "0.000151" (6 decimal places)
		return fmt.Sprintf("%.6f", price)
	case price < 0.01:
		// Mid-low price coins: PEPE, SHIB, MEME
		// 0.00556800 → "0.005568" (6 decimal places)
		return fmt.Sprintf("%.6f", price)
	case price < 1.0:
		// Low price coins: ASTER, DOGE, ADA, TRX
		// 0.9954 → "0.9954" (4 decimal places)
		return fmt.Sprintf("%.4f", price)
	case price < 100:
		// Mid price coins: SOL, AVAX, LINK, MATIC
		// 23.4567 → "23.4567" (4 decimal places)
		return fmt.Sprintf("%.4f", price)
	default:
		// High price coins: BTC, ETH (save tokens)
		// 45678.9123 → "45678.91" (2 decimal places)
		return fmt.Sprintf("%.2f", price)
	}
}

// formatFloatSlice formats float64 slice to string (using dynamic precision)
func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = formatPriceWithDynamicPrecision(v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}

// xyz dex assets that should NOT get USDT suffix
var xyzDexAssets = map[string]bool{
	// Stocks
	"TSLA": true, "NVDA": true, "AAPL": true, "MSFT": true, "META": true,
	"AMZN": true, "GOOGL": true, "AMD": true, "COIN": true, "NFLX": true,
	"PLTR": true, "HOOD": true, "INTC": true, "MSTR": true, "TSM": true,
	"ORCL": true, "MU": true, "RIVN": true, "COST": true, "LLY": true,
	"CRCL": true, "SKHX": true, "SNDK": true,
	// Forex
	"EUR": true, "JPY": true,
	// Commodities
	"GOLD": true, "SILVER": true,
	// Index
	"XYZ100": true,
}

// IsXyzDexAsset checks if a symbol is an xyz dex asset
func IsXyzDexAsset(symbol string) bool {
	base := strings.ToUpper(symbol)
	// Remove any prefix/suffix
	base = strings.TrimPrefix(base, "XYZ:")
	for _, suffix := range []string{"USDT", "USD", "-USDC"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
			break
		}
	}
	return xyzDexAssets[base]
}

// Normalize normalizes symbol
// For crypto: ensures it's a USDT trading pair
// For xyz dex assets (stocks, forex, commodities): uses xyz: prefix without USDT suffix
func Normalize(symbol string) string {
	symbol = strings.ToUpper(symbol)

	// Check if this is an xyz dex asset
	if IsXyzDexAsset(symbol) {
		// Remove any xyz: prefix (case-insensitive) and USDT suffix, then add xyz: prefix
		base := symbol
		// Handle both lowercase and uppercase xyz: prefix
		if strings.HasPrefix(strings.ToLower(base), "xyz:") {
			base = base[4:] // Remove first 4 characters ("xyz:")
		}
		for _, suffix := range []string{"USDT", "USD", "-USDC"} {
			if strings.HasSuffix(base, suffix) {
				base = strings.TrimSuffix(base, suffix)
				break
			}
		}
		return "xyz:" + base
	}

	// Remove exchange-specific separators (Gate uses BTC_USDT, OKX uses BTC-USDT-SWAP)
	symbol = strings.ReplaceAll(symbol, "_", "")
	symbol = strings.ReplaceAll(symbol, "-SWAP", "")
	symbol = strings.ReplaceAll(symbol, "-", "")

	// For regular crypto assets
	if strings.HasSuffix(symbol, "USDT") {
		return symbol
	}
	return symbol + "USDT"
}

// parseFloat parses float value
func parseFloat(v interface{}) (float64, error) {
	switch val := v.(type) {
	case string:
		return strconv.ParseFloat(val, 64)
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("unsupported type: %T", v)
	}
}

// BuildDataFromKlines constructs market data snapshot from preloaded K-line series (for backtesting/simulation).
func BuildDataFromKlines(symbol string, primary []Kline, longer []Kline) (*Data, error) {
	if len(primary) == 0 {
		return nil, fmt.Errorf("primary series is empty")
	}

	symbol = Normalize(symbol)
	current := primary[len(primary)-1]
	currentPrice := current.Close

	data := &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		DynamicIndicators: fillDynamicIndicators(primary, nil),
		Fibonacci:         fillFibonacci(primary),
		PriceChange1h:     priceChangeFromSeries(primary, time.Hour),
		PriceChange4h:     priceChangeFromSeries(primary, 4*time.Hour),
		OpenInterest:      &OIData{Latest: 0, Average: 0},
		FundingRate:       0,
		IntradaySeries:    calculateIntradaySeries(primary),
		LongerTermContext: nil,
	}

	if len(longer) > 0 {
		data.LongerTermContext = calculateLongerTermData(longer)
	}

	return data, nil
}

func priceChangeFromSeries(series []Kline, duration time.Duration) float64 {
	if len(series) == 0 || duration <= 0 {
		return 0
	}
	last := series[len(series)-1]
	target := last.CloseTime - duration.Milliseconds()
	for i := len(series) - 1; i >= 0; i-- {
		if series[i].CloseTime <= target {
			price := series[i].Close
			if price > 0 {
				return ((last.Close - price) / price) * 100
			}
			break
		}
	}
	return 0
}

// isStaleData detects stale data (consecutive price freeze)
// Fix DOGEUSDT-style issue: consecutive N periods with completely unchanged prices indicate data source anomaly
func isStaleData(klines []Kline, symbol string) bool {
	if len(klines) < 5 {
		return false // Insufficient data to determine
	}

	// Detection threshold: 5 consecutive 3-minute periods with unchanged price (15 minutes without fluctuation)
	const stalePriceThreshold = 5
	const priceTolerancePct = 0.0001 // 0.01% fluctuation tolerance (avoid false positives)

	// Take the last stalePriceThreshold K-lines
	recentKlines := klines[len(klines)-stalePriceThreshold:]
	firstPrice := recentKlines[0].Close

	// Check if all prices are within tolerance
	for i := 1; i < len(recentKlines); i++ {
		priceDiff := math.Abs(recentKlines[i].Close-firstPrice) / firstPrice
		if priceDiff > priceTolerancePct {
			return false // Price fluctuation exists, data is normal
		}
	}

	// Additional check: MACD and volume
	// If price is unchanged but MACD/volume shows normal fluctuation, it might be a real market situation (extremely low volatility)
	// Check if volume is also 0 (data completely frozen)
	allVolumeZero := true
	for _, k := range recentKlines {
		if k.Volume > 0 {
			allVolumeZero = false
			break
		}
	}

	if allVolumeZero {
		logger.Infof("⚠️  %s stale data confirmed: price freeze + zero volume", symbol)
		return true
	}

	// Price frozen but has volume: might be extremely low volatility market, allow but log warning
	logger.Infof("⚠️  %s detected extreme price stability (no fluctuation for %d consecutive periods), but volume is normal", symbol, stalePriceThreshold)
	return false
}

// ========== 导出的指标计算函数（供测试使用） ==========

// ExportCalculateEMA exports calculateEMA for testing
func ExportCalculateEMA(klines []Kline, period int) float64 {
	return calculateEMA(klines, period)
}

// ExportCalculateMACD exports calculateMACD for testing
func ExportCalculateMACD(klines []Kline) float64 {
	return calculateMACD(klines)
}

// ExportCalculateRSI exports calculateRSI for testing
func ExportCalculateRSI(klines []Kline, period int) float64 {
	return calculateRSI(klines, period)
}

// ExportCalculateATR exports calculateATR for testing
func ExportCalculateATR(klines []Kline, period int) float64 {
	return calculateATR(klines, period)
}

// ExportCalculateBOLL exports calculateBOLL for testing
func ExportCalculateBOLL(klines []Kline, period int, multiplier float64) (upper, middle, lower float64) {
	return calculateBOLL(klines, period, multiplier)
}

// calculateDonchian calculates Donchian channel (highest high, lowest low) for given period
func calculateDonchian(klines []Kline, period int) (upper, lower float64) {
	if len(klines) == 0 || period <= 0 {
		return 0, 0
	}

	// Use all available klines if period > len(klines)
	start := len(klines) - period
	if start < 0 {
		start = 0
	}

	upper = klines[start].High
	lower = klines[start].Low

	for i := start + 1; i < len(klines); i++ {
		if klines[i].High > upper {
			upper = klines[i].High
		}
		if klines[i].Low < lower {
			lower = klines[i].Low
		}
	}

	return upper, lower
}

// ExportCalculateDonchian exports calculateDonchian for testing
func ExportCalculateDonchian(klines []Kline, period int) (float64, float64) {
	return calculateDonchian(klines, period)
}

// Box period constants (in 1h candles)
const (
	ShortBoxPeriod = 72  // 3 days of 1h candles
	MidBoxPeriod   = 240 // 10 days of 1h candles
	LongBoxPeriod  = 500 // ~21 days of 1h candles
)

// calculateBoxData calculates multi-period box data from klines
func calculateBoxData(klines []Kline, currentPrice float64) *BoxData {
	box := &BoxData{
		CurrentPrice: currentPrice,
	}

	if len(klines) == 0 {
		return box
	}

	box.ShortUpper, box.ShortLower = calculateDonchian(klines, ShortBoxPeriod)
	box.MidUpper, box.MidLower = calculateDonchian(klines, MidBoxPeriod)
	box.LongUpper, box.LongLower = calculateDonchian(klines, LongBoxPeriod)

	return box
}

// ExportCalculateBoxData exports calculateBoxData for testing
func ExportCalculateBoxData(klines []Kline, currentPrice float64) *BoxData {
	return calculateBoxData(klines, currentPrice)
}

// GetBoxData fetches 1h klines and calculates box data for a symbol
func GetBoxData(symbol string) (*BoxData, error) {
	symbol = Normalize(symbol)

	// Fetch 500 1h klines
	var klines []Kline
	var err error

	if IsXyzDexAsset(symbol) {
		klines, err = getKlinesFromHyperliquid(symbol, "1h", LongBoxPeriod)
	} else {
		klines, err = getKlinesFromCoinAnk(symbol, "1h", "binance", LongBoxPeriod)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get 1h klines: %w", err)
	}

	if len(klines) == 0 {
		return nil, fmt.Errorf("no kline data available")
	}

	currentPrice := klines[len(klines)-1].Close

	return calculateBoxData(klines, currentPrice), nil
}
