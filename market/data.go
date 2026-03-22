package market

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"nofx/logger"
	"nofx/provider/nofxos"
	"nofx/store"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultOIHistorySamples = 30

type supplementalFetchResult struct {
	openInterest *OIData
	fundingRate  float64
	orderbook    *OrderbookData
	dexScreener  *DexScreenerData
	geckoSocial  *GeckoSentimentData
	quantData    *nofxos.QuantData
	cexVolumeH1  float64
}

// FundingRateCache is the funding rate cache structure
// Binance Funding Rate only updates every 8 hours, using 1-hour cache can significantly reduce API calls
type FundingRateCache struct {
	Rate      float64
	UpdatedAt time.Time
}

var (
	fundingRateMap sync.Map // map[string]*FundingRateCache
	frCacheTTL     = 1 * time.Hour
)

// Get retrieves market data for the specified token (uses Binance data by default)
func Get(symbol string) (*Data, error) {
	defaults := store.GetDefaultStrategyConfig("en")
	return GetWithExchange(symbol, "binance", &defaults.Indicators)
}

// GetWithExchange retrieves market data for the specified token using exchange-specific data
func GetWithExchange(symbol, exchange string, indicatorConfig *store.IndicatorConfig) (*Data, error) {
	return GetWithExchangeForScope("", symbol, exchange, indicatorConfig)
}

// GetWithExchangeForScope retrieves market data using adaptive weights from the provided scope.
func GetWithExchangeForScope(weightScope, symbol, exchange string, indicatorConfig *store.IndicatorConfig) (*Data, error) {
	var klines3m, klines4h []Kline
	var err error
	config := normalizedIndicatorConfig(indicatorConfig)
	// Normalize symbol
	symbol = Normalize(symbol)
	EnsureHeatHistoryPreloaded(symbol, "5m")

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

	// Calculate current indicators (based on 3-minute latest data)
	currentPrice := klines3m[len(klines3m)-1].Close
	currentIndicators := buildIndicatorSnapshot(klines3m, config)

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

	currentATR14 := calculateATR(klines3m, VolUtilLookback)
	volUtilization := CalculateVolatilityUtilization(symbol, klines3m, currentATR14)
	supplemental := fetchSupplementalMarketData(symbol, "5m", klines3m, config)

	timeframeData := map[string]*TimeframeSeriesData{
		"3m": calculateTimeframeSeries(klines3m, "3m", 30, &config),
		"4h": calculateTimeframeSeries(klines4h, "4h", 30, &config),
	}

	data := &Data{
		Symbol:                symbol,
		Sector:                DetermineSector(symbol, supplemental.cexVolumeH1, supplemental.geckoSocial),
		CurrentPrice:          currentPrice,
		PriceChange1h:         priceChange1h,
		PriceChange4h:         priceChange4h,
		Indicators:            currentIndicators,
		OpenInterest:          supplemental.openInterest,
		Orderbook:             supplemental.orderbook,
		DexScreener:           supplemental.dexScreener,
		GeckoSentiment:        supplemental.geckoSocial,
		VolatilityUtilization: volUtilization,
		PrimaryTimeframe:      "3m",
		FundingRate:           supplemental.fundingRate,
		TimeframeData:         timeframeData,
	}
	data.HeatScore = buildHeatScore(weightScope, symbol, data, supplemental.quantData, time.Now().UTC())

	return data, nil
}

// GetWithTimeframes retrieves market data for specified multiple timeframes
// timeframes: list of timeframes, e.g. ["5m", "15m", "1h", "4h"]
// primaryTimeframe: primary timeframe (used for calculating current indicators), defaults to timeframes[0]
// count: number of K-lines for each timeframe
func GetWithTimeframes(symbol string, timeframes []string, primaryTimeframe string, count int, indicatorConfig *store.IndicatorConfig) (*Data, error) {
	return GetWithTimeframesForScope("", symbol, timeframes, primaryTimeframe, count, indicatorConfig)
}

