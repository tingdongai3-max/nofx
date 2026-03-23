package market

import "time"

type BollResult struct {
	Upper  float64 `json:"upper"`
	Middle float64 `json:"middle"`
	Lower  float64 `json:"lower"`
}

type BollSeries struct {
	Upper  []float64 `json:"upper"`
	Middle []float64 `json:"middle"`
	Lower  []float64 `json:"lower"`
}

// DonchianResult is a trend filter factor describing the current box boundary.
type DonchianResult struct {
	Upper float64 `json:"upper"` // Highest high in the lookback window
	Lower float64 `json:"lower"` // Lowest low in the lookback window
	Mid   float64 `json:"mid"`   // Midpoint between upper and lower
}

type DonchianSeries struct {
	Upper []float64 `json:"upper"`
	Lower []float64 `json:"lower"`
	Mid   []float64 `json:"mid"`
}

type IndicatorResult struct {
	EMAs      map[int]float64        `json:"emas,omitempty"`
	RSIs      map[int]float64        `json:"rsis,omitempty"`
	ATRs      map[int]float64        `json:"atrs,omitempty"`
	Bolls     map[int]BollResult     `json:"bolls,omitempty"`
	Donchians map[int]DonchianResult `json:"donchians,omitempty"`
	MACD      float64                `json:"macd,omitempty"`
}

type IndicatorSeries struct {
	EMAs      map[int][]float64      `json:"emas,omitempty"`
	RSIs      map[int][]float64      `json:"rsis,omitempty"`
	ATRs      map[int]float64        `json:"atrs,omitempty"`
	Bolls     map[int]BollSeries     `json:"bolls,omitempty"`
	Donchians map[int]DonchianSeries `json:"donchians,omitempty"`
	MACD      []float64              `json:"macd,omitempty"`
}

// Data market data structure
type Data struct {
	Symbol                string
	Sector                string `json:"sector,omitempty"`
	CurrentPrice          float64
	CurrentPriceAt        time.Time `json:"current_price_at,omitempty"`
	PriceChange1h         float64   // 1-hour price change percentage
	PriceChange4h         float64   // 4-hour price change percentage
	Indicators            IndicatorResult
	OpenInterest          *OIData
	Orderbook             *OrderbookData
	DexScreener           *DexScreenerData    `json:"dex_screener,omitempty"`
	GeckoSentiment        *GeckoSentimentData `json:"gecko_sentiment,omitempty"`
	HeatScore             *HeatScoreData      `json:"heat_score,omitempty"`
	VolatilityUtilization float64             `json:"vol_utilization"`
	PrimaryTimeframe      string              `json:"primary_timeframe,omitempty"`
	FundingRate           float64
	TimeframeData         map[string]*TimeframeSeriesData `json:"timeframe_data,omitempty"`
}

// KlineBar single kline bar with OHLCV data
type KlineBar struct {
	Time   int64   `json:"time"`   // Unix timestamp in milliseconds
	Open   float64 `json:"open"`   // Open price
	High   float64 `json:"high"`   // High price
	Low    float64 `json:"low"`    // Low price
	Close  float64 `json:"close"`  // Close price
	Volume float64 `json:"volume"` // Volume
}

// TimeframeSeriesData series data for a single timeframe
type TimeframeSeriesData struct {
	Timeframe  string          `json:"timeframe"`  // Timeframe identifier, e.g. "5m", "15m", "1h"
	Klines     []KlineBar      `json:"klines"`     // Full OHLCV kline data
	MidPrices  []float64       `json:"mid_prices"` // Price series (deprecated, kept for compatibility)
	Volume     []float64       `json:"volume"`     // Volume series (deprecated, use Klines)
	Indicators IndicatorSeries `json:"indicators"`
}

// OIData Open Interest data
type OIData struct {
	Latest      float64 `json:"Latest"`
	Average     float64 `json:"Average"`
	SampleCount int     `json:"sample_count,omitempty"`
	Period      string  `json:"period,omitempty"`
}

type OrderbookData struct {
	BidTotal  float64 `json:"bid_total"`
	AskTotal  float64 `json:"ask_total"`
	Imbalance float64 `json:"imbalance"`
}

