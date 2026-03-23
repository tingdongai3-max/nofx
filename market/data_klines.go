package market

import (
	"context"
	"fmt"
	"math"
	"nofx/logger"
	"nofx/provider/coinank/coinank_api"
	"nofx/provider/coinank/coinank_enum"
	"nofx/provider/hyperliquid"
	"nofx/store"
	"sort"
	"strconv"
	"strings"
	"time"
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

	// Call CoinAnk free/open API (no authentication required)
	ctx := context.Background()
	ts := time.Now().UnixMilli()
	// Use "To" side to search backward from current time (get historical klines)
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

func sanitizePeriods(periods []int) []int {
	set := make(map[int]struct{})
	clean := make([]int, 0, len(periods))
	for _, period := range periods {
		if period <= 0 {
			continue
		}
		if _, exists := set[period]; exists {
			continue
		}
		set[period] = struct{}{}
		clean = append(clean, period)
	}
	sort.Ints(clean)
	return clean
}

func normalizedIndicatorConfig(config *store.IndicatorConfig) store.IndicatorConfig {
	defaults := store.GetDefaultStrategyConfig("en").Indicators
	if config == nil {
		defaults.EMAPeriods = sanitizePeriods(defaults.EMAPeriods)
		defaults.RSIPeriods = sanitizePeriods(defaults.RSIPeriods)
		defaults.ATRPeriods = sanitizePeriods(defaults.ATRPeriods)
		defaults.BOLLPeriods = sanitizePeriods(defaults.BOLLPeriods)
		defaults.DonchianPeriods = sanitizePeriods(defaults.DonchianPeriods)
		return defaults
	}

	normalized := *config
	if normalized.EnableEMA && len(normalized.EMAPeriods) == 0 {
		normalized.EMAPeriods = append([]int(nil), defaults.EMAPeriods...)
	}
	if normalized.EnableRSI && len(normalized.RSIPeriods) == 0 {
		normalized.RSIPeriods = append([]int(nil), defaults.RSIPeriods...)
	}
	if normalized.EnableATR && len(normalized.ATRPeriods) == 0 {
		normalized.ATRPeriods = append([]int(nil), defaults.ATRPeriods...)
	}
	if normalized.EnableBOLL && len(normalized.BOLLPeriods) == 0 {
		normalized.BOLLPeriods = append([]int(nil), defaults.BOLLPeriods...)
	}
	if normalized.EnableDonchianBox && len(normalized.DonchianPeriods) == 0 {
		normalized.DonchianPeriods = append([]int(nil), defaults.DonchianPeriods...)
	}
	normalized.EMAPeriods = sanitizePeriods(normalized.EMAPeriods)
	normalized.RSIPeriods = sanitizePeriods(normalized.RSIPeriods)
	normalized.ATRPeriods = sanitizePeriods(normalized.ATRPeriods)
	normalized.BOLLPeriods = sanitizePeriods(normalized.BOLLPeriods)
	normalized.DonchianPeriods = sanitizePeriods(normalized.DonchianPeriods)
	return normalized
}

func computationIndicatorConfig(config *store.IndicatorConfig) store.IndicatorConfig {
	computed := normalizedIndicatorConfig(config)
	defaults := normalizedIndicatorConfig(nil)

	computed.EnableRawKlines = true
	computed.EnableEMA = true
	computed.EnableMACD = true
	computed.EnableRSI = true
	computed.EnableATR = true
	computed.EnableBOLL = true
	computed.EnableDonchianBox = true

	if len(computed.EMAPeriods) == 0 {
		computed.EMAPeriods = append([]int(nil), defaults.EMAPeriods...)
	}
	if len(computed.RSIPeriods) == 0 {
		computed.RSIPeriods = append([]int(nil), defaults.RSIPeriods...)
	}
	if len(computed.ATRPeriods) == 0 {
		computed.ATRPeriods = append([]int(nil), defaults.ATRPeriods...)
	}
	if len(computed.BOLLPeriods) == 0 {
		computed.BOLLPeriods = append([]int(nil), defaults.BOLLPeriods...)
	}
	if len(computed.DonchianPeriods) == 0 {
		computed.DonchianPeriods = append([]int(nil), defaults.DonchianPeriods...)
	}

	return computed
}

// CalculateRequiredFetchCount returns the total window needed for indicator warmup
// plus the user-visible prompt window.
func CalculateRequiredFetchCount(config store.IndicatorConfig, userCount int) int {
	config = computationIndicatorConfig(&config)

	if userCount <= 0 {
		userCount = 30
	}

	maxWarmup := 0

	if config.EnableEMA {
		for _, period := range config.EMAPeriods {
			maxWarmup = max(maxWarmup, int(math.Ceil(float64(period)*store.MemoryFactor)))
		}
	}
	if config.EnableRSI {
		for _, period := range config.RSIPeriods {
			maxWarmup = max(maxWarmup, int(math.Ceil(float64(period)*store.MemoryFactor)))
		}
	}
	if config.EnableATR {
		for _, period := range config.ATRPeriods {
			maxWarmup = max(maxWarmup, int(math.Ceil(float64(period)*store.MemoryFactor)))
		}
	}
	if config.EnableMACD {
		maxWarmup = max(maxWarmup, int(math.Ceil(26*store.MemoryFactor)))
	}
	if config.EnableBOLL {
		for _, period := range config.BOLLPeriods {
			maxWarmup = max(maxWarmup, period)
		}
	}
	if config.EnableDonchianBox {
		for _, period := range config.DonchianPeriods {
			maxWarmup = max(maxWarmup, period)
		}
	}

	if maxWarmup == 0 {
		logger.Infof("📐 Indicator fetch sizing: display=%d warmup=%d fetch=%d", userCount, 0, userCount)
		return userCount
	}

	fetchCount := max(userCount, userCount+maxWarmup)
	logger.Infof("📐 Indicator fetch sizing: display=%d warmup=%d fetch=%d", userCount, maxWarmup, fetchCount)
	return fetchCount
}

func buildIndicatorSnapshot(klines []Kline, config store.IndicatorConfig) IndicatorResult {
	config = computationIndicatorConfig(&config)

	result := IndicatorResult{
		EMAs:      make(map[int]float64),
		RSIs:      make(map[int]float64),
		ATRs:      make(map[int]float64),
		Bolls:     make(map[int]BollResult),
		Donchians: make(map[int]DonchianResult),
	}

	if config.EnableEMA {
		for _, period := range config.EMAPeriods {
			result.EMAs[period] = calculateEMA(klines, period)
		}
	}
	if config.EnableMACD {
		result.MACD = calculateMACD(klines)
	}
	if config.EnableRSI {
		for _, period := range config.RSIPeriods {
			result.RSIs[period] = calculateRSI(klines, period)
		}
	}
	if config.EnableATR {
		for _, period := range config.ATRPeriods {
			result.ATRs[period] = calculateATR(klines, period)
		}
	}
	if config.EnableBOLL {
		for _, period := range config.BOLLPeriods {
			upper, middle, lower := calculateBOLL(klines, period, 2.0)
			result.Bolls[period] = BollResult{
				Upper:  upper,
				Middle: middle,
				Lower:  lower,
			}
		}
	}
	if config.EnableDonchianBox {
		for _, period := range config.DonchianPeriods {
			result.Donchians[period] = calculateDonchian(klines, period)
		}
	}

	return result
}

// calculateTimeframeSeries calculates series data for a single timeframe
func calculateTimeframeSeries(klines []Kline, timeframe string, count int, indicatorConfig *store.IndicatorConfig) *TimeframeSeriesData {
	if count <= 0 {
		count = 10 // default
	}
	config := computationIndicatorConfig(indicatorConfig)

	data := &TimeframeSeriesData{
		Timeframe: timeframe,
		Klines:    make([]KlineBar, 0, count),
		MidPrices: make([]float64, 0, count),
		Volume:    make([]float64, 0, count),
		Indicators: IndicatorSeries{
			EMAs:      make(map[int][]float64),
			RSIs:      make(map[int][]float64),
			ATRs:      make(map[int]float64),
			Bolls:     make(map[int]BollSeries),
			Donchians: make(map[int]DonchianSeries),
		},
	}
	for _, period := range config.EMAPeriods {
		data.Indicators.EMAs[period] = make([]float64, 0, count)
	}
	for _, period := range config.RSIPeriods {
		data.Indicators.RSIs[period] = make([]float64, 0, count)
	}
	for _, period := range config.BOLLPeriods {
		data.Indicators.Bolls[period] = BollSeries{
			Upper:  make([]float64, 0, count),
			Middle: make([]float64, 0, count),
			Lower:  make([]float64, 0, count),
		}
	}
	for _, period := range config.DonchianPeriods {
		data.Indicators.Donchians[period] = DonchianSeries{
			Upper: make([]float64, 0, count),
			Lower: make([]float64, 0, count),
			Mid:   make([]float64, 0, count),
		}
	}
	if config.EnableMACD {
		data.Indicators.MACD = make([]float64, 0, count)
	}

	// Compute indicators over the full fetched window first, then trim the visible
	// series to the user-requested display count. This keeps long lookback factors
	// (for example Donchian500 or EMA200) available even when the prompt only shows
	// the latest 10 candles.
	for i := 0; i < len(klines); i++ {
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

		if config.EnableEMA {
			for _, period := range config.EMAPeriods {
				if i >= period-1 {
					value := calculateEMA(klines[:i+1], period)
					data.Indicators.EMAs[period] = append(data.Indicators.EMAs[period], value)
				}
			}
		}

		if config.EnableMACD && i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.Indicators.MACD = append(data.Indicators.MACD, macd)
		}

		if config.EnableRSI {
			for _, period := range config.RSIPeriods {
				if i >= period {
					value := calculateRSI(klines[:i+1], period)
					data.Indicators.RSIs[period] = append(data.Indicators.RSIs[period], value)
				}
			}
		}

		if config.EnableBOLL {
			for _, period := range config.BOLLPeriods {
				if i >= period-1 {
					upper, middle, lower := calculateBOLL(klines[:i+1], period, 2.0)
					series := data.Indicators.Bolls[period]
					series.Upper = append(series.Upper, upper)
					series.Middle = append(series.Middle, middle)
					series.Lower = append(series.Lower, lower)
					data.Indicators.Bolls[period] = series
				}
			}
		}

		if config.EnableDonchianBox {
			for _, period := range config.DonchianPeriods {
				if i >= period-1 {
					box := calculateDonchian(klines[:i+1], period)
					series := data.Indicators.Donchians[period]
					series.Upper = append(series.Upper, box.Upper)
					series.Lower = append(series.Lower, box.Lower)
					series.Mid = append(series.Mid, box.Mid)
					data.Indicators.Donchians[period] = series
				}
			}
		}
	}

	if config.EnableATR {
		for _, period := range config.ATRPeriods {
			data.Indicators.ATRs[period] = calculateATR(klines, period)
		}
	}

	data.Klines = trimKlineBars(data.Klines, count)
	data.MidPrices = trimFloatSlice(data.MidPrices, count)
	data.Volume = trimFloatSlice(data.Volume, count)
	for period, values := range data.Indicators.EMAs {
		data.Indicators.EMAs[period] = trimFloatSlice(values, count)
	}
	data.Indicators.MACD = trimFloatSlice(data.Indicators.MACD, count)
	for period, values := range data.Indicators.RSIs {
		data.Indicators.RSIs[period] = trimFloatSlice(values, count)
	}
	for period, series := range data.Indicators.Bolls {
		series.Upper = trimFloatSlice(series.Upper, count)
		series.Middle = trimFloatSlice(series.Middle, count)
		series.Lower = trimFloatSlice(series.Lower, count)
		data.Indicators.Bolls[period] = series
	}
	for period, series := range data.Indicators.Donchians {
		series.Upper = trimFloatSlice(series.Upper, count)
		series.Lower = trimFloatSlice(series.Lower, count)
		series.Mid = trimFloatSlice(series.Mid, count)
		data.Indicators.Donchians[period] = series
	}

	return data
}

func trimFloatSlice(values []float64, count int) []float64 {
	if count <= 0 || len(values) <= count {
		return values
	}
	return values[len(values)-count:]
}

func trimKlineBars(values []KlineBar, count int) []KlineBar {
	if count <= 0 || len(values) <= count {
		return values
	}
	return values[len(values)-count:]
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