// GetWithTimeframesForScope retrieves market data using adaptive weights from the provided scope.
func GetWithTimeframesForScope(weightScope, symbol string, timeframes []string, primaryTimeframe string, count int, indicatorConfig *store.IndicatorConfig) (*Data, error) {
	symbol = Normalize(symbol)
	config := normalizedIndicatorConfig(indicatorConfig)
	fetchCount := CalculateRequiredFetchCount(config, count)

	if len(timeframes) == 0 {
		return nil, fmt.Errorf("at least one timeframe is required")
	}

	// If primary timeframe is not specified, use the first one
	if primaryTimeframe == "" {
		primaryTimeframe = timeframes[0]
	}
	EnsureHeatHistoryPreloaded(symbol, primaryTimeframe)

	// Ensure primary timeframe is in the list
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

	// Store data for all timeframes
	timeframeData := make(map[string]*TimeframeSeriesData)
	var primaryKlines []Kline

	// Check if this is an xyz dex asset (use Hyperliquid API)
	isXyzAsset := IsXyzDexAsset(symbol)

	// Get K-line data for each timeframe
	for _, tf := range timeframes {
		var klines []Kline
		var err error

		logger.Infof("📦 Market fetch window %s %s: display=%d fetch=%d", symbol, tf, count, fetchCount)

		if isXyzAsset {
			// Use Hyperliquid API for xyz dex assets
			klines, err = getKlinesFromHyperliquid(symbol, tf, fetchCount)
			if err != nil {
				logger.Infof("⚠️ Failed to get %s %s K-line from Hyperliquid: %v", symbol, tf, err)
				continue
			}
		} else {
			// Use CoinAnk for regular crypto assets (default to Binance)
			klines, err = getKlinesFromCoinAnk(symbol, tf, "binance", fetchCount)
			if err != nil {
				logger.Infof("⚠️ Failed to get %s %s K-line from CoinAnk: %v", symbol, tf, err)
				continue
			}
		}

		if len(klines) == 0 {
			logger.Infof("⚠️ %s %s K-line data is empty", symbol, tf)
			continue
		}

		// Save primary timeframe K-lines for calculating base indicators
		if tf == primaryTimeframe {
			primaryKlines = klines
		}

		// Calculate series data for this timeframe (use count from config)
		seriesData := calculateTimeframeSeries(klines, tf, count, &config)
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

	// Calculate current indicators (based on primary timeframe latest data)
	currentPrice := primaryKlines[len(primaryKlines)-1].Close
	currentIndicators := buildIndicatorSnapshot(primaryKlines, config)

	// Calculate price changes
	priceChange1h := calculatePriceChangeByBars(primaryKlines, primaryTimeframe, 60)  // 1 hour
	priceChange4h := calculatePriceChangeByBars(primaryKlines, primaryTimeframe, 240) // 4 hours

	currentATR14 := calculateATR(primaryKlines, VolUtilLookback)
	volUtilization := CalculateVolatilityUtilization(symbol, primaryKlines, currentATR14)
	supplemental := fetchSupplementalMarketData(symbol, primaryTimeframe, primaryKlines, config)

	data := &Data{
		Symbol:                symbol,
		Sector:                DetermineSector(symbol, supplemental.cexVolumeH1, supplemental.geckoSocial),
		CurrentPrice:          currentPrice,
		PriceChange1h:         priceChange1h,
		PriceChange4h:         priceChange4h,
		Indicators:            currentIndicators,
		OpenInterest:          supplemental.openInterest,
		Orderbook:             supplemental.orderbook,
		DexScreener:           supplemental.dexScreener,
		GeckoSentiment:        supplemental.geckoSocial,
		VolatilityUtilization: volUtilization,
		PrimaryTimeframe:      primaryTimeframe,
		FundingRate:           supplemental.fundingRate,
		TimeframeData:         timeframeData,
	}
	data.HeatScore = buildHeatScore(weightScope, symbol, data, supplemental.quantData, time.Now().UTC())

	return data, nil
}

func fetchQuantDataForHeat(symbol string, config store.IndicatorConfig) *nofxos.QuantData {
	apiKey := config.NofxOSAPIKey
	if apiKey == "" {
		apiKey = nofxos.DefaultAuthKey
	}

	client := nofxos.NewClient(nofxos.DefaultBaseURL, apiKey)
	quantData, err := client.GetCoinData(symbol, "netflow,oi,price")
	if err != nil {
		logger.Infof("⚠️ Heat quant fetch failed for %s: %v", symbol, err)
		return nil
	}

	return quantData
}

func fetchSupplementalMarketData(symbol, primaryTimeframe string, primaryKlines []Kline, config store.IndicatorConfig) supplementalFetchResult {
	result := supplementalFetchResult{
		openInterest: &OIData{Latest: 0, Average: 0},
		orderbook:    &OrderbookData{},
	}

	cexVolumeH1 := calculateCEXVolumeH1(primaryKlines, primaryTimeframe)
	result.cexVolumeH1 = cexVolumeH1
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(6)

	go func() {
		defer wg.Done()
		oiData, err := getOpenInterestData(symbol, primaryTimeframe, defaultOIHistorySamples)
		if err == nil && oiData != nil {
			mu.Lock()
			result.openInterest = oiData
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		fundingRate, _ := getFundingRate(symbol)
		mu.Lock()
		result.fundingRate = fundingRate
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		orderbook, err := fetchOrderbookImbalance(symbol)
		if err == nil && orderbook != nil {
			mu.Lock()
			result.orderbook = orderbook
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		currentPrice := 0.0
		if len(primaryKlines) > 0 {
			currentPrice = primaryKlines[len(primaryKlines)-1].Close
		}
		dexData, err := fetchDexScreenerData(symbol, currentPrice, cexVolumeH1)
		if err == nil && dexData != nil {
			mu.Lock()
			result.dexScreener = dexData
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		geckoData, err := fetchGeckoSentimentForSymbol(symbol)
		if err == nil && geckoData != nil {
			mu.Lock()
			result.geckoSocial = geckoData
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		quantData := fetchQuantDataForHeat(symbol, config)
		mu.Lock()
		result.quantData = quantData
		mu.Unlock()
	}()

	wg.Wait()
	return result
}

func calculateCEXVolumeH1(klines []Kline, timeframe string) float64 {
	if len(klines) == 0 {
		return 0
	}

	minutes := timeframeToMinutes(timeframe)
	if minutes <= 0 {
		return 0
	}

	barsNeeded := int(math.Ceil(60.0 / float64(minutes)))
	if barsNeeded < 1 {
		barsNeeded = 1
	}
	if barsNeeded > len(klines) {
		barsNeeded = len(klines)
	}

	total := 0.0
	start := len(klines) - barsNeeded
	for _, kline := range klines[start:] {
		if kline.QuoteVolume > 0 {
			total += kline.QuoteVolume
			continue
		}
		if kline.Close > 0 && kline.Volume > 0 {
			total += kline.Close * kline.Volume
		}
	}
	return total
}

func timeframeToMinutes(timeframe string) int {
	switch timeframe {
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
	default:
		return 0
	}
}

// getOpenInterestData retrieves live OI and computes a true arithmetic average
// over recent historical samples from Binance openInterestHist.
func getOpenInterestData(symbol, timeframe string, samples int) (*OIData, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/openInterest?symbol=%s", symbol)

	apiClient := NewAPIClient()
	resp, err := apiClient.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		OpenInterest string `json:"openInterest"`
		Symbol       string `json:"symbol"`
		Time         int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	oi, _ := strconv.ParseFloat(result.OpenInterest, 64)

	period := normalizeOIPeriod(timeframe)
	average, sampleCount, err := fetchOpenInterestHistory(symbol, period, samples)
	if err != nil {
		return nil, err
	}

	return &OIData{
		Latest:      oi,
		Average:     average,
		SampleCount: sampleCount,
		Period:      period,
	}, nil
}

func normalizeOIPeriod(timeframe string) string {
	switch timeframe {
	case "5m", "15m", "30m", "1h", "2h", "4h", "6h", "12h", "1d":
		return timeframe
	case "1m", "3m":
		return "5m"
	case "8h":
		return "4h"
	case "3d", "1w":
		return "1d"
	default:
		return "5m"
	}
}

func fetchOpenInterestHistory(symbol, period string, samples int) (float64, int, error) {
	if samples <= 0 {
		samples = defaultOIHistorySamples
	}

	url := fmt.Sprintf("https://fapi.binance.com/futures/data/openInterestHist?symbol=%s&period=%s&limit=%d", symbol, period, samples)
	apiClient := NewAPIClient()
	resp, err := apiClient.client.Get(url)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	var result []struct {
		SumOpenInterest string `json:"sumOpenInterest"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, err
	}
	if len(result) == 0 {
		return 0, 0, fmt.Errorf("open interest history is empty for %s %s", symbol, period)
	}

	total := 0.0
	used := 0
	for _, entry := range result {
		value, err := strconv.ParseFloat(entry.SumOpenInterest, 64)
		if err != nil {
			continue
		}
		total += value
		used++
	}
	if used == 0 {
		return 0, 0, fmt.Errorf("open interest history parse failed for %s %s", symbol, period)
	}

	return total / float64(used), used, nil
}

func fetchOpenInterestHistorySeries(symbol, period string, samples int) ([]float64, error) {
	if samples <= 0 {
		samples = defaultOIHistorySamples
	}

	url := fmt.Sprintf("https://fapi.binance.com/futures/data/openInterestHist?symbol=%s&period=%s&limit=%d", symbol, period, samples)
	apiClient := NewAPIClient()
	resp, err := apiClient.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result []struct {
		SumOpenInterest string `json:"sumOpenInterest"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("open interest history is empty for %s %s", symbol, period)
	}

	series := make([]float64, 0, len(result))
	for _, entry := range result {
		value, err := strconv.ParseFloat(entry.SumOpenInterest, 64)
		if err != nil {
			continue
		}
		series = append(series, value)
	}
	if len(series) == 0 {
		return nil, fmt.Errorf("open interest history parse failed for %s %s", symbol, period)
	}

	return series, nil
}

func fetchOrderbookImbalance(symbol string) (*OrderbookData, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/depth?symbol=%s&limit=100", symbol)

	apiClient := NewAPIClient()
	resp, err := apiClient.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	sumLevels := func(levels [][]string) float64 {
		total := 0.0
		for _, level := range levels {
			if len(level) < 2 {
				continue
			}
			price, err1 := strconv.ParseFloat(level[0], 64)
			qty, err2 := strconv.ParseFloat(level[1], 64)
			if err1 != nil || err2 != nil {
				continue
			}
			total += price * qty
		}
		return total
	}

	bidTotal := sumLevels(result.Bids)
	askTotal := sumLevels(result.Asks)
	denominator := bidTotal + askTotal
	imbalance := 0.0
	if denominator > 0 {
		imbalance = (bidTotal - askTotal) / denominator
	}

	logger.Infof("V3_AUDIT_DEPTH: %s, BidTotal=%.2f, AskTotal=%.2f, Imb=%.2f%%", symbol, bidTotal, askTotal, imbalance*100)

	return &OrderbookData{
		BidTotal:  bidTotal,
		AskTotal:  askTotal,
		Imbalance: imbalance,
	}, nil
}

// getFundingRate retrieves funding rate (optimized: uses 1-hour cache)
func getFundingRate(symbol string) (float64, error) {
	// Check cache (1-hour validity)
	// Funding Rate only updates every 8 hours, 1-hour cache is very reasonable
	if cached, ok := fundingRateMap.Load(symbol); ok {
		cache := cached.(*FundingRateCache)
		if time.Since(cache.UpdatedAt) < frCacheTTL {
			// Cache hit, return directly
			return cache.Rate, nil
		}
	}

	// Cache expired or doesn't exist, call API
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/premiumIndex?symbol=%s", symbol)

	apiClient := NewAPIClient()
	resp, err := apiClient.client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
		InterestRate    string `json:"interestRate"`
		Time            int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	rate, _ := strconv.ParseFloat(result.LastFundingRate, 64)

	// Update cache
	fundingRateMap.Store(symbol, &FundingRateCache{
		Rate:      rate,
		UpdatedAt: time.Now(),
	})

	return rate, nil
}

// Format formats and outputs market data
func Format(data *Data) string {
	var sb strings.Builder

	// Format price with dynamic precision
	priceStr := formatPriceWithDynamicPrecision(data.CurrentPrice)
	sb.WriteString(fmt.Sprintf("current_price = %s", priceStr))
	for _, line := range formatIndicatorSnapshot(data.Indicators) {
		sb.WriteString(", " + line)
	}
	sb.WriteString("\n\n")

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
	for period, values := range data.Indicators.EMAs {
		if len(values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA%d: %s\n", period, formatFloatSlice(values)))
		}
	}

	if len(data.Indicators.MACD) > 0 {
		sb.WriteString(fmt.Sprintf("MACD: %s\n", formatFloatSlice(data.Indicators.MACD)))
	}

	for period, values := range data.Indicators.RSIs {
		if len(values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI%d: %s\n", period, formatFloatSlice(values)))
		}
	}

	for period, value := range data.Indicators.ATRs {
		if value > 0 {
			sb.WriteString(fmt.Sprintf("ATR%d: %.4f\n", period, value))
		}
	}

	for period, series := range data.Indicators.Bolls {
		if len(series.Upper) > 0 {
			sb.WriteString(fmt.Sprintf("BOLL%d Upper: %s\n", period, formatFloatSlice(series.Upper)))
			sb.WriteString(fmt.Sprintf("BOLL%d Middle: %s\n", period, formatFloatSlice(series.Middle)))
			sb.WriteString(fmt.Sprintf("BOLL%d Lower: %s\n", period, formatFloatSlice(series.Lower)))
		}
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

func formatIndicatorSnapshot(indicators IndicatorResult) []string {
	parts := make([]string, 0)
	for period, value := range indicators.EMAs {
		parts = append(parts, fmt.Sprintf("current_ema%d = %.3f", period, value))
	}
	if indicators.MACD != 0 {
		parts = append(parts, fmt.Sprintf("current_macd = %.3f", indicators.MACD))
	}
	for period, value := range indicators.RSIs {
		parts = append(parts, fmt.Sprintf("current_rsi%d = %.3f", period, value))
	}
	for period, value := range indicators.ATRs {
		if value > 0 {
			parts = append(parts, fmt.Sprintf("current_atr%d = %.3f", period, value))
		}
	}
	return parts
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

// BuildDataFromKlines constructs market data snapshot from preloaded K-line series.
func BuildDataFromKlines(symbol string, primary []Kline, longer []Kline) (*Data, error) {
	if len(primary) == 0 {
		return nil, fmt.Errorf("primary series is empty")
	}
	defaults := store.GetDefaultStrategyConfig("en")
	config := normalizedIndicatorConfig(&defaults.Indicators)

	symbol = Normalize(symbol)
	current := primary[len(primary)-1]
	currentPrice := current.Close

	data := &Data{
		Symbol:        symbol,
		CurrentPrice:  currentPrice,
		Indicators:    buildIndicatorSnapshot(primary, config),
		PriceChange1h: priceChangeFromSeries(primary, time.Hour),
		PriceChange4h: priceChangeFromSeries(primary, 4*time.Hour),
		OpenInterest:  &OIData{Latest: 0, Average: 0},
		FundingRate:   0,
		TimeframeData: map[string]*TimeframeSeriesData{
			"primary": calculateTimeframeSeries(primary, "primary", 30, &config),
		},
	}

	if len(longer) > 0 {
		data.TimeframeData["longer"] = calculateTimeframeSeries(longer, "longer", 30, &config)
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