type DexScreenerData struct {
	ChainID           string  `json:"chain_id,omitempty"`
	PairAddress       string  `json:"pair_address,omitempty"`
	PairURL           string  `json:"pair_url,omitempty"`
	LiquidityUSD      float64 `json:"liquidity_usd"`
	VolumeH1          float64 `json:"volume_h1"`
	BuyTxnsH1         int     `json:"buy_txns_h1"`
	SellTxnsH1        int     `json:"sell_txns_h1"`
	BuyRatio          float64 `json:"buy_ratio"`
	BuySellRatio      float64 `json:"buy_sell_ratio"`
	CEXVolumeH1       float64 `json:"cex_volume_h1"`
	OnchainToCEXRatio float64 `json:"onchain_to_cex_ratio"`
}

type GeckoSentimentData struct {
	CoinID                     string    `json:"coin_id,omitempty"`
	Categories                 []string  `json:"categories,omitempty"`
	TrendingRank               int       `json:"trending_rank,omitempty"`
	TrendingRankScore          float64   `json:"trending_rank_score,omitempty"`
	PublicInterestScore        float64   `json:"public_interest_score"`
	SentimentVotesUpPercentage float64   `json:"sentiment_votes_up_percentage"`
	UsingPrivateKey            bool      `json:"using_private_key,omitempty"`
	CachedAt                   time.Time `json:"cached_at,omitempty"`
}

type HeatScoreData struct {
	CompositeScore          float64            `json:"composite_score"`
	TradingScore            float64            `json:"trading_score"`
	QuantScore              float64            `json:"quant_score"`
	MarketScore             float64            `json:"market_score"`
	TrendScore              float64            `json:"trend_score"`
	VolumeSpikeScore        float64            `json:"volume_spike_score"`
	DonchianFactorScore     float64            `json:"-"`
	MTFResonanceFactorScore float64            `json:"-"`
	QuantFactorScore        float64            `json:"quant_factor_score"`
	QuantOIRaw              float64            `json:"-"`
	QuantImbalanceRaw       float64            `json:"-"`
	QuantNetflowRaw         float64            `json:"-"`
	SocialScore             float64            `json:"social_score"`
	OnChainScore            float64            `json:"onchain_score"`
	OnChainRatioRaw         float64            `json:"-"`
	OnChainBuyRaw           float64            `json:"-"`
	SourceWeights           map[string]float64 `json:"source_weights,omitempty"`
	RawFactorScores         map[string]float64 `json:"-"`
	RawFactorAvailable      map[string]bool    `json:"-"`
}

// Binance API response structure
type ExchangeInfo struct {
	Symbols []SymbolInfo `json:"symbols"`
}

type SymbolInfo struct {
	Symbol            string `json:"symbol"`
	Status            string `json:"status"`
	BaseAsset         string `json:"baseAsset"`
	QuoteAsset        string `json:"quoteAsset"`
	ContractType      string `json:"contractType"`
	PricePrecision    int    `json:"pricePrecision"`
	QuantityPrecision int    `json:"quantityPrecision"`
}

type Kline struct {
	OpenTime            int64   `json:"openTime"`
	Open                float64 `json:"open"`
	High                float64 `json:"high"`
	Low                 float64 `json:"low"`
	Close               float64 `json:"close"`
	Volume              float64 `json:"volume"`
	CloseTime           int64   `json:"closeTime"`
	QuoteVolume         float64 `json:"quoteVolume"`
	Trades              int     `json:"trades"`
	TakerBuyBaseVolume  float64 `json:"takerBuyBaseVolume"`
	TakerBuyQuoteVolume float64 `json:"takerBuyQuoteVolume"`
}

type KlineResponse []interface{}

type PriceTicker struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

type Ticker24hr struct {
	Symbol             string `json:"symbol"`
	PriceChange        string `json:"priceChange"`
	PriceChangePercent string `json:"priceChangePercent"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
}

// SymbolFeatures feature data structure
type SymbolFeatures struct {
	Symbol           string    `json:"symbol"`
	Timestamp        time.Time `json:"timestamp"`
	Price            float64   `json:"price"`
	PriceChange15Min float64   `json:"price_change_15min"`
	PriceChange1H    float64   `json:"price_change_1h"`
	PriceChange4H    float64   `json:"price_change_4h"`
	Volume           float64   `json:"volume"`
	VolumeRatio5     float64   `json:"volume_ratio_5"`
	VolumeRatio20    float64   `json:"volume_ratio_20"`
	VolumeTrend      float64   `json:"volume_trend"`
	RSI14            float64   `json:"rsi_14"`
	SMA5             float64   `json:"sma_5"`
	SMA10            float64   `json:"sma_10"`
	SMA20            float64   `json:"sma_20"`
	HighLowRatio     float64   `json:"high_low_ratio"`
	Volatility20     float64   `json:"volatility_20"`
	PositionInRange  float64   `json:"position_in_range"`
}

// Alert alert data structure
type Alert struct {
	Type      string    `json:"type"`
	Symbol    string    `json:"symbol"`
	Value     float64   `json:"value"`
	Threshold float64   `json:"threshold"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type Config struct {
	AlertThresholds AlertThresholds `json:"alert_thresholds"`
	UpdateInterval  int             `json:"update_interval"` // seconds
	CleanupConfig   CleanupConfig   `json:"cleanup_config"`
}

type AlertThresholds struct {
	VolumeSpike      float64 `json:"volume_spike"`
	PriceChange15Min float64 `json:"price_change_15min"`
	VolumeTrend      float64 `json:"volume_trend"`
	RSIOverbought    float64 `json:"rsi_overbought"`
	RSIOversold      float64 `json:"rsi_oversold"`
}
type CleanupConfig struct {
	InactiveTimeout   time.Duration `json:"inactive_timeout"`    // Inactive timeout duration
	MinScoreThreshold float64       `json:"min_score_threshold"` // Minimum score threshold
	NoAlertTimeout    time.Duration `json:"no_alert_timeout"`    // No alert timeout duration
	CheckInterval     time.Duration `json:"check_interval"`      // Check interval
}

var config = Config{
	AlertThresholds: AlertThresholds{
		VolumeSpike:      3.0,
		PriceChange15Min: 0.05,
		VolumeTrend:      2.0,
		RSIOverbought:    70,
		RSIOversold:      30,
	},
	CleanupConfig: CleanupConfig{
		InactiveTimeout:   30 * time.Minute,
		MinScoreThreshold: 15.0,
		NoAlertTimeout:    20 * time.Minute,
		CheckInterval:     5 * time.Minute,
	},
	UpdateInterval: 60, // 1 minute
}

// BoxData represents multi-period Donchian channel (box) data
type BoxData struct {
	// Short-term box (72 1h candles = 3 days)
	ShortUpper float64 `json:"short_upper"`
	ShortLower float64 `json:"short_lower"`

	// Mid-term box (240 1h candles = 10 days)
	MidUpper float64 `json:"mid_upper"`
	MidLower float64 `json:"mid_lower"`

	// Long-term box (500 1h candles = ~21 days)
	LongUpper float64 `json:"long_upper"`
	LongLower float64 `json:"long_lower"`

	// Current price position relative to boxes
	CurrentPrice float64 `json:"current_price"`
}

// RegimeLevel represents the ranging classification level
type RegimeLevel string

const (
	RegimeLevelNarrow   RegimeLevel = "narrow"   // narrow range oscillation
	RegimeLevelStandard RegimeLevel = "standard" // standard oscillation
	RegimeLevelWide     RegimeLevel = "wide"     // wide range oscillation
	RegimeLevelVolatile RegimeLevel = "volatile" // extreme volatility
	RegimeLevelTrending RegimeLevel = "trending" // trending
)

// BreakoutLevel represents which box level has been broken
type BreakoutLevel string

const (
	BreakoutNone  BreakoutLevel = "none"
	BreakoutShort BreakoutLevel = "short"
	BreakoutMid   BreakoutLevel = "mid"
	BreakoutLong  BreakoutLevel = "long"
)

// GridDirection represents the current grid trading direction bias
type GridDirection string

const (
	GridDirectionNeutral   GridDirection = "neutral"    // 50% buy + 50% sell
	GridDirectionLong      GridDirection = "long"       // 100% buy
	GridDirectionShort     GridDirection = "short"      // 100% sell
	GridDirectionLongBias  GridDirection = "long_bias"  // 70% buy + 30% sell (default)
	GridDirectionShortBias GridDirection = "short_bias" // 30% buy + 70% sell (default)
)

// GetBuySellRatio returns the buy and sell ratio for this direction
// biasRatio is the ratio for biased directions (default 0.7 means 70%/30%)
func (d GridDirection) GetBuySellRatio(biasRatio float64) (buyRatio, sellRatio float64) {
	if biasRatio <= 0 || biasRatio > 1 {
		biasRatio = 0.7 // Default 70%/30%
	}

	switch d {
	case GridDirectionNeutral:
		return 0.5, 0.5
	case GridDirectionLong:
		return 1.0, 0.0
	case GridDirectionShort:
		return 0.0, 1.0
	case GridDirectionLongBias:
		return biasRatio, 1.0 - biasRatio
	case GridDirectionShortBias:
		return 1.0 - biasRatio, biasRatio
	default:
		return 0.5, 0.5
	}
}
